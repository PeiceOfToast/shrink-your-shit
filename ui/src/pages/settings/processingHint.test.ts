// The Files at once hint: the default for new workers, and a link to the Workers page. The node tests can't render JSX, so
// this reads the source and pins the sentence and the link.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

test("Files at once says it is the default for new workers and links to the Workers page", () => {
  const src = readFileSync(new URL("./Processing.tsx", import.meta.url), "utf8");
  assert.match(src, /Files at once for new workers\. Each worker's own setting is on the <Link to="\/workers">Workers page<\/Link>\./);
  assert.doesNotMatch(src, /How many files convert at the same time/);
});
