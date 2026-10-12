// Changing a password from the User overview (ticket 20), against a real bitmagnet.
//
// Every test here changes, or tries to change, the password of a User registered for that
// test alone. Changing the worker's administrator's password would leave that worker's later
// tests signing in with the old one, and changing the issuer's would leave every worker unable
// to mint an Invitation, since the issuer signs in afresh for each. See "Each worker has its
// own User" in e2e/README.md.

import {
  inAnotherBrowser,
  newPassword,
  registerUser,
  signIn,
  signInAt,
  expect,
  test,
} from "../support/credentialed.js";
import {
  recordCredentialStores,
  storedCredentials,
} from "../support/credential-store.js";
import { setHidden } from "../support/stats.js";

// Registers a User of the test's own, signs it in, and opens the User overview on it.
async function onAccountAs(page, request, issuer, prefix) {
  const user = await registerUser(page, request, issuer, prefix);
  await signInAt(page, user, "/account", "Change your password");
  return user;
}

// Waits for bitmagnet's answer to a request whose body names `field`, and returns that answer.
function answerTo(page, field) {
  return page
    .waitForResponse((response) =>
      (response.request().postData() ?? "").includes(field),
    )
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

// Fills the form and sends it, with bitmagnet's answer dropped before the page sees it.
// `reaching` decides whether the change reaches bitmagnet first; the answer it gives back,
// if it does, is returned.
async function changePasswordLosingTheAnswer(
  page,
  current,
  next,
  { reaching },
) {
  let reached;
  await page.route("**/graphql", async (route) => {
    if ((route.request().postData() ?? "").includes("updatePassword")) {
      if (reaching) {
        reached = await (await route.fetch()).json();
      }
      await route.abort();
    } else {
      await route.continue();
    }
  });
  await page.getByLabel("Current password").fill(current);
  await page.getByLabel("New password").fill(next);
  await page.getByRole("button", { name: "Change password" }).click();
  await expect(page.getByRole("alert")).toContainText(
    "Whether your password was changed is not known.",
  );
  await page.unroute("**/graphql");
  return reached;
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

const mayHaveChanged =
  "Your password may have changed: the answer to the change was lost";

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
  await expect(
    page.getByText("Changing your password signs you out everywhere"),
  ).toBeVisible();
  await expect(
    page.getByText("Your API keys are not affected and keep working."),
  ).toBeVisible();

  await inAnotherBrowser(browser, async (elsewhere) => {
    // The same User on another device, which shares no cookie with this browser.
    await elsewhere.goto("/login");
    await signIn(elsewhere, user);

    const answer = await changePassword(page, user.password, next);
    expect(answer.errors).toBeUndefined();
    expect(answer.data.self.updatePassword).toBeNull();

    // This browser is Anonymous, on the login form, on the User's own name, and told why,
    // with the cursor in the password field, which the reason describes.
    const reason = page.getByRole("status").filter({
      hasText: "Your password was changed, and that signed you out everywhere",
    });
    await expect(reason).toBeVisible();
    await expect(page.getByRole("link", { name: "Sign in" })).toBeVisible();
    await expect(page.getByRole("button", { name: user.username })).toHaveCount(
      0,
    );
    expect(new URL(page.url()).pathname).toBe("/login");
    expect(new URL(page.url()).searchParams.get("returnUrl")).toBe("/account");
    await expect(page.getByLabel("Username")).toHaveValue(user.username);
    await expect(page.getByLabel("Password")).toBeFocused();
    await expect(page.getByLabel("Password")).toHaveAccessibleDescription(
      await reason.textContent(),
    );

    // The other device was signed out too, which is what the form said would happen.
    await elsewhere.reload();
    await expect(
      elsewhere.getByRole("link", { name: "Sign in" }),
    ).toBeVisible();
    await expect(
      elsewhere.getByRole("button", { name: user.username }),
    ).toHaveCount(0);
  });

  // The new password was offered to the password manager, once.
  expect(await storedCredentials(page)).toEqual([
    { id: user.username, password: next },
  ]);

  // bitmagnet refuses the old password...
  await page.getByLabel("Password").fill(user.password);
  const refused = answerTo(page, "loginBrowser");
  await page.getByRole("button", { name: "Sign in" }).click();
  expect(codeOf(await refused)).toBe("INVALID_CREDENTIALS");
  await expect(page.getByRole("alert")).toHaveText(
    "That username and password do not match.",
  );

  // ...and accepts the new one, which returns to the User overview the change was made on.
  await signIn(page, { username: user.username, password: next });
  await expect(page.getByRole("heading", { name: "Your User" })).toBeVisible();
  expect(new URL(page.url()).pathname).toBe("/account");
});

