// Changing a password from /account (ticket 20), against a real bitmagnet.
//
// Every test here changes, or tries to change, the password of a User registered for that
// test alone. bitmagnet ends every sign-in a User has when their password changes, so doing
// it to the worker's administrator would sign out that worker's later tests, and doing it to
// the issuer would end the token every worker mints Invitations with. See "Each worker has
// its own User" in e2e/README.md.

import crypto from "crypto";

import { inAnotherBrowser, registerUser, signIn, expect, test } from "../support/credentialed.js";
import { recordCredentialStores, storedCredentials } from "../support/credential-store.js";

// A password nobody chose, as the harness makes them: well above bitmagnet's minimum entropy.
function newPassword() {
  return crypto.randomBytes(24).toString("base64url");
}

// Registers a User of the test's own, signs it in, and opens the User overview on it.
async function onAccountAs(page, request, issuer, prefix) {
  const user = await registerUser(page, request, issuer, prefix);
  await signIn(page, user);
  await page.goto("/account");
  await expect(page.getByRole("heading", { name: "Change your password" })).toBeVisible();
  return user;
}

// Waits for bitmagnet's answer to a request whose body names `field`, and returns that answer.
function answerTo(page, field) {
  return page
    .waitForResponse((response) => (response.request().postData() ?? "").includes(field))
    .then((response) => response.json());
}

// Fills the form and sends it, returning bitmagnet's answer to updatePassword.
async function changePassword(page, current, next) {
  await page.getByLabel("Current password").fill(current);
  await page.getByLabel("New password").fill(next);
  const answered = answerTo(page, "updatePassword");
  await page.getByRole("button", { name: "Change password" }).click();
  return answered;
}

function codeOf(answer) {
  return answer.errors?.[0]?.extensions?.code;
}

