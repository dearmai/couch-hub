import { expect, test } from "@playwright/test"

const COUCHDB_URL = process.env.COUCHHUB_E2E_COUCHDB ?? "http://127.0.0.1:15984"
const ADMIN_USER = process.env.COUCHHUB_E2E_COUCHDB_USER ?? "admin"
const ADMIN_PASSWORD = process.env.COUCHHUB_E2E_COUCHDB_PASSWORD ?? "couchhub-dev"

const auth = "Basic " + Buffer.from(`${ADMIN_USER}:${ADMIN_PASSWORD}`).toString("base64")

/** Reads a single value straight from CouchDB, to confirm the wizard actually wrote it. */
async function couchConfig(section: string, key: string): Promise<string | null> {
  const res = await fetch(`${COUCHDB_URL}/_node/_local/_config/${section}/${key}`, {
    headers: { Authorization: auth },
  })
  if (!res.ok) return null
  return (await res.json()) as string
}

test.beforeAll(async () => {
  const res = await fetch(`${COUCHDB_URL}/_up`).catch(() => null)
  if (!res?.ok) {
    throw new Error(
      `CouchDB is not reachable at ${COUCHDB_URL}. Run \`make e2e\`, which starts it first.`,
    )
  }
})

test("first run redirects to the wizard", async ({ page }) => {
  await page.goto("/")
  await expect(page).toHaveURL(/\/setup$/)
  await expect(page.getByRole("heading", { name: "CouchHub 설치" })).toBeVisible()
})

test("guide step renders the embedded install document", async ({ page }) => {
  await page.goto("/setup")

  // The guide is markdown served by the Go binary and rendered client-side;
  // asserting on a heading from inside it proves the whole path works.
  await expect(page.getByRole("heading", { name: "CouchDB 설치 가이드" })).toBeVisible()
  await expect(page.getByText("주소 두 개를 구분하세요")).toBeVisible()

  await page.getByRole("button", { name: /연결 단계로/ }).click()
  await expect(page.getByRole("heading", { name: "CouchDB 연결" })).toBeVisible()
})

test("form rejects a non-http address before contacting the server", async ({ page }) => {
  await page.goto("/setup")
  await page.getByRole("button", { name: /연결 단계로/ }).click()

  await page.getByLabel("Obsidian 연동용 주소").fill("sync.example.com")
  await page.getByLabel("관리자 비밀번호").fill(ADMIN_PASSWORD)
  await page.getByRole("button", { name: /설정 확인/ }).click()

  await expect(page.getByText("http:// 또는 https:// 로 시작해야 합니다")).toBeVisible()
  // Still on the connect step: no request was made.
  await expect(page.getByRole("heading", { name: "CouchDB 연결" })).toBeVisible()
})

test("wrong credentials surface a specific error", async ({ page }) => {
  await page.goto("/setup")
  await page.getByRole("button", { name: /연결 단계로/ }).click()

  await page.getByLabel("CouchHub 연동용 주소").fill(COUCHDB_URL)
  await page.getByLabel("Obsidian 연동용 주소").fill("https://sync.example.com")
  await page.getByLabel("관리자 계정").fill(ADMIN_USER)
  await page.getByLabel("관리자 비밀번호").fill("definitely-wrong")
  await page.getByRole("button", { name: /설정 확인/ }).click()

  await expect(page.getByText("관리자 계정 또는 비밀번호가 올바르지 않습니다")).toBeVisible()
})

