import { expect, signIn, test } from "../support/credentialed.js";
import { setHidden } from "../support/stats.js";

const causes = {
  network: "Could not reach bitmagnet.",
  graphql:
    "bitmagnet's authentication service is unavailable. Try again shortly.",
};

async function failIdentity(route, failure) {
  if (failure === "network") return route.abort("failed");
  return route.fulfill({
    json: {
      errors: [
        {
          message: "Authentication infrastructure unavailable",
          extensions: { code: "AUTHENTICATION_INFRASTRUCTURE_FAILURE" },
        },
      ],
    },
  });
}

for (const failure of ["network", "graphql"]) {
  test(`retry an Identity ${failure} failure after successful sign-in in place`, async ({
    page,
    credentials,
  }) => {
    await page.goto("/login?returnUrl=%2Faccount");
    await expect(page.getByRole("button", { name: "Sign in" })).toBeVisible();
    await page.evaluate(() => {
      window.identityRetryDocument = true;
    });
    let failedLooks = 0;
    let laterFailure = false;
    await page.route("**/graphql", async (route) => {
      const query = route.request().postDataJSON()?.query ?? "";
      if (!query.includes("identity")) return route.continue();
      if (laterFailure) return failIdentity(route, failure);
      if (failedLooks >= 2) return route.continue();
      failedLooks += 1;
      return failIdentity(route, failure);
    });

    // The login is real; only the Identity query it causes is failed. signIn's usual
    // helper waits for an Identity, so this test submits the form directly.
    await page.getByLabel("Username").fill(credentials.username);
    await page.getByLabel("Password").fill(credentials.password);
    const accepted = page.waitForResponse((response) =>
      (response.request().postData() ?? "").includes("loginBrowser"),
    );
    await page.getByRole("button", { name: "Sign in" }).click();
    expect((await (await accepted).json()).errors).toBeUndefined();
    const message = `You are signed in. Magnes could not load your Identity: ${causes[failure]}`;
    await expect(page.locator("main")).toContainText(message);
    await expect(page.getByRole("textbox", { name: "Username" })).toHaveCount(
      0,
    );

    const failedAgain =
      failure === "network"
        ? page.waitForEvent("requestfailed", {
            predicate: (request) =>
              (request.postData() ?? "").includes("identity"),
          })
        : page.waitForResponse((response) =>
            (response.request().postData() ?? "").includes("identity"),
          );
    await page.getByRole("button", { name: "Try again", exact: true }).click();
    await failedAgain;
    await expect(page.locator("main")).toContainText(message);
    expect(failedLooks).toBe(2);
    await page.getByRole("button", { name: "Try again", exact: true }).click();
    await expect(
      page.getByRole("heading", { name: "Your User" }),
    ).toBeVisible();
    await expect(
      page.getByRole("button", { name: credentials.username }),
    ).toBeVisible();
    await expect(
      page.getByRole("button", { name: "Try again", exact: true }),
    ).toHaveCount(0);
    expect(new URL(page.url()).pathname).toBe("/account");
    expect(await page.evaluate(() => window.identityRetryDocument)).toBe(true);

    // A later look after switching back to this tab is unrelated to sign-in. Resolving
    // the Identity above must have spent both its sentence and its recovery control.
    laterFailure = true;
    await setHidden(page, true);
    await setHidden(page, false);
    await expect(page.locator("main")).toHaveText(causes[failure]);
    await expect(
      page.getByRole("button", { name: "Try again", exact: true }),
    ).toHaveCount(0);
  });

  test(`an Identity ${failure} failure at startup keeps its wording`, async ({
    page,
  }) => {
    await page.route("**/graphql", (route) => {
      const query = route.request().postDataJSON()?.query ?? "";
      return query.includes("identity")
        ? failIdentity(route, failure)
        : route.continue();
    });
    await page.goto("/account");
    await expect(page.locator("main")).toHaveText(causes[failure]);
    await expect(
      page.getByRole("button", { name: "Try again", exact: true }),
    ).toHaveCount(0);
  });

  test(`an Identity ${failure} failure after sign-out keeps its wording`, async ({
    page,
    credentials,
  }) => {
    await page.goto("/login");
    await signIn(page, credentials);
    await page.route("**/graphql", (route) => {
      const query = route.request().postDataJSON()?.query ?? "";
      return query.includes("identity")
        ? failIdentity(route, failure)
        : route.continue();
    });
    await page.getByRole("button", { name: credentials.username }).click();
    await page.getByRole("button", { name: "Sign out" }).click();
    await expect(page.locator("main")).toHaveText(
      `You are signed out. Magnes could not load what this instance allows without a User: ${causes[failure]}`,
    );
    await expect(
      page.getByRole("button", { name: "Try again", exact: true }),
    ).toHaveCount(0);
  });
}
