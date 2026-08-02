import { execFileSync } from "node:child_process"
import { expect, test, type Page } from "@playwright/test"

const COUCHDB_URL = process.env.COUCHHUB_E2E_COUCHDB ?? "http://127.0.0.1:15984"
const ADMIN_USER = process.env.COUCHHUB_E2E_COUCHDB_USER ?? "admin"
const ADMIN_PASSWORD = process.env.COUCHHUB_E2E_COUCHDB_PASSWORD ?? "couchhub-dev"
const PUBLIC_URL = "https://sync.example.com"

const auth = "Basic " + Buffer.from(`${ADMIN_USER}:${ADMIN_PASSWORD}`).toString("base64")

// A deliberately non-Latin name: Obsidian vault names are free-form, and this
// is the case where the derived CouchDB name cannot reuse any of the input.
const VAULT_NAME = "업무 노트"

/**
 * Reads the current vault straight from the API.
 *
 * Later tests resolve it this way rather than sharing state from the creation
 * test: Playwright discards a worker after a failure and restarts, which would
 * leave module-level variables empty for the remaining tests.
 */
async function currentVault(request: import("@playwright/test").APIRequestContext) {
  const vaults = await (await request.get("/api/vaults")).json()
  expect(vaults.length, "expected a vault to exist").toBeGreaterThan(0)
  return vaults[0] as { id: string; name: string; dbName: string; couchUser: string }
}

function couch(path: string, init?: RequestInit) {
  return fetch(`${COUCHDB_URL}${path}`, { ...init, headers: { Authorization: auth, ...init?.headers } })
}

/**
 * Reads a CopyField's value.
 *
 * Secret fields render masked bullets until revealed, so the toggle has to be
 * clicked first - reading the input directly would yield "••••".
 */
async function readField(page: Page, label: string): Promise<string> {
  const reveal = page.getByRole("button", { name: `${label} 표시`, exact: true })
  if (await reveal.isVisible().catch(() => false)) {
    await reveal.click()
  }
  return page.getByRole("textbox", { name: label, exact: true }).inputValue()
}

/**
 * The desktop paste path must be visible without any disclosure to open: on a
 * machine with no camera it is the only way in.
 */
async function expectPinSectionVisible(page: Page) {
  await expect(page.getByRole("heading", { name: /컴퓨터에서 붙여넣기로 설정/ })).toBeVisible()
  await expect(page.getByLabel("Setup PIN", { exact: true })).toBeVisible()
}

/**
 * Opens the section holding the credentials in the clear. It is behind a
 * disclosure because the encrypted code is what the panel offers by default.
 */
async function openPlainSection(page: Page) {
  const summary = page.getByText(/PIN 없이 연결하기/)
  await summary.click()
  await expect(page.getByRole("textbox", { name: "Setup URI (평문)", exact: true })).toBeVisible()
}

/** Decrypts a Setup URI with the same binary the server issued it from. */
function parseSetupURI(uri: string, pin: string): Record<string, unknown> {
  const out = execFileSync("../bin/couchhub", ["parse-setup-uri"], {
    input: JSON.stringify({ uri, uriPassphrase: pin }),
    encoding: "utf8",
    stdio: ["pipe", "pipe", "ignore"],
  })
  return JSON.parse(out)
}

// The wizard specs run first (workers: 1, alphabetical order), but this file
// must not depend on that - provision through the API if it has not happened.
test.beforeAll(async ({ request }) => {
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
})

test.afterAll(async ({ request }) => {
  // Leave no database behind for the next run.
  const vaults = await (await request.get("/api/vaults")).json()
  for (const v of vaults) {
    await request.delete(`/api/vaults/${v.id}?confirm=${encodeURIComponent(v.name)}`)
  }
})

