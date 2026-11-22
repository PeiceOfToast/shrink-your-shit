// Non-OK answers (dev: `npm test`, Node 22.6+ with --experimental-strip-types). call() in api.ts sends only non-OK
// answers here; the 200 path is not touched, and api.ts itself can't be imported under node test.
import { test } from "node:test";
import assert from "node:assert/strict";
import { httpError } from "./httpError.ts";

test("an error object with a message is used as it is", () => {
  const issue = { code: "invalid_setting", message: "Look for new files every 15 seconds." };
  assert.deepEqual(httpError(400, { error: issue }), issue);
});

test("a bare error string becomes an Issue with the status as its code", () => {
  assert.deepEqual(httpError(500, { error: "blocked" }), { code: "http_500", message: "blocked" });
});

test("anything else (no JSON, HTML, other shapes) gets the status fallback", () => {
  const fallback = { code: "http_502", message: "The server answered 502." };
  for (const body of [null, undefined, "<html>Bad gateway</html>", 42, { error: "" }, { error: "   " }, { error: 5 }, { error: {} }, {}, []])
    assert.deepEqual(httpError(502, body), fallback, JSON.stringify(body));
});
