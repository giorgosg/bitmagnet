// User administration, driven against a real bitmagnet (ticket 21). Until now it had only
// unit tests, and its review found two defects in the Main wiring that elm-test cannot
// reach, one of them a successful act leaving every control disabled. So after each act
// these tests check that the screen settles, as well as what the server did.
//
// Every User acted on is registered for its test, so the worker's own administrator is only
// ever the one acting. Disabling a User refuses everything its sessions ask while it stays
// disabled, deleting one ends them for good, and a new Role changes what they may do.

import {
  inAnotherBrowser,
  registerUser,
  setRole,
  signIn,
  signInAt,
  expect,
  test,
} from "../support/credentialed.js";

// The listed User, found through the search the way an administrator would. Waits for the
// search's own answer, so the row is not one from the unfiltered page the screen opened on.
async function find(page, username) {
  const searched = page.waitForResponse((response) =>
    (response.request().postData() ?? "").includes(username),
  );
  await page.getByLabel("Find a User").fill(username);
  await searched;

  const row = page.getByRole("listitem").filter({ has: page.getByText(username, { exact: true }) });
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

// Signs `user` in from a browser of its own, standing for the person themselves, and returns
// what the login form said.
function attemptSignIn(browser, user) {
  return inAnotherBrowser(browser, async (them) => {
    await them.goto("/login");
    await them.getByLabel("Username").fill(user.username);
    await them.getByLabel("Password").fill(user.password);
    await them.getByRole("button", { name: "Sign in" }).click();

    const signedIn = them.getByRole("button", { name: user.username });
    const refused = them.getByRole("alert");
    await expect(signedIn.or(refused)).toBeVisible();
    return (await signedIn.isVisible()) ? "signed in" : await refused.textContent();
  });
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

  // bitmagnet's User has no enabled field, so the screen says it cannot show one rather than
  // inventing a column.
  await expect(
    page.getByText("Whether a User is disabled is not shown: bitmagnet does not report it."),
  ).toBeVisible();

  let row = await find(page, target.username);
  await expect(row.getByLabel(`Role for ${target.username}`)).toHaveValue("user");

  // A Role change for someone else applies at once, with no ask: an administrator can as
  // easily undo it.
  await setRole(page, target.username, "editor");
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
  await signInAt(page, credentials, "/admin/users", "Users");

  const row = await find(page, target.username);
  await row.getByRole("button", { name: "Delete" }).click();
  await row.getByRole("button", { name: "Keep" }).click();
  await settled(row);

  expect(await attemptSignIn(browser, target)).toBe("signed in");
});
