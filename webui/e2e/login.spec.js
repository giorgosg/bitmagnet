import { expect, test } from "@playwright/test";

// A username that cannot exist, paired with a value that is nobody's password. Every
// assertion here is about being *refused*, so the suite needs no real credential and can
// run unattended.
const NO_SUCH_USER = "e2e-no-such-user";
const WRONG_VALUE = "not-a-real-password";

async function fillCredentials(page) {
  await page.getByLabel("Username").fill(NO_SUCH_USER);
  await page.getByLabel("Password").fill(WRONG_VALUE);
}

test.describe("the login form", () => {
  test("labels its fields and hides what is typed into the password", async ({ page }) => {
    await page.goto("/login");

    await expect(page.getByLabel("Username")).toHaveAttribute("type", "text");
    await expect(page.getByLabel("Password")).toHaveAttribute("type", "password");
  });

  test("puts the cursor in the username field", async ({ page }) => {
    // Regression: focus used to land nowhere. `autofocus` cannot work under
    // Browser.application, and focusing when the route is entered is too early — the
    // guard renders "Resolving Identity…" until self.identity answers, so the field does
    // not exist yet. Focus follows the form appearing instead.
    await page.goto("/login");

    await expect(page.getByLabel("Username")).toBeFocused();
  });

  test("offers no submission until both fields are filled", async ({ page }) => {
    await page.goto("/login");
    const submit = page.getByRole("button", { name: "Sign in" });

    await expect(submit).toBeDisabled();

    await page.getByLabel("Username").fill(NO_SUCH_USER);
    await expect(submit).toBeDisabled();

    await page.getByLabel("Password").fill(WRONG_VALUE);
    await expect(submit).toBeEnabled();
  });

  test("a username of only spaces is not a username", async ({ page }) => {
    await page.goto("/login");

    await page.getByLabel("Username").fill("   ");
    await page.getByLabel("Password").fill(WRONG_VALUE);

    await expect(page.getByRole("button", { name: "Sign in" })).toBeDisabled();
  });
});

test.describe("a refused sign-in", () => {
  test("says so beside the fields, and marks them", async ({ page }) => {
    await page.goto("/login");
    await fillCredentials(page);
    await page.getByRole("button", { name: "Sign in" }).click();

    // The text is bitmagnet's INVALID_CREDENTIALS mapped through ApiError, so this
    // exercises the whole path from extensions.code to the rendered sentence.
    const rejection = page.getByRole("alert");
    await expect(rejection).toHaveText("That username and password do not match.");

    await expect(page.getByLabel("Username")).toHaveAttribute("aria-invalid", "true");
    await expect(page.getByLabel("Username")).toHaveAttribute(
      "aria-describedby",
      "login-rejection",
    );
  });

  test("is not dressed up as throttling", async ({ page }) => {
    await page.goto("/login");
    await fillCredentials(page);
    await page.getByRole("button", { name: "Sign in" }).click();

    await expect(page.getByRole("alert")).toBeVisible();
    await expect(page.getByRole("status")).toHaveCount(0);
  });

  test("stays on the login route rather than navigating anywhere", async ({ page }) => {
    await page.goto("/login?returnUrl=%2Faccount");
    await fillCredentials(page);
    await page.getByRole("button", { name: "Sign in" }).click();

    await expect(page.getByRole("alert")).toBeVisible();
    expect(new URL(page.url()).pathname).toBe("/login");
  });

  test("is dismissed by typing, which is no longer what it described", async ({ page }) => {
    await page.goto("/login");
    await fillCredentials(page);
    await page.getByRole("button", { name: "Sign in" }).click();
    await expect(page.getByRole("alert")).toBeVisible();

    await page.getByLabel("Password").fill("something-else");

    await expect(page.getByRole("alert")).toHaveCount(0);
    await expect(page.getByLabel("Username")).toHaveAttribute("aria-invalid", "false");
  });
});

test.describe("routes that require a User", () => {
  // The Identity here is Anonymous, so these exercise the guards from ticket 07 without
  // anyone signing in.
  for (const path of ["/account", "/account/api-keys"]) {
    test(`${path} sends an Anonymous Identity to login, remembering where it was going`, async ({
      page,
    }) => {
      await page.goto(path);

      await expect(page.getByRole("button", { name: "Sign in" })).toBeVisible();

      const url = new URL(page.url());
      expect(url.pathname).toBe("/login");
      expect(url.searchParams.get("returnUrl")).toBe(path);
    });
  }

  test("the login form does not pile up in history on the way there", async ({ page }) => {
    await page.goto("/search?q=linux");
    await page.goto("/account");
    await expect(page.getByRole("button", { name: "Sign in" })).toBeVisible();

    // The redirect replaced the entry rather than pushing one, so one step back reaches
    // the search rather than bouncing through /account and landing here again.
    await page.goBack();

    expect(new URL(page.url()).pathname).toBe("/search");
  });
});
