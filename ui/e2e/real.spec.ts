// Stop 1 end to end on a real server (contract section 6): first run, a folder of clips, one clip converts, and its
// Job log shows each step. Needs a running syp (BASE_URL), a folder the server can see (E2E_LIBRARY) and the password
// the run sets (E2E_PASSWORD). Without E2E_LIBRARY the test is skipped, so the default run stays green.
import { expect, test } from "@playwright/test";

// The environment, read without Node's type definitions (the ui tsconfig has none).
const env = (globalThis as { process?: { env?: Record<string, string | undefined> } }).process?.env ?? {};
const BASE_URL = env.BASE_URL ?? "http://127.0.0.1:8484";
const LIBRARY = env.E2E_LIBRARY;
const PASSWORD = env.E2E_PASSWORD ?? "e2e-password-1";

test.describe.configure({ mode: "serial" });
test.setTimeout(11 * 60_000);

// Without a library the test is declared skipped, so no browser is launched for it.
const realTest = LIBRARY ? test : test.skip;

realTest("first run, then a clip converts and its Job log shows each step", async ({ page }) => {

  // 1. First run: a password and a folder on this server (the real client: ?api=real).
  await page.goto(`${BASE_URL}/start?api=real`);
  await page.getByLabel("Password", { exact: true }).fill(PASSWORD);
  await page.getByLabel("Folder", { exact: true }).fill(LIBRARY!);
  await page.getByRole("button", { name: "Finish", exact: true }).click();
  await page.waitForURL((u) => new URL(u).pathname === "/");

  // 2. The clips convert. Poll the job list until one job is Done (up to 10 minutes).
  const deadline = Date.now() + 10 * 60_000;
  let jobHref: string | null = null;
  while (Date.now() < deadline && !jobHref) {
    await page.goto(`${BASE_URL}/jobs?api=real`);
    const done = page.getByRole("link").filter({ hasText: /Done/ }).first();
    if (await done.count()) jobHref = await done.getAttribute("href");
    if (!jobHref) await page.waitForTimeout(15_000);
  }
  expect(jobHref, "a job reached Done within 10 minutes").not.toBeNull();

  // 3. Its Job log shows the run and each step.
  await page.goto(`${BASE_URL}${jobHref!.replace(/\/?$/, "")}/log?api=real`);
  for (const kind of ["run_start", "run_end"]) {
    await expect(page.getByText(kind, { exact: false }).first()).toBeVisible();
  }
  await expect(page.getByText("step_end", { exact: false })).toHaveCount(3);
});
