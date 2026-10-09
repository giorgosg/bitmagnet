// The queue's jobs, ticket 04 in .scratch/dashboard.
//
// The fixture server seeds jobs in every status across two queues and never runs them
// (`--seed-dashboard-data`; see e2e/README.md, "What the fixture serves the operational
// pages"). Nothing in the suite changes the queue, so every worker reads the same jobs.
// The tests still count what bitmagnet answers rather than the seed's numbers, so a
// change to the seed does not read as a defect here.

import { registerUser, signIn, signInAt, expect, test } from "../support/credentialed.js";

// The chip for a facet value, by its label alone: its accessible name also carries the count.
function chip(page, label) {
  return page.getByRole("button", { name: new RegExp(`^${label} [\\d,]+$`) });
}

async function chipCount(page, label) {
  const name = await chip(page, label).textContent();
  return Number(name.replace(label, "").replace(/,/g, ""));
}

function jobRows(page) {
  return page.getByRole("table").getByRole("row").filter({ has: page.getByRole("button", { name: "Details" }) });
}

test("an administrator finds the jobs from the status page, filters them by status, and the link keeps it", async ({
  page,
  credentials,
}) => {
  await signInAt(page, credentials, "/status", "Status");
  await page.getByRole("link", { name: "Queue jobs" }).click();
  await expect(page.getByRole("heading", { name: "Queue jobs" })).toBeVisible();
  expect(new URL(page.url()).pathname).toBe("/queue/jobs");

  const failed = await chipCount(page, "failed");
  expect(failed).toBeGreaterThan(0);

  await chip(page, "failed").click();
  await expect(page).toHaveURL(/\/queue\/jobs\?status=failed$/);
  await expect(chip(page, "failed")).toHaveAttribute("aria-pressed", "true");

  // The facet's count is the number of jobs the filter lists, every one of them failed.
  await expect(jobRows(page)).toHaveCount(Math.min(failed, 20));
  for (const row of await jobRows(page).all()) {
    await expect(row.getByRole("cell").first()).toHaveText(/^failed/);
  }
  await expect(page.getByText(new RegExp(`· ${failed} jobs?$`))).toBeVisible();

  // A facet's own filter is left out of its counts, so the other statuses are still
  // offered with what choosing them would add, and each queue's count is of failed jobs.
  expect(await chipCount(page, "failed")).toBe(failed);
  await expect(chip(page, "pending")).toHaveAttribute("aria-pressed", "false");
  const byQueue =
    (await chipCount(page, "process_torrent")) + (await chipCount(page, "process_torrent_batch"));
  expect(byQueue).toBe(failed);

  // The filtered list is a link: opened afresh, it is the same list.
  await page.reload();
  await expect(chip(page, "failed")).toHaveAttribute("aria-pressed", "true");
  await expect(jobRows(page)).toHaveCount(Math.min(failed, 20));
});

test("a job opens to show its whole payload and error", async ({ page, credentials }) => {
  await signInAt(page, credentials, "/queue/jobs?status=failed", "Queue jobs");

  const row = jobRows(page).first();
  const details = row.getByRole("button", { name: "Details" });
  await expect(details).toHaveAttribute("aria-expanded", "false");
  await details.click();
  await expect(details).toHaveAttribute("aria-expanded", "true");

  // The details row is the one the button controls.
  const opened = page.locator(`#${await details.getAttribute("aria-controls")}`);
  await expect(opened.getByRole("heading", { name: "Payload" })).toBeVisible();
  // The seed's payloads are compact JSON; shown, they are indented one key to a line.
  await expect(opened.locator(".job-payload")).toHaveText(/^\{\n {2}"run": \d+,\n {2}"seed": \d+\n\}$/);
  await expect(opened.locator(".job-error")).toHaveText(
    "seeded failure, so the error column has something to show",
  );

  await details.click();
  await expect(opened).toHaveCount(0);
});

test("ordering and paging are in the URL too", async ({ page, credentials }) => {
  await signInAt(page, credentials, "/queue/jobs", "Queue jobs");

  await page.getByLabel("Order jobs").selectOption({ label: "priority, highest number first" });
  await expect(page).toHaveURL(/\/queue\/jobs\?order=priority&direction=desc$/);
  await expect(page.getByLabel("Order jobs")).toHaveValue("priority-desc");

  // Past the last page, the page says how many there are rather than showing nothing.
  await page.goto("/queue/jobs?order=priority&direction=desc&page=99");
  await expect(page.getByText(/^There (is|are) only \d+ pages? of jobs\.$/)).toBeVisible();
  await page.getByRole("link", { name: "Previous" }).click();
  await expect(page).toHaveURL(/\/queue\/jobs\?order=priority&direction=desc(&page=\d+)?$/);
  await expect(jobRows(page).first()).toBeVisible();
  await expect(page.getByLabel("Order jobs")).toHaveValue("priority-desc");
});

test("an Identity without queue::query is refused, and not offered the page", async ({
  page,
  request,
  issuer,
}) => {
  // The core `user` Role holds health::query but not queue::query.
  const ordinary = await registerUser(page, request, issuer, "e2e-nojobs");
  await signIn(page, ordinary);

  await page.goto("/status");
  await expect(page.getByRole("heading", { name: "Status" })).toBeVisible();
  await expect(page.getByRole("link", { name: "Queue jobs" })).toHaveCount(0);

  await page.goto("/queue/jobs?status=failed");
  await expect(page.getByText("Your Identity does not permit reading bitmagnet's queue.")).toBeVisible();
  await expect(page.getByRole("table")).toHaveCount(0);
});
