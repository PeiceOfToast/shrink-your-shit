// Drift test: the editor's table (flowFields.ts) against the step definitions golden. Per step: the same fields, the
// same choices (values and labels, in order), and the same defaults. Every difference is listed in one failure.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { DRAWN, greyedChoices, offeredChoices } from "./flowFields.ts";
import { STEP_DEFINITIONS } from "../api/fixtures/stepDefinitions.ts";

// The golden is the server's GET /api/v2/step-definitions (internal/api/testdata/contract, from #834).
const GOLDEN = new URL("../../../internal/api/testdata/contract/step-definitions.json", import.meta.url);
interface Prop {
  default?: unknown;
  "x-ui": { choices?: { value: string | number | null; label: string }[] };
}
interface Def {
  type: keyof typeof DRAWN;
  form: { schema: { properties: Record<string, Prop> } };
}
const defs = (JSON.parse(readFileSync(GOLDEN, "utf8")) as { body: Def[] }).body;

for (const def of defs) {
  test(`${def.type} matches the step definitions golden`, () => {
    const drawn = DRAWN[def.type];
    const props = def.form.schema.properties;
    const problems: string[] = [];
    if (!drawn) problems.push("no editor table for this step");
    else {
      const have = [...drawn.fields].sort();
      const want = Object.keys(props).sort();
      const missing = want.filter((k) => !have.includes(k));
      const extra = have.filter((k) => !want.includes(k));
      if (missing.length) problems.push(`fields missing from the editor: ${missing.join(", ")}`);
      if (extra.length) problems.push(`fields the golden does not have: ${extra.join(", ")}`);
      for (const key of Object.keys(drawn.choices)) if (!(key in props)) problems.push(`${key}: choices for a field the golden does not have`);
      for (const [key, prop] of Object.entries(props)) {
        const wantChoices = prop["x-ui"].choices?.map((c) => ({ value: c.value, label: c.label })) ?? null;
        const haveChoices = drawn.choices[key] ?? null;
        if (JSON.stringify(haveChoices) !== JSON.stringify(wantChoices)) {
          problems.push(`${key}: choices ${JSON.stringify(haveChoices)} vs golden ${JSON.stringify(wantChoices)}`);
        }
        if (!(key in drawn.defaults)) problems.push(`${key}: no default in the editor table`);
        else if (JSON.stringify(drawn.defaults[key] ?? null) !== JSON.stringify(prop.default ?? null)) {
          problems.push(`${key}: default ${JSON.stringify(drawn.defaults[key] ?? null)} vs golden ${JSON.stringify(prop.default ?? null)}`);
        }
      }
    }
    assert.deepEqual(problems, [], `${def.type}: ${problems.length} difference(s)`);
  });
}

// The mock serves a copy of the golden; the copy must be the golden, so the editor is checked against what the mock serves.
test("the mock serves a copy of the golden", () => {
  assert.deepEqual(STEP_DEFINITIONS, defs);
});

// A choice the definitions grey out, by key, with the server's reason.
test("greyedChoices reads available: false and its reason", () => {
  const def = {
    form: {
      schema: {
        properties: {
          rateControl: {
            "x-ui": {
              choices: [
                { value: null, label: "Quality", available: true },
                { value: "vbr", label: "Variable bitrate", available: false, why: "Not with AV1" },
              ],
            },
          },
        },
      },
    },
  };
  assert.deepEqual(greyedChoices(def, "rateControl"), { vbr: "Not with AV1" });
  assert.deepEqual(greyedChoices(def, "missing"), {});
});

// A stored value no choice matches is shown as one more option, not swapped for the first choice.
test("offeredChoices keeps a stored value no choice matches", () => {
  const hdr = DRAWN["video.convert"].choices.hdr;
  const gone = offeredChoices(hdr, "sdr");
  assert.equal(gone.stale, "sdr");
  assert.equal(gone.options.length, hdr.length + 1);
  assert.equal(gone.options[gone.options.length - 1].label, "sdr (no longer offered)");
  assert.equal(offeredChoices(hdr, "keep").stale, null);
  assert.equal(offeredChoices(hdr, "keep").options, hdr);
  // A number choice matches its string key, and an unset stored value that no choice holds is shown as "none".
  assert.equal(offeredChoices(DRAWN["video.convert"].choices.bitDepth, 8).stale, null);
  assert.equal(offeredChoices(DRAWN["video.convert"].choices.bitDepth, 12).stale, "12");
  assert.equal(offeredChoices(hdr, null).options[hdr.length].label, "none (no longer offered)");
});
