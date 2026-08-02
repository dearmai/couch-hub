import { execFileSync } from "node:child_process"
import { expect, test, type APIRequestContext } from "@playwright/test"

const COUCHDB_URL = process.env.COUCHHUB_E2E_COUCHDB ?? "http://127.0.0.1:15984"
const ADMIN_USER = process.env.COUCHHUB_E2E_COUCHDB_USER ?? "admin"
const ADMIN_PASSWORD = process.env.COUCHHUB_E2E_COUCHDB_PASSWORD ?? "couchhub-dev"
const PUBLIC_URL = "https://sync.example.com"

const auth = "Basic " + Buffer.from(`${ADMIN_USER}:${ADMIN_PASSWORD}`).toString("base64")

const VAULT_NAME = "docs-vault"
const MARKDOWN = "# 제목\n\n본문 **굵게** 그리고 [링크](https://example.com).\n\n- 하나\n- 둘\n"
const LONG_NOTE = "x".repeat(30) + "\n\n두 번째 청크로 넘어가는 내용."

let vaultId = ""
let dbName = ""
let passphrase = ""

function couch(path: string, init?: RequestInit) {
  return fetch(`${COUCHDB_URL}${path}`, { ...init, headers: { Authorization: auth, ...init?.headers } })
}

/**
 * Writes notes in the plugin's own format, encrypted with the official library.
 *
 * encryptedMeta selects between the two shapes the plugin has used: the current
 * one bundles path, timestamps, size and the chunk list into a single encrypted
 * `path` field, while the older one encrypts the path on its own.
 */
function seed(notes: { path: string; text: string }[], encryptedMeta = true) {
  const out = execFileSync("node", ["scripts/seed-vault.mjs", JSON.stringify({
    couchdb: COUCHDB_URL,
    db: dbName,
    user: ADMIN_USER,
    password: ADMIN_PASSWORD,
    passphrase,
    notes,
    encryptedMeta,
  })], { cwd: "..", encoding: "utf8" })
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

test.beforeAll(async ({ request }) => {
  await ensureProvisioned(request)

  const created = await request.post("/api/vaults", { data: { name: VAULT_NAME, dbName: "" } })
  expect(created.ok(), `vault creation failed: ${await created.text()}`).toBeTruthy()
  const body = await created.json()
  vaultId = body.vault.id
  dbName = body.vault.dbName
  passphrase = body.credentials.e2eePassphrase

  // Seeded with the real library, not with CouchHub's own encoder - otherwise
  // this would only prove the reader agrees with itself.
  const result = seed([
    { path: "notes/hello.md", text: MARKDOWN },
    { path: "notes/long.md", text: LONG_NOTE },
  ])
  expect(result.seeded).toBe(2)

  // Also seed the older shape, so both remain readable.
  expect(seed([{ path: "notes/legacy-meta.md", text: "# 예전 형식\n" }], false).seeded).toBe(1)
})

test.afterAll(async ({ request }) => {
  for (const v of await (await request.get("/api/vaults")).json()) {
    await request.delete(`/api/vaults/${v.id}?confirm=${encodeURIComponent(v.name)}`)
  }
})

test("lists notes with their paths decrypted", async ({ request }) => {
  const res = await request.get(`/api/vaults/${vaultId}/documents`)
  expect(res.status()).toBe(200)
  // Decrypted vault content must never be cached by anything in between.
  expect(res.headers()["cache-control"]).toContain("no-store")

  const docs = await res.json()
  const paths = docs.map((d: { path: string }) => d.path)
  expect(paths).toContain("notes/hello.md")
  expect(paths).toContain("notes/long.md")
  expect(paths).toContain("notes/legacy-meta.md")

  // The encrypted metadata bundle carries size, mtime and the chunk list. If it
  // is not unwrapped they all read as zero, which is indistinguishable from an
  // empty vault at a glance.
  const hello = docs.find((d: { path: string }) => d.path === "notes/hello.md")
  expect(hello.size).toBeGreaterThan(0)
  expect(hello.mtime).toBeGreaterThan(0)
  expect(hello.chunks).toBeGreaterThan(0)

  // The chunks themselves are documents too, and must not show up as notes.
  const all = await (await couch(`/${dbName}/_all_docs`)).json()
  expect(all.rows.length).toBeGreaterThan(docs.length)
})

test("reassembles a note from its chunks", async ({ request }) => {
  const docs = await (await request.get(`/api/vaults/${vaultId}/documents`)).json()
  const long = docs.find((d: { path: string }) => d.path === "notes/long.md")
  expect(long.chunks).toBe(2)

  const res = await request.get(`/api/vaults/${vaultId}/document?docId=${encodeURIComponent(long.id)}`)
  expect(res.status()).toBe(200)
  const content = await res.json()

  // Byte-exact across the chunk boundary; a reordering or a lost byte here would
  // corrupt every note in the vault.
  expect(content.text).toBe(LONG_NOTE)
})

test("shows markdown before and after rendering", async ({ page }) => {
  // The tab lives in the URL, so the panel can be opened directly.
  await page.goto(`/vaults/${vaultId}?tab=documents`)

  await expect(page.getByRole("heading", { name: "문서" })).toBeVisible()
  await page.getByText("notes/hello.md").click()

  // Rendered first: the markdown became real elements.
  await expect(page.getByRole("heading", { name: "제목" })).toBeVisible()
  await expect(page.getByRole("link", { name: "링크" })).toBeVisible()
  await expect(page.getByRole("listitem").filter({ hasText: "하나" })).toBeVisible()

  // ...and the raw source is available to compare against.
  await page.getByRole("tab", { name: /원문/ }).click()
  await expect(page.getByText("# 제목", { exact: false })).toBeVisible()
  await expect(page.getByText("**굵게**", { exact: false })).toBeVisible()
})

test("the tab selection survives a reload", async ({ page }) => {
  await page.goto(`/vaults/${vaultId}?tab=documents`)
  await expect(page.getByRole("tab", { name: /문서/ })).toHaveAttribute("aria-selected", "true")

  await page.reload()
  await expect(page.getByRole("tab", { name: /문서/ })).toHaveAttribute("aria-selected", "true")
  await expect(page.getByText("notes/hello.md")).toBeVisible()
})

test("does not execute script embedded in a note", async ({ page, request }) => {
  // A vault can receive notes from a zone peer, so note content is not trusted.
  // Script in a note must not run inside the panel that holds every credential.
  seed([{ path: "notes/evil.md", text: `# evil\n\n<img src=x onerror="window.__pwned=1">\n` }])

  const docs = await (await request.get(`/api/vaults/${vaultId}/documents`)).json()
  const evil = docs.find((d: { path: string }) => d.path === "notes/evil.md")
  expect(evil, "the seeded note should be listed").toBeTruthy()

  await page.goto(`/vaults/${vaultId}?tab=documents`)
  await page.getByText("notes/evil.md").click()
  await expect(page.getByRole("heading", { name: "evil" })).toBeVisible()

  expect(await page.evaluate(() => (window as unknown as { __pwned?: number }).__pwned)).toBeUndefined()
  // The handler is stripped, not merely inert.
  expect(await page.locator("img[onerror]").count()).toBe(0)
})
