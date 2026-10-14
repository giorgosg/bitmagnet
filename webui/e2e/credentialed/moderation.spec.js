import fs from "node:fs/promises";
import path from "node:path";
import {
  expect,
  test,
  signInAt,
  signIn,
  registerUser,
  inAnotherBrowser,
  setRole,
  createRole,
  uniqueName,
} from "../support/credentialed.js";

test("a moderator reaches the private review queue", async ({
  page,
  credentials,
}) => {
  await signInAt(page, credentials, "/moderation", "Moderation");
  await expect(
    page.getByRole("link", { name: "Allowed", exact: true }),
  ).toBeVisible();
});

test("downloads only shareable local bans with automatic origin on deliberate inclusion", async ({
  page,
  credentials,
}, testInfo) => {
  await signInAt(page, credentials, "/moderation", "Moderation");
  const downloadFile = async (label) => {
    const pending = page.waitForEvent("download");
    await page.getByRole("link", { name: label, exact: true }).click();
    const download = await pending;
    expect(download.suggestedFilename()).toBe("moderation-bans-v1.json");
    expect(await download.failure()).toBeNull();
    const saved = testInfo.outputPath(
      label.includes("automatic")
        ? "moderation-bans-automatic.json"
        : "moderation-bans-reviewed.json",
    );
    await download.saveAs(saved);
    return JSON.parse(await fs.readFile(saved, "utf8"));
  };
  const reviewed = await downloadFile("Export reviewed bans");
  expect(Object.keys(reviewed).sort()).toEqual([
    "entries",
    "identifierFormat",
    "version",
  ]);
  expect(reviewed.version).toBe(1);
  expect(reviewed.identifierFormat).toBe("sha256-btih-v1");
  expect(reviewed.entries).toEqual([
    {
      digest:
        "10b514348d2b2f2c4c530f249918be96529db6afcb6480ed0f476a6e7f5e8417",
      origin: "moderator",
    },
  ]);
  const automatic = await downloadFile("Export reviewed and automatic bans");
  expect(automatic.entries).toEqual(expect.arrayContaining(reviewed.entries));
  expect(automatic.entries).toHaveLength(2);
  expect(automatic.entries).toContainEqual({
    digest: "e34312e8ab124d4db5b5f99ae20fae6a11b48ad3eda2b7de6c5f9c564204fd69",
    origin: "automatic",
  });
  for (const entry of automatic.entries)
    expect(Object.keys(entry).sort()).toEqual(["digest", "origin"]);
  await expect(
    page.getByRole("heading", { name: "Moderation", exact: true }),
  ).toBeVisible();
});

const hash = (number) =>
  `fab7${number.toString(16).padStart(2, "0")}${"00".repeat(17)}`;
const name = (number) =>
  `Harmless ${number >= 10 && number !== 54 && number !== 55 ? "pagination " : ""}synthetic review marker handbook ${number.toString().padStart(2, "0")}.pdf`;

async function graphql(page, query, variables = {}) {
  return page.evaluate(
    async ({ query, variables }) => {
      const response = await fetch("/graphql", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ query, variables }),
      });
      return response.json();
    },
    { query, variables },
  );
}

async function openReview(page, number) {
  await page.goto(`/moderation?q=${encodeURIComponent(name(number))}`);
  await page.getByRole("link", { name: name(number), exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "Review details" }),
  ).toBeVisible();
}

async function decide(page, action, status) {
  await page.getByRole("button", { name: action, exact: true }).click();
  await page.getByRole("button", { name: "Confirm decision" }).click();
  await expect(page.getByRole("status")).toContainText(`Saved: ${status}.`);
}

