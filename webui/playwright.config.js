// End-to-end tests: the real Elm bundle, in a real browser, against a real bitmagnet.
//
// These cover what elm-test cannot reach. `Test.Html` renders a view function to virtual
// DOM and asserts about the result, which never runs Elm's runtime — so focus, history,
// navigation, and anything that depends on a response actually arriving are all invisible
// to it. The login field's focus bug was exactly that shape: every unit test passed.
//
// The suite deliberately needs no credentials, so it can run unattended. See
// e2e/README.md for what that leaves uncovered and what it would take to close.

import { defineConfig, devices } from "@playwright/test";

const port = Number(process.env.MAGNES_E2E_PORT || 8443);
const baseURL = `https://localhost:${port}`;

export default defineConfig({
  testDir: "e2e",
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  reporter: process.env.CI ? "github" : "list",
  use: {
    baseURL,
    // The development certificate is trusted on a developer's machine (see
    // docs/serving-and-testing.md) but not inside Playwright's bundled browser, which
    // brings its own profile. The origin is localhost and the server is the one this
    // config just started, so there is nothing for the check to protect here.
    ignoreHTTPSErrors: true,
    trace: "on-first-retry",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
  webServer: {
    // dev.js reads the upstream from the gitignored .dev/env, so no host is named here.
    command: `env PORT=${port} npm run dev`,
    url: baseURL,
    ignoreHTTPSErrors: true,
    reuseExistingServer: !process.env.CI,
    timeout: 120_000,
  },
});