test("creates a vault and issues a working Setup URI", async ({ page, request }) => {
  await page.goto("/vaults")

  await page.getByRole("button", { name: /Vault 만들기/ }).first().click()
  await page.getByLabel("Vault 이름").fill(VAULT_NAME)
  await page.getByRole("button", { name: "만들기", exact: true }).click()

  // Creation lands on the vault's own page, not in a dialog of credentials
  // nobody asked for: the code is issued there, one per device.
  await expect(page).toHaveURL(/\/vaults\/vault-[^/]+\?tab=clients$/)
  await page.getByRole("button", { name: /코드 발급/ }).click()
  await expect(page.getByRole("img", { name: "Setup URI QR code" })).toBeVisible()

  await openPlainSection(page)
  const couchPassword = await readField(page, "CouchDB 비밀번호")
  const passphrase = await readField(page, "E2EE 패스프레이즈")

  // A non-Latin display name has no legal CouchDB representation, so the
  // database name is generated. It must still be unique and legal.
  const { dbName } = await currentVault(request)
  expect(dbName).toMatch(/^[a-z][a-z0-9_$()+/-]*$/)

  // The disclosed QR is livesync's `?settingsQR=` form: unencrypted on purpose,
  // so a single scan configures the client with nothing to type. That means the
  // passphrase really is in there - assert it rather than assuming.
  const plainUri = await readField(page, "Setup URI (평문)")
  expect(plainUri.startsWith("obsidian://setuplivesync?settingsQR=")).toBeTruthy()
  // The plugin's "Enter Setup URI" dialog accepts a URI only when it starts with
  // "obsidian://setuplivesync?settings=", then decrypts it. The plain form is
  // therefore camera/link-only, and the UI has to say so - this pins the
  // distinction so the two never get conflated.
  expect(plainUri.startsWith("obsidian://setuplivesync?settings=")).toBeFalsy()
  await expect(page.getByText(/"Enter Setup URI" 창에 붙여넣을 수 없습니다/)).toBeVisible()
  expect(plainUri).toContain(couchPassword)
  expect(plainUri).toContain(passphrase)
  expect(plainUri).toContain(dbName)

  // The code the panel offers by default must behave the opposite way.
  await expectPinSectionVisible(page)
  const pin = (await page.getByLabel("Setup PIN", { exact: true }).innerText()).trim()
  expect(pin).toMatch(/^\d{6}$/)

  const encryptedUri = await readField(page, "Setup URI (PIN 보호)")
  expect(encryptedUri.startsWith("obsidian://setuplivesync?settings=")).toBeTruthy()
  for (const secret of [couchPassword, passphrase, pin]) {
    expect(encryptedUri).not.toContain(secret)
  }

  const settings = parseSetupURI(encryptedUri, pin)
  expect(settings.couchDB_URI).toBe(PUBLIC_URL)
  expect(settings.couchDB_DBNAME).toBe(dbName)
  expect(settings.couchDB_USER).toBe(`vault_${dbName}`)
  expect(settings.couchDB_PASSWORD).toBe(couchPassword)
  expect(settings.passphrase).toBe(passphrase)
  expect(settings.encrypt).toBe(true)

  // A wrong PIN must fail rather than yield anything.
  expect(() => parseSetupURI(encryptedUri, "000000")).toThrow()

  await page.goto("/vaults")
  await expect(page.getByRole("link", { name: new RegExp(VAULT_NAME) })).toBeVisible()
})

test("provisions the database, the account and its security document", async ({ request }) => {
  const { dbName } = await currentVault(request)

  expect((await couch(`/${dbName}`)).status).toBe(200)
  expect((await couch(`/_users/org.couchdb.user:vault_${dbName}`)).status).toBe(200)

  const security = await (await couch(`/${dbName}/_security`)).json()
  // Member-only: the vault account may read and write documents but must not be
  // able to change the database's own permissions.
  expect(security.members.names).toEqual([`vault_${dbName}`])
  expect(security.admins.names ?? []).toEqual([])
})

test("the vault account reaches its own database and nothing else", async ({ request }) => {
  const v = await currentVault(request)
  const issued = await request.post(`/api/vaults/${v.id}/setup-uri`)
  const { credentials } = await issued.json()
  const header =
    "Basic " + Buffer.from(`${credentials.couchUser}:${credentials.couchPassword}`).toString("base64")

  const own = await fetch(`${COUCHDB_URL}/${v.dbName}`, { headers: { Authorization: header } })
  expect(own.status).toBe(200)

  // _users is admin-only; a leaked Setup URI must not reach it. CouchDB answers
  // 401 rather than 403 for a valid non-admin account here - either way it is
  // denied, so accept both instead of pinning the exact code.
  const others = await fetch(`${COUCHDB_URL}/_users/_all_docs`, { headers: { Authorization: header } })
  expect([401, 403]).toContain(others.status)
})

