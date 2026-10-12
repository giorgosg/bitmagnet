// Stops the credentialed harness politely, before Playwright stops it rudely.
//
// e2e/harness/serve.js cleans up properly when it is asked to: it waits for the fixture
// server to drop its cloned database, which happens in an fx OnStop hook and takes a moment.
// Playwright's own web-server teardown does not leave that moment — measured, not assumed:
// without this, every run left a bitmagnet_test_* database behind on the fixture instance.
//
// So the shutdown is driven from here, where there is time to wait for it, and Playwright's
// kill afterwards finds nothing left to kill.

import fs from "fs";

const patience = 30_000;

export default async function stopHarness() {
  const file = process.env.MAGNES_E2E_CREDENTIALS;
  if (!file || !fs.existsSync(file)) return;

  const { pid } = JSON.parse(fs.readFileSync(file, "utf8"));
  if (!pid || !running(pid)) return;

  process.kill(pid, "SIGTERM");

  const deadline = Date.now() + patience;
  while (running(pid)) {
    if (Date.now() > deadline) {
      // Said rather than thrown: the tests have already run and their result is the point.
      // A leftover database is a nuisance, not a reason to report the suite as failed.
      console.error(
        `the credentialed harness (pid ${pid}) did not stop; ` +
          "a bitmagnet_test_* database may be left behind.",
      );

      return;
    }

    await new Promise((resolve) => setTimeout(resolve, 100));
  }
}

function running(pid) {
  try {
    // Signal 0 checks for the process without touching it.
    process.kill(pid, 0);

    return true;
  } catch {
    return false;
  }
}
