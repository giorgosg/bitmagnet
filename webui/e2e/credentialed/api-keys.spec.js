// API-key management, driven end to end: make a key, read its value the one time it
// exists, and take it back. Every screen in this feature that got a real browser pass found
// a defect every unit test had passed through, and this screen — a value shown once, a
// revoke that must revoke — is where an unverified one would cost the most.
//
// The User is the run's throwaway administrator, so the registry is reachable and the whole
// permission grid is offered. See e2e/README.md.

import { signIn, expect, test } from "../support/credentialed.js";

test.describe("API-key management", () => {
  test.beforeEach(async ({ page, credentials }) => {
    await page.goto("/login");
    await signIn(page, credentials);
    await page.goto("/account/api-keys");
    await expect(page.getByRole("heading", { name: "API keys" })).toBeVisible();
  });

  test("makes a key, shows its value once, and lists it afterwards", async ({ page }) => {
    const name = `probe-${Date.now()}`;

    await page.getByLabel("Name").fill(name);
    // A concrete registered action, so createAPIKey accepts it. version::query is the most
    // harmless grant there is.
    await page.getByRole("checkbox", { name: "version::query" }).check();
    await page.getByRole("button", { name: "Make key" }).click();

    // The key's value exists in this render and nowhere afterwards.
    const reveal = page.getByRole("alert").filter({ hasText: "only time it is shown" });
    await expect(reveal).toBeVisible();
    const value = await reveal.locator("code").innerText();
    expect(value.length).toBeGreaterThan(0);

    await page.getByRole("button", { name: "I have stored it" }).click();

    // Dismissed, the value is gone and the key is in the list, read back from the server.
    await expect(page.getByText("only time it is shown")).toHaveCount(0);
    const row = page.getByRole("listitem").filter({ hasText: name });
    await expect(row).toBeVisible();
    await expect(row).toContainText("graphql::version::query");
  });

  test("the key's value is not offered as retrievable later", async ({ page }) => {
    const name = `once-${Date.now()}`;

    await page.getByLabel("Name").fill(name);
    await page.getByRole("checkbox", { name: "version::query" }).check();
    await page.getByRole("button", { name: "Make key" }).click();
    await page.getByRole("button", { name: "I have stored it" }).click();

    // Reloading the page re-fetches the listing. Nothing in it is the value: the API has no
    // field that returns one, so a screen that showed a key's value here would be inventing it.
    await page.reload();
    const row = page.getByRole("listitem").filter({ hasText: name });
    await expect(row).toBeVisible();
    await expect(row.locator("code")).toHaveCount(0);
  });

  test("offers the non-browser namespaces, because a key is made for what is not this browser", async ({
    page,
  }) => {
    // The admin holds the wildcard, so the offer is the whole registry — including http and
    // torznab, which a browser cannot reach but an automated client is precisely for.
    await expect(page.getByRole("checkbox", { name: "torznab::query" })).toBeVisible();
    await expect(page.getByRole("checkbox", { name: "import::mutate" })).toBeVisible();
  });

  test("will not make a key with no name or nothing chosen", async ({ page }) => {
    const submit = page.getByRole("button", { name: "Make key" });
    await expect(submit).toBeDisabled();

    await page.getByLabel("Name").fill("nameless-no-more");
    await expect(submit).toBeDisabled();

    await page.getByRole("checkbox", { name: "version::query" }).check();
    await expect(submit).toBeEnabled();
  });

  test("revokes a key after a deliberate confirmation", async ({ page }) => {
    const name = `doomed-${Date.now()}`;

    await page.getByLabel("Name").fill(name);
    await page.getByRole("checkbox", { name: "version::query" }).check();
    await page.getByRole("button", { name: "Make key" }).click();
    await page.getByRole("button", { name: "I have stored it" }).click();

    const row = page.getByRole("listitem").filter({ hasText: name });
    await expect(row).toBeVisible();

    // The ask stands where the act would be; the act only happens on confirming it.
    await row.getByRole("button", { name: "Revoke" }).click();
    await expect(row.getByText("stops working immediately")).toBeVisible();
    await row.getByRole("button", { name: "Revoke" }).click();

    // Gone from the list after the server confirmed it, not optimistically.
    await expect(page.getByRole("listitem").filter({ hasText: name })).toHaveCount(0);
  });
});
