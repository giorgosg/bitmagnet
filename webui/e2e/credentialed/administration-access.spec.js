// Who gets the administration screens' controls (ticket 21; the acceptance criteria of
// tickets 13 to 15). Reading them needs `auth::query`, and every act on them `auth::mutate`.
// An Identity without the first is refused the screens; one with only the first sees them
// and nothing it could act with. The Permissions are bitmagnet's to enforce either way:
// these check that Magnes offers no control that would only be refused.

import {
  createRole,
  inAnotherBrowser,
  registerUser,
  setRole,
  signIn,
  signInAt,
  uniqueName,
  expect,
  test,
} from "../support/credentialed.js";

const screens = ["/admin/users", "/admin/invitations", "/admin/roles"];

test("an Identity without administration is refused every administration screen", async ({
  page,
  request,
  issuer,
}) => {
  const ordinary = await registerUser(page, request, issuer, "e2e-noadmin");
  await signIn(page, ordinary);

  for (const screen of screens) {
    await page.goto(screen);
    await expect(page.getByText("Your Identity does not permit administration.")).toBeVisible();
    await expect(page.getByLabel("Find a User")).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Create Invitation" })).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Create Role" })).toHaveCount(0);
  }
});

test("an Identity that may read administration but not change it is offered no control", async ({
  page,
  browser,
  request,
  issuer,
  credentials,
}) => {
  const readOnly = uniqueName("e2e-reader");
  await signInAt(page, credentials, "/admin/roles", "Create a Role");
  await createRole(page, readOnly, ["auth::query"]);

  await inAnotherBrowser(browser, async (them) => {
    const reader = await registerUser(them, request, issuer, "e2e-reader");
    await page.goto("/admin/users");
    await setRole(page, reader.username, readOnly);
    await signIn(them, reader);

    // The listings are there, so reading is allowed; every act on them is not offered.
    await them.goto("/admin/users");
    await them.getByLabel("Find a User").fill(reader.username);
    await expect(them.getByRole("listitem").filter({ hasText: reader.username })).toBeVisible();
    await expect(them.getByRole("listitem").getByRole("combobox")).toHaveCount(0);
    for (const act of ["Disable", "Enable", "Delete"]) {
      await expect(them.getByRole("button", { name: act })).toHaveCount(0);
    }

    await them.goto("/admin/invitations");
    await expect(them.getByRole("heading", { name: "Invitations" })).toBeVisible();
    await expect(them.getByRole("listitem").first()).toBeVisible();
    await expect(them.getByRole("button", { name: "Create Invitation" })).toHaveCount(0);
    await expect(them.getByRole("button", { name: "Withdraw" })).toHaveCount(0);

    await them.goto("/admin/roles");
    await expect(them.getByText(readOnly, { exact: true })).toBeVisible();
    await expect(them.getByRole("button", { name: "Create Role" })).toHaveCount(0);
    await expect(them.getByRole("button", { name: "Delete" })).toHaveCount(0);
  });
});
