import { expect, test } from "@playwright/test";

test("the release binary serves a working UI and its same-origin API", async ({
  page,
}) => {
  const errors = [];
  page.on("pageerror", (error) => errors.push(error.message));
  const identity = page.waitForResponse(
    (response) =>
      response.url().endsWith("/graphql") &&
      response.request().method() === "POST",
  );

  await page.goto("/");
  await expect(page).toHaveURL(/\/ui\/$/);
  expect((await identity).status()).toBe(200);
  expect(await page.locator("base").getAttribute("href")).toBe("/ui/");

  await page.getByRole("link", { name: "Sign in" }).click();
  await expect(page).toHaveURL(/\/ui\/login\?/);
  await expect(page.getByRole("textbox", { name: "Username" })).toBeVisible();

  await page.reload();
  await expect(page.getByRole("textbox", { name: "Username" })).toBeVisible();
  expect(errors).toEqual([]);
});
