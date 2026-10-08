// User administration, driven against a real bitmagnet (ticket 21). Until now it had only
// unit tests, and its review found two defects in the Main wiring that elm-test cannot
// reach, one of them a successful act leaving every control disabled. So after each act
// these tests check that the screen settles, as well as what the server did.
//
// Every User acted on is registered for its test. Disabling, deleting or demoting a User
// ends its sessions for good, so the worker's own administrator is only ever the one acting.

import { registerUser, signIn, expect, test } from "../support/credentialed.js";

// The listed User, found through the search the way an administrator would.
async function find(page, username) {
  await page.getByLabel("Find a User").fill(username);
  const row = page.getByRole("listitem").filter({ hasText: username });
  await expect(row).toHaveCount(1);
  return row;
}

// Whether the screen has settled after an act: every control on the row answers again.
// Confirming closes the ask and holds the controls in one update, so once "Keep" is gone the
// controls showing are the held ones, and their coming back means the server has answered.
async function settled(row) {
  await expect(row.getByRole("button", { name: "Keep" })).toHaveCount(0);
  await expect(row.getByRole("button", { name: "Disable" })).toBeEnabled();
  await expect(row.getByRole("button", { name: "Enable" })).toBeEnabled();
  await expect(row.getByRole("button", { name: "Delete" })).toBeEnabled();
}

// Signs `user` in from a browser context of its own, standing for the person themselves, and
// returns what the login form said. Nothing is shared with the administrator's session.
async function attemptSignIn(browser, user) {
  const theirs = await browser.newContext();
  try {
    const them = await theirs.newPage();
    await them.goto("/login");
    await them.getByLabel("Username").fill(user.username);
    await them.getByLabel("Password").fill(user.password);
    await them.getByRole("button", { name: "Sign in" }).click();

    const signedIn = them.getByRole("button", { name: user.username });
    const refused = them.getByRole("alert");
    await expect(signedIn.or(refused)).toBeVisible();
    return (await signedIn.isVisible()) ? "signed in" : await refused.textContent();
  } finally {
    await theirs.close();
  }
}

test("finds a User, and each act on them lands on the server and leaves the screen usable", async ({
  page,
  browser,
  request,
  issuer,
  credentials,
}) => {
  const target = await registerUser(page, request, issuer, "e2e-acted");
  await signIn(page, credentials);
  await page.goto("/admin/users");

  let row = await find(page, target.username);
  const role = row.getByLabel(`Role for ${target.username}`);
  await expect(role).toHaveValue("user");

  // A Role change for someone else applies at once: an administrator can as easily undo it.
  // There is no ask to wait out, so it waits for bitmagnet's answer instead.
  const answered = page.waitForResponse((response) =>
    (response.request().postData() ?? "").includes("setUserRole"),
  );
  await role.selectOption("editor");
  await answered;
  await settled(row);
  await page.reload();
  row = await find(page, target.username);
  await expect(row.getByLabel(`Role for ${target.username}`)).toHaveValue("editor");

  // Disabling asks first, and then takes their sign-ins away.
  await row.getByRole("button", { name: "Disable" }).click();
  await expect(row.getByRole("button", { name: "Keep" })).toBeVisible();
  await row.getByRole("button", { name: "Disable" }).click();
  await settled(row);
  expect(await attemptSignIn(browser, target)).toBe("That User is disabled.");

  // Enabling asks too, and gives them back.
  await row.getByRole("button", { name: "Enable" }).click();
  await row.getByRole("button", { name: "Enable" }).click();
  await settled(row);
  expect(await attemptSignIn(browser, target)).toBe("signed in");

  // Deleting asks, then the row goes because bitmagnet deleted them, and so does the User.
  await row.getByRole("button", { name: "Delete" }).click();
  await row.getByRole("button", { name: "Delete" }).click();
  await expect(page.getByText(`No User matches "${target.username}".`)).toBeVisible();
  await page.reload();
  await page.getByLabel("Find a User").fill(target.username);
  await expect(page.getByText(`No User matches "${target.username}".`)).toBeVisible();
  expect(await attemptSignIn(browser, target)).toBe("That username and password do not match.");
});

test("an act asked about and then kept changes nothing", async ({
  page,
  browser,
  request,
  issuer,
  credentials,
}) => {
  const target = await registerUser(page, request, issuer, "e2e-kept");
  await signIn(page, credentials);
  await page.goto("/admin/users");

  const row = await find(page, target.username);
  await row.getByRole("button", { name: "Delete" }).click();
  await row.getByRole("button", { name: "Keep" }).click();
  await settled(row);

  expect(await attemptSignIn(browser, target)).toBe("signed in");
});

test("is refused to an Identity without administration", async ({ page, request, issuer }) => {
  const ordinary = await registerUser(page, request, issuer, "e2e-nousers");
  await signIn(page, ordinary);

  await page.goto("/admin/users");

  await expect(page.getByText("Your Identity does not permit administration.")).toBeVisible();
  await expect(page.getByLabel("Find a User")).toHaveCount(0);
});
