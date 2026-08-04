import { execFileSync } from "node:child_process"
import { expect, test, type APIRequestContext } from "@playwright/test"

const COUCHDB_URL = process.env.COUCHHUB_E2E_COUCHDB ?? "http://127.0.0.1:15984"
const ADMIN_USER = process.env.COUCHHUB_E2E_COUCHDB_USER ?? "admin"
const ADMIN_PASSWORD = process.env.COUCHHUB_E2E_COUCHDB_PASSWORD ?? "couchhub-dev"
const PUBLIC_URL = "https://sync.example.com"

const VAULT_NAME = "export-vault"
// Long enough that the seeder splits it, so the archive proves chunks are
// reassembled in order rather than merely fetched.
const LONG_NOTE = "y".repeat(30) + "\n\n두 번째 청크로 넘어가는 내용."

let vaultId = ""
let dbName = ""
let passphrase = ""

/**
 * A binary file big enough that the plugin splits it into several chunks.
 *
 * Not compressible noise by accident: the bytes are pseudo-random so the zip
 * cannot flatten them, and the length is not a multiple of the piece size, so
 * the last chunk is short and every earlier one ends in base64 padding.
 */
const BINARY = Buffer.from(
  Array.from({ length: 5000 }, (_, i) => (i * 37 + (i % 7) * 11) % 256),
)

function seed(notes: ({ path: string } & ({ text: string } | { binaryBase64: string; pieceSize?: number }))[]) {
  const out = execFileSync(
    "node",
    [
      "scripts/seed-vault.mjs",
      JSON.stringify({
        couchdb: COUCHDB_URL,
        db: dbName,
        user: ADMIN_USER,
        password: ADMIN_PASSWORD,
        passphrase,
        notes,
      }),
    ],
    { cwd: "..", encoding: "utf8" },
  )
  return JSON.parse(out)
}

async function ensureProvisioned(request: APIRequestContext) {
  const status = await (await request.get("/api/status")).json()
  if (!status.needsSetup) return
  const res = await request.post("/api/setup/apply", {
    data: {
      name: "e2e",
      adminBaseUrl: COUCHDB_URL,
      publicBaseUrl: PUBLIC_URL,
      adminUser: ADMIN_USER,
      adminPassword: ADMIN_PASSWORD,
    },
  })
  expect(res.ok(), `provisioning failed: ${await res.text()}`).toBeTruthy()
}

/** Polls until the export stops working, the way the panel does. */
async function waitForArchive(request: APIRequestContext) {
  for (let i = 0; i < 100; i++) {
    const status = await (await request.get(`/api/vaults/${vaultId}/export`)).json()
    if (status.state !== "listing" && status.state !== "packing") return status
    await new Promise((r) => setTimeout(r, 100))
  }
  throw new Error("export never finished")
}

test.beforeAll(async ({ request }) => {
  await ensureProvisioned(request)

  const created = await request.post("/api/vaults", { data: { name: VAULT_NAME, dbName: "" } })
  expect(created.ok(), `vault creation failed: ${await created.text()}`).toBeTruthy()
  const body = await created.json()
  vaultId = body.vault.id
  dbName = body.vault.dbName
  passphrase = body.credentials.e2eePassphrase

  // Seeded with the real library, so the archive is proof the reader agrees
  // with the plugin rather than with itself.
  const result = seed([
    { path: "notes/short.md", text: "# 짧은 노트\n" },
    { path: "notes/long.md", text: LONG_NOTE },
    { path: "deep/nested/dir/file.md", text: "nested" },
    // Split by the plugin's own splitter, so the chunk boundaries are the real
    // ones rather than boundaries this test chose to agree with.
    { path: "assets/big.bin", binaryBase64: BINARY.toString("base64"), pieceSize: 1024 },
  ])
  expect(result.seeded).toBe(4)
})

test.afterAll(async ({ request }) => {
  for (const v of await (await request.get("/api/vaults")).json()) {
    await request.delete(`/api/vaults/${v.id}?confirm=${encodeURIComponent(v.name)}`)
  }
})

