// The fit rule's table (dev-review #596): run with `npm test` (Node 22.6+: `node --test --experimental-strip-types`).
// Each row is a case the server decides the same way at save; a mismatch here is a mismatch with the server.
import { test } from "node:test";
import assert from "node:assert/strict";
import { refProblem, refFits, kindOf } from "./refFit.ts";

const integer = { type: "integer", minimum: 1, maximum: 5 };
const number = { type: "number" };
const text = { type: "string" };
const yesno = { type: "boolean" };
const choiceNums = { enum: [0, 720, 1080, 2160] };
const choiceText = { enum: ["keep", "1080", "720", "480"] };
const list = { type: "array", items: { type: "string" } };
const untyped = {};

test("kindOf maps the doc's words", () => {
  assert.equal(kindOf("number"), "number");
  assert.equal(kindOf("bool"), "boolean");
  assert.equal(kindOf("boolean"), "boolean");
  assert.equal(kindOf("string"), "string");
  assert.equal(kindOf("path"), "string");
  assert.equal(kindOf("enum"), "string");
  assert.equal(kindOf("secret"), "");
  assert.equal(kindOf(undefined), "");
});

test("ui-shop's three cases: text that reads as a number, a fraction on an integer, no value yet", () => {
  assert.equal(refProblem(integer, { type: "string", value: "4" }), "");
  assert.equal(refProblem(choiceNums, { type: "string", value: "1080" }), "");
  assert.equal(refProblem(integer, { type: "number", value: "4.5" }), "isn't a whole number");
  assert.equal(refProblem(number, { type: "number", value: "4.5" }), "");
  assert.equal(refProblem(text, { type: "string", value: null }), "has no value yet");
  assert.equal(refProblem(integer, { type: "number", value: null }), "has no value yet");
});

test("the file's own values are judged on kind alone, as the server does at save (ui-shop A)", () => {
  assert.equal(refProblem(integer, { type: "number", value: null, file: true }), "");
  assert.equal(refProblem(integer, { type: "number", value: "4215883110", file: true }), "");
  assert.equal(refProblem(text, { type: "string", value: null, file: true }), "");
  assert.equal(refProblem(integer, { type: "string", value: null, file: true }), "text won't fit here"); // {{file.name}} on Quality
  assert.equal(refProblem(integer, { type: "string", value: "4", file: true }), "text won't fit here");
  assert.equal(refProblem(integer, { type: "boolean", value: null, file: true }), "yes or no won't fit here");
  assert.equal(refProblem(choiceText, { type: "number", value: null, file: true }), "a number won't fit here");
  assert.equal(refProblem(choiceNums, { type: "number", value: null, file: true }), "");
  assert.equal(refProblem(yesno, { type: "boolean", value: null, file: true }), "");
});

test("kinds that never fit", () => {
  assert.equal(refProblem(integer, { type: "secret", value: null }), "a secret can't set this");
  assert.equal(refProblem(number, { type: "string", value: "abc" }), "text won't fit here");
  assert.equal(refProblem(text, { type: "number", value: "3" }), "a number won't fit here");
  assert.equal(refProblem(yesno, { type: "string", value: "yes" }), "text won't fit here");
  assert.equal(refProblem(number, { type: "bool", value: "true" }), "yes or no won't fit here");
  assert.equal(refProblem(yesno, { type: "boolean", value: "true" }), "");
});

test("a choice setting takes one of its choices, in the choice's own kind (ui-shop B, C)", () => {
  assert.equal(refProblem(choiceText, { type: "string", value: "720" }), "");
  assert.equal(refProblem(choiceText, { type: "string", value: "mine" }), "isn't one of the choices");
  assert.equal(refProblem(choiceNums, { type: "number", value: "4" }), "isn't one of the choices");
  assert.equal(refProblem(choiceNums, { type: "number", value: "1080" }), "");
  assert.equal(refProblem(choiceText, { type: "number", value: "720" }), "a number won't fit here"); // B: sameValue(720, "720") is false
  const choiceBool = { enum: [true, false] };
  assert.equal(refProblem(choiceBool, { type: "string", value: "true" }), "text won't fit here"); // C: sameValue("true", true) is false
  assert.equal(refProblem(choiceBool, { type: "bool", value: "true" }), "");
});

test("dev-review's case: a list or group setting never takes a variable; an untyped one takes anything but a secret", () => {
  assert.equal(refProblem(list, { type: "string", value: "h264" }), "a list setting can't take a variable");
  assert.equal(refProblem(list, { type: "string", value: null, file: true }), "a list setting can't take a variable");
  assert.equal(refFits(untyped, { type: "string", value: "x" }), true);
  assert.equal(refFits(untyped, { type: "secret", value: null }), false);
});
