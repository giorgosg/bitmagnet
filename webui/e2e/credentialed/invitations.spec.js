// Invitation administration, driven against a real bitmagnet (ticket 21). Until now it had
// only been rendered behind a stubbed API.
//
// The list is everyone's: other workers mint Invitations all through a run. So each test
// finds its own Invitations by their codes, and never acts on one it did not make.

import crypto from "crypto";

import { registerUser, signIn, expect, test } from "../support/credentialed.js";

async function asAdministrator(page, credentials) {
  await page.goto("/login");
  await signIn(page, credentials);
  await page.goto("/admin/invitations");
  await expect(page.getByRole("heading", { name: "Invite someone" })).toBeVisible();
}

// Makes an Invitation through the form and returns its registration link, as the screen
// announces it.
async function invite(page, { role, expires }) {
  await page.getByLabel("Role").selectOption(role);
  await page.getByLabel("Expires").selectOption({ label: expires });
  await page.getByRole("button", { name: "Create Invitation" }).click();

  const created = page.getByRole("status");
  await expect(created).toContainText(role);
  return created.getByRole("link").getAttribute("href");
}

// The listed Invitation behind a link.
function listed(page, link) {
  return page.getByRole("listitem").filter({ has: page.getByRole("link", { name: link }) });
}

test.describe("an administrator", () => {
  test.beforeEach(async ({ page, credentials }) => {
    await asAdministrator(page, credentials);
  });

  test("creates an Invitation with a Role and an expiry, and lists it", async ({ page }) => {
    const link = await invite(page, { role: "editor", expires: "7 days" });

    // At the origin root the link is the registration route itself. Under a mount it carries
    // the mount, which Invitations' own tests check: this harness serves at the root.
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

    // Registered from a second browser context, as the person the link was sent to would.
    const theirs = await browser.newContext();
    try {
      const them = await theirs.newPage();
      const code = new URL(link, "https://magnes.test").searchParams.get("code");
      const username = `e2e-inv-${crypto.randomBytes(3).toString("hex")}`;
      await them.goto(link);
      await expect(them.getByLabel("Invitation code")).toHaveValue(code);
      await them.getByLabel("Username").fill(username);
      await them
        .getByLabel("Password", { exact: true })
        .fill(crypto.randomBytes(24).toString("base64url"));
      await them.getByRole("button", { name: "Register" }).click();
      await expect(them.getByRole("button", { name: "Sign in" })).toBeVisible();

      await page.reload();
      const row = listed(page, link);
      await expect(row).toContainText(`Claimed by ${username}`);
      await expect(row.getByRole("button", { name: "Withdraw" })).toHaveCount(0);
    } finally {
      await theirs.close();
    }
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
    await expect(row.getByRole("button", { name: "Withdraw" })).toBeEnabled();

    await row.getByRole("button", { name: "Withdraw" }).click();
    await row.getByRole("button", { name: "Withdraw" }).click();

    // The list is refetched from the server: the row is gone because bitmagnet deleted it.
    await expect(listed(page, link)).toHaveCount(0);
    await page.reload();
    await expect(page.getByRole("heading", { name: "Invite someone" })).toBeVisible();
    await expect(listed(page, link)).toHaveCount(0);
  });
});

test("is refused to an Identity without administration", async ({ page, request, issuer }) => {
  const ordinary = await registerUser(page, request, issuer, "e2e-noinv");
  await signIn(page, ordinary);

  await page.goto("/admin/invitations");

  await expect(page.getByText("Your Identity does not permit administration.")).toBeVisible();
  await expect(page.getByRole("button", { name: "Create Invitation" })).toHaveCount(0);
});
