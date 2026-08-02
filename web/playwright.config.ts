import { defineConfig, devices } from "@playwright/test"

// End-to-end tests drive the real binary against a real CouchDB, because the
// parts most likely to break - the config diff and the provisioning writes -
// only exist in that combination. `make e2e` starts the CouchDB container and
// builds the binary first.
const PORT = 10040
const COUCHDB_URL = process.env.COUCHHUB_E2E_COUCHDB ?? "http://127.0.0.1:15984"

export default defineConfig({
  testDir: "./e2e",
  fullyParallel: false, // the tests share one CouchDB and one store
  workers: 1,
  forbidOnly: !!process.env.CI,
  retries: 0,
  reporter: process.env.CI ? "list" : [["list"]],

  use: {
    baseURL: `http://127.0.0.1:${PORT}`,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },

  // One project only: the wizard mutates shared state (the store flips to
  // "provisioned"), so a second project replaying the same specs would start
  // from a different first-run state. Mobile layout is covered by a
  // viewport-scoped block inside the spec instead.
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],

  webServer: {
    // A fresh store per run, so the wizard always starts from "needs setup".
    command: `rm -rf .e2e-data && COUCHHUB_SECRET=e2e-secret COUCHHUB_ADDR=:${PORT} COUCHHUB_DATA_DIR=.e2e-data ../bin/couchhub serve`,
    url: `http://127.0.0.1:${PORT}/api/health`,
    reuseExistingServer: false,
    timeout: 30_000,
    stdout: "pipe",
    stderr: "pipe",
    env: { COUCHHUB_E2E_COUCHDB: COUCHDB_URL },
  },
})

export { COUCHDB_URL, PORT }
