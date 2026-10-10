// The queue's statistics, ticket 05 in .scratch/dashboard.
//
// `dev fixture serve --seed-dashboard-data` seeds jobs in every status across two queues,
// created over the last eight hours or so, each that has run having run half a minute after
// it was queued (see e2e/README.md, "What the fixture serves the operational pages"). The tests
// read what bitmagnet answers and work out from it what the page should draw, rather than
// using the seed's numbers, so a change to the seed does not read as a defect here. What they
// rely on is that the seed leaves something in every status, in more than one queue, inside the
// last day.

import { registerUser, signIn, signInAt, expect, test } from "../support/credentialed.js";

function isQueueMetrics(request) {
  const body = request.postData() ?? "";
  return new URL(request.url()).pathname === "/graphql" && body.includes("queue") && body.includes("metrics");
}

// Resolves with bitmagnet's next answer to the page's query, as the page was given it, and the
// query that asked for it. The predicate says which question is waited for, so an answer to an
// earlier one still on its way is not mistaken for it.
function nextMetrics(page, predicate = () => true) {
  return page
    .waitForResponse((response) => isQueueMetrics(response.request()) && predicate(response.request().postDataJSON().query))
    .then(async (response) => ({
      buckets: (await response.json()).data.queue.metrics.buckets,
      query: response.request().postDataJSON().query,
    }));
}

// Every look the page asks for, as it is asked.
function metricsRequests(page) {
  const asked = [];
  page.on("request", (request) => {
    if (isQueueMetrics(request)) asked.push(request);
  });
  return asked;
}

const units = { minute: 60_000, hour: 3_600_000, day: 86_400_000 };
const events = ["created", "processed", "failed"];
const statuses = ["pending", "retry", "failed", "processed"];

// What a query asked for: the unit bitmagnet buckets by, and where the timeframe began, if it
// has a start.
function askedIn(query) {
  const start = query.match(/startTime: "([^"]+)"/);
  return { unit: units[query.match(/bucketDuration: (\w+)/)[1]], start: start ? Date.parse(start[1]) : null };
}

// What happened to the jobs of an answer, as the ticket says to read it: created where a job
// was queued, whatever became of it; processed or failed where it last ran, for jobs now in that
// status. A bucket that was over before the timeframe began is not the timeframe's: bitmagnet
// answers with jobs queued long before that ran inside it.
function occurrences(buckets, query) {
  const { unit, start } = askedIn(query);
  const reaches = (at) => start === null || Date.parse(at) + unit > start;
  const happened = [];
  for (const bucket of buckets) {
    if (reaches(bucket.createdAtBucket)) {
      happened.push({ queue: bucket.queue, event: "created", count: bucket.count });
    }
    if (["processed", "failed"].includes(bucket.status) && bucket.ranAtBucket && reaches(bucket.ranAtBucket)) {
      happened.push({ queue: bucket.queue, event: bucket.status, count: bucket.count });
    }
  }
  return happened;
}

function queuesOf(buckets) {
  return [...new Set(buckets.map((bucket) => bucket.queue))].sort();
}

function total(items, predicate = () => true) {
  return items.filter(predicate).reduce((all, item) => all + item.count, 0);
}

// What the timeline's table should hold for an answer: a line for each event chosen (all of
// them where none was) of each queue that had anything happen, as the page draws them. Two
// queues are drawn apart; with more, the first is, and the rest are added together. With
// nothing chosen counted, there is no chart and no table.
function expectedLines(buckets, query, chosen = {}) {
  const happened = occurrences(buckets, query);
  const order = chosen.queues?.length ? chosen.queues : queuesOf(buckets);
  const groups =
    order.length <= 2
      ? order.map((queue) => ({ name: queue, members: [queue] }))
      : [{ name: order[0], members: [order[0]] }, { name: "Other queues", members: order.slice(1) }];
  const drawnEvents = events.filter((event) => !chosen.events?.length || chosen.events.includes(event));

  const lines = {};
  for (const group of groups) {
    const ours = happened.filter((occurrence) => group.members.includes(occurrence.queue));
    if (ours.length > 0) {
      for (const event of drawnEvents) {
        lines[`${group.name}: ${event}`] = total(ours, (occurrence) => occurrence.event === event);
      }
    }
  }
  return Object.values(lines).some((count) => count > 0) ? lines : {};
}

