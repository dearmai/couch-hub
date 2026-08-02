import { expect, test, type APIRequestContext } from "@playwright/test"

const COUCHDB_URL = process.env.COUCHHUB_E2E_COUCHDB ?? "http://127.0.0.1:15984"
const ADMIN_USER = process.env.COUCHHUB_E2E_COUCHDB_USER ?? "admin"
const ADMIN_PASSWORD = process.env.COUCHHUB_E2E_COUCHDB_PASSWORD ?? "couchhub-dev"
const PUBLIC_URL = "https://sync.example.com"
const HUB_URL = "http://127.0.0.1:10040"

const auth = "Basic " + Buffer.from(`${ADMIN_USER}:${ADMIN_PASSWORD}`).toString("base64")
const VAULT_NAME = "zone-vault"

let token = ""
let zoneId = ""
let dbName = ""

function couch(path: string) {
  return fetch(`${COUCHDB_URL}${path}`, { headers: { Authorization: auth } })
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
  dbName = (await created.json()).vault.dbName
})

test.afterAll(async ({ request }) => {
  for (const z of await (await request.get("/api/zones")).json()) {
    await request.delete(`/api/zones/${z.id}`)
  }
  for (const v of await (await request.get("/api/vaults")).json()) {
    await request.delete(`/api/vaults/${v.id}?confirm=${encodeURIComponent(v.name)}`)
  }
})

test("creating a zone shows the shared token once", async ({ page }) => {
  await page.goto("/zones")
  await page.getByRole("button", { name: /존 추가/ }).first().click()

  await page.getByLabel("존 이름").fill("self-zone")
  // Pointing the zone at this same hub keeps the test to one process. It
  // exercises the token auth and the replication-document writing; whether
  // CouchDB then succeeds at replicating a database onto itself is CouchDB's
  // business and deliberately not asserted.
  await page.getByLabel("상대 CouchHub 주소").fill(HUB_URL)
  await page.getByRole("button", { name: "만들기", exact: true }).click()

  // Role-scoped: the dialog title and the copy field share this text.
  await expect(page.getByRole("heading", { name: "존 토큰" })).toBeVisible()
  await page.getByRole("button", { name: "존 토큰 표시", exact: true }).click()
  token = await page.getByRole("textbox", { name: "존 토큰", exact: true }).inputValue()
  expect(token.length).toBeGreaterThan(16)

  await page.getByRole("button", { name: "닫기" }).click()
  await expect(page.getByText(HUB_URL)).toBeVisible()
})

test("the export endpoint refuses a missing or wrong token", async ({ request }) => {
  const anonymous = await request.get("/api/zone/export")
  expect(anonymous.status()).toBe(401)

  const wrong = await request.get("/api/zone/export", { headers: { Authorization: "Bearer nope" } })
  expect(wrong.status()).toBe(401)
})

test("the export endpoint hands a peer the vault credentials", async ({ request }) => {
  const res = await request.get("/api/zone/export", { headers: { Authorization: `Bearer ${token}` } })
  expect(res.status()).toBe(200)
  // Live credentials must never be cached by anything in between.
  expect(res.headers()["cache-control"]).toContain("no-store")

  const body = await res.json()
  expect(body.publicBaseUrl).toBe(PUBLIC_URL)

  const exported = body.vaults.find((v: { dbName: string }) => v.dbName === dbName)
  expect(exported, "the vault should be exported").toBeTruthy()
  expect(exported.couchUser).toBe(`vault_${dbName}`)
  expect(exported.couchPassword.length).toBeGreaterThan(8)
})

test("syncing writes replication documents into CouchDB", async ({ page, request }) => {
  const zones = await (await request.get("/api/zones")).json()
  zoneId = zones[0].id

  await page.goto("/zones")
  await page.getByRole("button", { name: /동기화/ }).first().click()

  await expect(page.getByText("동기화 결과")).toBeVisible()
  await expect(page.getByText(/복제 문서 2개를 적용했습니다/)).toBeVisible()

  // Both directions, named so they are recognisable in _replicator.
  for (const direction of ["pull", "push"]) {
    const id = `couchhub:${zoneId}:${dbName}:${direction}`
    const res = await couch(`/_replicator/${encodeURIComponent(id)}`)
    expect(res.status, `replication document ${id} should exist`).toBe(200)

    const doc = await res.json()
    expect(doc.continuous).toBe(true)
    // The target database is provisioned by CouchHub with a _security document;
    // letting the replicator create it would leave it open to every account.
    expect(doc.create_target).toBe(false)
  }
})

test("deleting a zone removes its replication documents", async ({ request }) => {
  const res = await request.delete(`/api/zones/${zoneId}`)
  expect(res.status()).toBe(204)

  for (const direction of ["pull", "push"]) {
    const id = `couchhub:${zoneId}:${dbName}:${direction}`
    expect((await couch(`/_replicator/${encodeURIComponent(id)}`)).status).toBe(404)
  }
})
