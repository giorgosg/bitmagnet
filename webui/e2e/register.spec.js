import { expect, test } from "@playwright/test";

// A username that cannot exist, and a code that is nobody's invitation. Every assertion
// here is about being refused or about the form itself, so the suite stays unattended and
// no real invitation is spent.
const NO_SUCH_USER = "e2e-no-such-user";
const NOT_AN_INVITATION = "e2e-not-an-invitation";
const STRONG = "correct horse battery staple wolf";

test.describe("the registration form", () => {
  test("puts the cursor in the username field", async ({ page }) => {
    // Same shape as login: `autofocus` cannot work under Browser.application, and the
    // guard renders "Resolving Identity…" until self.identity answers, so focus has to
    // follow the form appearing rather than the route being entered.
    await page.goto("/register");

    await expect(page.getByLabel("Username")).toBeFocused();
  });

  test("collects no email address", async ({ page }) => {
    // bitmagnet's email verification is inert, so the spec defers email entirely rather
    // than implying an address was verified.
    await page.goto("/register");

    await expect(page.locator('input[type="email"]')).toHaveCount(0);
  });

  test("offers no submission until a username and password are filled", async ({ page }) => {
    await page.goto("/register");
    const submit = page.getByRole("button", { name: "Register" });

    await expect(submit).toBeDisabled();

    await page.getByLabel("Username").fill(NO_SUCH_USER);
    await expect(submit).toBeDisabled();

    await page.getByLabel("Password", { exact: true }).fill(STRONG);
    await expect(submit).toBeEnabled();
  });
});

test.describe("an invitation link", () => {
  test("arrives with its code already in the form", async ({ page }) => {
    await page.goto(`/register?code=${NOT_AN_INVITATION}`);

    await expect(page.getByLabel("Invitation code")).toHaveValue(NOT_AN_INVITATION);
  });

  test("leaves the field empty when the link carries no code", async ({ page }) => {
    await page.goto("/register");

    await expect(page.getByLabel("Invitation code")).toHaveValue("");
  });
});

test.describe("the password-entropy meter", () => {
  // The measurement is a real query against bitmagnet, which answers it anonymously —
  // so this exercises the debounce and the round trip, neither of which elm-test can see.
  test("appears only once a password has been scored", async ({ page }) => {
    await page.goto("/register");

    await expect(page.getByRole("progressbar")).toHaveCount(0);

    await page.getByLabel("Password", { exact: true }).fill("short");

    await expect(page.getByRole("progressbar")).toBeVisible();
    await expect(page.getByText(/of \d+ bits needed/)).toBeVisible();
  });

  test("a weak password does not block submission, because the server decides", async ({
    page,
  }) => {
    await page.goto("/register");

    await page.getByLabel("Username").fill(NO_SUCH_USER);
    await page.getByLabel("Password", { exact: true }).fill("a");

    await expect(page.getByRole("progressbar")).toBeVisible();
    await expect(page.getByRole("button", { name: "Register" })).toBeEnabled();
  });

  test("stops describing a password that is no longer typed", async ({ page }) => {
    await page.goto("/register");
    const password = page.getByLabel("Password", { exact: true });

    await password.fill("short");
    await expect(page.getByRole("progressbar")).toBeVisible();

    await password.fill("");

    await expect(page.getByRole("progressbar")).toHaveCount(0);
  });
});

test.describe("a refused registration", () => {
  test("says why, and marks the fields", async ({ page }) => {
    await page.goto(`/register?code=${NOT_AN_INVITATION}`);

    await page.getByLabel("Username").fill(NO_SUCH_USER);
    await page.getByLabel("Password", { exact: true }).fill(STRONG);
    await page.getByRole("button", { name: "Register" }).click();

    // Whatever this instance is configured to require, the refusal is a mapped one
    // rather than a raw server string, and it names the invitation.
    const rejection = page.getByRole("alert");
    await expect(rejection).toContainText(/invitation/i);

    // And it is marked on the invitation, not scattered across fields it is not about.
    const invitation = page.getByLabel("Invitation code");
    await expect(invitation).toHaveAttribute("aria-invalid", "true");
    await expect(invitation).toHaveAttribute("aria-describedby", "register-rejection");

    await expect(page.getByLabel("Username")).toHaveAttribute("aria-invalid", "false");
  });

  test("stays on the registration route rather than navigating anywhere", async ({ page }) => {
    await page.goto(`/register?code=${NOT_AN_INVITATION}`);

    await page.getByLabel("Username").fill(NO_SUCH_USER);
    await page.getByLabel("Password", { exact: true }).fill(STRONG);
    await page.getByRole("button", { name: "Register" }).click();

    await expect(page.getByRole("alert")).toBeVisible();
    expect(new URL(page.url()).pathname).toBe("/register");
  });

  test("is dismissed by typing, which is no longer what it described", async ({ page }) => {
    await page.goto(`/register?code=${NOT_AN_INVITATION}`);

    await page.getByLabel("Username").fill(NO_SUCH_USER);
    await page.getByLabel("Password", { exact: true }).fill(STRONG);
    await page.getByRole("button", { name: "Register" }).click();
    await expect(page.getByRole("alert")).toBeVisible();

    await page.getByLabel("Invitation code").fill("something-else");

    await expect(page.getByRole("alert")).toHaveCount(0);
    await expect(page.getByLabel("Invitation code")).toHaveAttribute(
      "aria-invalid",
      "false",
    );
  });
});
