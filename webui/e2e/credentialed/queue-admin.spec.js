// The queue's administration, ticket 07 in .scratch/dashboard: purging jobs and queueing a
// batch that reprocesses torrents.
//
// These tests change the queue the rest of the suite counts, so they run in a Playwright
// project of their own that starts only once every other credentialed spec has finished, and
// they run one after another in this file's order (see `changesTheQueue` in
// playwright.config.js, and e2e/README.md, "Specs that change the queue"). Each reads what
// the queue holds from bitmagnet's own answer rather than from the seed, since the tests before
// it have changed it, and the last one empties it.
//
// Serially and never retried: a retry would find what its first try consumed gone (the failed
// jobs it purged, the queue it emptied), and a test after a failed one would find the queue
// in a state nobody meant. Serial mode skips the rest instead.

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

test.describe.configure({ mode: "serial", retries: 0 });

function graphqlQuery(request) {
  if (new URL(request.url()).pathname !== "/graphql") return null;
  return request.postDataJSON()?.query ?? null;
}

// The page's count of the queue: two looks at `jobs` that ask for no job.
function isCount(request) {
  const query = graphqlQuery(request);
  return query !== null && query.includes("jobs(input: {limit: 0, facets:") && query.includes("totalCount: true");
}

function isMutation(request, field) {
  const query = graphqlQuery(request);
  return query !== null && query.startsWith("mutation") && query.includes(field);
}

// bitmagnet's count, as the page was given it. elm-graphql aliases both `jobs` fields and the
// buckets' `value`, so they are found by what they hold rather than by name.
function tallyOf(body) {
  expect(body.errors).toBeUndefined();
  const looks = Object.values(body.data.queue);
  const scoped = looks.find((look) => look.aggregations);
  const valueOf = (bucket) => bucket[Object.keys(bucket).find((key) => key.startsWith("value"))];
  const counts = (buckets) => Object.fromEntries((buckets ?? []).map((bucket) => [valueOf(bucket), bucket.count]));
  return {
    queues: counts(scoped.aggregations.queue),
    statuses: counts(scoped.aggregations.status),
    everything: looks.find((look) => "totalCount" in look).totalCount,
  };
}

// The next count the page is answered, whose query `asks` approves.
function nextCount(page, asks = () => true) {
  return page
    .waitForResponse((response) => isCount(response.request()) && asks(graphqlQuery(response.request())))
    .then(async (response) => tallyOf(await response.json()));
}

// Every mutation the page sends, as it sends it, so a test can show one was not sent.
function mutationsSent(page) {
  const sent = [];
  page.on("request", (request) => {
    if (isMutation(request, "queue")) sent.push(graphqlQuery(request));
  });
  return sent;
}

function nextMutation(page, field) {
  return page.waitForResponse((response) => isMutation(response.request(), field));
}

function sum(counts) {
  return Object.values(counts).reduce((total, n) => total + n, 0);
}

// Signs the worker's administrator in and opens the page, resolving with bitmagnet's first count.
async function openAdministration(page, credentials) {
  await page.goto("/login");
  await signIn(page, credentials);
  const counted = nextCount(page);
  await page.goto("/admin/queue");
  await expect(page.getByRole("heading", { name: "Queue administration" })).toBeVisible();
  return counted;
}

// The chip for a value, by its label alone: its accessible name also carries the count.
function chip(page, label) {
  return page.getByRole("button", { name: new RegExp(`^${label} [\\d,]+$`) });
}

function purgePanel(page) {
  return page.getByRole("region", { name: "Purge jobs" });
}

function reprocessPanel(page) {
  return page.getByRole("region", { name: "Reprocess torrents" });
}

function jobRows(page) {
  return page.getByRole("table").getByRole("row").filter({ has: page.getByRole("button", { name: "Details" }) });
}

function jobsMatch(n) {
  return new RegExp(`^${n.toLocaleString("en-US")} jobs? match(es)?\\.$`);
}

