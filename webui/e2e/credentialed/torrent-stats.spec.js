// The torrent timeline, ticket 06 in .scratch/dashboard.
//
// `dev fixture serve --seed-dashboard-data` moves a bounded set of torrent-source rows into
// the last hour, half of them with an old `created_at` so bitmagnet counts them as updated
// (see e2e/README.md, "What the fixture serves the operational pages"). The rest of the
// corpus is a snapshot whose timestamps are months old, so the last week holds those rows
// and nothing else. The tests still read what bitmagnet answers and compare the page with
// it, rather than with the seed's numbers, so a change to the seed does not read as a defect
// here. What they do rely on is that the seed leaves something to count.

import {
  createRole,
  inAnotherBrowser,
  registerUser,
  setRole,
  signIn,
  signInAt,
  uniqueName,
  expect,
  test,
} from "../support/credentialed.js";

// Resolves with bitmagnet's next answer to the timeline's query, as the page was given it.
function nextMetrics(page, predicate = () => true) {
  return page
    .waitForResponse(
      (response) => {
        const request = response.request();
        return (
          new URL(response.url()).pathname === "/graphql" &&
          (request.postData() ?? "").includes("metrics") &&
          predicate(request.postDataJSON().query)
        );
      },
    )
    .then(async (response) => {
      const { torrent } = (await response.json()).data;
      return { buckets: torrent.metrics.buckets, sources: torrent.listSources.sources };
    });
}

// The chart's numbers, read from the table that stands in for it for anyone who cannot see
// the drawing: a column of counts for each line, by the line's name.
async function chartedCounts(page) {
  const table = page.locator(".visually-hidden table");
  const headings = await table.locator("thead th").allTextContents();
  const rows = await table.locator("tbody tr").all();

  const totals = Object.fromEntries(headings.slice(1).map((heading) => [heading, 0]));
  for (const row of rows) {
    const cells = await row.locator("td").allTextContents();
    cells.forEach((cell, index) => {
      totals[headings[index + 1]] += Number(cell.replace(/,/g, ""));
    });
  }
  return totals;
}

function total(buckets, predicate = () => true) {
  return buckets.filter(predicate).reduce((sum, bucket) => sum + bucket.count, 0);
}

function sourcesWithData(buckets) {
  return [...new Set(buckets.map((bucket) => bucket.source))].sort();
}

function chip(page, label) {
  return page.getByRole("button", { name: label, exact: true });
}

test("an administrator finds the timeline from the status page, and it draws what bitmagnet counted", async ({
  page,
  credentials,
}) => {
  await signInAt(page, credentials, "/status", "Status");

  const answered = nextMetrics(page);
  await page.getByRole("link", { name: "Torrent statistics" }).click();
  await expect(page.getByRole("heading", { name: "Torrent statistics" })).toBeVisible();
  expect(new URL(page.url()).pathname).toBe("/stats/torrents");

  // The default is the last hour, a bucket a minute.
  const { buckets, sources } = await answered;
  await expect(page.getByRole("figure")).toBeVisible();
  await expect(page.getByRole("heading", { name: "Torrents per minute" })).toBeVisible();
  expect(total(buckets), "the fixture seeds torrent sources into the last hour").toBeGreaterThan(0);

  // Every torrent bitmagnet counted is in the chart's table, new and updated apart.
  const charted = await chartedCounts(page);
  const named = sourcesWithData(buckets);
  const nameOf = (key) => sources.find((source) => source.key === key)?.name ?? key;
  if (named.length <= 3) {
    for (const key of named) {
      expect(charted[`${nameOf(key)}: new`]).toBe(total(buckets, (b) => b.source === key && !b.updated));
      expect(charted[`${nameOf(key)}: updated`]).toBe(total(buckets, (b) => b.source === key && b.updated));
    }
  } else {
    expect(charted["Other sources: new"]).toBeGreaterThanOrEqual(0);
  }
  expect(Object.values(charted).reduce((sum, count) => sum + count, 0)).toBe(total(buckets));

  // Sources are named as bitmagnet names them, not by key.
  const legend = page.getByRole("listitem").filter({ hasText: /: (new|updated)$/ });
  for (const key of named.slice(0, 3)) {
    await expect(legend.filter({ hasText: `${nameOf(key)}: new` })).toHaveCount(1);
    if (nameOf(key) !== key) {
      await expect(legend.filter({ hasText: new RegExp(`^${key}: `) })).toHaveCount(0);
    }
  }
});

