// End-to-end tests: the real Elm bundle, in a real browser, against a real bitmagnet.
//
// These cover what elm-test cannot reach. `Test.Html` renders a view function to virtual
// DOM and asserts about the result, which never runs Elm's runtime — so focus, history,
// navigation, and anything that depends on a response actually arriving are all invisible
// to it. The login field's focus bug was exactly that shape: every unit test passed.
//
// There are two suites, and which one runs is decided here rather than by a flag on the
// command line, because they need different services:
//
//   npm run test:e2e              the credential-free suite. Every assertion is about being
//                                 refused, so it needs no password and no database — only a
//                                 bitmagnet to be refused by, named in the gitignored
//                                 .dev/env.
//
//   npm run test:e2e:credentialed the credentialed suite. It brings its own bitmagnet: a
//                                 fixture server over a clone of the btm-testdb seed
//                                 template, with a throwaway administrator registered
//                                 through the bootstrap Invitation it prints. Nothing is
//                                 left behind and no password exists anywhere.
//
// They are deliberately not run together. See e2e/README.md.

import path from "path";

import { defineConfig, devices } from "@playwright/test";

const credentialed = process.env.MAGNES_E2E_CREDENTIALED === "1";

// A port of its own, so a credentialed run does not adopt — or evict — a dev server someone
// already has open against their own instance.
const port = Number(process.env.MAGNES_E2E_PORT || (credentialed ? 8444 : 8443));
const baseURL = `https://localhost:${port}`;

// Where e2e/harness/serve.js writes this run's credentials and the test fixture reads them.
// Named once, here, because the two sides never meet: one is a plain node script and the
// other runs inside Playwright. .dev/ is gitignored and already holds local-only state.
// Resolved against the working directory, which npm scripts set to the repository root.
process.env.MAGNES_E2E_CREDENTIALS = path.resolve(".dev/e2e-credentials.json");

const credentialFree = {
  name: "chromium",
  testDir: "e2e",
  // The credentialed specs live under e2e/ too, and would fail here for the honest reason
  // that there is no User — which is not a failure worth reporting.
  testIgnore: "credentialed/**",
  use: { ...devices["Desktop Chrome"] },
};

const withCredentials = {
  name: "credentialed",
  testDir: "e2e/credentialed",
  use: { ...devices["Desktop Chrome"] },
};

export default defineConfig({
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
  projects: [credentialed ? withCredentials : credentialFree],
  // Only the credentialed run has anything to take down. See e2e/harness/teardown.js for
  // why Playwright's own web-server teardown is not enough.
  globalTeardown: credentialed ? "./e2e/harness/teardown.js" : undefined,
  webServer: {
    // dev.js reads the upstream from the gitignored .dev/env, so no host is named here.
    // The credentialed command names its own upstream instead, and starts it first.
    // node directly for the credentialed one, not through its npm script: npm does not
    // forward SIGTERM, and with it in the middle the harness is never told to stop — which
    // means the fixture server is never told either, and its cloned database is left behind.
    command: credentialed
      ? `env PORT=${port} node e2e/harness/serve.js`
      : `env PORT=${port} npm run dev`,
    url: baseURL,
    ignoreHTTPSErrors: true,
    // A credentialed run wants its own database and its own User, so it never adopts a
    // server that is already up — that one belongs to a previous run's, now dropped.
    reuseExistingServer: !credentialed && !process.env.CI,
    // Long enough for a cold `go build` of bitmagnet, which is the slow part the first time
    // and cached afterwards.
    timeout: credentialed ? 300_000 : 120_000,
  },
});
