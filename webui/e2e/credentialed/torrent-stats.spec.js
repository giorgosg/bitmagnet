// The torrent timeline, ticket 06 in .scratch/dashboard.
//
// `dev fixture serve --seed-dashboard-data` moves a bounded set of torrent-source rows into
// the last hour, half of them with an old `created_at` so bitmagnet counts them as updated
// (see e2e/README.md, "What the fixture serves the operational pages"). The rest of the
// corpus is a snapshot whose timestamps are months old, so the last week holds those rows
// and nothing else. The tests still read what bitmagnet answers and compare the page with
// it, rather than with the seed's numbers, so a change to the seed does not read as a defect
// here. What they do rely on is that the seed leaves something to count, and, in one place,
// that it counts more than one source.
//
// That second thing the fixture does not promise: `seedDashboardData` picks its rows with a
// `limit` and no `order by`, so which sources get rows follows how the table happens to be
// laid out. In the seed template loaded here (observed 2026-10-10 against bitmagnet
// `448a4cf7c`) all three sources, DHT, magnetico and RARBG, have some. Drawing several
// sources, and the more than three that are added together, is checked in the unit tests
// (`tests/TorrentStatsTest.elm`), which do not depend on the seed.

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

// Resolves with bitmagnet's next answer to the timeline's query, as the page was given it,
// and the query that asked for it. The predicate says which question is waited for, so an
// answer to an earlier one still on its way is not mistaken for it.
function nextMetrics(page, predicate = () => true) {
  return page
    .waitForResponse((response) => {
      const request = response.request();
      return (
        new URL(response.url()).pathname === "/graphql" &&
        (request.postData() ?? "").includes("metrics") &&
        predicate(request.postDataJSON().query)
      );
    })
    .then(async (response) => {
      const { torrent } = (await response.json()).data;
      return {
        buckets: torrent.metrics.buckets,
        sources: torrent.listSources.sources,
        query: response.request().postDataJSON().query,
      };
    });
}

