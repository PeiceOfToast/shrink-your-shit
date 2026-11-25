import { test } from "node:test";
import assert from "node:assert/strict";
import { MIN_PASSWORD, TOO_MANY, WAIT_A_MINUTE, authWord, canSave, passwordProblem, tryAgainIn } from "./signinRules.ts";

test("too many tries: the count reads in seconds, singular for one", () => {
  assert.equal(tryAgainIn(1), "Try again in 1 second.");
  assert.equal(tryAgainIn(2), "Try again in 2 seconds.");
  assert.equal(tryAgainIn(60), "Try again in 60 seconds.");
  assert.equal(TOO_MANY + " " + WAIT_A_MINUTE, "Too many tries. Wait a minute, then try again.");
});

test("a password must be at least the minimum and typed the same twice", () => {
  assert.equal(MIN_PASSWORD, 8);
  assert.equal(passwordProblem("", ""), "Too short: use at least 8 characters.");
  assert.equal(passwordProblem("1234567", "1234567"), "Too short: use at least 8 characters.");
  assert.equal(passwordProblem("12345678", "12345679"), "The two passwords don't match.");
  assert.equal(passwordProblem("12345678", "12345678"), null);
});

test("switching to home needs no password, whatever the fields say", () => {
  assert.equal(canSave({ mode: "password", passwordSet: true }, { mode: "home", pw: "", again: "" }), true);
  assert.equal(canSave({ mode: "home", passwordSet: false }, { mode: "home", pw: "x", again: "" }), true);
});

test("switching to password from home needs a valid password when none is set", () => {
  assert.equal(canSave({ mode: "home", passwordSet: false }, { mode: "password", pw: "", again: "" }), false);
  assert.equal(canSave({ mode: "home", passwordSet: false }, { mode: "password", pw: "short", again: "short" }), false);
  assert.equal(canSave({ mode: "home", passwordSet: false }, { mode: "password", pw: "longenough", again: "longenugh" }), false);
  assert.equal(canSave({ mode: "home", passwordSet: false }, { mode: "password", pw: "longenough", again: "longenough" }), true);
});

test("password mode with a password already set, or an existing token-only install, can save with the fields empty", () => {
  assert.equal(canSave({ mode: "home", passwordSet: true }, { mode: "password", pw: "", again: "" }), true);
  assert.equal(canSave({ mode: "password", passwordSet: false }, { mode: "password", pw: "", again: "" }), true);
});

test("server codes get design's sentence; other codes keep the server's text", () => {
  assert.equal(authWord("bad_password", "x"), "That password isn't right.");
  assert.equal(authWord("too_many_tries", "x"), "Too many tries. Wait a minute, then try again.");
  assert.equal(authWord("something_new", "Server text."), "Server text.");
});
