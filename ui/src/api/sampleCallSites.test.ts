// The sample-data call sites. The node tests can't render the JSX, so each test reads the source and pins the line that
// makes the call site right: the banner is shown when sampleOnly says so, and the placeholders replace the screens.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const src = (rel: string) => readFileSync(new URL(rel, import.meta.url), "utf8");

test("SampleBanner shows only when sampleOnly says so (not inverted)", () => {
  assert.match(src("../ui/SampleBanner.tsx"), /if \(!sampleOnly\(group\)\) return null;/);
});

test("Workers, a worker, and System log are the Not built yet placeholder while sample data is shown", () => {
  const app = src("../app.tsx");
  assert.equal((app.match(/sampleOnly\(\) \? <Placeholder title="Workers"/g) ?? []).length, 2);
  assert.match(app, /sampleOnly\(\) \? <Placeholder title="System log"/);
});

test("Settings > Access says Not built yet while sample data is shown, with no token or password form", () => {
  const page = src("../pages/settings/SettingsPage.tsx");
  assert.match(page, /case "access":\n\s*return sampleOnly\("access"\) \? \(/);
  assert.match(page, /Not built yet\. Changing the password and the access token come in a later batch\./);
});
