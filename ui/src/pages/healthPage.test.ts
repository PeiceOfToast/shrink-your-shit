// Settings > Health on a real server: the sample banner shows only while settings is sample data, and the problem dot
// shows whenever settings is real and a check is bad. The node tests can't render the page, so these pin its source.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const page = () => readFileSync(new URL("./settings/SettingsPage.tsx", import.meta.url), "utf8");

test("the Health checks banner is gated on the settings group (real settings: no banner)", () => {
  assert.match(page(), /case "health": return <><SampleBanner group="settings" text=/);
});

test("the problem dot follows the settings group: real settings, and a bad check, shows it", () => {
  assert.match(page(), /const healthBad = !sampleOnly\("settings"\) && \(doctor\.data\?\.checks\.some\(\(c\) => c\.status === "bad"\)/);
});