// Whether `page`'s browser is still signed in as `user`, asked of bitmagnet afresh: the reload
// starts the app again, so the header names the User only if self.identity still does.
async function expectStillSignedIn(page, user) {
  await page.reload();
  await expect(page.getByRole("button", { name: user.username })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Your User" })).toBeVisible();
}

test("a changed password signs the User out, says why, and only the new one signs in", async ({
  page,
  browser,
  request,
  issuer,
}) => {
  await recordCredentialStores(page);
  const user = await onAccountAs(page, request, issuer, "e2e-pw");
  const next = newPassword();

  // Before anything is sent, the form says what the change will do.
  await expect(page.getByText("Changing your password signs you out everywhere")).toBeVisible();
  await expect(page.getByText("Your API keys are not affected and keep working.")).toBeVisible();

  await inAnotherBrowser(browser, async (elsewhere) => {
    // The same User on another device, which shares no cookie with this browser.
    await elsewhere.goto("/login");
    await signIn(elsewhere, user);

    const answer = await changePassword(page, user.password, next);
    expect(answer.errors).toBeUndefined();
    expect(answer.data.self.updatePassword).toBeNull();

    // This browser is Anonymous, on the login form, on the User's own name, and told why.
    await expect(
      page
        .getByRole("status")
        .filter({ hasText: "Your password was changed, and that signed you out everywhere" }),
    ).toBeVisible();
    await expect(page.getByRole("link", { name: "Sign in" })).toBeVisible();
    await expect(page.getByRole("button", { name: user.username })).toHaveCount(0);
    expect(new URL(page.url()).pathname).toBe("/login");
    expect(new URL(page.url()).searchParams.get("returnUrl")).toBe("/account");
    await expect(page.getByLabel("Username")).toHaveValue(user.username);

    // The other device was signed out too, which is what the form said would happen.
    await elsewhere.reload();
    await expect(elsewhere.getByRole("link", { name: "Sign in" })).toBeVisible();
    await expect(elsewhere.getByRole("button", { name: user.username })).toHaveCount(0);
  });

  // The new password was offered to the password manager, once.
  expect(await storedCredentials(page)).toEqual([{ id: user.username, password: next }]);

  // bitmagnet refuses the old password...
  await page.getByLabel("Password").fill(user.password);
  const refused = answerTo(page, "loginBrowser");
  await page.getByRole("button", { name: "Sign in" }).click();
  expect(codeOf(await refused)).toBe("INVALID_CREDENTIALS");
  await expect(page.getByRole("alert")).toHaveText("That username and password do not match.");

  // ...and accepts the new one, which returns to the User overview the change was made on.
  await signIn(page, { username: user.username, password: next });
  await expect(page.getByRole("heading", { name: "Your User" })).toBeVisible();
  expect(new URL(page.url()).pathname).toBe("/account");
});

test("another tab is told the User was signed out", async ({ page, context, request, issuer }) => {
  const user = await onAccountAs(page, request, issuer, "e2e-pw-tab");
  const other = await context.newPage();
  await other.goto("/account");
  await expect(other.getByRole("heading", { name: "Your User" })).toBeVisible();

  const answer = await changePassword(page, user.password, newPassword());
  expect(answer.errors).toBeUndefined();

  // Without a reload: the other tab hears the credential-free notification and asks
  // bitmagnet who it is now. It is not told why; the notification carries nothing.
  await expect(other.getByRole("link", { name: "Sign in" })).toBeVisible();
  await expect(other.getByRole("heading", { name: "Sign in" })).toBeVisible();
  await other.close();
});

test("a wrong current password is refused on that field, and changes nothing", async ({
  page,
  browser,
  request,
  issuer,
}) => {
  await recordCredentialStores(page);
  const user = await onAccountAs(page, request, issuer, "e2e-pw-wrong");

  const answer = await changePassword(page, `not ${user.password}`, newPassword());
  expect(codeOf(answer)).toBe("PASSWORD_INCORRECT");

  const current = page.getByLabel("Current password");
  await expect(current).toHaveAttribute("aria-invalid", "true");
  await expect(page.getByLabel("New password")).toHaveAttribute("aria-invalid", "false");
  await expect(page.locator(`#${await current.getAttribute("aria-describedby")}`)).toHaveText(
    "That is not your current password.",
  );

  // Nothing was offered, and the button offers the change again.
  expect(await storedCredentials(page)).toEqual([]);
  await expect(page.getByRole("button", { name: "Change password" })).toBeEnabled();

  // Still signed in, and the password is the one it was: it still signs in elsewhere.
  await expectStillSignedIn(page, user);
  await inAnotherBrowser(browser, async (elsewhere) => {
    await elsewhere.goto("/login");
    await signIn(elsewhere, user);
  });
});

test("a weak new password is refused on that field, and changes nothing", async ({
  page,
  request,
  issuer,
}) => {
  await recordCredentialStores(page);
  const user = await onAccountAs(page, request, issuer, "e2e-pw-weak");

  // The registration meter, measuring the new password against the instance's own minimum.
  const measured = answerTo(page, "passwordEntropy");
  await page.getByLabel("New password").fill("password");
  const { entropy, minEntropy } = (await measured).data.self.passwordEntropy;
  expect(entropy).toBeLessThan(minEntropy);
  await expect(
    page.getByText(`${Math.round(entropy)} of ${Math.round(minEntropy)} bits needed`),
  ).toBeVisible();

  // The meter is advice: the change is still sent, and bitmagnet is the one that refuses it.
  const answer = await changePassword(page, user.password, "password");
  expect(codeOf(answer)).toBe("PASSWORD_INSUFFICIENT_ENTROPY");

  const next = page.getByLabel("New password");
  await expect(next).toHaveAttribute("aria-invalid", "true");
  await expect(page.getByLabel("Current password")).toHaveAttribute("aria-invalid", "false");
  await expect(page.locator(`#${await next.getAttribute("aria-describedby")}`)).toHaveText(
    "That password is too easy to guess. Make it longer or less predictable.",
  );

  expect(await storedCredentials(page)).toEqual([]);
  await expectStillSignedIn(page, user);
});

test("an answer lost on the way back leaves the change unknown, though bitmagnet made it", async ({
  page,
  browser,
  request,
  issuer,
}) => {
  await recordCredentialStores(page);
  const user = await onAccountAs(page, request, issuer, "e2e-pw-lost");
  const next = newPassword();

  // The change reaches bitmagnet, which makes it, and its answer is dropped before the page
  // sees it: the case where saying "not changed" would send the person back to a password
  // that no longer works.
  let reached;
  await page.route("**/graphql", async (route) => {
    if ((route.request().postData() ?? "").includes("updatePassword")) {
      reached = await (await route.fetch()).json();
      await route.abort();
    } else {
      await route.continue();
    }
  });
  await page.getByLabel("Current password").fill(user.password);
  await page.getByLabel("New password").fill(next);
  await page.getByRole("button", { name: "Change password" }).click();

  await expect(page.getByRole("alert")).toContainText("Whether your password was changed is not known.");
  await expect(page.getByText("was not changed")).toHaveCount(0);
  expect(reached.errors).toBeUndefined();

  // Only a change the page saw accepted offers a credential.
  expect(await storedCredentials(page)).toEqual([]);

  // And it was made: elsewhere, the old password is refused and the new one signs in.
  await inAnotherBrowser(browser, async (elsewhere) => {
    await elsewhere.goto("/login");
    await elsewhere.getByLabel("Username").fill(user.username);
    await elsewhere.getByLabel("Password").fill(user.password);
    const refused = answerTo(elsewhere, "loginBrowser");
    await elsewhere.getByRole("button", { name: "Sign in" }).click();
    expect(codeOf(await refused)).toBe("INVALID_CREDENTIALS");

    await signIn(elsewhere, { username: user.username, password: next });
  });
});