test("an Identity without queue::mutate is neither offered the page nor let in", async ({
  page,
  browser,
  request,
  issuer,
  credentials,
}) => {
  // The administrator is offered it on the status page, and in the Identity menu.
  await signInAt(page, credentials, "/status", "Status");
  await expect(page.getByRole("link", { name: "Queue administration" })).toHaveAttribute("href", "/admin/queue");
  await page.goto("/admin/roles");
  await expect(page.getByRole("heading", { name: "Create a Role" })).toBeVisible();
  await page.getByRole("button", { name: credentials.username }).click();
  await expect(page.getByRole("link", { name: "Queue administration" })).toHaveAttribute("href", "/admin/queue");

  // A Role that may read the queue, and bitmagnet's health, and not change the queue.
  const reading = uniqueName("e2e-qread");
  await page.keyboard.press("Escape");
  await createRole(page, reading, ["queue::query", "health::query"]);

  await inAnotherBrowser(browser, async (them) => {
    const reader = await registerUser(them, request, issuer, "e2e-qread");
    await page.goto("/admin/users");
    await setRole(page, reader.username, reading);
    await signIn(them, reader);

    // The Role took: the queue's jobs may be read.
    await them.goto("/queue/jobs");
    await expect(them.getByRole("heading", { name: "Queue jobs" })).toBeVisible();
    await expect(jobRows(them).first()).toBeVisible();

    // But neither the status page nor the menu offers its administration, and the page
    // refuses it.
    await them.goto("/status");
    await expect(them.getByRole("link", { name: "Queue jobs" })).toBeVisible();
    await expect(them.getByRole("link", { name: "Queue administration" })).toHaveCount(0);
    await them.getByRole("button", { name: reader.username }).click();
    await expect(them.getByRole("link", { name: "Your User" })).toBeVisible();
    await expect(them.getByRole("link", { name: "Queue administration" })).toHaveCount(0);

    await them.goto("/admin/queue");
    await expect(them.getByText("Your Identity does not permit changing bitmagnet's queue.")).toBeVisible();
    await expect(them.getByRole("button", { name: "Purge…" })).toHaveCount(0);
    await expect(them.getByRole("button", { name: "Reprocess…" })).toHaveCount(0);
  });

  // Anonymous, which the fixture grants every read action and nothing that changes anything, is
  // refused too, and not sent to sign in instead.
  await inAnotherBrowser(browser, async (anonymous) => {
    await anonymous.goto("/status");
    await expect(anonymous.getByRole("link", { name: "Queue jobs" })).toBeVisible();
    await expect(anonymous.getByRole("link", { name: "Queue administration" })).toHaveCount(0);
    await anonymous.goto("/admin/queue");
    await expect(anonymous.getByText("Your Identity does not permit changing bitmagnet's queue.")).toBeVisible();
    expect(new URL(anonymous.url()).pathname).toBe("/admin/queue");
    await expect(anonymous.getByRole("button", { name: "Purge…" })).toHaveCount(0);
  });
});

test("purges the jobs chosen, only after asking, and the jobs table shows what bitmagnet left", async ({
  page,
  credentials,
}) => {
  const before = await openAdministration(page, credentials);

  // Nothing chosen is every job, and the page says how many bitmagnet counted.
  expect(sum(before.statuses)).toBe(before.everything);
  await expect(purgePanel(page).getByText(jobsMatch(before.everything))).toBeVisible();

  // Failed jobs in process_torrent. Each choice is counted afresh.
  let counted = nextCount(page, (query) => query.includes('queue: {aggregate: true, filter: ["process_torrent"]}'));
  await chip(page, "process_torrent").click();
  await counted;
  counted = nextCount(page, (query) => query.includes("status: {aggregate: true, filter: [failed]}"));
  await chip(page, "failed").click();
  const chosen = await counted;
  const failed = chosen.statuses.failed;
  expect(failed).toBeGreaterThan(0);
  await expect(purgePanel(page).getByText(jobsMatch(failed))).toBeVisible();

  // Asked about first, with what it would delete; keeping them sends nothing.
  const sent = mutationsSent(page);
  await page.getByRole("button", { name: "Purge…" }).click();
  const question = purgePanel(page).getByRole("group", { name: "Confirm" });
  await expect(question).toContainText("Purge every job in process_torrent with status failed?");
  await expect(question).toContainText(`${failed} job${failed === 1 ? "" : "s"} matched when last counted.`);
  await question.getByRole("button", { name: "Keep them" }).click();
  await expect(question).toHaveCount(0);
  expect(sent).toEqual([]);

  await page.getByRole("button", { name: "Purge…" }).click();
  const purged = nextMutation(page, "purgeJobs");
  const recounted = nextCount(page);
  await question.getByRole("button", { name: "Purge", exact: true }).click();

  const answer = await purged;
  expect(graphqlQuery(answer.request())).toContain('purgeJobs(input: {queues: ["process_torrent"], statuses: [failed]})');
  expect(await answer.json()).toEqual({ data: { queue: { purgeJobs: null } } });
  expect(sent).toHaveLength(1);

  // bitmagnet's count afterwards: the failed jobs in process_torrent are gone, and nothing else.
  const after = await recounted;
  expect(after.statuses.failed).toBe(0);
  for (const status of ["pending", "retry", "processed"]) {
    expect(after.statuses[status] ?? 0).toBe(chosen.statuses[status] ?? 0);
  }
  expect(after.everything).toBe(chosen.everything - failed);
  await expect(purgePanel(page).getByRole("status")).toHaveText(
    "bitmagnet purged every job in process_torrent with status failed. The counts above are what it holds now.",
  );
  await expect(purgePanel(page).getByText(jobsMatch(0))).toBeVisible();

  // The jobs table, opened from the page, lists none of them; the failed jobs of the other
  // queue are still there.
  await purgePanel(page).getByRole("link", { name: "See them in the jobs table" }).click();
  await expect(page).toHaveURL(/\/queue\/jobs\?queue=process_torrent&status=failed$/);
  await expect(page.getByText("No jobs match these filters.")).toBeVisible();

  await page.goto("/queue/jobs?status=failed");
  await expect(jobRows(page).first()).toBeVisible();
  for (const row of await jobRows(page).all()) {
    await expect(row.getByRole("cell").nth(1)).toHaveText("process_torrent_batch");
  }
});

