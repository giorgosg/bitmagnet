// This changes the shared anon Role, so its project runs after the ordinary and queue
// projects have finished. Every test restores the exact stored grants in finally, using
// the administrator's cookie through the API even if an assertion interrupted the form.
import {
  inAnotherBrowser,
  signInAt,
  expect,
  test,
} from "../support/credentialed.js";

const permissionsQuery =
  "{ auth { listRoles { name permissions { core objectAction { namespace object action } } } } }";

async function graphql(page, query, variables) {
  const response = await page.request.post("/graphql", {
    headers: { origin: new URL(page.url()).origin },
    data: { query, variables },
  });
  const body = await response.json();
  expect(body.errors).toBeUndefined();
  return body.data;
}

async function storedAnonymous(page) {
  const result = await graphql(page, permissionsQuery);
  return result.auth.listRoles
    .find((role) => role.name === "anon")
    .permissions.filter((permission) => !permission.core)
    .map((permission) => permission.objectAction);
}

async function restoreAnonymous(page, objectActions) {
  await graphql(
    page,
    'mutation RestoreAnon($objectActions: [AuthObjectActionInput!]!) { auth { putRole(role: "anon", objectActions: $objectActions) { name } } }',
    { objectActions },
  );
  expect(await storedAnonymous(page)).toEqual(
    expect.arrayContaining(objectActions),
  );
  expect((await storedAnonymous(page)).length).toBe(objectActions.length);
}

async function editAnonymous(page) {
  await page.goto("/admin/roles");
  const row = page
    .getByRole("listitem")
    .filter({ has: page.getByText("anon", { exact: true }) });
  await row.getByRole("button", { name: "Edit" }).click();
  await expect(
    page.getByRole("heading", { name: "Editing anon" }),
  ).toBeVisible();
}

function permission(page, name) {
  return page.getByRole("checkbox", { name, exact: true });
}

async function saveAnonymous(page) {
  await page.getByRole("button", { name: "Save anon", exact: true }).click();
  await expect(page.getByRole("status")).toHaveText("Saved the Role anon.");
}

test.beforeEach(async ({ page, credentials }) => {
  await signInAt(page, credentials, "/admin/roles", "Create a Role");
});

test("an administrator grants and revokes an anon read action, and Anonymous loses it", async ({
  page,
  browser,
}) => {
  const original = await storedAnonymous(page);
  try {
    await editAnonymous(page);
    await expect(
      page.getByText("Magnes cannot see whether", { exact: false }),
    ).toBeVisible();
    await expect(permission(page, "auth::query")).toHaveCount(0);
    await expect(permission(page, "auth::mutate")).toHaveCount(0);
    await permission(page, "torrent::query").check();
    await saveAnonymous(page);
    await editAnonymous(page); // Refetched listRoles, rather than the submitted draft.
    await expect(permission(page, "torrent::query")).toBeChecked();

    await inAnotherBrowser(browser, async (anonymous) => {
      await anonymous.goto("/stats/torrents");
      await expect(
        anonymous.getByRole("heading", { name: "Torrent statistics" }),
      ).toBeVisible();
      await expect(
        anonymous.getByRole("link", { name: "Sign in", exact: true }),
      ).toBeVisible();

      await permission(page, "torrent::query").uncheck();
      await saveAnonymous(page);
      await editAnonymous(page);
      await expect(permission(page, "torrent::query")).not.toBeChecked();
      await anonymous.reload();
      await expect(
        anonymous.getByText(
          "Your Identity does not permit reading bitmagnet's torrents.",
        ),
      ).toBeVisible();
      await expect(
        anonymous.getByRole("heading", { name: "Torrent statistics" }),
      ).toHaveCount(0);
    });
  } finally {
    await restoreAnonymous(page, original);
  }
});

test("anon writes require explicit cross-origin confirmation, including a repeated Enter", async ({
  page,
}) => {
  const original = await storedAnonymous(page);
  try {
    await editAnonymous(page);
    const before = await storedAnonymous(page);
    await permission(page, "torrent::delete").check();
    await page.getByRole("button", { name: "Save anon", exact: true }).click();
    const ask = page.getByRole("alert");
    await expect(ask).toContainText("cross-origin");
    await expect(ask).toContainText("CORS defaults to *");
    // Submitting the form again must only ask again, never accept the warning.
    await permission(page, "torrent::delete").press("Enter");
    expect(await storedAnonymous(page)).toEqual(before);
    await ask.getByRole("button", { name: "Keep", exact: true }).click();
    expect(await storedAnonymous(page)).toEqual(before);
    await page.getByRole("button", { name: "Save anon", exact: true }).click();
    await ask
      .getByRole("button", { name: "Allow Anonymous writes", exact: true })
      .click();
    await expect(page.getByRole("status")).toHaveText("Saved the Role anon.");
    await editAnonymous(page);
    await expect(permission(page, "torrent::delete")).toBeChecked();
  } finally {
    await restoreAnonymous(page, original);
  }
});

test("pre-existing anon auth and wildcard grants are explained and carried, never offered", async ({
  page,
}) => {
  const original = await storedAnonymous(page);
  const carried = [
    { namespace: "graphql", object: "auth", action: "query" },
    { namespace: "graphql", object: "auth", action: "mutate" },
    { namespace: "http", object: "**", action: "**" },
  ];
  try {
    // These grants came from another client. The editor must report and preserve them,
    // and the carried write wildcard must not evade confirmation merely by having no box.
    await restoreAnonymous(page, carried);
    await page.goto("/admin/roles");
    await page.getByLabel("Name").fill(" anon ");
    await expect(
      page.getByRole("heading", { name: "Editing anon" }),
    ).toBeVisible();
    await expect(permission(page, "auth::query")).toHaveCount(0);
    await expect(permission(page, "auth::mutate")).toHaveCount(0);
    await expect(
      page.getByText("Saving preserves this access", { exact: false }),
    ).toBeVisible();
    await expect(
      page.getByText("graphql::auth::query", { exact: false }),
    ).toBeVisible();
    await expect(
      page.getByText("http::**::**", { exact: false }),
    ).toBeVisible();
    await permission(page, "torrent::query").check();
    await page.getByRole("button", { name: "Save anon", exact: true }).click();
    await expect(page.getByRole("alert")).toContainText("cross-origin");
    await page
      .getByRole("button", { name: "Allow Anonymous writes", exact: true })
      .click();
    await expect(page.getByRole("status")).toHaveText("Saved the Role anon.");
    expect(await storedAnonymous(page)).toEqual(
      expect.arrayContaining(carried),
    );
  } finally {
    await restoreAnonymous(page, original);
  }
});
