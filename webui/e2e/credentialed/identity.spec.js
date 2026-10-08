// What a real credential makes reachable for the first time.
//
// Everything here was previously covered only up to the point of being refused, or against
// a stubbed endpoint that proved the client's own behaviour and nothing about the server's.
// Each worker signs in as an administrator of its own, registered against a disposable
// bitmagnet — see e2e/README.md.

import { inAnotherBrowser, signIn, expect, test } from "../support/credentialed.js";

test.describe("a successful sign-in", () => {
  test("returns to the route the guard interrupted", async ({ page, credentials }) => {
    await page.goto("/admin/invitations");

    // Waiting for the form, not just for the navigation: the guard only redirects once
    // self.identity has answered, so reading the URL before that sees the route we asked
    // for and none of the returnUrl the guard is about to add.
    await expect(page.getByRole("heading", { name: "Sign in" })).toBeVisible();
    expect(new URL(page.url()).searchParams.get("returnUrl")).toBe("/admin/invitations");

    await signIn(page, credentials);

    await expect(page.getByRole("heading", { name: "Invitations" })).toBeVisible();
    expect(new URL(page.url()).pathname).toBe("/admin/invitations");
  });

  test("goes to the search when nothing was interrupted", async ({ page, credentials }) => {
    // Route.returnDestination falls back to the front page, not to the User overview:
    // someone who opened the form directly asked to sign in, not to read about themselves.
    await page.goto("/login");
    await signIn(page, credentials);

    expect(new URL(page.url()).pathname).toBe("/search");
  });

  test("does not leave the login form in history", async ({ page, credentials }) => {
    await page.goto("/search?q=linux");
    await page.goto("/account");
    await signIn(page, credentials);

    // Both hops replaced their entry — the guard's redirect to the form, and the form's
    // departure from it — so one step back reaches the search rather than a form that has
    // already been used.
    await page.goBack();

    expect(new URL(page.url()).pathname).toBe("/search");
  });

  test("makes the administration screens reachable", async ({ page, credentials }) => {
    // The worker's User registered through an `admin` Invitation, so a refusal here means
    // the guard and the Permissions disagree rather than that this User is ordinary.
    await page.goto("/login");
    await signIn(page, credentials);

    await page.goto("/admin/users");
    await expect(page.getByRole("heading", { name: "Users" })).toBeVisible();

    await page.goto("/admin/roles");
    await expect(page.getByRole("heading", { name: "Roles" })).toBeVisible();
  });
});

test.describe("a signed-in User", () => {
  test.beforeEach(async ({ page, credentials }) => {
    await page.goto("/login");
    await signIn(page, credentials);
  });

  test("is sent away from the login form", async ({ page }) => {
    await page.goto("/login");

    await expect(page.getByRole("heading", { name: "Your User" })).toBeVisible();
    expect(new URL(page.url()).pathname).toBe("/account");
  });

  test("is sent away from registration", async ({ page }) => {
    await page.goto("/register");

    await expect(page.getByRole("heading", { name: "Your User" })).toBeVisible();
    expect(new URL(page.url()).pathname).toBe("/account");
  });

  test("is named by the header's Identity menu", async ({ page, credentials }) => {
    await page.getByRole("button", { name: credentials.username }).click();

    await expect(page.getByRole("link", { name: "Your User" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Sign out" })).toBeVisible();
  });
});

test.describe("signing out", () => {
  test.beforeEach(async ({ page, credentials }) => {
    await page.goto("/login");
    await signIn(page, credentials);
  });

  async function signOut(page, credentials) {
    await page.getByRole("button", { name: credentials.username }).click();
    await page.getByRole("button", { name: "Sign out" }).click();
  }

  test("returns to Anonymous", async ({ page, credentials }) => {
    await signOut(page, credentials);

    // The header is the visible half. bitmagnet ended every session the User has, this
    // one included, and Magnes refetched self.identity. Nothing local was erased, because
    // Magnes never held the credential.
    await expect(page.getByRole("link", { name: "Sign in" })).toBeVisible();
    await expect(page.getByRole("button", { name: credentials.username })).toHaveCount(0);
  });

  test("leaves the routes that need a User guarded again", async ({ page, credentials }) => {
    await signOut(page, credentials);

    await page.goto("/account");

    await expect(page.getByRole("button", { name: "Sign in" })).toBeVisible();
    expect(new URL(page.url()).pathname).toBe("/login");
  });

  test("ends the session on another device too", async ({ page, browser, credentials }) => {
    // Another browser shares nothing with this one, the cookie included, so it stands for
    // another device. The other-tab test below says nothing about this: tabs in one browser
    // share a cookie, and one expiring takes the other's with it.
    await inAnotherBrowser(browser, async (elsewhere) => {
      await elsewhere.goto("/login");
      await signIn(elsewhere, credentials);

      await signOut(page, credentials);
      // Waited for so the reload below cannot reach bitmagnet before the sign-out has.
      await expect(page.getByRole("link", { name: "Sign in" })).toBeVisible();

      // Nothing tells the other device. Its next request carries a cookie bitmagnet has
      // revoked, since signing out ends every session the User has.
      await elsewhere.reload();
      await expect(elsewhere.getByRole("link", { name: "Sign in" })).toBeVisible();
      await expect(elsewhere.getByRole("button", { name: credentials.username })).toHaveCount(0);
    });
  });

  test("is noticed by another tab", async ({ page, context, credentials }) => {
    const other = await context.newPage();
    await other.goto("/account");
    await expect(other.getByRole("heading", { name: "Your User" })).toBeVisible();

    await signOut(page, credentials);

    // The other tab hears a credential-free notification on a BroadcastChannel and asks
    // the server who it is now. Nothing about the credential crosses between the tabs.
    await expect(other.getByRole("link", { name: "Sign in" })).toBeVisible();
    await other.close();
  });
});
