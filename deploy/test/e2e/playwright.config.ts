import { defineConfig, devices } from "@playwright/test";

// Chromium only: the e2e proves the server path, not the browsers.
export default defineConfig({
  testDir: ".",
  fullyParallel: false,
  workers: 1,
  reporter: [["list"]],
  use: { trace: "retain-on-failure", ...devices["Desktop Chrome"] },
});
