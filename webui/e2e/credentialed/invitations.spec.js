// Invitation administration, driven against a real bitmagnet (ticket 21). Until now it had
// only been rendered behind a stubbed API.
//
// The list is everyone's: other workers mint Invitations all through a run. So each test
// finds its own Invitations by their codes, and never acts on one it did not make. They are
// on the first page because bitmagnet lists the newest first (`listInvitations` orders by
// `created_at` descending), and fifty would have to be minted between a test's own and its
// next look for one to be pushed off it.
//
// Registration links under a base path are not checked here, because this harness serves at
// the origin root. Invitations' unit tests check them; see e2e/README.md.

import { inAnotherBrowser, registerThrough, signInAt, expect, test } from "../support/credentialed.js";

test.beforeEach(async ({ page, credentials }) => {
  await signInAt(page, credentials, "/admin/invitations", "Invite someone");
});

// Makes an Invitation through the form and returns its registration link, as the screen
// announces it.
async function invite(page, { role, expires }) {
  await page.getByLabel("Role").selectOption(role);
  await page.getByLabel("Expires").selectOption({ label: expires });
  await page.getByRole("button", { name: "Create Invitation" }).click();

  const created = page.getByRole("status");
  await expect(created).toContainText(`for the Role ${role}.`);
  return created.getByRole("link").getAttribute("href");
}

// The listed Invitation behind a link.
function listed(page, link) {
  return page.getByRole("listitem").filter({ has: page.getByRole("link", { name: link }) });
}

test("creates an Invitation with a Role and an expiry, and lists it", async ({ page }) => {
  const link = await invite(page, { role: "editor", expires: "7 days" });
  expect(link).toMatch(/^\/register\?code=[0-9a-f]+$/);

  const row = listed(page, link);
  await expect(row).toContainText("editor");
  await expect(row).toContainText(/Expires: \d{4}-\d{2}-\d{2} \d{2}:\d{2}/);
  await expect(row).toContainText("Unclaimed, from");
  await expect(row.getByRole("button", { name: "Withdraw" })).toBeEnabled();
});

test("an Invitation used to register shows who claimed it, and offers no withdrawal", async ({
  page,
  browser,
}) => {
  const link = await invite(page, { role: "user", expires: "Never" });
  const code = new URL(link, "https://magnes.test").searchParams.get("code");

  // Registered from another browser, as the person the link was sent to would.
  const registered = await inAnotherBrowser(browser, (them) =>
    registerThrough(them, code, "e2e-inv"),
  );

  await page.reload();
  const row = listed(page, link);
  await expect(row).toContainText(`Claimed by ${registered.username}`);
  await expect(row.getByRole("button", { name: "Withdraw" })).toHaveCount(0);
});

test("withdraws an unclaimed Invitation only after asking, naming what it would grant", async ({
  page,
}) => {
  const link = await invite(page, { role: "admin", expires: "24 hours" });
  const row = listed(page, link);

  await row.getByRole("button", { name: "Withdraw" }).click();
  await expect(row).toContainText("can no longer register as admin");

  // Keeping it puts the row back as it was.
  await row.getByRole("button", { name: "Keep it" }).click();
  await expect(row.getByRole("button", { name: "Keep it" })).toHaveCount(0);
  await expect(row.getByRole("button", { name: "Withdraw" })).toBeEnabled();

  await row.getByRole("button", { name: "Withdraw" }).click();
  await row.getByRole("button", { name: "Withdraw" }).click();

  // The list is refetched from the server: the row is gone because bitmagnet deleted it.
  await expect(listed(page, link)).toHaveCount(0);
  await page.reload();
  await expect(page.getByRole("heading", { name: "Invite someone" })).toBeVisible();
  await expect(listed(page, link)).toHaveCount(0);
});