test("each issue mints its own PIN, in place on the page", async ({ page, request }) => {
  const { id, dbName } = await currentVault(request)

  await page.goto("/vaults")
  await page.getByRole("link", { name: new RegExp(VAULT_NAME) }).click()
  await page.getByRole("tab", { name: /연결/ }).click()

  // No dialog: the code renders on the page it was asked for.
  await page.getByRole("button", { name: /코드 발급/ }).click()
  await expectPinSectionVisible(page)
  const firstPin = (await page.getByLabel("Setup PIN", { exact: true }).innerText()).trim()
  const firstUri = await readField(page, "Setup URI (PIN 보호)")
  expect(firstPin).toMatch(/^\d{6}$/)
  await expect(page.getByLabel("남은 시간")).toContainText(/^[0-4]:\d\d 뒤 만료$/)

  await page.getByRole("button", { name: /새 코드 발급/ }).click()
  await expect
    .poll(async () => (await page.getByLabel("Setup PIN", { exact: true }).innerText()).trim())
    .not.toBe(firstPin)

  const secondPin = (await page.getByLabel("Setup PIN", { exact: true }).innerText()).trim()
  const secondUri = await readField(page, "Setup URI (PIN 보호)")

  expect(parseSetupURI(secondUri, secondPin).couchDB_DBNAME).toBe(dbName)
  expect(() => parseSetupURI(secondUri, firstPin)).toThrow()

  // The vault carries the deadline the page counts down to, so the two cannot
  // disagree about when the PIN is replaced.
  const vault = await (await request.get(`/api/vaults/${id}`)).json()
  const remaining = new Date(vault.setupPinExpiresAt).getTime() - Date.now()
  expect(remaining).toBeGreaterThan(0)
  expect(remaining).toBeLessThanOrEqual(5 * 60 * 1000)

  // A previously issued URI is a separate blob and still opens with its own PIN:
  // minting a new one protects the next code, it does not revoke a code someone
  // already photographed alongside its PIN.
  expect(parseSetupURI(firstUri, firstPin).couchDB_DBNAME).toBe(dbName)
})

test("deletion requires the exact vault name", async ({ page, request }) => {
  const { dbName } = await currentVault(request)

  await page.goto("/vaults")
  await page.getByRole("link", { name: new RegExp(VAULT_NAME) }).click()

  await page.getByRole("tab", { name: /관리/ }).click()
  await page.getByRole("button", { name: "삭제", exact: true }).click()

  const confirmButton = page.getByRole("button", { name: /영구 삭제/ })
  await expect(confirmButton).toBeDisabled()

  await page.getByLabel(/확인을 위해 Vault 이름/).fill("wrong name")
  await expect(confirmButton).toBeDisabled()

  await page.getByLabel(/확인을 위해 Vault 이름/).fill(VAULT_NAME)
  await expect(confirmButton).toBeEnabled()
  await confirmButton.click()

  await expect(page).toHaveURL(/\/vaults$/)
  await expect(page.getByText("아직 Vault가 없습니다.")).toBeVisible()

  // The database and account must be gone from CouchDB too, not just the list.
  expect((await couch(`/${dbName}`)).status).toBe(404)
  expect((await couch(`/_users/org.couchdb.user:vault_${dbName}`)).status).toBe(404)
})

// Runs last: it creates its own vault, and the deletion test above expects an
// empty list.
test.describe("mobile viewport", () => {
  test.use({ viewport: { width: 393, height: 852 }, isMobile: true, hasTouch: true })

  test("the issued code fits a phone screen and stays scannable", async ({ page }) => {
    await page.goto("/vaults")
    await page.getByRole("button", { name: /Vault 만들기/ }).first().click()
    await page.getByLabel("Vault 이름").fill("mobile-fit")
    await page.getByRole("button", { name: "만들기", exact: true }).click()

    await page.getByRole("button", { name: /코드 발급/ }).click()
    const qr = page.getByRole("img", { name: "Setup URI QR code" })
    await expect(qr).toBeVisible()

    // A dense QR is close to 600px square at its intrinsic size. It has to be
    // scaled to the viewport rather than scrolled sideways to - and not scaled
    // so far down that a camera cannot read it.
    const qrBox = await qr.boundingBox()
    expect(qrBox!.width).toBeLessThanOrEqual(393)
    expect(qrBox!.width).toBeGreaterThanOrEqual(200)

    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)
    expect(overflow).toBeLessThanOrEqual(1)
  })
})

// --- adopting a database that predates CouchHub ----------------------------

