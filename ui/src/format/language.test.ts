import { test } from "node:test";
import assert from "node:assert/strict";
import { languageName, normalizeLanguage, sameLanguage } from "./language.ts";
import { LANGUAGES } from "./languages.ts";

test("two-letter and alternate three-letter codes map to the bibliographic code", () => {
  assert.equal(normalizeLanguage("en"), "eng");
  assert.equal(normalizeLanguage("de"), "ger");
  assert.equal(normalizeLanguage("deu"), "ger");
  assert.equal(normalizeLanguage("fra"), "fre");
  assert.equal(normalizeLanguage("fre"), "fre");
  assert.equal(normalizeLanguage("zho"), "chi");
  assert.equal(normalizeLanguage(" JPN "), "jpn");
});

test("names are shown for every synonym, and unknown codes fall back to what was typed", () => {
  assert.equal(languageName("eng"), "English");
  assert.equal(languageName("en"), "English");
  assert.equal(languageName("deu"), "German");
  assert.equal(languageName("zho"), "Chinese");
  assert.equal(languageName("xyz"), "xyz");
});

test("same language across synonyms", () => {
  assert.equal(sameLanguage("de", "ger"), true);
  assert.equal(sameLanguage("deu", "ger"), true);
  assert.equal(sameLanguage("en", "ger"), false);
});

test("every code the synonyms map to is in the offered list", () => {
  const codes = new Set(LANGUAGES.map((l) => l.code));
  for (const two of ["en", "de", "deu", "fr", "fra", "es", "it", "pt", "nl", "nld", "sv", "no", "nb", "da", "fi", "is", "isl", "pl", "cs", "ces", "sk", "slk", "hu", "ro", "ron", "bg", "ja", "zh", "zho"]) {
    assert.ok(codes.has(normalizeLanguage(two)), `${two} maps to ${normalizeLanguage(two)}, which is not offered`);
  }
});
