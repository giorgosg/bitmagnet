// The half of ticket 17 that only happens when something succeeds.
//
// The refusal paths — where nothing may be offered — are covered credential-free in
// e2e/register.spec.js. The call itself happens on the way out of a successful registration
// and a successful sign-in, which is exactly what needs a real Invitation and a real User.
//
// This is the case that cost a real password: a User was registered with a password from a
// manager, the manager was never offered it because Elm's onSubmit prevents the default, the
// form cleared, and bitmagnet has no password reset.

import { registerUser, signIn, expect, test } from "../support/credentialed.js";
import { recordCredentialStores, storedCredentials } from "../support/credential-store.js";

test("a successful sign-in offers the credential", async ({ page, credentials }) => {
  await recordCredentialStores(page);
  await page.goto("/login");

  await signIn(page, credentials);

  expect(await storedCredentials(page)).toEqual([
    { id: credentials.username, password: credentials.password },
  ]);
});

test("a successful registration offers the credential", async ({ page, request, issuer }) => {
  await recordCredentialStores(page);
  const registered = await registerUser(page, request, issuer, "e2e-new");

  // Registration does not sign anyone in: it lands on the login form with the username
  // already there, and the password on its way to the store as the form is emptied.
  await expect(page.getByLabel("Username")).toHaveValue(registered.username);

  expect(await storedCredentials(page)).toEqual([
    { id: registered.username, password: registered.password },
  ]);
});