test("every control is in the URL, and the link opens the same look", async ({ page, credentials }) => {
  await signInAt(page, credentials, "/stats/torrents?timeframe=1w", "Torrent statistics");
  await expect(chip(page, "1 week")).toHaveAttribute("aria-pressed", "true");

  // The timeframe.
  let answered = nextMetrics(page);
  await chip(page, "6 hours").click();
  await expect(page).toHaveURL(/\/stats\/torrents\?timeframe=6h$/);
  const { buckets, sources } = await answered;
  expect(total(buckets), "the fixture seeds torrent sources into the last hour").toBeGreaterThan(0);

  // The resolution: a unit, and a multiplier typed into its own field.
  answered = nextMetrics(page, (query) => query.includes("bucketDuration: hour"));
  await chip(page, "hours").click();
  await expect(page).toHaveURL(/\/stats\/torrents\?timeframe=6h&resolution=hour$/);
  await answered;
  await expect(page.getByRole("heading", { name: "Torrents per hour" })).toBeVisible();

  await page.getByLabel("Buckets of how many").fill("2");
  await page.getByLabel("Buckets of how many").press("Enter");
  await expect(page).toHaveURL(/\/stats\/torrents\?timeframe=6h&resolution=hour&every=2$/);
  await expect(page.getByRole("heading", { name: "Torrents per 2 hours" })).toBeVisible();

  // A source with something counted, so the filter has something to show: bitmagnet is asked
  // for it alone, and answers with nothing else.
  const chosen = sources.find((source) => buckets.some((bucket) => bucket.source === source.key));
  expect(chosen, "a listed source with something counted").toBeTruthy();
  answered = nextMetrics(page, (query) => query.includes(`sources: ["${chosen.key}"]`));
  await chip(page, chosen.name).click();
  await expect(page).toHaveURL(
    new RegExp(`/stats/torrents\\?timeframe=6h&resolution=hour&every=2&source=${chosen.key}$`),
  );
  const only = await answered;
  expect(only.buckets.length).toBeGreaterThan(0);
  expect(only.buckets.every((bucket) => bucket.source === chosen.key)).toBe(true);
  await expect
    .poll(async () => Object.keys(await chartedCounts(page)))
    .toEqual([`${chosen.name}: new`, `${chosen.name}: updated`]);

  // Auto-refresh is in the URL too, and does not ask for anything by itself.
  await chip(page, "5 minutes").click();
  await expect(page).toHaveURL(
    new RegExp(`/stats/torrents\\?timeframe=6h&resolution=hour&every=2&refresh=5m&source=${chosen.key}$`),
  );

  // Opened afresh, the link is the same look.
  await page.reload();
  await expect(chip(page, "6 hours")).toHaveAttribute("aria-pressed", "true");
  await expect(chip(page, "hours")).toHaveAttribute("aria-pressed", "true");
  await expect(page.getByLabel("Buckets of how many")).toHaveValue("2");
  await expect(chip(page, "5 minutes")).toHaveAttribute("aria-pressed", "true");
  await expect(chip(page, chosen.name)).toHaveAttribute("aria-pressed", "true");
  await expect(page.getByRole("heading", { name: "Torrents per 2 hours" })).toBeVisible();

  // Choosing it again takes it away.
  await chip(page, chosen.name).click();
  await expect(page).toHaveURL(/\/stats\/torrents\?timeframe=6h&resolution=hour&every=2&refresh=5m$/);
});

test.describe("auto-refresh", () => {
  // Elm's Time.every is a setInterval, so the page's clock decides when a look is due. Only
  // the requests are counted: the faked clock also holds back requestAnimationFrame, so what
  // is drawn is not the thing to look at.
  test.beforeEach(async ({ page, credentials }) => {
    await signInAt(page, credentials, "/stats/torrents", "Torrent statistics");
    await page.clock.install();
  });

  test("is off until asked for, asks again at its interval while the tab shows, never while it is hidden", async ({
    page,
  }) => {
    const asked = metricsRequests(page);

    // Off by default: time passing asks for nothing.
    await page.goto("/stats/torrents");
    await expect.poll(() => asked.length).toBe(1);
    await settle(page);
    await page.clock.runFor(10 * 60_000);
    await settle(page);
    expect(asked.length).toBe(1);

    await page.goto("/stats/torrents?refresh=10s");
    await expect.poll(() => asked.length).toBe(2);
    await settle(page);

    await page.clock.runFor(10_000);
    await expect.poll(() => asked.length).toBe(3);
    await settle(page);

    await setHidden(page, true);
    await page.clock.runFor(60_000);
    await settle(page);
    expect(asked.length).toBe(3);

    // Coming back asks at once rather than at the next tick: what is on screen is as old as
    // the moment the tab was hidden.
    await setHidden(page, false);
    await expect.poll(() => asked.length).toBe(4);
  });

  test("changing how often to look does not ask again, and turning it off stops the asking", async ({
    page,
  }) => {
    const asked = metricsRequests(page);
    await page.goto("/stats/torrents");
    await expect.poll(() => asked.length).toBe(1);
    await settle(page);

    await chip(page, "10 seconds").click();
    await expect(page).toHaveURL(/\/stats\/torrents\?refresh=10s$/);
    await settle(page);
    expect(asked.length).toBe(1);

    await page.clock.runFor(10_000);
    await expect.poll(() => asked.length).toBe(2);
    await settle(page);

    await chip(page, "off").click();
    await expect(page).toHaveURL(/\/stats\/torrents$/);
    await page.clock.runFor(60_000);
    await settle(page);
    expect(asked.length).toBe(2);
  });
});

