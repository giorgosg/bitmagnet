// The test-side half of the credentialed harness: where a spec gets a User from.
//
// Each worker signs in as an administrator of its own, because signing out ends every
// session a User has: see "Each worker has its own User" in e2e/README.md. The User
// e2e/harness/serve.js registered through the bootstrap Invitation is the issuer, which only
// mints the Invitations they register through. Its credentials are read from the file
// serve.js wrote before it started the dev server Playwright waited on. Nothing is committed
// and nothing is asked of a person; every User goes away with the database when the run ends.

import { test as base, expect } from "@playwright/test";
import crypto from "crypto";
import fs from "fs";

export { expect };

export const test = base.extend({
  // The harness's administrator. Only for minting Invitations: no test signs in as it, so
  // nothing can end the sessions it mints with.
  issuer: [
    async ({}, use) => {
      const file = process.env.MAGNES_E2E_CREDENTIALS;
      if (!file || !fs.existsSync(file)) {
        throw new Error(
          "no harness credentials. This project is started by e2e/harness/serve.js; " +
            "run it with `npm run test:e2e:credentialed`. See e2e/README.md.",
        );
      }

      // The file is the harness's handshake, not a record for tests: it also carries the
      // pid that e2e/harness/teardown.js stops. A spec has no business with that, so it is
      // not handed one.
      const { username, password, graphqlEndpoint } = JSON.parse(fs.readFileSync(file, "utf8"));

      await use({ username, password, graphqlEndpoint });
    },
    { scope: "worker" },
  ],

  // This worker's own administrator, registered over the API through an `admin` Invitation
  // the issuer mints. What a test signs in as.
  credentials: [
    async ({ issuer, playwright }, use, workerInfo) => {
      const user = newUser(`e2e-w${workerInfo.workerIndex}`);
      const request = await playwright.request.newContext();
      let role;
      try {
        const code = await mintInvitation(request, issuer, "admin");
        const registered = await call(request, issuer.graphqlEndpoint, {
          query:
            "mutation Register($input: RegisterInput!) " +
            "{ self { register(input: $input) { user { role } } } }",
          variables: { input: { invitationCode: code, ...user } },
        });
        role = registered.self.register.user.role;
      } finally {
        await request.dispose();
      }

      // Checked here for the reason serve.js checks the issuer: a test would only report
      // that a screen was refused, not that the User it signed in as was ordinary.
      if (role !== "admin") {
        throw new Error(`an admin Invitation produced a ${role}, not an admin`);
      }

      await use(user);
    },
    { scope: "worker" },
  ],
});

// A name no other test, in any worker, will make: for a User, a Role, anything listed on a
// screen every worker shares. bitmagnet's usernames are
// ^[a-zA-Z0-9][a-zA-Z0-9._-]{1,18}[a-zA-Z0-9]$, so twenty characters is the ceiling and a
// timestamp does not fit under it.
export function uniqueName(prefix) {
  return `${prefix}-${crypto.randomBytes(3).toString("hex")}`;
}

// A username and password nobody chose. The password is a real User's for as long as the
// run lasts, so the suite's claim to hold no password should stay literally true.
function newUser(prefix) {
  return { username: uniqueName(prefix), password: crypto.randomBytes(24).toString("base64url") };
}

// Signs in through the form, the way a person does, and waits for the Identity that follows
// rather than for the navigation. A successful login replaces the URL and then refetches
// self.identity; asserting on the URL alone would pass while the header still said Anonymous.
export async function signIn(page, credentials) {
  await page.getByLabel("Username").fill(credentials.username);
  await page.getByLabel("Password").fill(credentials.password);
  await page.getByRole("button", { name: "Sign in" }).click();

  await expect(page.getByRole("button", { name: credentials.username })).toBeVisible();
}

// Signs `user` in, then opens `path` and waits for `heading`: the screen has loaded.
export async function signInAt(page, user, path, heading) {
  await page.goto("/login");
  await signIn(page, user);
  await page.goto(path);
  await expect(page.getByRole("heading", { name: heading })).toBeVisible();
}