const ADOPT_DB = "e2e_adopted"

test.describe("adopting an existing database", () => {
  test.beforeAll(async () => {
    await couch(`/${ADOPT_DB}`, { method: "DELETE" })
    expect((await couch(`/${ADOPT_DB}`, { method: "PUT" })).status).toBe(201)

    // A document that must survive being adopted: the whole point is that
    // CouchHub takes over a database without touching its contents.
    const res = await couch(`/${ADOPT_DB}`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ _id: "predates-couchhub", note: "keep me" }),
    })
    expect(res.status).toBe(201)
  })

  test.afterAll(async () => {
    await couch(`/${ADOPT_DB}`, { method: "DELETE" })
  })

  test("requires the existing passphrase before it will adopt", async ({ page }) => {
    await page.goto("/vaults")
    await page.getByRole("button", { name: /기존 DB 추가/ }).first().click()

    await page.getByRole("combobox", { name: "데이터베이스" }).click()
    await page.getByRole("option", { name: new RegExp(ADOPT_DB) }).click()
    await page.getByRole("button", { name: "추가", exact: true }).click()

    // Generating a passphrase would leave the client unable to read anything
    // already stored, so it has to be supplied.
    await expect(page.getByText("기존 Vault가 쓰던 패스프레이즈를 입력하세요")).toBeVisible()
  })

  test("adopts without creating, emptying or locking out the database", async ({ page, request }) => {
    const before = await (await couch(`/${ADOPT_DB}`)).json()

    await page.goto("/vaults")
    await page.getByRole("button", { name: /기존 DB 추가/ }).first().click()

    await page.getByRole("combobox", { name: "데이터베이스" }).click()
    await page.getByRole("option", { name: new RegExp(ADOPT_DB) }).click()
    await page.getByLabel("기존 E2EE 패스프레이즈").fill("the-original-passphrase")
    await page.getByRole("button", { name: "추가", exact: true }).click()

    // Adoption lands on the vault's page, same as creation.
    await expect(page).toHaveURL(/\/vaults\/vault-[^/]+\?tab=clients$/)

    // The document is still there and the database was not recreated.
    const after = await (await couch(`/${ADOPT_DB}`)).json()
    expect(after.doc_count).toBe(before.doc_count)
    expect((await couch(`/${ADOPT_DB}/predates-couchhub`)).status).toBe(200)

    // The account was added to _security rather than replacing what was there.
    const security = await (await couch(`/${ADOPT_DB}/_security`)).json()
    expect(security.members.names).toContain(`vault_${ADOPT_DB}`)

    // The passphrase the operator supplied is the one issued to clients - not a
    // freshly generated one, which would decrypt nothing.
    const vaults = await (await request.get("/api/vaults")).json()
    const adopted = vaults.find((v: { dbName: string }) => v.dbName === ADOPT_DB)
    expect(adopted.adopted).toBe(true)

    const issued = await request.post(`/api/vaults/${adopted.id}/setup-uri`)
    const { credentials } = await issued.json()
    expect(credentials.e2eePassphrase).toBe("the-original-passphrase")
  })

  test("removing an adopted vault can keep the database", async ({ page, request }) => {
    const vaults = await (await request.get("/api/vaults")).json()
    const adopted = vaults.find((v: { dbName: string }) => v.dbName === ADOPT_DB)

    await page.goto(`/vaults/${adopted.id}?tab=manage`)
    await page.getByRole("button", { name: "삭제", exact: true }).click()

    // Defaults to keeping data for an adopted database.
    await expect(page.getByLabel("데이터는 남기기")).toBeChecked()

    await page.getByLabel(/확인을 위해 Vault 이름/).fill(adopted.name)
    await page.getByRole("button", { name: /목록에서 제거/ }).click()
    await expect(page).toHaveURL(/\/vaults$/)

    // Database intact, CouchHub's account gone.
    expect((await couch(`/${ADOPT_DB}`)).status).toBe(200)
    expect((await couch(`/${ADOPT_DB}/predates-couchhub`)).status).toBe(200)
    expect((await couch(`/_users/org.couchdb.user:vault_${ADOPT_DB}`)).status).toBe(404)

    const security = await (await couch(`/${ADOPT_DB}/_security`)).json()
    expect(security.members?.names ?? []).not.toContain(`vault_${ADOPT_DB}`)
  })
})
