// The real path on a real server (contract section 6). Test 1 is the first run: a password, a starter, and a folder
// (E2E_LIBRARY) or a skip. Test 2 converts a clip and checks its Job log; it needs E2E_LIBRARY and E2E_CONVERT=1, so it
// waits until the jobs routes are live (workshop R1-9). Each test runs on the real client (?api=real).
import { expect, test } from "@playwright/test";

// The environment, read without Node's type definitions (the ui tsconfig has none).
const env = (globalThis as { process?: { env?: Record<string, string | undefined> } }).process?.env ?? {};
const BASE_URL = env.BASE_URL ?? "http://127.0.0.1:8484";
const LIBRARY = env.E2E_LIBRARY;
const PASSWORD = env.E2E_PASSWORD ?? "e2e-password-1";
const CONVERT = env.E2E_CONVERT === "1";

test.describe.configure({ mode: "serial" });
test.setTimeout(11 * 60_000);

test("first run: a password, a starter, then the Dashboard", async ({ page }) => {
  await page.goto(`${BASE_URL}/start?api=real`);
  await page.locator("section[aria-labelledby='st-account']").getByLabel("Password", { exact: true }).fill(PASSWORD);
  await page.locator("button.start-starter").first().click();
  if (LIBRARY) {
    await page.locator("section[aria-labelledby='st-lib']").getByLabel("Folder", { exact: true }).fill(LIBRARY);
    await page.getByRole("button", { name: "Finish", exact: true }).click();
  } else {
    await page.getByRole("button", { name: "Skip the library for now", exact: true }).click();
  }
  await page.waitForURL((u) => new URL(u).pathname === "/");
});

const convert = CONVERT && LIBRARY ? test : test.skip;

convert("a clip converts, and its Job log shows each step", async ({ page }) => {
  // Each Playwright test gets a fresh browser context, so this test signs in itself (test 1's session is not kept).
  await page.goto(`${BASE_URL}/signin?api=real`);
  await page.getByLabel("Password", { exact: true }).fill(PASSWORD);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await page.waitForURL((u) => new URL(u).pathname !== "/signin");
  // Poll the Done jobs (up to 10 minutes): the rows are tr.row-link, opened by a click.
  const deadline = Date.now() + 10 * 60_000;
  let row = page.locator("tr.row-link").first();
  while (Date.now() < deadline) {
    await page.goto(`${BASE_URL}/jobs?state=done&api=real`);
    // Wait up to 15 s for the list to render: count() reads at once, before the page has loaded, so it never breaks.
    if (await row.waitFor({ state: "visible", timeout: 15_000 }).then(() => true, () => false)) break;
  }
  await expect(row, "a job reached Done within 10 minutes").toHaveCount(1);
  await row.click();
  await page.waitForURL(/\/jobs\/[^/]+/);
  const id = new URL(page.url()).pathname.split("/").filter(Boolean)[1];

  await page.goto(`${BASE_URL}/jobs/${id}/log?api=real`);
  await expect(page.getByText("Not started yet")).toHaveCount(0);
  const steps = page.getByRole("list", { name: "Steps" }).locator("li button");
  await expect(steps.first()).toBeVisible();
  expect(await steps.count()).toBeGreaterThan(0);
  for (const b of await steps.all()) await expect(b).toBeEnabled();
  await expect(page.locator("div.ln").first()).toBeVisible();
});