// Runs `act` with a page in a browser context of its own: another person, or another device.
// It shares nothing with the test's own page, the cookie included.
export async function inAnotherBrowser(browser, act) {
  const context = await browser.newContext();
  try {
    return await act(await context.newPage());
  } finally {
    await context.close();
  }
}

// Registers a User of the test's own, through the form the way a person does, and returns
// its credentials. It lands on the login form, signed out, with the username filled in. For
// a test that acts on a User rather than as one (see e2e/README.md), or that needs the core
// `user` Role, which an Invitation with no Role grants.
export async function registerUser(page, request, issuer, prefix) {
  return registerThrough(page, await mintInvitation(request, issuer), prefix);
}

// Registers through the Invitation `code` names, as a person following its link would.
export async function registerThrough(page, code, prefix) {
  const registered = newUser(prefix);

  await page.goto(`/register?code=${code}`);
  await expect(page.getByLabel("Invitation code")).toHaveValue(code);
  await page.getByLabel("Username").fill(registered.username);
  await page.getByLabel("Password", { exact: true }).fill(registered.password);
  await page.getByRole("button", { name: "Register" }).click();
  await expect(page.getByRole("button", { name: "Sign in" })).toBeVisible();

  return registered;
}

// Gives `username` the Role `role` on the User administration screen, which `page` must be
// on, and waits for bitmagnet to have accepted it: a refusal fails here, not later as a
// confusing absence somewhere else.
export async function setRole(page, username, role) {
  await page.getByLabel("Find a User").fill(username);
  const select = page.getByLabel(`Role for ${username}`, { exact: true });
  const answered = page.waitForResponse((response) =>
    (response.request().postData() ?? "").includes("setUserRole"),
  );
  await select.selectOption(role);

  const body = await (await answered).json();
  expect(body.errors).toBeUndefined();
}

// Creates a Role holding `objectActions` on the Role administration screen, which `page`
// must be on, and waits for bitmagnet to have saved it.
export async function createRole(page, name, objectActions) {
  await page.getByLabel("Name").fill(name);
  for (const objectAction of objectActions) {
    await page.getByRole("checkbox", { name: objectAction, exact: true }).check();
  }
  await page.getByRole("button", { name: "Create Role" }).click();
  await expect(page.getByRole("status")).toHaveText(`Saved the Role ${name}.`);
}

// Mints an Invitation, granting `role` or, without one, bitmagnet's default. Done over the API
// with a bearer credential rather than through the browser, deliberately: the point of the
// test that wants one is the registration, and driving the administration screen to get there
// would make an unrelated screen's markup a reason for it to fail.
//
// Always as the issuer, never as a worker's User, whose sessions any of that worker's tests
// may end by signing out. The run's bootstrap Invitation is already spent —
// e2e/harness/serve.js claimed it to create the issuer — so there is no other way to get one.
export async function mintInvitation(request, issuer, role) {
  const login = await call(request, issuer.graphqlEndpoint, {
    query: "mutation Login($username: String!, $password: String!) { self { login(username: $username, password: $password) { token } } }",
    variables: { username: issuer.username, password: issuer.password },
  });

  const invitation = await call(
    request,
    issuer.graphqlEndpoint,
    {
      query: "mutation Invite($input: InviteInput!) { auth { invite(input: $input) { code } } }",
      variables: { input: role ? { role } : {} },
    },
    login.self.login.token,
  );

  return invitation.auth.invite.code;
}

async function call(request, graphqlEndpoint, body, token) {
  const response = await request.post(graphqlEndpoint, {
    data: body,
    headers: token ? { authorization: `Bearer ${token}` } : {},
  });

  const answered = await response.json();
  if (answered.errors) {
    throw new Error(`the fixture server refused a harness request: ${JSON.stringify(answered.errors)}`);
  }

  return answered.data;
}