test("pending review can be allowed, classified, searched and explicitly reopened and banned", async ({
  page,
  credentials,
}) => {
  await signInAt(page, credentials, "/moderation", "Moderation");
  await openReview(page, 1);
  const reviewURL = page.url();
  await expect(
    page.getByText("full/private/handbook 01.pdf", { exact: true }),
  ).toBeVisible();
  await expect(
    page
      .locator(".moderation-page")
      .getByRole("link", { name: /magnet|download torrent/i }),
  ).toHaveCount(0);
  await expect(page.locator('a[href^="magnet:"]')).toHaveCount(0);
  await page.goto(`/search?q=${encodeURIComponent(name(1))}`);
  await expect(page.getByText(name(1), { exact: true })).toHaveCount(0);
  await page.goto(reviewURL);
  await decide(page, "Allow", "allowed");
  await expect(page.getByRole("status")).toContainText(
    "Normal classification is queued.",
  );
  const processed = await graphql(
    page,
    `
      mutation ($input: TorrentReprocessInput!) {
        torrent {
          reprocess(input: $input)
        }
      }
    `,
    {
      input: {
        infoHashes: [hash(1)],
        apisDisabled: true,
        localSearchDisabled: true,
      },
    },
  );
  expect(processed.errors).toBeUndefined();
  await page.getByRole("link", { name: "Allowed", exact: true }).click();
  await expect(
    page.getByRole("link", { name: name(1), exact: true }),
  ).toBeVisible();
  await page.goto(`/search?q=${encodeURIComponent(name(1))}`);
  await expect(page.getByText(name(1), { exact: true }).first()).toBeVisible();
  const reprocessed = await graphql(
    page,
    `
      mutation ($input: TorrentReprocessInput!) {
        torrent {
          reprocess(input: $input)
        }
      }
    `,
    {
      input: {
        infoHashes: [hash(1)],
        classifierRematch: true,
        apisDisabled: true,
        localSearchDisabled: true,
      },
    },
  );
  expect(reprocessed.errors).toBeUndefined();
  await page.goto(reviewURL);
  await expect(
    page.locator("dd").filter({ hasText: /^allowed$/ }),
  ).toBeVisible();
  await decide(page, "Reopen", "pending");
  await decide(page, "Ban", "banned");
  await page.getByRole("link", { name: "Banned", exact: true }).click();
  await expect(
    page.getByRole("link", { name: name(1), exact: true }),
  ).toBeVisible();
});

test("search is bounded and bookmarkable and handles an empty list", async ({
  page,
  credentials,
}) => {
  await signInAt(
    page,
    credentials,
    "/moderation?q=pagination%20synthetic%20review%20marker",
    "Moderation",
  );
  await expect(page.locator(".moderation-page li > a")).toHaveCount(50);
  await page.getByRole("link", { name: "Next page", exact: true }).click();
  await expect(page).toHaveURL(/after=/);
  await expect(page.locator(".moderation-page li > a")).toHaveCount(4);
  await expect(
    page.getByRole("link", { name: "Next page", exact: true }),
  ).toHaveCount(0);
  await page.getByRole("link", { name: "First page", exact: true }).click();
  await expect(page.locator(".moderation-page li > a")).toHaveCount(50);
  await page
    .getByLabel("Find a moderation record")
    .fill("full/private/handbook 10.pdf");
  await page.getByRole("button", { name: "Search decisions" }).click();
  await expect(page).toHaveURL(/q=full/);
  await expect(
    page.getByRole("link", { name: name(10), exact: true }),
  ).toBeVisible();
  await page.reload();
  await expect(page.getByLabel("Find a moderation record")).toHaveValue(
    "full/private/handbook 10.pdf",
  );
  await page
    .getByLabel("Find a moderation record")
    .fill("no synthetic record has this phrase");
  await page.getByRole("button", { name: "Search decisions" }).click();
  await expect(
    page.getByText("No moderation records match this search."),
  ).toBeVisible();
});

test("a digest-only record remains reviewable and purge keeps its decision", async ({
  page,
  credentials,
}) => {
  await signInAt(page, credentials, "/moderation?status=banned", "Moderation");
  await page
    .getByRole("link", { name: "Metadata unavailable", exact: true })
    .click();
  await expect(
    page.getByText(
      "Metadata unavailable. Evidence may not have been observed or may have been purged.",
    ),
  ).toBeVisible();
  await expect(
    page
      .locator(".moderation-page")
      .getByRole("link", { name: /magnet|download torrent/i }),
  ).toHaveCount(0);
  await decide(page, "Allow", "allowed");
  await expect(page.getByRole("status")).not.toContainText(
    "classification is queued",
  );
  await page
    .getByRole("button", { name: "Purge evidence", exact: true })
    .click();
  await expect(
    page.getByText(
      "Permanently clear retained metadata and prior private history? The decision remains enforced.",
    ),
  ).toBeVisible();
  await page.getByRole("button", { name: "Cancel", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "Confirm decision" }),
  ).toHaveCount(0);
  await decide(page, "Purge evidence", "allowed");
  await expect(
    page.locator("dd").filter({ hasText: /^allowed$/ }),
  ).toBeVisible();
  await expect(
    page.locator(".moderation-page li").filter({ hasText: /^purge/ }),
  ).toHaveCount(1);
  await expect(
    page.locator(".moderation-page li").filter({ hasText: /^allow/ }),
  ).toHaveCount(0);
});