test("queues a reprocessing batch only after asking, as the form said, and points to the jobs to watch it", async ({
  page,
  credentials,
}) => {
  const before = await openAdministration(page, credentials);
  const form = reprocessPanel(page);

  await form.getByLabel("Only torrents whose content record is of these types").check();
  await form.getByRole("button", { name: "movie", exact: true }).click();
  await form.getByRole("button", { name: "unclassified", exact: true }).click();
  await form.getByLabel("Classify from scratch").check();
  await form.getByLabel("Torrents per processing job").fill("50");
  await form.getByLabel("Torrents per batch job").fill("2000");

  const sent = mutationsSent(page);
  await form.getByRole("button", { name: "Reprocess…" }).click();
  const question = form.getByRole("group", { name: "Confirm" });
  await expect(question).toContainText("Queue a batch that reprocesses every torrent whose content record is movie or unclassified?");
  await expect(question).toContainText("queues a process_torrent job for every 50 of them; after 2,000 torrents it hands the rest on to a new batch job.");
  await expect(question).toContainText("Each torrent is classified from scratch");
  await expect(question).toContainText("No external API such as TMDB is asked.");
  await expect(question).not.toContainText("Delete");
  await question.getByRole("button", { name: "Don't queue it" }).click();
  await expect(question).toHaveCount(0);
  expect(sent).toEqual([]);

  await form.getByRole("button", { name: "Reprocess…" }).click();
  const queued = nextMutation(page, "enqueueReprocessTorrentsBatch");
  const recounted = nextCount(page);
  await question.getByRole("button", { name: "Queue the batch" }).click();

  const answer = await queued;
  expect(graphqlQuery(answer.request())).toContain(
    "enqueueReprocessTorrentsBatch(input: {purge: false, batchSize: 50, chunkSize: 2000, contentTypes: [movie, null], orphans: false, classifierRematch: true, apisDisabled: true, localSearchDisabled: true})",
  );
  expect(await answer.json()).toEqual({ data: { queue: { enqueueReprocessTorrentsBatch: null } } });

  // It added exactly one job, and took nothing away.
  const after = await recounted;
  expect(after.everything).toBe(before.everything + 1);
  expect(after.statuses.pending).toBe((before.statuses.pending ?? 0) + 1);

  // The page points to the jobs table, where the newest job is the batch, holding what was asked.
  const outcome = form.getByRole("status");
  await expect(outcome).toContainText("bitmagnet queued the batch.");
  await outcome.getByRole("link", { name: "Watch it in the queue's jobs" }).click();
  await expect(page).toHaveURL(/\/queue\/jobs$/);
  const newest = jobRows(page).first();
  await expect(newest.getByRole("cell").nth(0)).toHaveText("pending");
  await expect(newest.getByRole("cell").nth(1)).toHaveText("process_torrent_batch");

  const details = newest.getByRole("button", { name: "Details" });
  await details.click();
  const payload = JSON.parse(await page.locator(`#${await details.getAttribute("aria-controls")} .job-payload`).textContent());
  expect(payload).toMatchObject({
    BatchSize: 50,
    ChunkSize: 2000,
    ClassifyMode: 1,
    ClassifierFlags: { apis_enabled: false, local_search_enabled: false },
  });
  expect(payload.ContentTypes).toHaveLength(2);
  expect(payload.Orphans).toBeUndefined();
});

test("purging first empties the whole queue before the batch is queued, and says how much it deletes", async ({
  page,
  credentials,
}) => {
  const before = await openAdministration(page, credentials);
  expect(before.everything).toBeGreaterThan(1);
  const form = reprocessPanel(page);

  await form.getByLabel("Delete every job in the queue first").check();
  await form.getByRole("button", { name: "Reprocess…" }).click();
  const question = form.getByRole("group", { name: "Confirm" });
  await expect(question).toContainText(
    "Delete every job in bitmagnet's queue, then queue a batch that reprocesses every torrent in the index?",
  );
  await expect(question).toContainText(
    `${before.everything.toLocaleString("en-US")} jobs were in the queue when last counted, in every queue and status.`,
  );

  const queued = nextMutation(page, "enqueueReprocessTorrentsBatch");
  const recounted = nextCount(page);
  await question.getByRole("button", { name: "Queue the batch" }).click();
  const answer = await queued;
  expect(graphqlQuery(answer.request())).toContain("enqueueReprocessTorrentsBatch(input: {purge: true,");
  expect(await answer.json()).toEqual({ data: { queue: { enqueueReprocessTorrentsBatch: null } } });

  // bitmagnet emptied the queue, then queued the batch into it: one job is left.
  const after = await recounted;
  expect(after.everything).toBe(1);
  expect(after.statuses).toEqual({ pending: 1 });
  await expect(form.getByRole("status")).toContainText("bitmagnet emptied the queue and queued the batch.");

  await page.goto("/queue/jobs");
  await expect(jobRows(page)).toHaveCount(1);
  await expect(jobRows(page).first().getByRole("cell").nth(1)).toHaveText("process_torrent_batch");
});