function metricsRequests(page) {
  const asked = [];
  page.on("request", (request) => {
    if (new URL(request.url()).pathname === "/graphql" && (request.postData() ?? "").includes("metrics")) {
      asked.push(request);
    }
  });
  return asked;
}

// Overrides what the page reads as its visibility, then tells it, the way the browser does
// when a tab is switched away from and back.
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

// A request the page should not have made would have been sent by now, and one it did make
// has been answered: the page is given a real moment, not a faked one.
async function settle(page) {
  await page.waitForTimeout(500);
}

test("an answer that comes back after the controls moved on does not replace the newer one", async ({
  page,
  credentials,
}) => {
  await signInAt(page, credentials, "/status", "Status");

  // The first look at the last hour is held back, and answered long after the page has
  // moved on, with a source nobody else would answer with.
  let release;
  const held = new Promise((resolve) => {
    release = resolve;
  });
  let holding = true;
  await page.route("**/graphql", async (route) => {
    const query = route.request().postDataJSON().query;
    if (holding && query.includes("metrics")) {
      holding = false;
      await held;
      await route.fulfill({
        json: {
          data: {
            torrent: {
              metrics: {
                buckets: [
                  { source: "stale", bucket: new Date().toISOString(), updated: false, count: 777 },
                ],
              },
              listSources: { sources: [{ key: "stale", name: "Stale source" }] },
            },
          },
        },
      });
    } else {
      await route.continue();
    }
  });

  await page.goto("/stats/torrents");
  await expect(page.getByRole("heading", { name: "Torrent statistics" })).toBeVisible();

  const newer = nextMetrics(page, (query) => query.includes("startTime"));
  await chip(page, "1 week").click();
  const { buckets } = await newer;
  expect(total(buckets), "the fixture seeds torrent sources into the last hour").toBeGreaterThan(0);
  await expect(page.getByRole("heading", { name: "Torrents per 60 minutes" })).toBeVisible();

  release();
  await settle(page);
  await expect(page.getByText("Stale source")).toHaveCount(0);
  await expect(chip(page, "1 week")).toHaveAttribute("aria-pressed", "true");
  const charted = await chartedCounts(page);
  expect(Object.values(charted).reduce((sum, count) => sum + count, 0)).toBe(total(buckets));
});

test("an ordinary User is offered the page and may open it", async ({ page, request, issuer }) => {
  // The core `user` Role holds torrent::query, so this is the one statistics page most
  // Identities can open. It holds health::query too, which is what lists the page; it holds
  // queue::query no more than it did for the jobs.
  const ordinary = await registerUser(page, request, issuer, "e2e-stats");
  await signIn(page, ordinary);

  await page.goto("/status");
  await expect(page.getByRole("heading", { name: "Status" })).toBeVisible();
  await expect(page.getByRole("link", { name: "Queue jobs" })).toHaveCount(0);

  const answered = nextMetrics(page);
  await page.getByRole("link", { name: "Torrent statistics" }).click();
  await expect(page.getByRole("heading", { name: "Torrent statistics" })).toBeVisible();
  const { buckets } = await answered;
  expect(buckets).toBeDefined();
  await expect(page.getByRole("figure")).toBeVisible();
});

test("an Identity without torrent::query is refused the page, and not offered it", async ({
  page,
  browser,
  request,
  issuer,
  credentials,
}) => {
  const healthOnly = uniqueName("e2e-nostats");
  await signInAt(page, credentials, "/admin/roles", "Create a Role");
  await createRole(page, healthOnly, ["health::query"]);

  await inAnotherBrowser(browser, async (them) => {
    const reader = await registerUser(them, request, issuer, "e2e-nostats");
    await page.goto("/admin/users");
    await setRole(page, reader.username, healthOnly);
    await signIn(them, reader);

    await them.goto("/status");
    await expect(them.getByRole("heading", { name: "Status" })).toBeVisible();
    await expect(them.getByRole("link", { name: "Torrent statistics" })).toHaveCount(0);

    await them.goto("/stats/torrents");
    await expect(them.getByText("Your Identity does not permit reading bitmagnet's torrents.")).toBeVisible();
    await expect(them.getByRole("figure")).toHaveCount(0);
  });
});
