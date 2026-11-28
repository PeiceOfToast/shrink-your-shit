// The library panel says why the preview could not be worked out, in words, instead of showing nothing. Source-level, since
// the node tests can't render the page.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

test("a failed preview shows a line with the error's message", () => {
  const page = readFileSync(new URL("./LibraryPanel.tsx", import.meta.url), "utf8");
  assert.match(page, /\{prev\.error && <span className="hint">Couldn&apos;t work out the preview: \{prev\.error\}<\/span>\}/);
});