// Runs before provisioning, so the wizard is still reachable at "/".
test.describe("mobile viewport", () => {
  test.use({ viewport: { width: 393, height: 852 }, isMobile: true, hasTouch: true })

  test("wizard is usable at phone width without sideways scrolling", async ({ page }) => {
    await page.goto("/setup")
    await page.getByRole("button", { name: /연결 단계로/ }).click()
    await expect(page.getByRole("heading", { name: "CouchDB 연결" })).toBeVisible()

    await page.getByLabel("CouchHub 연동용 주소").fill(COUCHDB_URL)
    await page.getByLabel("Obsidian 연동용 주소").fill("https://sync.example.com")
    await page.getByLabel("관리자 계정").fill(ADMIN_USER)
    await page.getByLabel("관리자 비밀번호").fill(ADMIN_PASSWORD)
    await page.getByRole("button", { name: /설정 확인/ }).click()

    await expect(page.getByRole("heading", { name: "적용 전 확인" })).toBeVisible()

    // The diff table is deliberately wide; it must scroll inside its own box
    // rather than pushing the page sideways.
    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
    )
    expect(overflow).toBeLessThanOrEqual(1)
  })
})

test("provisions CouchDB end to end", async ({ page }) => {
  await page.goto("/setup")
  await page.getByRole("button", { name: /연결 단계로/ }).click()

  await page.getByLabel("프로필 이름").fill("e2e")
  await page.getByLabel("CouchHub 연동용 주소").fill(COUCHDB_URL)
  await page.getByLabel("Obsidian 연동용 주소").fill("https://sync.example.com")
  await page.getByLabel("관리자 계정").fill(ADMIN_USER)
  await page.getByLabel("관리자 비밀번호").fill(ADMIN_PASSWORD)
  await page.getByRole("button", { name: /설정 확인/ }).click()

  // Verify step: the diff table must list every setting before anything is written.
  await expect(page.getByRole("heading", { name: "적용 전 확인" })).toBeVisible()
  await expect(page.getByText("[cors] origins", { exact: true })).toBeVisible()
  // exact: "[chttpd] require_valid_user" is a prefix of
  // "[chttpd] require_valid_user_except_for_up", and both are on this table.
  await expect(page.getByText("[chttpd] require_valid_user", { exact: true })).toBeVisible()
  await expect(page.getByText("[chttpd] require_valid_user_except_for_up", { exact: true })).toBeVisible()

  await page.getByRole("button", { name: "적용", exact: true }).click()

  await expect(page.getByRole("heading", { name: "설치 완료" })).toBeVisible({ timeout: 60_000 })
  await expect(page.getByText("config: [cors] origins", { exact: true })).toBeVisible()

  // The UI claiming success is not enough - check CouchDB itself.
  expect(await couchConfig("cors", "origins")).toBe(
    "app://obsidian.md,capacitor://localhost,http://localhost",
  )
  expect(await couchConfig("chttpd", "require_valid_user")).toBe("true")
  expect(await couchConfig("couchdb", "max_document_size")).toBe("50000000")

  // Regression: require_valid_user makes /_up answer 401 unless the health-check
  // exemption is also set, which leaves every readiness probe reporting a
  // perfectly healthy CouchDB as down.
  const unauthenticatedUp = await fetch(`${COUCHDB_URL}/_up`)
  expect(unauthenticatedUp.status).toBe(200)
  // ...while everything else must still require credentials.
  const unauthenticatedDbs = await fetch(`${COUCHDB_URL}/_all_dbs`)
  expect(unauthenticatedDbs.status).toBe(401)

  // And the wizard should now be behind us.
  await page.getByRole("button", { name: /Vault 만들기/ }).click()
  await expect(page).toHaveURL(/\/vaults$/)
})

test("re-running the wizard reports nothing left to change", async ({ page }) => {
  // Depends on the previous test having provisioned the server.
  await page.goto("/setup")
  await page.getByRole("button", { name: /연결 단계로/ }).click()

  await page.getByLabel("CouchHub 연동용 주소").fill(COUCHDB_URL)
  await page.getByLabel("Obsidian 연동용 주소").fill("https://sync.example.com")
  await page.getByLabel("관리자 계정").fill(ADMIN_USER)
  await page.getByLabel("관리자 비밀번호").fill(ADMIN_PASSWORD)
  await page.getByRole("button", { name: /설정 확인/ }).click()

  await expect(page.getByText("이미 livesync 규격에 맞게 설정되어 있습니다")).toBeVisible()
  await expect(page.getByText("변경 0개")).toBeVisible()
})
