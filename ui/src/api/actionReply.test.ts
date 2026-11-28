// A Needs attention action's reply: a refusal shows, whether everything or some of it was refused.
import { test } from "node:test";
import assert from "node:assert/strict";
import { actionOutcome } from "./actionReply.ts";
import { ApiError } from "./errors.ts";

test("every item refused: the action is an error with the server's message", () => {
  assert.throws(() => actionOutcome({ done: [], refused: [{ id: "j1", code: "job_busy", message: "Still running." }] }), (e: unknown) =>
    e instanceof ApiError && e.code === "job_busy" && e.message === "Still running.");
});

test("every item refused: more than one says how many more", () => {
  assert.throws(() => actionOutcome({ done: [], refused: [{ id: "j1", code: "job_busy", message: "Still running." }, { id: "j2", code: "job_busy", message: "Still running." }] }), (e: unknown) =>
    e instanceof ApiError && e.message === "Still running. (and 1 more)");
});

test("some items refused: a line says how many went through and what was refused", () => {
  assert.deepEqual(actionOutcome({ done: ["j3"], refused: [{ id: "j1", code: "job_busy", message: "Still running." }] }), {
    line: "1 retrying. 1 couldn't be: Still running.",
  });
});

test("everything done: nothing to show", () => {
  assert.deepEqual(actionOutcome({ done: ["j1", "j2"], refused: [] }), {});
});

test("a reply with only a line shows that line", () => {
  assert.deepEqual(actionOutcome({ line: "Test finished." }), { line: "Test finished." });
});
