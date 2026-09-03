// The test-side half of the credentialed harness: where a spec gets a User from.
//
// The credentials are read from the file e2e/harness/serve.js wrote before it started the
// dev server Playwright waited on, so they are there by the time any test runs. Nothing is
// committed and nothing is asked of a person: the User was registered this run through the
// fixture server's bootstrap Invitation, and goes away with the database when the run ends.

import { test as base, expect } from "@playwright/test";
import fs from "fs";

export { expect };

export const test = base.extend({
  credentials: [
    // Worker-scoped: every test signs in as the same administrator, and re-reading a file
    // per test would say nothing new.
    async ({}, use) => {
      const file = process.env.MAGNES_E2E_CREDENTIALS;
      if (!file || !fs.existsSync(file)) {
        throw new Error(
          "no harness credentials. This project is started by e2e/harness/serve.js; " +
            "run it with `npm run test:e2e:credentialed`. See e2e/README.md.",
        );
      }

      await use(JSON.parse(fs.readFileSync(file, "utf8")));
    },
    { scope: "worker" },
  ],
});

// Signs in through the form, the way a person does, and waits for the Identity that follows
// rather than for the navigation. A successful login replaces the URL and then refetches
// self.identity; asserting on the URL alone would pass while the header still said Anonymous.
export async function signIn(page, credentials) {
  await page.getByLabel("Username").fill(credentials.username);
  await page.getByLabel("Password").fill(credentials.password);
  await page.getByRole("button", { name: "Sign in" }).click();

  await expect(page.getByRole("button", { name: credentials.username })).toBeVisible();
}

// Mints an Invitation for a test that needs to register someone. Done over the API with a
// bearer credential rather than through the browser, deliberately: the point of the test
// that wants one is the registration, and driving the administration screen to get there
// would make an unrelated screen's markup a reason for it to fail.
//
// The run's bootstrap Invitation is already spent — e2e/harness/serve.js claimed it to
// create this administrator — so there is no other way to get a second one.
export async function mintInvitation(request, credentials) {
  const login = await call(request, credentials, {
    query: "mutation Login($username: String!, $password: String!) { self { login(username: $username, password: $password) { token } } }",
    variables: { username: credentials.username, password: credentials.password },
  });

  const invitation = await call(
    request,
    credentials,
    { query: "mutation Invite { auth { invite(input: {}) { code } } }" },
    login.self.login.token,
  );

  return invitation.auth.invite.code;
}

async function call(request, credentials, body, token) {
  const response = await request.post(credentials.graphqlEndpoint, {
    data: body,
    headers: token ? { authorization: `Bearer ${token}` } : {},
  });

  const answered = await response.json();
  if (answered.errors) {
    throw new Error(`the fixture server refused a harness request: ${JSON.stringify(answered.errors)}`);
  }

  return answered.data;
}
