// The status page and the header's health indicator, ticket 03 in .scratch/dashboard.
//
// These run against the fixture server because it is the one bitmagnet here that answers
// `health` and `workers` (bitmagnet #83): one real check, against the cloned database, and
// the workers production registers, with only the ones this stack runs marked started.
// See e2e/README.md, "What the fixture serves the operational pages".

import crypto from "crypto";

import { mintInvitation, signIn, expect, test } from "../support/credentialed.js";

test("Anonymous reaches the status page from the header", async ({ page }) => {
  await page.goto("/search");

  // The indicator is named by the same sentence the page leads with.
  await page.getByRole("link", { name: "bitmagnet is up." }).click();

  await expect(page.getByRole("heading", { name: "Status" })).toBeVisible();
  expect(new URL(page.url()).pathname).toBe("/status");
  await expect(page.getByText("bitmagnet is up.", { exact: true })).toBeVisible();
  await expect(page.getByRole("row", { name: /^Postgres Up / })).toBeVisible();
});

test("an administrator sees which workers are running", async ({ page, credentials }) => {
  await page.goto("/login");
  await signIn(page, credentials);
  await page.goto("/status");

  const workers = page.getByRole("table").filter({
    has: page.getByRole("columnheader", { name: "Worker" }),
  });
  await expect(workers.getByRole("row", { name: "HTTP server Started" })).toBeVisible();
  await expect(workers.getByRole("row", { name: "DHT crawler Not started" })).toBeVisible();
});

test("an ordinary User sees health but not workers", async ({ page, request, credentials }) => {
  // A User of its own, per e2e/README.md: the shared administrator holds `**`, and the
  // point here is the core `user` Role, which holds health::query and not workers::query.
  const code = await mintInvitation(request, credentials);
  const ordinary = {
    username: `e2e-status-${crypto.randomBytes(3).toString("hex")}`,
    password: crypto.randomBytes(24).toString("base64url"),
  };
  await page.goto(`/register?code=${code}`);
  await page.getByLabel("Username").fill(ordinary.username);
  await page.getByLabel("Password", { exact: true }).fill(ordinary.password);
  await page.getByRole("button", { name: "Register" }).click();
  await expect(page.getByRole("button", { name: "Sign in" })).toBeVisible();
  await signIn(page, ordinary);

  await page.goto("/status");

  await expect(page.getByRole("row", { name: /^Postgres Up / })).toBeVisible();
  await expect(page.getByText("Your Identity may not see bitmagnet's workers.")).toBeVisible();
  await expect(page.getByRole("columnheader", { name: "Worker" })).toHaveCount(0);
});

test.describe("the health poll", () => {
  // Elm's Time.every is a setInterval, so the page's clock decides when a poll is due, and
  // half a minute passes in no time. Only the requests are counted: the faked clock also
  // holds back requestAnimationFrame, so what is drawn is not the thing to look at.
  test.beforeEach(async ({ page }) => {
    await page.clock.install();
  });

  test("asks every 30 seconds while the tab shows, never while it is hidden", async ({
    page,
  }) => {
    const asked = healthRequests(page);
    await page.goto("/search");

    // Once the Identity is known: which fields are asked for depends on it.
    await expect.poll(() => asked.length).toBe(1);

    await page.clock.runFor(30_000);
    await expect.poll(() => asked.length).toBe(2);

    await setHidden(page, true);
    await page.clock.runFor(120_000);
    await settle(page);
    expect(asked.length).toBe(2);

    // Coming back asks at once rather than at the next poll, since the header is as old
    // as the moment the tab was hidden.
    await setHidden(page, false);
    await expect.poll(() => asked.length).toBe(3);
  });

  test("a tab opened in the background does not poll until it is shown", async ({ page }) => {
    await page.addInitScript(() => {
      Object.defineProperty(document, "hidden", { configurable: true, get: () => true });
    });
    const asked = healthRequests(page);
    await page.goto("/search");

    // The first answer is still asked for, so the header has something to show when the
    // tab is first looked at; nothing after it.
    await expect.poll(() => asked.length).toBe(1);
    await page.clock.runFor(120_000);
    await settle(page);
    expect(asked.length).toBe(1);
  });
});

function healthRequests(page) {
  const asked = [];
  page.on("request", (request) => {
    if (new URL(request.url()).pathname === "/graphql" && (request.postData() ?? "").includes("health")) {
      asked.push(request);
    }
  });
  return asked;
}

// Overrides what the page reads as its visibility, then tells it, the way the browser
// does when a tab is switched away from and back.
async function setHidden(page, hidden) {
  await page.evaluate((value) => {
    Object.defineProperty(document, "hidden", { configurable: true, get: () => value });
    Object.defineProperty(document, "visibilityState", {
      configurable: true,
      get: () => (value ? "hidden" : "visible"),
    });
    document.dispatchEvent(new Event("visibilitychange"));
  }, hidden);
}

// A request the poll should not have made would have been sent by now: Elm sends from the
// update that the timer fires, and the page has been given a real moment to do so.
async function settle(page) {
  await page.waitForTimeout(500);
}

test("a refreshed Identity loses its previous worker report immediately", async ({ page }) => {
  await page.goto("/status");
  await expect(page.getByRole("row", { name: "HTTP server Started" })).toBeVisible();

  let release;
  const held = new Promise((resolve) => { release = resolve; });
  await page.route("**/graphql", async (route) => {
    const query = route.request().postDataJSON().query;
    if (query.includes("identity")) {
      const response = await route.fetch();
      const body = await response.json();
      body.data.self.identity.permissions = [
        { namespace: "graphql", object: "health", action: "query" },
      ];
      await route.fulfill({ response, json: body });
    } else if (query.includes("health")) {
      await held;
      await route.continue();
    } else {
      await route.continue();
    }
  });

  try {
    await setHidden(page, true);
    await setHidden(page, false);
    await expect(page.getByText("Checking…", { exact: true })).toBeVisible();
    await expect(page.getByRole("columnheader", { name: "Worker" })).toHaveCount(0);
  } finally {
    release();
  }
  await expect(page.getByText("Your Identity may not see bitmagnet's workers.")).toBeVisible();
});

test("an unanswered health request becomes unavailable before the next poll", async ({ page }) => {
  test.setTimeout(35_000);
  let release;
  const held = new Promise((resolve) => { release = resolve; });
  await page.route("**/graphql", async (route) => {
    if (route.request().postDataJSON().query.includes("health")) {
      await held;
      await route.abort();
    } else {
      await route.continue();
    }
  });
  try {
    await page.goto("/status");
    await expect(page.getByRole("alert")).toBeVisible({ timeout: 25_000 });
    await expect(page.getByRole("link", { name: /bitmagnet's health is unavailable/ })).toBeVisible();
  } finally {
    release();
  }
});

test("the status page fits a narrow screen in both colour schemes", async ({ page }, testInfo) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/status");
  await expect(page.getByRole("row", { name: /^Postgres Up / })).toBeVisible();
  for (const colorScheme of ["light", "dark"]) {
    await page.emulateMedia({ colorScheme });
    await page.screenshot({ path: testInfo.outputPath(`status-${colorScheme}.png`), fullPage: true });
    expect(await page.evaluate(() => [...document.querySelectorAll("body *")]
      .filter((element) => element.getBoundingClientRect().right > window.innerWidth)
      .map((element) => `${element.tagName}.${element.className}`))).toEqual([]);
  }
});
