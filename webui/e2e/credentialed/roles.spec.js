// Role administration, driven against a real bitmagnet (ticket 21). Until now it had only
// been checked as a throwaway all-states preview, which found layout defects and exercised
// no server behaviour.
//
// Every Role made here has a name of its own, and every User given one is registered for
// its test. bitmagnet recompiles its policy as soon as a Role is written, in the one process
// the fixture server runs, so nothing waits out `auth.rbac_cache_ttl`.

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

// A Permission's box, by the Object action it stands for.
function permission(page, objectAction) {
  return page.getByRole("checkbox", { name: objectAction, exact: true });
}

// The listed Role, by its exact name.
function listedRole(page, name) {
  return page.getByRole("listitem").filter({ has: page.getByText(name, { exact: true }) });
}

async function editRole(page, name) {
  await listedRole(page, name).getByRole("button", { name: "Edit" }).click();
  await expect(page.getByRole("heading", { name: `Editing ${name}` })).toBeVisible();
}

test.beforeEach(async ({ page, credentials }) => {
  await signInAt(page, credentials, "/admin/roles", "Create a Role");
});

test("creates a Role with the Permissions chosen, and an edit keeps those it did not touch", async ({
  page,
}) => {
  const name = uniqueName("e2e-role");
  await createRole(page, name, ["torrent::query", "version::query"]);

  await editRole(page, name);
  await expect(permission(page, "torrent::query")).toBeChecked();
  await expect(permission(page, "version::query")).toBeChecked();
  await permission(page, "health::query").check();
  await page.getByRole("button", { name: `Save ${name}` }).click();
  await expect(page.getByRole("status")).toHaveText(`Saved the Role ${name}.`);

  // Read back from the server, not from the form: `putRole` replaces the whole set, so a
  // Permission the edit did not touch survives only if the form sent it.
  await page.reload();
  await editRole(page, name);
  await expect(permission(page, "torrent::query")).toBeChecked();
  await expect(permission(page, "version::query")).toBeChecked();
  await expect(permission(page, "health::query")).toBeChecked();
  await expect(permission(page, "auth::query")).not.toBeChecked();
});

test("deletes a custom Role only after asking, and offers no deletion of a core Role", async ({
  page,
}) => {
  const name = uniqueName("e2e-role");
  await createRole(page, name, ["version::query"]);

  const row = listedRole(page, name);
  await row.getByRole("button", { name: "Delete" }).click();
  await row.getByRole("button", { name: "Keep" }).click();
  await expect(row.getByRole("button", { name: "Keep" })).toHaveCount(0);
  await expect(row.getByRole("button", { name: "Delete" })).toBeEnabled();

  await row.getByRole("button", { name: "Delete" }).click();
  await row.getByRole("button", { name: "Delete" }).click();
  await expect(listedRole(page, name)).toHaveCount(0);
  await page.reload();
  await expect(page.getByRole("heading", { name: "Create a Role" })).toBeVisible();
  await expect(listedRole(page, name)).toHaveCount(0);

  for (const core of ["admin", "editor", "user", "anon"]) {
    const coreRow = listedRole(page, core);
    await expect(coreRow).toHaveCount(1);
    await expect(coreRow.getByRole("button", { name: "Delete" })).toHaveCount(0);
  }
});

test("a change to a Role reaches the Identity of a User who holds it", async ({
  page,
  browser,
  request,
  issuer,
}) => {
  const name = uniqueName("e2e-role");
  await createRole(page, name, ["auth::query", "torrentContent::query"]);

  await inAnotherBrowser(browser, async (them) => {
    const holder = await registerUser(them, request, issuer, "e2e-holder");
    await page.goto("/admin/users");
    await setRole(page, holder.username, name);

    await signIn(them, holder);
    await them.getByRole("button", { name: holder.username }).click();
    await expect(them.getByRole("link", { name: "Users", exact: true })).toBeVisible();

    // Take administration away from the Role. Nothing tells the holder's browser; its next
    // request is told by bitmagnet.
    await page.goto("/admin/roles");
    await editRole(page, name);
    await permission(page, "auth::query").uncheck();
    await page.getByRole("button", { name: `Save ${name}` }).click();
    await expect(page.getByRole("status")).toHaveText(`Saved the Role ${name}.`);

    await them.reload();
    await them.getByRole("button", { name: holder.username }).click();
    await expect(them.getByRole("link", { name: "Your User" })).toBeVisible();
    await expect(them.getByRole("link", { name: "Users", exact: true })).toHaveCount(0);
    await them.goto("/admin/users");
    await expect(them.getByText("Your Identity does not permit administration.")).toBeVisible();
  });
});
