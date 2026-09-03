// The half of ticket 17 that only happens when something succeeds.
//
// The refusal paths — where nothing may be offered — are covered credential-free in
// e2e/register.spec.js. The call itself happens on the way out of a successful registration
// and a successful sign-in, which is exactly what needs a real Invitation and a real User.
//
// This is the case that cost a real password: a User was registered with a password from a
// manager, the manager was never offered it because Elm's onSubmit prevents the default, the
// form cleared, and bitmagnet has no password reset.

import crypto from "crypto";

import { mintInvitation, signIn, expect, test } from "../support/credentialed.js";
import { recordCredentialStores, storedCredentials } from "../support/credential-store.js";

test("a successful sign-in offers the credential", async ({ page, credentials }) => {
  await recordCredentialStores(page);
  await page.goto("/login");

  await signIn(page, credentials);

  expect(await storedCredentials(page)).toEqual([
    { id: credentials.username, password: credentials.password },
  ]);
});

test("a successful registration offers the credential", async ({
  page,
  request,
  credentials,
}) => {
  const code = await mintInvitation(request, credentials);
  const registered = {
    // bitmagnet's usernames are ^[a-zA-Z0-9][a-zA-Z0-9._-]{1,18}[a-zA-Z0-9]$, so twenty
    // characters is the ceiling and a timestamp does not fit under it.
    username: `e2e-new-${crypto.randomBytes(3).toString("hex")}`,
    // Generated, not written down. This one is a real User's password for as long as the
    // run lasts, so the suite's claim to hold no password should stay literally true.
    password: crypto.randomBytes(24).toString("base64url"),
  };

  await recordCredentialStores(page);
  await page.goto(`/register?code=${code}`);

  await page.getByLabel("Username").fill(registered.username);
  await page.getByLabel("Password", { exact: true }).fill(registered.password);
  await page.getByRole("button", { name: "Register" }).click();

  // Registration does not sign anyone in: it lands on the login form with the username
  // already there, and the password on its way to the store as the form is emptied.
  await expect(page.getByRole("button", { name: "Sign in" })).toBeVisible();
  await expect(page.getByLabel("Username")).toHaveValue(registered.username);

  expect(await storedCredentials(page)).toEqual([
    { id: registered.username, password: registered.password },
  ]);
});