// What the totals' table should hold: every job in the answer, by queue and status.
function expectedTotals(buckets, queues = queuesOf(buckets)) {
  return Object.fromEntries(
    queues
      .filter((queue) => buckets.some((bucket) => bucket.queue === queue))
      .map((queue) => [
        queue,
        Object.fromEntries(
          statuses.map((status) => [status, total(buckets, (bucket) => bucket.queue === queue && bucket.status === status)]),
        ),
      ]),
  );
}

// A chart's table, which stands in for it for anyone who cannot see the drawing: its column
// headings and its rows of numbers.
async function chartTable(page, index) {
  const table = page.getByRole("figure").nth(index).locator("table");
  if ((await table.count()) === 0) return { headings: [], rows: [] };
  const headings = await table.locator("thead th").allTextContents();
  const rows = [];
  for (const row of await table.locator("tbody tr").all()) {
    rows.push({
      label: await row.locator("th").textContent(),
      cells: (await row.locator("td").allTextContents()).map((cell) => Number(cell.replace(/,/g, ""))),
    });
  }
  return { headings, rows };
}

// The timeline's numbers: a column of counts for each line, added up, by the line's name.
async function timelineCounts(page) {
  const { headings, rows } = await chartTable(page, 0);
  const totals = Object.fromEntries(headings.slice(1).map((heading) => [heading, 0]));
  for (const row of rows) {
    row.cells.forEach((cell, index) => {
      totals[headings[index + 1]] += cell;
    });
  }
  return totals;
}

// The totals' numbers: a row for each queue, by status.
async function totalsCounts(page) {
  const { headings, rows } = await chartTable(page, 1);
  return Object.fromEntries(
    rows.map((row) => [row.label, Object.fromEntries(row.cells.map((cell, index) => [headings[index + 1], cell]))]),
  );
}

function chip(page, label) {
  return page.getByRole("button", { name: label, exact: true });
}

