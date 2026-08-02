import { expect, test, type APIRequestContext } from "@playwright/test"

const COUCHDB_URL = process.env.COUCHHUB_E2E_COUCHDB ?? "http://127.0.0.1:15984"
const ADMIN_USER = process.env.COUCHHUB_E2E_COUCHDB_USER ?? "admin"
const ADMIN_PASSWORD = process.env.COUCHHUB_E2E_COUCHDB_PASSWORD ?? "couchhub-dev"
const PUBLIC_URL = "https://sync.example.com"

const auth = "Basic " + Buffer.from(`${ADMIN_USER}:${ADMIN_PASSWORD}`).toString("base64")

const VAULT_NAME = "stats-vault"

function couch(path: string, init?: RequestInit) {
  return fetch(`${COUCHDB_URL}${path}`, { ...init, headers: { Authorization: auth, ...init?.headers } })
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

let dbName = ""

test.beforeAll(async ({ request }) => {
  await ensureProvisioned(request)

  const created = await request.post("/api/vaults", { data: { name: VAULT_NAME, dbName: "" } })
  expect(created.ok(), `vault creation failed: ${await created.text()}`).toBeTruthy()
  dbName = (await created.json()).vault.dbName

  // The poller samples on a timer; the dashboard would otherwise be empty for
  // the whole run.
  await request.post("/api/metrics/refresh")
})

test.afterAll(async ({ request }) => {
  const vaults = await (await request.get("/api/vaults")).json()
  for (const v of vaults) {
    await request.delete(`/api/vaults/${v.id}?confirm=${encodeURIComponent(v.name)}`)
  }
})

test("dashboard reports totals from the polled snapshot", async ({ page }) => {
  await page.goto("/")

  await expect(page.getByRole("heading", { name: "대시보드" })).toBeVisible()
  await expect(page.getByText("아직 수집된 통계가 없습니다")).toBeHidden()

  await expect(page.getByLabel("Vault", { exact: true })).toHaveText("1")
  // A freshly created vault holds no documents but does occupy some disk.
  await expect(page.getByLabel("문서", { exact: true })).toHaveText("0")
  await expect(page.getByLabel("디스크 사용", { exact: true })).not.toHaveText("0 B")
  await expect(page.getByRole("link", { name: new RegExp(VAULT_NAME) })).toBeVisible()

  // The heatmap renders even with no activity yet.
  await expect(page.getByText(/최근 1년 쓰기/)).toBeVisible()
})

test("writes to CouchDB show up as activity", async ({ request }) => {
  // Baseline first: the counter is a delta of update_seq between polls, so the
  // first sample after vault creation must not be counted as activity.
  await request.post("/api/metrics/refresh")

  for (let i = 0; i < 3; i++) {
    const res = await couch(`/${dbName}`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ note: `e2e-${i}` }),
    })
    expect(res.status).toBe(201)
  }

  await request.post("/api/metrics/refresh")

  const dashboard = await (await request.get("/api/dashboard")).json()
  const today = new Date().toISOString().slice(0, 10)
  const todayEntry = (dashboard.activity ?? []).find((d: { day: string }) => d.day === today)

  expect(todayEntry, "expected today to have activity").toBeTruthy()
  expect(todayEntry.writes).toBeGreaterThanOrEqual(3)

  // Documents should be reflected too.
  expect(dashboard.totals.documents).toBeGreaterThanOrEqual(3)
  expect(dashboard.totals.sizeFile).toBeGreaterThan(0)
})

test("vault detail charts appear once there are two snapshots", async ({ page }) => {
  await page.goto("/vaults")
  await page.getByRole("link", { name: new RegExp(VAULT_NAME) }).click()

  await expect(page.getByRole("heading", { name: "현황" })).toBeVisible()

  // Both measures are charted separately - never one chart with two y-axes.
  await expect(page.getByRole("heading", { name: "용량", level: 3 })).toBeVisible()
  await expect(page.getByRole("heading", { name: "문서 수", level: 3 })).toBeVisible()
  await expect(page.getByText("추이를 그리려면")).toBeHidden()

  // Recharts renders into an SVG; two charts means two of them.
  await expect(page.locator(".recharts-surface")).toHaveCount(2)

  // The size chart has two series and therefore a legend, so identity is never
  // carried by colour alone.
  await expect(page.getByText("디스크", { exact: true })).toBeVisible()
  await expect(page.getByText("실데이터", { exact: true })).toBeVisible()
})

test.describe("mobile viewport", () => {
  test.use({ viewport: { width: 393, height: 852 }, isMobile: true, hasTouch: true })

  test("dashboard fits a phone screen", async ({ page }) => {
    await page.goto("/")
    await expect(page.getByText(/최근 1년 쓰기/)).toBeVisible()

    // The 53-week grid is wider than a phone; it must scroll inside its own box.
    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
    )
    expect(overflow).toBeLessThanOrEqual(1)
  })
})
