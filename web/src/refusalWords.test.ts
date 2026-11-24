// W11 (#703 review): the words of a refused order, for the bar and the status region.
import { test } from "node:test";
import assert from "node:assert/strict";
import { refusalText } from "./refusalWords.ts";

test("a refused order says it can't run yet, then the server's sentence, then the draft note", () => {
  assert.equal(
    refusalText("'Audio' must come after 'Convert video'. It's saved as a draft until it can."),
    "This order can't run yet: 'Audio' must come after 'Convert video'. It's saved as a draft until it can.",
  );
});

test("a one-sentence refusal gets its full stop and the draft note", () => {
  assert.equal(refusalText("'Audio' must come after 'Convert video'"), "This order can't run yet: 'Audio' must come after 'Convert video'. It's saved as a draft until it can.");
});

test("a refusal that already ends in a full stop is not given a second one", () => {
  assert.equal(refusalText("'Audio' must come after 'Convert video'."), "This order can't run yet: 'Audio' must come after 'Convert video'. It's saved as a draft until it can.");
});