test("another tab is told the User was signed out", async ({
  page,
  context,
  request,
  issuer,
}) => {
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

  const answer = await changePassword(
    page,
    `not ${user.password}`,
    newPassword(),
  );
  expect(codeOf(answer)).toBe("PASSWORD_INCORRECT");

  const current = page.getByLabel("Current password");
  await expect(current).toHaveAttribute("aria-invalid", "true");
  await expect(page.getByLabel("New password")).toHaveAttribute(
    "aria-invalid",
    "false",
  );
  await expect(
    page.locator(`#${await current.getAttribute("aria-describedby")}`),
  ).toHaveText("That is not your current password.");

  // Nothing was offered, and the button offers the change again.
  expect(await storedCredentials(page)).toEqual([]);
  await expect(
    page.getByRole("button", { name: "Change password" }),
  ).toBeEnabled();

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
    page.getByText(
      `${Math.round(entropy)} of ${Math.round(minEntropy)} bits needed`,
    ),
  ).toBeVisible();

  // The meter is advice: the change is still sent, and bitmagnet is the one that refuses it.
  const answer = await changePassword(page, user.password, "password");
  expect(codeOf(answer)).toBe("PASSWORD_INSUFFICIENT_ENTROPY");

  const next = page.getByLabel("New password");
  await expect(next).toHaveAttribute("aria-invalid", "true");
  await expect(page.getByLabel("Current password")).toHaveAttribute(
    "aria-invalid",
    "false",
  );
  await expect(
    page.locator(`#${await next.getAttribute("aria-describedby")}`),
  ).toHaveText(
    "That password is too easy to guess. Make it longer or less predictable.",
  );

  expect(await storedCredentials(page)).toEqual([]);
  await expectStillSignedIn(page, user);
});

test("a change bitmagnet made whose answer was lost is found out at the next look, and its password tried first", async ({
  page,
  request,
  issuer,
}) => {
  await recordCredentialStores(page);
  const user = await onAccountAs(page, request, issuer, "e2e-pw-lost");
  const next = newPassword();

  // The change reaches bitmagnet, which makes it, and its answer is dropped before the page
  // sees it: the case where saying "not changed" would send the person back to a password
  // that no longer works.
  const reached = await changePasswordLosingTheAnswer(
    page,
    user.password,
    next,
    { reaching: true },
  );
  expect(reached.errors).toBeUndefined();
  await expect(page.getByText("was not changed")).toHaveCount(0);

  // No second change can be sent while this one is unsettled, and the page says why.
  await expect(
    page.getByText("Another change cannot be sent until this one is settled"),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Change password" }),
  ).toBeDisabled();

  // Only a change the page saw accepted offers a credential.
  expect(await storedCredentials(page)).toEqual([]);

  // Switching away and back asks bitmagnet who this is, which is Anonymous, since the change
  // ended every sign-in. The login form says the password may have changed and has the new
  // one filled in, ready to be sent.
  await setHidden(page, true);
  await setHidden(page, false);
  const reason = page.getByRole("status").filter({ hasText: mayHaveChanged });
  await expect(reason).toBeVisible();
  expect(new URL(page.url()).pathname).toBe("/login");
  expect(new URL(page.url()).searchParams.get("returnUrl")).toBe("/account");
  await expect(page.getByLabel("Username")).toHaveValue(user.username);
  await expect(page.getByLabel("Password")).toHaveValue(next);
  await expect(page.getByLabel("Password")).toBeFocused();
  await expect(page.getByLabel("Password")).toHaveAccessibleDescription(
    await reason.textContent(),
  );
  expect(await storedCredentials(page)).toEqual([]);

  // Pressing Enter sends it. bitmagnet accepts it, and the sign-in offers it, once.
  const signedIn = answerTo(page, "loginBrowser");
  await page.getByLabel("Password").press("Enter");
  expect((await signedIn).errors).toBeUndefined();
  await expect(page.getByRole("button", { name: user.username })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Your User" })).toBeVisible();
  expect(new URL(page.url()).pathname).toBe("/account");
  expect(await storedCredentials(page)).toEqual([
    { id: user.username, password: next },
  ]);

  // Settled: the form offers a change again.
  await expect(page.getByText("Another change cannot be sent")).toHaveCount(0);
  await page.getByLabel("Current password").fill(next);
  await page.getByLabel("New password").fill(newPassword());
  await expect(
    page.getByRole("button", { name: "Change password" }),
  ).toBeEnabled();
});

test("a change that never reached bitmagnet is settled by signing out and in, with the old password", async ({
  page,
  request,
  issuer,
}) => {
  await recordCredentialStores(page);
  const user = await onAccountAs(page, request, issuer, "e2e-pw-unmade");
  const next = newPassword();

  await changePasswordLosingTheAnswer(page, user.password, next, {
    reaching: false,
  });

  // Still signed in, which a change would have ended, but the page does not guess: it keeps
  // the change unsettled and says how to settle it.
  await setHidden(page, true);
  await setHidden(page, false);
  await expect(page.getByRole("button", { name: user.username })).toBeVisible();
  await expect(
    page.getByText("Another change cannot be sent until this one is settled"),
  ).toBeVisible();

  // Signing out, as the page says, finds the browser signed out with the change unsettled,
  // so the login form tries the new password first. bitmagnet refuses it: never made.
  await page.getByRole("button", { name: "Sign out" }).first().click();
  await expect(
    page.getByRole("status").filter({ hasText: mayHaveChanged }),
  ).toBeVisible();
  await expect(page.getByLabel("Password")).toHaveValue(next);
  const refused = answerTo(page, "loginBrowser");
  await page.getByRole("button", { name: "Sign in" }).click();
  expect(codeOf(await refused)).toBe("INVALID_CREDENTIALS");
  await expect(page.getByRole("alert")).toHaveText(
    "That username and password do not match.",
  );
  expect(await storedCredentials(page)).toEqual([]);

  // The old password signs in, and that settles it.
  await signIn(page, user);
  await expect(page.getByRole("heading", { name: "Your User" })).toBeVisible();
  await expect(page.getByText("Another change cannot be sent")).toHaveCount(0);
  expect(await storedCredentials(page)).toEqual([
    { id: user.username, password: user.password },
  ]);
});
