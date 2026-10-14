import { expect, test, type Page } from "@playwright/test";

const PASSWORD = process.env.REMORA_ADMIN_PASSWORD;

test.beforeAll(() => {
  if (!PASSWORD) throw new Error("set REMORA_ADMIN_PASSWORD to the admin password of the remora under test");
});

/** Signs in through the login page and waits for the overview. */
async function signIn(page: Page) {
  await page.goto("/login");
  await page.getByLabel("Password").fill(PASSWORD!);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page).toHaveURL(/\/$/);
  await expect(page.getByRole("heading", { level: 1, name: "Overview" })).toBeVisible();
}

test("redirects to login and rejects a wrong password", async ({ page }) => {
  await page.goto("/keys");
  await expect(page).toHaveURL(/\/login\?redirect=/);
  await page.getByLabel("Password").fill("wrong-password");
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.getByText("Wrong password.")).toBeVisible();
});

test("every page renders after sign-in, deep links survive a reload", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await signIn(page);
  const pages: [string, string][] = [
    ["/providers", "Providers"],
    ["/aliases", "Aliases"],
    ["/keys", "Keys"],
    ["/logs", "Logs"],
    ["/settings", "Settings"],
  ];
  for (const [path, heading] of pages) {
    await page.goto(path);
    await expect(page.getByRole("heading", { level: 1, name: heading })).toBeVisible();
  }
  await page.goto("/logs?page=1&range=1h");
  await page.reload();
  await expect(page.getByRole("heading", { level: 1, name: "Logs" })).toBeVisible();
  expect(errors).toEqual([]);
});

test("a new key's value is shown once", async ({ page }) => {
  await signIn(page);
  await page.goto("/keys");
  await page.getByRole("button", { name: "New key" }).click();
  const form = page.getByRole("dialog", { name: "New key" });
  await form.getByLabel("Name").fill("smoke");
  await form.getByRole("button", { name: "Create key" }).click();
  const secret = page.getByRole("dialog", { name: "Key created" });
  const value = await secret.getByRole("textbox", { name: "Secret" }).inputValue();
  expect(value.length).toBeGreaterThan(20);
  await secret.getByRole("button", { name: "Done" }).click();
  await page.reload();
  await expect(page.getByRole("row", { name: /smoke/ })).toBeVisible();
  await expect(page.getByText(value)).toHaveCount(0);
  expect(await page.content()).not.toContain(value);

  // Clean up through the row action, so the test can run again against the same stack.
  await page.getByRole("button", { name: "Delete smoke" }).click();
  const confirm = page.getByRole("alertdialog", { name: "Delete smoke?" });
  await confirm.getByRole("button", { name: "Delete" }).click();
  await expect(confirm).toBeHidden();
  await expect(page.getByRole("row", { name: /smoke/ })).toHaveCount(0);
});

test("no engine name reaches the UI outside credits", async ({ page }) => {
  await signIn(page);
  for (const path of ["/", "/providers", "/aliases", "/keys", "/logs"]) {
    await page.goto(path);
    await expect(page.getByRole("heading", { level: 1 }).first()).toBeVisible();
    await expect(page.locator("body")).not.toContainText(/bifrost|cliproxy|maxim/i);
  }
});
