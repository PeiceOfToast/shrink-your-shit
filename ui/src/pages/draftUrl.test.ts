// After a refused publish of a draft made on the page, the address bar moves to that draft (silently), so a reload opens
// it. The editor and Guided each make that move; the node tests can't render their JSX, so the lines are pinned in source.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { replaceUrl } from "../urlBar.ts";

test("replaceUrl changes the address bar with replaceState, not pushState, and only when the URL differs", () => {
  const g = globalThis as unknown as Record<string, unknown>;
  const calls: string[] = [];
  g.location = { pathname: "/workflows/new/editor", search: "" };
  g.history = {
    replaceState: (_s: unknown, _t: string, url: string) => calls.push(`replace ${url}`),
    pushState: (_s: unknown, _t: string, url: string) => calls.push(`push ${url}`),
  };
  try {
    replaceUrl("/workflows/7");
    assert.deepEqual(calls, ["replace /workflows/7"]);
    replaceUrl("/workflows/new/editor");
    assert.deepEqual(calls, ["replace /workflows/7"]);
  } finally {
    delete g.location;
    delete g.history;
  }
});

test("the editor writes the draft's URL after a refused publish of a draft it made", () => {
  const page = readFileSync(new URL("./FlowEditor.tsx", import.meta.url), "utf8");
  assert.match(page, /setErr\(toErr\(e\)\);\n\s*\/\/ A refused publish of a draft made here[^\n]*\n\s*if \(madeRef\.current\) replaceUrl\(`\/workflows\/\$\{madeRef\.current\}`\);/);
});

test("Guided writes the draft's URL after a refused Save and use of a draft it made", () => {
  const page = readFileSync(new URL("./Guided.tsx", import.meta.url), "utf8");
  assert.match(page, /if \(madeRef\.current\) replaceUrl\(`\/workflows\/\$\{madeRef\.current\}\/guided`\);/);
});
