import { expect, test, type APIRequestContext } from "@playwright/test"

// Named to sort last. The specs share one store, and the wizard spec needs it
// to still be in its first-run state - this one provisions.

const COUCHDB_URL = process.env.COUCHHUB_E2E_COUCHDB ?? "http://127.0.0.1:15984"
const ADMIN_USER = process.env.COUCHHUB_E2E_COUCHDB_USER ?? "admin"
const ADMIN_PASSWORD = process.env.COUCHHUB_E2E_COUCHDB_PASSWORD ?? "couchhub-dev"
const PUBLIC_URL = "https://sync.example.com"

// The second server is the same CouchDB under a second registration. Nothing in
// the profile list is server-side state, so one container is enough to exercise
// the list, the primary flag and the removal rules.
const SECOND_NAME = "second"
const SECOND_PUBLIC_URL = "https://sync2.example.com"

interface Profile {
  id: string
  name: string
  primary: boolean
  vaultCount: number
}

async function profiles(request: APIRequestContext): Promise<Profile[]> {
  return (await request.get("/api/profiles")).json()
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
})

test.afterAll(async ({ request }) => {
  for (const v of await (await request.get("/api/vaults")).json()) {
    await request.delete(`/api/vaults/${v.id}?confirm=${encodeURIComponent(v.name)}`)
  }
  for (const p of await profiles(request)) {
    if (p.name === SECOND_NAME) await request.delete(`/api/profiles/${p.id}`)
  }
})

test("the wizard's server is listed as the primary", async ({ page }) => {
  await page.goto("/couchdbs")
  await expect(page.getByText("e2e", { exact: true })).toBeVisible()
  await expect(page.getByText("주 서버", { exact: true })).toBeVisible()
})

test("adding a CouchDB provisions it and leaves the primary alone", async ({ page, request }) => {
  await page.goto("/couchdbs")
  await page.getByRole("button", { name: /CouchDB 추가/ }).first().click()

  await page.getByLabel("이름").fill(SECOND_NAME)
  await page.getByLabel("CouchHub 연동용 주소").fill(COUCHDB_URL)
  await page.getByLabel("Obsidian 연동용 주소").fill(SECOND_PUBLIC_URL)
  await page.getByLabel("관리자 계정").fill(ADMIN_USER)
  await page.getByLabel("관리자 비밀번호").fill(ADMIN_PASSWORD)
  await page.getByRole("button", { name: "추가", exact: true }).click()

  // The result dialog reports the diagnosis, which provisioning just satisfied.
  await expect(page.getByRole("heading", { name: `${SECOND_NAME} 상태` })).toBeVisible()
  await expect(page.getByText("livesync 설정이 모두 적용되어 있습니다")).toBeVisible()
  await page.getByRole("button", { name: "닫기" }).click()

  const list = await profiles(request)
  expect(list.map((p) => p.name)).toContain(SECOND_NAME)
  // Adding a server never moves the flag: promoting one is its own action.
  expect(list.find((p) => p.primary)?.name).toBe("e2e")
})

test("주 서버로 moves the primary flag", async ({ page, request }) => {
  await page.goto("/couchdbs")

  const row = page.getByRole("listitem").filter({ hasText: SECOND_NAME })
  await row.getByRole("button", { name: /주 서버로/ }).click()

  await expect(row.getByText("주 서버", { exact: true })).toBeVisible()
  expect((await profiles(request)).find((p) => p.primary)?.name).toBe(SECOND_NAME)

  // A vault created without naming a server lands on the new primary.
  const created = await request.post("/api/vaults", { data: { name: "primary-check", dbName: "" } })
  expect(created.ok(), `vault creation failed: ${await created.text()}`).toBeTruthy()
  const vault = (await created.json()).vault
  expect(vault.profileId).toBe((await profiles(request)).find((p) => p.primary)?.id)
})

test("a server holding vaults cannot be removed", async ({ page, request }) => {
  const target = (await profiles(request)).find((p) => p.name === SECOND_NAME)!
  expect(target.vaultCount).toBeGreaterThan(0)

  const refused = await request.delete(`/api/profiles/${target.id}`)
  expect(refused.status()).toBe(409)

  await page.goto("/couchdbs")
  const row = page.getByRole("listitem").filter({ hasText: SECOND_NAME })
  await row.getByRole("button", { name: `${SECOND_NAME} 제거` }).click()
  await expect(page.getByText(/Vault \d+개가 이 서버에 있습니다/)).toBeVisible()
  await expect(page.getByRole("button", { name: "제거", exact: true })).toBeDisabled()
})
