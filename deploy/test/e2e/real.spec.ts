// The real path on a real server (contract section 6), until ui-shop's ui/e2e/real.spec.ts is merged: test 1 is
// the first run; test 2 converts a clip and reads its result in the job panel. Both use the real client (?api=real).
// The per-step Job-log assertions belong to ui-shop's spec and wait for the job log data (r1-9c).
import { expect, test } from "@playwright/test";

// The environment, read without Node's type definitions (no @types/node here).
const env = (globalThis as { process?: { env?: Record<string, string | undefined> } }).process?.env ?? {};
const BASE_URL = env.BASE_URL ?? "http://127.0.0.1:8484";
const LIBRARY = env.E2E_LIBRARY;
const PASSWORD = env.E2E_PASSWORD ?? "e2e-password-1";
const CONVERT = env.E2E_CONVERT === "1";
// A red run can be bounded from outside; the default covers a slow real conversion.
const TEST_TIMEOUT = Number(env.E2E_TIMEOUT_MS ?? 11 * 60_000);

test.describe.configure({ mode: "serial" });
test.setTimeout(TEST_TIMEOUT);

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

convert("a clip converts, and the job panel shows its result", async ({ page }) => {
  // Each test gets a fresh browser context, so setup's session cookie is not here: sign in again.
  await page.goto(`${BASE_URL}/signin?api=real`);
  await page.getByLabel("Password", { exact: true }).fill(PASSWORD);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await page.waitForURL((u) => new URL(u).pathname === "/");

  const deadline = Date.now() + 10 * 60_000;
  const row = page.locator("tr.row-link").first();
  while (Date.now() < deadline) {
    await page.goto(`${BASE_URL}/jobs?state=done&api=real`);
    if (await row.count()) break;
    await page.waitForTimeout(15_000);
  }
  await expect(row, "a job reached Done within 10 minutes").toHaveCount(1);
  await row.click();
  await page.waitForURL(/\/jobs\/[^/]+/);
  const id = new URL(page.url()).pathname.split("/").filter(Boolean)[1];

  // The panel is a drawer over the list; it names the file and its result.
  const drawer = page.getByRole("dialog");
  await expect(drawer).toBeVisible();
  await expect(drawer).toContainText("Done");
  await expect(drawer).toContainText("smaller");
  expect(id).toBeTruthy();
});
