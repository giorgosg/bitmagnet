// The test-side half of the credentialed harness: where a spec gets a User from.
//
// Every worker has an administrator of its own. bitmagnet ends every session for a User when
// that User signs out (bitmagnet 77f3fd9e3), so a User shared across parallel workers had
// its sessions revoked by whichever test signed out, mid-way through other workers' tests.
// Now a sign-out can only reach later tests in its own worker, which sign in again anyway.
//
// The User e2e/harness/serve.js registered through the bootstrap Invitation is the issuer:
// it mints the Invitations the workers register through, over the API, and never signs in
// through a browser, so no test can sign it out. Its credentials are read from the file
// serve.js wrote before it started the dev server Playwright waited on. Nothing is committed
// and nothing is asked of a person; every User goes away with the database when the run ends.

import { test as base, expect } from "@playwright/test";
import crypto from "crypto";
import fs from "fs";

export { expect };

export const test = base.extend({
  // The harness's administrator. Only for minting Invitations: a test that signs in as it
  // could sign it out, and that would revoke the bearer token every other worker mints with.
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
      const request = await playwright.request.newContext();
      try {
        const code = await mintInvitation(request, issuer, "admin");
        const user = newUser(`e2e-w${workerInfo.workerIndex}`);
        const registered = await call(request, issuer.graphqlEndpoint, {
          query:
            "mutation Register($input: RegisterInput!) " +
            "{ self { register(input: $input) { user { role } } } }",
          variables: { input: { invitationCode: code, ...user } },
        });

        const role = registered.self.register.user.role;
        if (role !== "admin") {
          // Every credentialed spec assumes administration is reachable. Finding out here
          // says which assumption broke; finding out in a test says only that a screen was
          // refused.
          throw new Error(`an admin Invitation produced a ${role}, not an admin`);
        }

        await use({ ...user, graphqlEndpoint: issuer.graphqlEndpoint });
      } finally {
        await request.dispose();
      }
    },
    { scope: "worker" },
  ],
});

// A username and password nobody chose. bitmagnet's usernames are
// ^[a-zA-Z0-9][a-zA-Z0-9._-]{1,18}[a-zA-Z0-9]$, so twenty characters is the ceiling and a
// timestamp does not fit under it. The password is a real User's for as long as the run
// lasts, so the suite's claim to hold no password should stay literally true.
function newUser(prefix) {
  return {
    username: `${prefix}-${crypto.randomBytes(3).toString("hex")}`,
    password: crypto.randomBytes(24).toString("base64url"),
  };
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

// Registers a User of the test's own, through the form the way a person does, and returns
// its credentials. It lands on the login form, signed out, with the username filled in. For
// a test that acts on a User rather than as one (see e2e/README.md), or that needs the core
// `user` Role, which an Invitation with no Role grants.
export async function registerUser(page, request, issuer, prefix) {
  const code = await mintInvitation(request, issuer);
  const registered = newUser(prefix);

  await page.goto(`/register?code=${code}`);
  await page.getByLabel("Username").fill(registered.username);
  await page.getByLabel("Password", { exact: true }).fill(registered.password);
  await page.getByRole("button", { name: "Register" }).click();
  await expect(page.getByRole("button", { name: "Sign in" })).toBeVisible();

  return registered;
}

// Mints an Invitation, granting `role` or, without one, bitmagnet's default. Done over the API
// with a bearer credential rather than through the browser, deliberately: the point of the
// test that wants one is the registration, and driving the administration screen to get there
// would make an unrelated screen's markup a reason for it to fail.
//
// Always as the issuer, never as a worker's User: a worker that signs its own User out would
// otherwise revoke the token this is using. The run's bootstrap Invitation is already spent —
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