// The look is drawn, and none is on its way.
async function drawn(page) {
  await expect(page.getByRole("figure").first()).toBeVisible();
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

// A request the page should not have made would have been sent by now: the page is given a
// real moment, not a faked one.
async function settle(page) {
  await page.waitForTimeout(500);
}

// Two frames on, whatever the page was going to do with an answer it has been handed, it has
// done: Elm draws on the animation frame after it updates.
async function afterFrames(page) {
  await page.evaluate(() => new Promise((done) => requestAnimationFrame(() => requestAnimationFrame(done))));
}

// An answer from a queue nobody else would answer with, so it can be told from the real one.
const staleAnswer = {
  data: {
    queue: {
      metrics: {
        buckets: [
          { queue: "stale_queue", status: "pending", createdAtBucket: new Date().toISOString(), ranAtBucket: null, count: 777 },
        ],
      },
    },
  },
};

// Holds back the first `count` looks the page asks for, and answers each with the stale answer
// once it is let go. `release()` lets them all go, and `delivered()` resolves when the last has
// arrived at the page.
async function holdLooks(page, count) {
  let release;
  const held = new Promise((resolve) => {
    release = resolve;
  });
  let remaining = count;
  await page.route("**/graphql", async (route) => {
    if (remaining > 0 && isQueueMetrics(route.request())) {
      remaining -= 1;
      await held;
      await route.fulfill({ json: staleAnswer });
    } else {
      await route.continue();
    }
  });
  return {
    release,
    delivered: () =>
      page.waitForResponse(async (response) => (await response.text().catch(() => "")).includes("stale_queue")),
  };
}

test("an administrator finds the queue's statistics from the status page, and both charts draw what bitmagnet counted", async ({
  page,
  credentials,
}) => {
  await signInAt(page, credentials, "/status", "Status");

  const answered = nextMetrics(page);
  await page.getByRole("link", { name: "Queue statistics" }).click();
  await expect(page.getByRole("heading", { name: "Queue statistics" })).toBeVisible();
  expect(new URL(page.url()).pathname).toBe("/stats/queue");

  // The default is everything the queue holds, by the hour: no start.
  const { buckets, query } = await answered;
  expect(query).toContain("bucketDuration: hour");
  expect(query).not.toContain("startTime");
  await drawn(page);
  await expect(chip(page, "all time")).toHaveAttribute("aria-pressed", "true");
  await expect(page.getByRole("heading", { name: /^Jobs per (\d+ )?hours?$/ })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Jobs by queue and status" })).toBeVisible();

  for (const status of statuses) {
    expect(total(buckets, (bucket) => bucket.status === status), `the fixture seeds ${status} jobs`).toBeGreaterThan(0);
  }
  expect(queuesOf(buckets).length, "the fixture seeds two queues").toBeGreaterThan(1);

  // Every event bitmagnet's answer makes is in the timeline's table, by queue, and every job is
  // in the totals', by queue and status.
  const lines = expectedLines(buckets, query);
  expect(lines[`${queuesOf(buckets)[0]}: failed`], "the fixture's failed jobs are on the timeline").toBeGreaterThan(0);
  expect(await timelineCounts(page)).toEqual(lines);
  expect(await totalsCounts(page)).toEqual(expectedTotals(buckets));
  await expect(page.locator('svg[role="img"]')).toHaveCount(2);
});

test("Anonymous is offered the page on the status page and may open it", async ({ page }) => {
  // The fixture is served with --anonymous-access, which grants the anon Role every read
  // action but auth, pprof and metrics, queue::query among them. A new installation grants
  // none, and an administrator chooses.
  await page.goto("/status");
  await expect(page.getByRole("heading", { name: "Status" })).toBeVisible();

  const answered = nextMetrics(page);
  await page.getByRole("link", { name: "Queue statistics" }).click();
  const { buckets, query } = await answered;
  await drawn(page);
  expect(total(buckets), "the fixture seeds queue jobs").toBeGreaterThan(0);
  expect(await timelineCounts(page)).toEqual(expectedLines(buckets, query));
  expect(await totalsCounts(page)).toEqual(expectedTotals(buckets));
});

test("every control is in the URL, choosing a queue or an event picks it out without asking again, and the link opens the same look", async ({
  page,
  credentials,
}) => {
  const asked = metricsRequests(page);
  await signInAt(page, credentials, "/stats/queue", "Queue statistics");
  await drawn(page);

  // The timeframe: a day has a start, on the hour, since a day of hours is drawn by the hour.
  let answered = nextMetrics(page, (query) => query.includes("startTime"));
  await chip(page, "1 day").click();
  await expect(page).toHaveURL(/\/stats\/queue\?timeframe=1d$/);
  let { buckets, query } = await answered;
  expect(query).toMatch(/startTime: "[^"]*T\d\d:00:00\.000Z"/);
  await drawn(page);
  expect(await timelineCounts(page)).toEqual(expectedLines(buckets, query));

  // The resolution: a unit, and a multiplier typed into its own field. Half-hours are asked
  // for by the minute.
  answered = nextMetrics(page, (query) => query.includes("bucketDuration: minute"));
  await chip(page, "minutes").click();
  await expect(page).toHaveURL(/\/stats\/queue\?timeframe=1d&resolution=minute$/);
  await page.getByLabel("Buckets of how many").fill("30");
  await page.getByLabel("Buckets of how many").press("Enter");
  await expect(page).toHaveURL(/\/stats\/queue\?timeframe=1d&resolution=minute&every=30$/);
  ({ buckets, query } = await answered);
  await drawn(page);
  await expect(page.getByRole("heading", { name: "Jobs per 30 minutes" })).toBeVisible();
  expect(await timelineCounts(page)).toEqual(expectedLines(buckets, query));
  const looks = asked.length;

  // A queue, picked out of the answer the page has: nothing is asked again, and both charts
  // narrow to it.
  expect(queuesOf(buckets).length, "the fixture seeds two queues").toBeGreaterThan(1);
  const queue = queuesOf(buckets)[0];
  await chip(page, queue).click();
  await expect(page).toHaveURL(new RegExp(`/stats/queue\\?timeframe=1d&resolution=minute&every=30&queue=${queue}$`));
  await expect.poll(() => timelineCounts(page)).toEqual(expectedLines(buckets, query, { queues: [queue] }));
  expect(await totalsCounts(page)).toEqual(expectedTotals(buckets, [queue]));

  // An event, the same way: only that queue's failures are drawn.
  const failures = expectedLines(buckets, query, { queues: [queue], events: ["failed"] });
  expect(failures[`${queue}: failed`], "the fixture's failed jobs are in the last day").toBeGreaterThan(0);
  await chip(page, "failed").click();
  await expect(page).toHaveURL(
    new RegExp(`/stats/queue\\?timeframe=1d&resolution=minute&every=30&queue=${queue}&event=failed$`),
  );
  await expect.poll(() => timelineCounts(page)).toEqual(failures);

  // Auto-refresh is in the URL too, and does not ask for anything by itself.
  await chip(page, "5 minutes").click();
  await expect(page).toHaveURL(
    new RegExp(`/stats/queue\\?timeframe=1d&resolution=minute&every=30&refresh=5m&queue=${queue}&event=failed$`),
  );
  await settle(page);
  expect(asked.length, "choosing a queue, an event or how often to look asks nothing").toBe(looks);

  // Opened afresh, the link is the same look.
  answered = nextMetrics(page);
  await page.reload();
  ({ buckets, query } = await answered);
  await drawn(page);
  await expect(chip(page, "1 day")).toHaveAttribute("aria-pressed", "true");
  await expect(chip(page, "minutes")).toHaveAttribute("aria-pressed", "true");
  await expect(page.getByLabel("Buckets of how many")).toHaveValue("30");
  await expect(chip(page, "5 minutes")).toHaveAttribute("aria-pressed", "true");
  await expect(chip(page, queue)).toHaveAttribute("aria-pressed", "true");
  await expect(chip(page, "failed")).toHaveAttribute("aria-pressed", "true");
  expect(await timelineCounts(page)).toEqual(expectedLines(buckets, query, { queues: [queue], events: ["failed"] }));

  // Choosing them again takes them away, and asks nothing.
  const reloaded = asked.length;
  await chip(page, "failed").click();
  await chip(page, queue).click();
  await expect(page).toHaveURL(/\/stats\/queue\?timeframe=1d&resolution=minute&every=30&refresh=5m$/);
  await expect.poll(() => timelineCounts(page)).toEqual(expectedLines(buckets, query));
  expect(asked.length).toBe(reloaded);
});

test.describe("auto-refresh", () => {
  // Elm's Time.every is a setInterval, so the page's clock decides when a look is due. Only the
  // requests are counted: the faked clock also holds back requestAnimationFrame, so what is
  // drawn is not the thing to look at.
  test.beforeEach(async ({ page, credentials }) => {
    await signInAt(page, credentials, "/stats/queue", "Queue statistics");
    await page.clock.install();
  });

  test("is off until asked for, asks again at its interval while the tab shows, never while it is hidden", async ({
    page,
  }) => {
    const asked = metricsRequests(page);

    await page.goto("/stats/queue");
    await expect.poll(() => asked.length).toBe(1);
    await settle(page);
    await page.clock.runFor(10 * 60_000);
    await settle(page);
    expect(asked.length).toBe(1);

    await page.goto("/stats/queue?refresh=10s");
    await expect.poll(() => asked.length).toBe(2);
    await settle(page);

    await page.clock.runFor(10_000);
    await expect.poll(() => asked.length).toBe(3);
    await settle(page);

    await setHidden(page, true);
    await page.clock.runFor(60_000);
    await settle(page);
    expect(asked.length).toBe(3);

    // Coming back asks at once rather than at the next tick: what is on screen is as old as the
    // moment the tab was hidden.
    await setHidden(page, false);
    await expect.poll(() => asked.length).toBe(4);
  });

  test("goes on looking at its interval while queues and events are chosen, and turning it off stops it", async ({
    page,
  }) => {
    const asked = metricsRequests(page);
    await page.goto("/stats/queue?refresh=10s");
    await expect.poll(() => asked.length).toBe(1);
    await settle(page);

    await chip(page, "processed").click();
    await expect(page).toHaveURL(/\/stats\/queue\?refresh=10s&event=processed$/);
    await settle(page);
    expect(asked.length).toBe(1);

    await page.clock.runFor(10_000);
    await expect.poll(() => asked.length).toBe(2);
    await settle(page);

    await chip(page, "off").click();
    await expect(page).toHaveURL(/\/stats\/queue\?event=processed$/);
    await page.clock.runFor(60_000);
    await settle(page);
    expect(asked.length).toBe(2);
  });

  test("does not pile a look on one that is still on its way", async ({ page }) => {
    const asked = metricsRequests(page);
    await page.goto("/stats/queue?refresh=10s");
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

  // The first look, at everything, is held back, and answered long after the page has moved
  // on, with a queue nobody else would answer with.
  const looks = await holdLooks(page, 1);
  await page.goto("/stats/queue");
  await expect(page.getByRole("heading", { name: "Queue statistics" })).toBeVisible();

  const newer = nextMetrics(page, (query) => query.includes("startTime"));
  await chip(page, "1 day").click();
  const { buckets, query } = await newer;
  expect(total(buckets), "the fixture seeds jobs in the last day").toBeGreaterThan(0);
  await drawn(page);

  const delivered = looks.delivered();
  looks.release();
  await delivered;
  await afterFrames(page);
  await expect(page.getByText("stale_queue")).toHaveCount(0);
  await expect(chip(page, "1 day")).toHaveAttribute("aria-pressed", "true");
  expect(await timelineCounts(page)).toEqual(expectedLines(buckets, query));
  expect(await totalsCounts(page)).toEqual(expectedTotals(buckets));
});

test("asking for a look at once gives up on one that is not answered, and its answer, if it comes, is dropped", async ({
  page,
  credentials,
}) => {
  await signInAt(page, credentials, "/stats/queue", "Queue statistics");
  await drawn(page);

  // The next look is held back, as if the instance were not answering.
  const looks = await holdLooks(page, 1);
  await page.getByRole("button", { name: "Refresh now" }).click();
  await expect(page.locator('.stats[aria-busy="true"]')).toBeVisible();

  // Asking again does not wait for it: that is a look of its own, and it is answered.
  const answered = nextMetrics(page);
  await page.getByRole("button", { name: "Refresh now" }).click();
  const { buckets, query } = await answered;
  await drawn(page);
  expect(await timelineCounts(page)).toEqual(expectedLines(buckets, query));

  // The one that was given up on arrives at last, and is not drawn over the newer one.
  const delivered = looks.delivered();
  looks.release();
  await delivered;
  await afterFrames(page);
  await expect(page.getByText("stale_queue")).toHaveCount(0);
  expect(await timelineCounts(page)).toEqual(expectedLines(buckets, query));
  await expect(page.locator('.stats[aria-busy="false"]')).toBeVisible();
});

test("an Identity without queue::query is refused the page, and not offered it", async ({ page, request, issuer }) => {
  // The core `user` Role holds health::query and torrent::query, but not queue::query.
  const ordinary = await registerUser(page, request, issuer, "e2e-noqstats");
  await signIn(page, ordinary);

  await page.goto("/status");
  await expect(page.getByRole("heading", { name: "Status" })).toBeVisible();
  await expect(page.getByRole("link", { name: "Torrent statistics" })).toBeVisible();
  await expect(page.getByRole("link", { name: "Queue statistics" })).toHaveCount(0);

  const asked = metricsRequests(page);
  await page.goto("/stats/queue?timeframe=1d");
  await expect(page.getByText("Your Identity does not permit reading bitmagnet's queue.")).toBeVisible();
  await expect(page.getByRole("figure")).toHaveCount(0);
  expect(asked.length).toBe(0);
});
