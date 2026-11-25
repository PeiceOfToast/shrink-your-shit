import { test } from "node:test";
import assert from "node:assert/strict";
import { pathOf, settingsTarget } from "./settingsRoutes.ts";

test("every old Settings address goes to the tab or page that holds it now", () => {
  const table: [string, string][] = [
    ["/settings", "/settings/general"],
    ["/settings/general", "/settings/general"],
    ["/settings/appearance", "/settings/general#appearance"],
    ["/settings/processing", "/settings/processing"],
    ["/settings/safety", "/settings/processing#safety"],
    ["/settings/connections", "/settings/connections"],
    ["/settings/integrations", "/settings/connections"],
    ["/settings/notifications", "/settings/connections#notifications"],
    ["/settings/system", "/settings/system"],
    ["/settings/health", "/settings/system#health"],
    ["/settings/backup", "/settings/system#backup"],
    ["/settings/about", "/settings/system#about"],
    ["/settings/shares", "/libraries/shares"],
    ["/settings/variables", "/workflows?tab=variables"],
  ];
  for (const [from, to] of table) assert.equal(settingsTarget(from), to, from);
});

test("a path that is not an old Settings address has no target", () => {
  assert.equal(settingsTarget("/settings/nope"), null);
  assert.equal(settingsTarget("/settings/general/extra"), null);
  assert.equal(settingsTarget("/libraries"), null);
});

test("the path part of a target drops the query and hash", () => {
  assert.equal(pathOf("/workflows?tab=variables"), "/workflows");
  assert.equal(pathOf("/settings/system#health"), "/settings/system");
  assert.equal(pathOf("/libraries/shares"), "/libraries/shares");
});