test("flag retains catalogue metadata privately without a download action", async ({
  page,
  credentials,
}) => {
  await signInAt(page, credentials, "/moderation", "Moderation");
  await page.getByLabel("Torrent infohash").fill(hash(0));
  await page.getByRole("button", { name: "Flag for review" }).click();
  await expect(
    page.getByRole("heading", { name: "Review details" }),
  ).toBeVisible();
  await expect(page).toHaveURL(/record=/);
  await expect(
    page.getByText("Harmless catalogue handbook.pdf", { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText("full/private/handbook 00.pdf", { exact: true }),
  ).toBeVisible();
  await expect(page.locator('a[href^="magnet:"]')).toHaveCount(0);
});

test("a stale decision fails without showing success or overwriting another moderator", async ({
  page,
  credentials,
}) => {
  await signInAt(page, credentials, "/moderation", "Moderation");
  await openReview(page, 2);
  const identifier = new URL(page.url()).searchParams.get("record");
  const current = await graphql(
    page,
    `
      query ($id: String!) {
        moderation {
          record(identifier: $id) {
            revision
          }
        }
      }
    `,
    { id: identifier },
  );
  const changed = await graphql(
    page,
    `
      mutation ($input: ModerationDecisionInput!) {
        moderation {
          ban(input: $input) {
            status
          }
        }
      }
    `,
    {
      input: {
        identifier,
        expectedRevision: current.data.moderation.record.revision,
      },
    },
  );
  expect(changed.errors).toBeUndefined();
  await page.getByRole("button", { name: "Allow", exact: true }).click();
  await page.getByRole("button", { name: "Confirm decision" }).click();
  await expect(page.getByRole("alert")).toContainText(
    "The decision was not saved.",
  );
  await expect(page.getByText(/Saved:/)).toHaveCount(0);
  await expect(
    page.getByRole("button", { name: "Allow", exact: true }),
  ).toBeDisabled();
  await page.getByRole("button", { name: "Refresh review" }).click();
  await expect(
    page.locator("dd").filter({ hasText: /^banned$/ }),
  ).toBeVisible();
});

test("a lost mutation answer leaves an unknown outcome until refreshed", async ({
  page,
  credentials,
}) => {
  await signInAt(page, credentials, "/moderation", "Moderation");
  await openReview(page, 3);
  await page.route("**/graphql", async (route) => {
    if ((route.request().postData() ?? "").includes("allow(")) {
      await route.fetch();
      await route.abort("failed");
    } else await route.continue();
  });
  await page.getByRole("button", { name: "Allow", exact: true }).click();
  await page.getByRole("button", { name: "Confirm decision" }).click();
  await expect(page.getByRole("alert")).toContainText(
    "The decision outcome is unknown.",
  );
  await expect(page.getByText(/Saved:/)).toHaveCount(0);
  await expect(
    page.getByRole("button", { name: "Allow", exact: true }),
  ).toBeDisabled();
  await page.unroute("**/graphql");
  await page.getByRole("button", { name: "Refresh review" }).click();
  await expect(
    page.locator("dd").filter({ hasText: /^allowed$/ }),
  ).toBeVisible();
});

test("search failure is legible and retry replaces the failed look", async ({
  page,
  credentials,
}) => {
  await signInAt(page, credentials, "/moderation", "Moderation");
  await page.route("**/graphql", (route) =>
    (route.request().postData() ?? "").includes("moderation")
      ? route.abort("failed")
      : route.continue(),
  );
  await page.getByRole("button", { name: "Refresh review" }).click();
  await expect(page.getByRole("alert")).toBeVisible();
  await expect(page.getByText(/Saved:/)).toHaveCount(0);
  await page.unroute("**/graphql");
  await page.getByRole("button", { name: "Refresh review" }).click();
  await expect(page.locator(".moderation-page li > a")).toHaveCount(50);
});

test("ordinary reader and editor cannot enter review or invoke moderation actions", async ({
  page,
  browser,
  request,
  issuer,
  credentials,
}) => {
  await signInAt(page, credentials, "/admin/users", "Users");
  for (const role of ["user", "editor"]) {
    await inAnotherBrowser(browser, async (them) => {
      const user = await registerUser(them, request, issuer, `e2e-${role}`);
      if (role === "editor") await setRole(page, user.username, "editor");
      await signIn(them, user);
      await them.goto("/moderation?status=banned&record=" + "a".repeat(64));
      await expect(
        them.getByText(
          "Your Identity does not permit private moderation review.",
        ),
      ).toBeVisible();
      await expect(
        them.getByRole("heading", { name: "Moderation" }),
      ).toHaveCount(0);
      await expect(
        them.getByRole("button", { name: "Allow", exact: true }),
      ).toHaveCount(0);
      await them.getByRole("button", { name: user.username }).click();
      await expect(
        them.getByRole("link", { name: "Moderation", exact: true }),
      ).toHaveCount(0);
      const read = await graphql(
        them,
        `
          query {
            moderation {
              search(input: { limit: 1 }) {
                items {
                  identifier
                }
              }
            }
          }
        `,
      );
      expect(read.errors[0].extensions.code).toBe("UNAUTHORIZED");
      const action = await graphql(
        them,
        `
          mutation ($hash: Hash20!) {
            moderation {
              flag(infoHash: $hash) {
                identifier
              }
            }
          }
        `,
        { hash: hash(4) },
      );
      expect(action.errors[0].extensions.code).toBe("UNAUTHORIZED");
    });
  }
});

test("private query Permission alone reads evidence but offers no decision controls", async ({
  page,
  browser,
  request,
  issuer,
  credentials,
}) => {
  const role = uniqueName("e2e-review");
  await signInAt(page, credentials, "/admin/roles", "Create a Role");
  await createRole(page, role, ["moderation::query"]);
  await inAnotherBrowser(browser, async (them) => {
    const reader = await registerUser(them, request, issuer, "e2e-private");
    await page.goto("/admin/users");
    await setRole(page, reader.username, role);
    await signIn(them, reader);
    await openReview(them, 4);
    await expect(
      them.getByText("full/private/handbook 04.pdf", { exact: true }),
    ).toBeVisible();
    for (const caption of [
      "Allow",
      "Ban",
      "Reopen",
      "Purge evidence",
      "Flag for review",
      "Select ban file",
      "Import selected bans",
    ]) {
      await expect(
        them.getByRole("button", { name: caption, exact: true }),
      ).toHaveCount(0);
    }
    await expect(
      them.getByRole("link", { name: /^Export reviewed/ }),
    ).toHaveCount(0);
    const refusedExport = await them.request.get("/graphql/moderation/export");
    expect(refusedExport.status()).toBe(403);
    const action = await graphql(
      them,
      `
        mutation ($hash: Hash20!) {
          moderation {
            flag(infoHash: $hash) {
              identifier
            }
          }
        }
      `,
      { hash: hash(4) },
    );
    expect(action.errors[0].extensions.code).toBe("UNAUTHORIZED");
    await setRole(page, reader.username, "user");
    await them.getByRole("button", { name: "Refresh review" }).click();
    await expect(
      them.getByText(
        "Your Identity does not permit private moderation review.",
      ),
    ).toBeVisible();
    await expect(them.getByRole("heading", { name: "Moderation" })).toHaveCount(
      0,
    );
    await expect(
      them.getByText("full/private/handbook 04.pdf", { exact: true }),
    ).toHaveCount(0);
  });
});

test("retained name with absent file metadata is valid", async ({
  page,
  credentials,
}) => {
  await signInAt(page, credentials, "/moderation", "Moderation");
  await openReview(page, 55);
  await expect(page.getByText(name(55), { exact: true })).toBeVisible();
  await expect(page.getByText("File metadata unavailable.")).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Allow", exact: true }),
  ).toBeEnabled();
});

test("history is paged in bounded batches without changing the decision", async ({
  page,
  credentials,
}) => {
  await signInAt(page, credentials, "/moderation", "Moderation");
  await openReview(page, 54);
  const identifier = new URL(page.url()).searchParams.get("record");
  let current = await graphql(
    page,
    `
      query ($id: String!) {
        moderation {
          record(identifier: $id) {
            revision
          }
        }
      }
    `,
    { id: identifier },
  );
  let revision = current.data.moderation.record.revision;
  for (let i = 0; i < 51; i++) {
    const action = i % 2 === 0 ? "allow" : "reopen";
    const selection = action === "allow" ? "record { revision }" : "revision";
    const changed = await graphql(
      page,
      `mutation($input: ModerationDecisionInput!) { moderation { ${action}(input: $input) { ${selection} } } }`,
      { input: { identifier, expectedRevision: revision } },
    );
    expect(changed.errors).toBeUndefined();
    const result = changed.data.moderation[action];
    revision = action === "allow" ? result.record.revision : result.revision;
  }
  await page.getByRole("button", { name: "Refresh review" }).click();
  await expect(
    page.locator(".moderation-page li").filter({ hasText: / · / }),
  ).toHaveCount(50);
  await page.getByRole("button", { name: "More history" }).click();
  await expect(
    page.locator(".moderation-page li").filter({ hasText: / · / }),
  ).toHaveCount(52);
  await expect(page.getByRole("button", { name: "More history" })).toHaveCount(
    0,
  );
  await expect(
    page.locator("dd").filter({ hasText: /^allowed$/ }),
  ).toBeVisible();
});

test("a delayed search answer cannot replace the newer search", async ({
  page,
  credentials,
}) => {
  await signInAt(page, credentials, "/moderation", "Moderation");
  let held;
  const started = new Promise((resolve) => {
    held = resolve;
  });
  let release;
  const gate = new Promise((resolve) => {
    release = resolve;
  });
  await page.route("**/graphql", async (route) => {
    if ((route.request().postData() ?? "").includes("handbook 06.pdf")) {
      const response = await route.fetch();
      held();
      await gate;
      await route.fulfill({ response });
    } else await route.continue();
  });
  await page.getByLabel("Find a moderation record").fill(name(6));
  await page.getByRole("button", { name: "Search decisions" }).click();
  await started;
  await page.getByLabel("Find a moderation record").fill(name(7));
  await page.getByRole("button", { name: "Search decisions" }).click();
  await expect(
    page.getByRole("link", { name: name(7), exact: true }),
  ).toBeVisible();
  const answered = page.waitForResponse((response) =>
    (response.request().postData() ?? "").includes("handbook 06.pdf"),
  );
  release();
  await (await answered).finished();
  await page.evaluate(
    () =>
      new Promise((resolve) =>
        requestAnimationFrame(() => requestAnimationFrame(resolve)),
      ),
  );
  await expect(
    page.getByRole("link", { name: name(6), exact: true }),
  ).toHaveCount(0);
  await expect(
    page.getByRole("link", { name: name(7), exact: true }),
  ).toBeVisible();
});

test("flagging from another record moves the detail route and survives refresh and reload", async ({
  page,
  credentials,
}) => {
  await signInAt(page, credentials, "/moderation", "Moderation");
  await openReview(page, 4);
  const previousIdentifier = new URL(page.url()).searchParams.get("record");
  await page.getByLabel("Torrent infohash").fill(hash(66));
  await page.getByRole("button", { name: "Flag for review" }).click();
  await expect(
    page.getByText("Harmless catalogue handbook 66.pdf", { exact: true }),
  ).toBeVisible();
  await expect(page).not.toHaveURL(new RegExp(`record=${previousIdentifier}`));
  const targetURL = page.url();
  await page.getByRole("button", { name: "Refresh review" }).click();
  await expect(
    page.getByText("Harmless catalogue handbook 66.pdf", { exact: true }),
  ).toBeVisible();
  await expect(page).toHaveURL(targetURL);
  await page.reload();
  await expect(
    page.getByText("Harmless catalogue handbook 66.pdf", { exact: true }),
  ).toBeVisible();
  await expect(page).toHaveURL(targetURL);
});

test("a lost flag answer stays associated with its torrent when another record refreshes", async ({
  page,
  credentials,
}) => {
  await signInAt(page, credentials, "/moderation", "Moderation");
  await openReview(page, 5);
  const previousIdentifier = new URL(page.url()).searchParams.get("record");
  await page.route("**/graphql", async (route) => {
    if ((route.request().postData() ?? "").includes("flag(")) {
      await route.fetch();
      await route.abort("failed");
    } else await route.continue();
  });
  await page.getByLabel("Torrent infohash").fill(hash(67));
  await page.getByRole("button", { name: "Flag for review" }).click();
  await expect(page.getByRole("alert")).toContainText("outcome is unknown.");
  await expect(page.getByText(/Saved:/)).toHaveCount(0);
  await page.unroute("**/graphql");
  await page.getByRole("button", { name: "Refresh review" }).click();
  await expect(page.getByText(name(5), { exact: true })).toBeVisible();
  await expect(page).toHaveURL(new RegExp(`record=${previousIdentifier}`));
  await expect(page.getByRole("alert")).toContainText(
    "The flag outcome is unknown.",
  );
  await expect(page.getByRole("alert")).toContainText(hash(67));
  await expect(
    page.getByRole("button", { name: "Flag for review" }),
  ).toBeDisabled();
  await expect(page.getByText(/Saved:/)).toHaveCount(0);
  await page.reload();
  await expect(page.getByText(name(5), { exact: true })).toBeVisible();
  await expect(page.getByRole("alert")).toContainText(
    "The flag outcome is unknown.",
  );
  await expect(page.getByRole("alert")).toContainText(hash(67));
  await expect(
    page.getByRole("button", { name: "Allow", exact: true }),
  ).toBeDisabled();
  await expect(page.getByText(/Saved:/)).toHaveCount(0);
  await page
    .getByLabel("Find a moderation record")
    .fill("Harmless catalogue handbook 67.pdf");
  await page.getByRole("button", { name: "Search decisions" }).click();
  await expect(page.getByRole("alert")).toContainText(hash(67));
  await page
    .getByRole("link", {
      name: "Harmless catalogue handbook 67.pdf",
      exact: true,
    })
    .click();
  await expect(
    page.getByText("Harmless catalogue handbook 67.pdf", { exact: true }),
  ).toBeVisible();
  await expect(page).not.toHaveURL(new RegExp(`record=${previousIdentifier}`));
  await expect(page.getByRole("status")).toContainText(
    "Review record found for the requested infohash.",
  );
  await expect(page).not.toHaveURL(/uncertainFlag=/);
  await expect(page.getByRole("alert")).toHaveCount(0);
  await expect(page.getByText(/Saved:/)).toHaveCount(0);
  await expect(
    page.getByRole("button", { name: "Allow", exact: true }),
  ).toBeEnabled();
  await page.getByRole("button", { name: "Refresh review" }).click();
  await expect(
    page.getByText("Harmless catalogue handbook 67.pdf", { exact: true }),
  ).toBeVisible();
});

test("dismissing an unknown flag warning acknowledges uncertainty without success", async ({
  page,
  credentials,
}) => {
  await signInAt(page, credentials, "/moderation", "Moderation");
  await openReview(page, 8);
  await page.route("**/graphql", async (route) => {
    if ((route.request().postData() ?? "").includes("flag("))
      await route.abort("failed");
    else await route.continue();
  });
  await page.getByLabel("Torrent infohash").fill(hash(68));
  await page.getByRole("button", { name: "Flag for review" }).click();
  await expect(page.getByRole("alert")).toContainText(
    "The flag outcome is unknown.",
  );
  await expect(page.getByRole("alert")).toContainText(
    "Dismissing this warning does not confirm whether the flag was saved.",
  );
  await expect(
    page.getByRole("button", { name: "Allow", exact: true }),
  ).toBeDisabled();
  await page.unroute("**/graphql");
  await page
    .getByRole("link", { name: "Dismiss unknown flag warning" })
    .click();
  await expect(page).not.toHaveURL(/uncertainFlag=/);
  await expect(page.getByText(name(8), { exact: true })).toBeVisible();
  await expect(page.getByRole("alert")).toHaveCount(0);
  await expect(
    page.getByRole("button", { name: "Allow", exact: true }),
  ).toBeEnabled();
  await expect(page.getByText(/Saved:/)).toHaveCount(0);
});

test("a selected trusted ban file imports additively and rejects a malformed file", async ({
  page,
  credentials,
}) => {
  await signInAt(page, credentials, "/moderation", "Moderation");
  const select = async (file) => {
    const pending = page.waitForEvent("filechooser");
    await page
      .getByRole("button", { name: "Select ban file", exact: true })
      .click();
    await (await pending).setFiles(file);
  };
  const fixture = path.resolve(
    "../internal/moderation/testdata/ban-file-v1.json",
  );
  await select(fixture);
  await expect(
    page.getByText("ban-file-v1.json", { exact: true }),
  ).toBeVisible();
  await page
    .getByRole("button", { name: "Import selected bans", exact: true })
    .click();
  await expect(
    page.getByRole("status").filter({ hasText: /^Ban file accepted:/ }),
  ).toContainText("Ban file accepted: 2 distinct identifiers.");
  await select(fixture);
  await page
    .getByRole("button", { name: "Import selected bans", exact: true })
    .click();
  await expect(
    page.getByRole("status").filter({ hasText: /^Ban file accepted:/ }),
  ).toContainText("Ban file accepted: 2 distinct identifiers.");
  await select({
    name: "truncated.json",
    mimeType: "application/json",
    buffer: Buffer.from('{"version":1,"entries":['),
  });
  await page
    .getByRole("button", { name: "Import selected bans", exact: true })
    .click();
  await expect(page.getByRole("alert")).toContainText(
    "The ban file was rejected. No bans were applied.",
  );
  await expect(page.getByText(/Ban file accepted:/)).toHaveCount(0);
});

test("a lost ban-file answer reports uncertainty and safely retries the same selected file", async ({
  page,
  credentials,
}) => {
  await signInAt(page, credentials, "/moderation", "Moderation");
  const pending = page.waitForEvent("filechooser");
  await page
    .getByRole("button", { name: "Select ban file", exact: true })
    .click();
  await (
    await pending
  ).setFiles(path.resolve("../internal/moderation/testdata/ban-file-v1.json"));
  await page.route("**/graphql/moderation/import", async (route) => {
    await route.fetch();
    await route.abort("failed");
  });
  await page
    .getByRole("button", { name: "Import selected bans", exact: true })
    .click();
  await expect(page.getByRole("alert")).toContainText(
    "The import outcome is unknown.",
  );
  await expect(page.getByText(/Ban file accepted:/)).toHaveCount(0);
  await page.unroute("**/graphql/moderation/import");
  await page
    .getByRole("button", { name: "Import selected bans", exact: true })
    .click();
  await expect(
    page.getByRole("status").filter({ hasText: /^Ban file accepted:/ }),
  ).toContainText("Ban file accepted: 2 distinct identifiers.");
});

test("an old ban-file upload answer cannot repaint after navigation", async ({
  page,
  credentials,
}) => {
  await signInAt(page, credentials, "/moderation", "Moderation");
  const pending = page.waitForEvent("filechooser");
  await page
    .getByRole("button", { name: "Select ban file", exact: true })
    .click();
  await (
    await pending
  ).setFiles(path.resolve("../internal/moderation/testdata/ban-file-v1.json"));
  let release;
  const held = new Promise((resolve) => {
    release = resolve;
  });
  let entered;
  const ready = new Promise((resolve) => {
    entered = resolve;
  });
  await page.route("**/graphql/moderation/import", async (route) => {
    const response = await route.fetch();
    entered();
    await held;
    await route.fulfill({ response });
  });
  await page
    .getByRole("button", { name: "Import selected bans", exact: true })
    .click();
  await ready;
  await page.getByRole("link", { name: "Allowed", exact: true }).click();
  release();
  await expect(
    page.getByRole("status").filter({ hasText: /^Loading moderation/ }),
  ).toHaveCount(0);
  await expect(page.getByText(/Ban file accepted:/)).toHaveCount(0);
  await expect(page.getByText("ban-file-v1.json", { exact: true })).toHaveCount(
    0,
  );
});

test("removing import Permission before upload refreshes Identity without false success", async ({
  page,
  browser,
  request,
  issuer,
  credentials,
}) => {
  const role = uniqueName("e2e-import");
  await signInAt(page, credentials, "/admin/roles", "Create a Role");
  await createRole(page, role, ["moderation::query", "moderation::import"]);
  await inAnotherBrowser(browser, async (them) => {
    const importing = await registerUser(them, request, issuer, "e2e-file");
    await page.goto("/admin/users");
    await setRole(page, importing.username, role);
    await signIn(them, importing);
    await them.goto("/moderation");
    const pending = them.waitForEvent("filechooser");
    await them
      .getByRole("button", { name: "Select ban file", exact: true })
      .click();
    await (
      await pending
    ).setFiles(
      path.resolve("../internal/moderation/testdata/ban-file-v1.json"),
    );
    await setRole(page, importing.username, "user");
    await them
      .getByRole("button", { name: "Import selected bans", exact: true })
      .click();
    await expect(
      them.getByText(
        "Your Identity does not permit private moderation review.",
      ),
    ).toBeVisible();
    await expect(them.getByText(/Ban file accepted:/)).toHaveCount(0);
    await expect(
      them.getByText("ban-file-v1.json", { exact: true }),
    ).toHaveCount(0);
  });
});