test("packs every note and serves the archive", async ({ request }) => {
  const started = await request.post(`/api/vaults/${vaultId}/export`)
  expect(started.status(), await started.text()).toBe(202)
  expect((await started.json()).state).toBe("listing")

  const status = await waitForArchive(request)
  expect(status.state, status.error).toBe("ready")
  expect(status.total).toBe(4)
  expect(status.done, JSON.stringify(status.problems)).toBe(4)
  expect(status.skipped, JSON.stringify(status.problems)).toBe(0)
  expect(status.sizeBytes).toBeGreaterThan(0)
  expect(status.filename).toMatch(/^export-vault-\d{8}-\d{6}\.zip$/)

  const res = await request.get(`/api/vaults/${vaultId}/export/download`)
  expect(res.status()).toBe(200)
  expect(res.headers()["content-type"]).toBe("application/zip")
  // The archive is the vault in plaintext; nothing in between may keep a copy.
  expect(res.headers()["cache-control"]).toContain("no-store")
  expect(res.headers()["content-disposition"]).toContain("attachment")

  const body = await res.body()
  expect(body.length).toBe(status.sizeBytes)
  // A real zip, not an error page rendered with the wrong content type.
  expect(body.subarray(0, 4)).toEqual(Buffer.from([0x50, 0x4b, 0x03, 0x04]))
  // Entry names sit uncompressed in the local headers, so the archive can be
  // checked for its contents without a zip library.
  for (const name of [
    "notes/short.md",
    "notes/long.md",
    "deep/nested/dir/file.md",
    "assets/big.bin",
    "_couchhub-export.txt",
  ]) {
    expect(body.includes(Buffer.from(name, "utf8")), `archive is missing ${name}`).toBeTruthy()
  }
})

test("a split binary file comes out byte-identical", async ({ request }) => {
  // The failure this guards against is silent and total: livesync slices a
  // binary file's bytes and base64-encodes each slice on its own, so joining
  // the chunks and decoding once produces invalid base64 the moment a file is
  // large enough to have a second chunk - which is most images in a vault.
  await request.delete(`/api/vaults/${vaultId}/export`)
  await request.post(`/api/vaults/${vaultId}/export`)
  const status = await waitForArchive(request)
  expect(status.state, status.error).toBe("ready")
  expect(status.skipped, JSON.stringify(status.problems)).toBe(0)

  const archive = await (await request.get(`/api/vaults/${vaultId}/export/download`)).body()
  const unpacked = execFileSync("python3", ["-c", UNZIP_ONE, "assets/big.bin"], {
    input: archive,
    maxBuffer: 64 * 1024 * 1024,
  })
  expect(unpacked.equals(BINARY)).toBeTruthy()
})

// Reads one entry out of a zip on stdin. Node has no zip reader, and pulling a
// dependency in for one assertion is worse than four lines of Python.
const UNZIP_ONE = `
import io, sys, zipfile
data = sys.stdin.buffer.read()
sys.stdout.buffer.write(zipfile.ZipFile(io.BytesIO(data)).read(sys.argv[1]))
`

test("refuses a second export while one is running", async ({ request }) => {
  await request.delete(`/api/vaults/${vaultId}/export`)

  const first = await request.post(`/api/vaults/${vaultId}/export`)
  expect(first.status()).toBe(202)

  // Either the first is still going - in which case the second is refused - or
  // it has already finished, which is the one case a restart is allowed.
  const second = await request.post(`/api/vaults/${vaultId}/export`)
  if (second.status() === 409) {
    expect((await second.json()).code).toBe("export_in_progress")
  } else {
    expect(second.status()).toBe(202)
  }

  await waitForArchive(request)
})

test("discarding removes the archive from the server", async ({ request }) => {
  await request.post(`/api/vaults/${vaultId}/export`)
  await waitForArchive(request)

  expect((await request.delete(`/api/vaults/${vaultId}/export`)).status()).toBe(204)
  expect((await request.get(`/api/vaults/${vaultId}/export`)).status()).toBe(404)
  expect((await request.get(`/api/vaults/${vaultId}/export/download`)).status()).toBe(404)
})

test("the manage tab runs an export through to a download link", async ({ page, request }) => {
  // Start from a vault with no export, so the panel offers the first-run
  // button rather than the one that replaces an existing archive.
  await request.delete(`/api/vaults/${vaultId}/export`)

  await page.goto(`/vaults/${vaultId}?tab=manage`)
  await expect(page.getByRole("heading", { name: "Vault 내보내기" })).toBeVisible()

  await page.getByRole("button", { name: "내보내기", exact: true }).click()

  await expect(page.getByText("내려받을 수 있습니다")).toBeVisible()
  await expect(page.getByRole("progressbar", { name: "내보내기 진행률" })).toHaveAttribute("aria-valuenow", "100")

  const link = page.getByRole("link", { name: /zip 내려받기/ })
  await expect(link).toHaveAttribute("href", `/api/vaults/${vaultId}/export/download`)
  await expect(link).toHaveAttribute("download", /^export-vault-.*\.zip$/)

  // Nothing was left out, so the panel says nothing about exclusions.
  await expect(page.getByText(/개를 제외했습니다/)).toHaveCount(0)
})