// Every look the page asks for, as it is asked.
function metricsRequests(page) {
  const asked = [];
  page.on("request", (request) => {
    if (new URL(request.url()).pathname === "/graphql" && (request.postData() ?? "").includes("metrics")) {
      asked.push(request);
    }
  });
  return asked;
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

function sum(counts) {
  return Object.values(counts).reduce((all, count) => all + count, 0);
}

function total(buckets, predicate = () => true) {
  return buckets.filter(predicate).reduce((all, bucket) => all + bucket.count, 0);
}

function sourcesWithData(buckets) {
  return [...new Set(buckets.map((bucket) => bucket.source))].sort();
}

// What the table should hold for an answer: a pair of lines for each source with something
// counted, new and updated apart, as the page draws them. Up to three sources are drawn
// apart; with more, the first two, in the order bitmagnet lists them in, are, and the rest are
// added together.
function expectedLines(buckets, sources) {
  const listed = sources.map((source) => source.key);
  const order = [...listed, ...sourcesWithData(buckets).filter((key) => !listed.includes(key))];
  const nameOf = (key) => sources.find((source) => source.key === key)?.name ?? key;

  const groups =
    order.length <= 3
      ? order.map((key) => ({ name: nameOf(key), members: [key] }))
      : [
          ...order.slice(0, 2).map((key) => ({ name: nameOf(key), members: [key] })),
          { name: "Other sources", members: order.slice(2) },
        ];

  const lines = {};
  for (const group of groups) {
    const counted = buckets.filter((bucket) => group.members.includes(bucket.source));
    if (counted.length > 0) {
      lines[`${group.name}: new`] = total(counted, (bucket) => !bucket.updated);
      lines[`${group.name}: updated`] = total(counted, (bucket) => bucket.updated);
    }
  }
  return lines;
}

function chip(page, label) {
  return page.getByRole("button", { name: label, exact: true });
}

// The look is drawn, and none is on its way.
async function drawn(page) {
  await expect(page.getByRole("figure")).toBeVisible();
  await expect(page.locator('.stats[aria-busy="false"]')).toBeVisible();
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

// Two frames on, whatever the page was going to do with an answer it has been handed, it has
// done: Elm draws on the animation frame after it updates.
async function afterFrames(page) {
  await page.evaluate(() => new Promise((done) => requestAnimationFrame(() => requestAnimationFrame(done))));
}

// An answer from a source nobody else would answer with, so it can be told from the real one.
const staleAnswer = {
  data: {
    torrent: {
      metrics: {
        buckets: [{ source: "stale", bucket: new Date().toISOString(), updated: false, count: 777 }],
      },
      listSources: { sources: [{ key: "stale", name: "Stale source" }] },
    },
  },
};

// Holds back the first `count` looks the page asks for, and answers each with the stale
// answer once it is let go. Resolves `release()` to let them all go, and `delivered` when the
// last has arrived at the page.
async function holdLooks(page, count) {
  let release;
  const held = new Promise((resolve) => {
    release = resolve;
  });
  let remaining = count;
  await page.route("**/graphql", async (route) => {
    const query = route.request().postDataJSON().query;
    if (remaining > 0 && query.includes("metrics")) {
      remaining -= 1;
      await held;
      await route.fulfill({ json: staleAnswer });
    } else {
      await route.continue();
    }
  });
  return {
    release,
    // The page has been handed the stale answer. Not the same as having done anything with it.
    delivered: () =>
      page.waitForResponse(async (response) => (await response.text().catch(() => "")).includes("Stale source")),
  };
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
  const { buckets, sources, query } = await answered;
  await expect(page.getByRole("figure")).toBeVisible();
  await expect(page.getByRole("heading", { name: "Torrents per minute" })).toBeVisible();
  expect(total(buckets), "the fixture seeds torrent sources into the last hour").toBeGreaterThan(0);

  // The first column is whole: the look begins on the minute, not part way through one.
  expect(query).toMatch(/startTime: "[^"]*T\d\d:\d\d:00\.000Z"/);

  // Every torrent bitmagnet counted is in the chart's table, new and updated apart, and
  // under the sources as the page groups them.
  expect(
    sourcesWithData(buckets).length,
    "the seed counts more than one source (see the note at the top of this file)",
  ).toBeGreaterThan(1);
  const charted = await chartedCounts(page);
  expect(charted).toEqual(expectedLines(buckets, sources));
  expect(sum(charted)).toBe(total(buckets));

  // Sources are named as bitmagnet names them, not by key.
  const legend = page.getByRole("listitem").filter({ hasText: /: (new|updated)$/ });
  for (const key of sourcesWithData(buckets).slice(0, 3)) {
    const name = sources.find((source) => source.key === key)?.name ?? key;
    await expect(legend.filter({ hasText: `${name}: new` })).toHaveCount(1);
    if (name !== key) {
      await expect(legend.filter({ hasText: new RegExp(`^${key}: `) })).toHaveCount(0);
    }
  }
});

test("Anonymous is offered the timeline on the status page and may open it", async ({ page }) => {
  // The fixture is served with --anonymous-access, which grants the anon Role every read
  // action but auth, pprof and metrics (bitmagnet's `AnonymousReadSurface`), torrent::query
  // among them. A new installation grants none, and an administrator chooses.
  await page.goto("/status");
  await expect(page.getByRole("heading", { name: "Status" })).toBeVisible();
  await expect(page.getByRole("link", { name: "Torrent statistics" })).toBeVisible();

  const answered = nextMetrics(page);
  await page.getByRole("link", { name: "Torrent statistics" }).click();
  const { buckets, sources } = await answered;
  await drawn(page);
  expect(total(buckets), "the fixture seeds torrent sources into the last hour").toBeGreaterThan(0);
  expect(await chartedCounts(page)).toEqual(expectedLines(buckets, sources));
});

test("every control is in the URL, and the link opens the same look", async ({ page, credentials }) => {
  const asked = metricsRequests(page);
  await signInAt(page, credentials, "/stats/torrents?timeframe=1w", "Torrent statistics");
  await drawn(page);

  // A week by the default resolution is drawn in hours, and bitmagnet is asked for hours:
  // minutes, a week of them, would be merged into hours here anyway.
  await expect(page.getByRole("heading", { name: "Torrents per hour" })).toBeVisible();
  expect(asked[0].postDataJSON().query).toContain("bucketDuration: hour");
  await expect(chip(page, "1 week")).toHaveAttribute("aria-pressed", "true");

  // The timeframe.
  let answered = nextMetrics(page, (query) => query.includes("bucketDuration: minute"));
  await chip(page, "6 hours").click();
  await expect(page).toHaveURL(/\/stats\/torrents\?timeframe=6h$/);
  const { buckets, sources } = await answered;
  expect(total(buckets), "the fixture seeds torrent sources into the last hour").toBeGreaterThan(0);
  await drawn(page);

  // The resolution: a unit, and a multiplier typed into its own field.
  answered = nextMetrics(page, (query) => query.includes("bucketDuration: hour"));
  await chip(page, "hours").click();
  await expect(page).toHaveURL(/\/stats\/torrents\?timeframe=6h&resolution=hour$/);
  await answered;
  await drawn(page);
  await expect(page.getByRole("heading", { name: "Torrents per hour" })).toBeVisible();

  const field = page.getByLabel("Buckets of how many");
  await field.fill("2");
  await field.press("Enter");
  await expect(page).toHaveURL(/\/stats\/torrents\?timeframe=6h&resolution=hour&every=2$/);
  await expect(page.getByRole("heading", { name: "Torrents per 2 hours" })).toBeVisible();
  await drawn(page);

  // A number that is not whole comes to the whole one nearest. Here that is the multiplier
  // already in force, so the address does not change, and the field must say what the page
  // does, not what was typed into it.
  await field.fill("2.2");
  await field.press("Enter");
  await expect(page).toHaveURL(/\/stats\/torrents\?timeframe=6h&resolution=hour&every=2$/);
  await expect(page.getByLabel("Buckets of how many")).toHaveValue("2");

  // Nothing below 1 is a multiplier, so it comes to 1, and that is in the address.
  await page.getByLabel("Buckets of how many").fill("0");
  await page.getByLabel("Buckets of how many").press("Enter");
  await expect(page).toHaveURL(/\/stats\/torrents\?timeframe=6h&resolution=hour&every=1$/);
  await expect(page.getByLabel("Buckets of how many")).toHaveValue("1");
  await drawn(page);
  await page.getByLabel("Buckets of how many").fill("2");
  await page.getByLabel("Buckets of how many").press("Enter");
  await expect(page).toHaveURL(/\/stats\/torrents\?timeframe=6h&resolution=hour&every=2$/);
  await drawn(page);

  // A source with something counted, so the filter has something to show: bitmagnet is asked
  // for it alone, and answers with nothing else. There is more than one source to narrow
  // from, or choosing one would show nothing that was not there.
  expect(
    sourcesWithData(buckets).length,
    "the seed counts more than one source (see the note at the top of this file)",
  ).toBeGreaterThan(1);
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

  test("does not pile a look on one that is still on its way", async ({ page }) => {
    const asked = metricsRequests(page);
    await page.goto("/stats/torrents?refresh=10s");
    await expect.poll(() => asked.length).toBe(1);
    await settle(page);

    // The next look is never answered, as far as the page can tell.
    const looks = await holdLooks(page, 1);
    await page.clock.runFor(10_000);
    await expect.poll(() => asked.length).toBe(2);
    await settle(page);

    // The timer fires twice more over it, and asks for nothing.
    await page.clock.runFor(20_000);
    await settle(page);
    expect(asked.length).toBe(2);
    looks.release();
  });
});

test("an answer that comes back after the controls moved on does not replace the newer one", async ({
  page,
  credentials,
}) => {
  await signInAt(page, credentials, "/status", "Status");

  // The first look at the last hour is held back, and answered long after the page has moved
  // on, with a source nobody else would answer with.
  const looks = await holdLooks(page, 1);
  await page.goto("/stats/torrents");
  await expect(page.getByRole("heading", { name: "Torrent statistics" })).toBeVisible();

  const newer = nextMetrics(page, (query) => query.includes("bucketDuration: hour"));
  await chip(page, "1 week").click();
  const { buckets, sources } = await newer;
  expect(total(buckets), "the fixture seeds torrent sources into the last hour").toBeGreaterThan(0);
  await drawn(page);
  await expect(page.getByRole("heading", { name: "Torrents per hour" })).toBeVisible();

  const delivered = looks.delivered();
  looks.release();
  await delivered;
  await afterFrames(page);
  await expect(page.getByText("Stale source")).toHaveCount(0);
  await expect(chip(page, "1 week")).toHaveAttribute("aria-pressed", "true");
  expect(await chartedCounts(page)).toEqual(expectedLines(buckets, sources));
});

test("asking for a look at once gives up on one that is not answered, and its answer, if it comes, is dropped", async ({
  page,
  credentials,
}) => {
  await signInAt(page, credentials, "/stats/torrents", "Torrent statistics");
  await drawn(page);

  // The next look is held back, as if the instance were not answering.
  const looks = await holdLooks(page, 1);
  await page.getByRole("button", { name: "Refresh now" }).click();
  await expect(page.locator('.stats[aria-busy="true"]')).toBeVisible();

  // Asking again does not wait for it: that is a look of its own, and it is answered.
  const answered = nextMetrics(page);
  await page.getByRole("button", { name: "Refresh now" }).click();
  const { buckets, sources } = await answered;
  await drawn(page);
  expect(await chartedCounts(page)).toEqual(expectedLines(buckets, sources));

  // The one that was given up on arrives at last, and is not drawn over the newer one.
  const delivered = looks.delivered();
  looks.release();
  await delivered;
  await afterFrames(page);
  await expect(page.getByText("Stale source")).toHaveCount(0);
  expect(await chartedCounts(page)).toEqual(expectedLines(buckets, sources));
  await expect(page.locator('.stats[aria-busy="false"]')).toBeVisible();
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
  const { buckets, sources } = await answered;
  await drawn(page);
  expect(total(buckets), "the fixture seeds torrent sources into the last hour").toBeGreaterThan(0);
  expect(await chartedCounts(page)).toEqual(expectedLines(buckets, sources));
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
