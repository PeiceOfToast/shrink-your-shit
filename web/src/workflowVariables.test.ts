// W4b (#511): the workflow-variables rules the page relies on. Run with `npm test`. The tests run in node, so the
// browser's ?mock=1 is stubbed before the mock is loaded, and the DOM is not needed: the rules are pure functions.
import { beforeAll, describe, expect, it } from "vitest";
import type * as Mock from "./mock";
import type * as Api from "./workflowApi";
import type * as Input from "./VarInput";
import type * as Vars from "./mock/variables";
import type { WorkflowDoc } from "./types/v2";
import REGISTRY from "./mock/registry-a699472.json";

let mock: typeof Mock, api: typeof Api, input: typeof Input, vars: typeof Vars;

beforeAll(async () => {
  // the page reads location and window at import time; a bare node run has neither, so stub just enough
  const location = { search: "?mock=1", pathname: "/", href: "http://localhost/?mock=1" };
  Object.assign(globalThis, { location, window: { addEventListener() {}, removeEventListener() {}, location, history: { pushState() {}, replaceState() {} } } });
  mock = await import("./mock");
  api = await import("./workflowApi");
  input = await import("./VarInput");
  vars = await import("./mock/variables");
});

// w2 is a workflow with a published copy and a clean draft (no own variables, no bad names)
function draft(id: string) {
  const r = mock.getDraft(id)!;
  return { etag: r.etag, layout: r.layout, doc: structuredClone(r.workflow) as WorkflowDoc };
}
const outputNode = (doc: WorkflowDoc) => doc.nodes.find((n) => n.type === "output.commit")!;

describe("secrets", () => {
  it("a secret round-trip keeps no value key: stored, untouched, and typed again", () => {
    const { etag, doc } = draft("w2");
    doc.variables = [{ name: "tmdb_key", type: "secret", value: "abc123" }];
    const first = mock.saveDraft("w2", doc, etag);
    expect(first.refused).toBeUndefined();
    const v1 = first.draft!.workflow.variables[0];
    expect("value" in v1).toBe(false);
    expect(v1.hasValue).toBe(true);

    // untouched: the page saves the row back as it came, with no value key
    const second = mock.saveDraft("w2", structuredClone(first.draft!.workflow), first.draft!.etag);
    const v2 = second.draft!.workflow.variables[0];
    expect("value" in v2).toBe(false);
    expect(v2.hasValue).toBe(true);

    // typed: the new string is sent, and the reply still has no value key
    const typed = structuredClone(second.draft!.workflow);
    typed.variables[0] = { ...typed.variables[0], value: "new-secret" };
    const third = mock.saveDraft("w2", typed, second.draft!.etag);
    const v3 = third.draft!.workflow.variables[0];
    expect("value" in v3).toBe(false);
    expect(v3.hasValue).toBe(true);
  });

  it("a secret without a stored value says so (no hasValue)", () => {
    const { etag, doc } = draft("w2");
    doc.variables = [{ name: "api_token", type: "secret" }];
    const r = mock.saveDraft("w2", doc, etag);
    expect(r.draft!.workflow.variables[0].hasValue).toBe(false);
    expect("value" in r.draft!.workflow.variables[0]).toBe(false);
  });

  it("a secret is never offered in the picker, and a {{name}} to it is refused as a secret", () => {
    const list = input.withOwn(vars.mockVariables().variables, [{ name: "tmdb_key", type: "secret", value: "abc123" }]);
    // listed (so the page can name the reason) but allowed in no field, so the picker never offers it
    for (const f of ["folder", "outputName", "srtName", "trackTitle"]) expect(list.filter((v) => vars.allowedIn(v, f)).some((v) => v.name === "tmdb_key")).toBe(false);
    const defs = [{ type: "output.commit", configSchema: (REGISTRY as Record<string, { configSchema: object }>)["output.commit"].configSchema }];
    const page = input.varProblems([{ id: "o1", type: "output.commit", config: { outputName: "{{tmdb_key}}" } }], defs as never, list);
    expect(page[0]).toMatchObject({ field: "outputName", code: "unknown_variable", name: "tmdb_key" });
    expect(page[0].error).toContain("is a secret");
    const { etag, doc } = draft("w2");
    doc.variables = [{ name: "tmdb_key", type: "secret", value: "abc123" }];
    outputNode(doc).config = { ...outputNode(doc).config, outputName: "{{tmdb_key}}" };
    // a draft save keeps it as a problem (200); publish is the step that refuses it
    const r = mock.saveDraft("w2", doc, etag);
    expect(r.refused).toBeUndefined();
    expect(r.draft!.validation.find((v) => v.name === "tmdb_key")).toMatchObject({ code: "unknown_variable", suggest: null });
    expect(r.draft!.validation.find((v) => v.name === "tmdb_key")!.message).toContain("is a secret");
    const p = mock.publish("w2", r.draft!.etag);
    expect(p.refused?.[0]).toMatchObject({ code: "unknown_variable", name: "tmdb_key" });
  });
});

describe("picker", () => {
  it("sub_lang is offered only in srtName", () => {
    const list = input.withOwn(vars.mockVariables().variables, []);
    const offered = (field: string) => list.filter((v) => vars.allowedIn(v, field)).map((v) => v.name);
    expect(offered("srtName")).toContain("sub_lang");
    expect(offered("outputName")).not.toContain("sub_lang");
    expect(offered("trackTitle")).not.toContain("sub_lang");
    expect(offered("folder")).not.toContain("sub_lang");
    expect(offered("outputName")).toContain("title");
  });

  it("the problem check counts sub_lang as unknown outside srtName", () => {
    const defs = [{ type: "output.commit", configSchema: (REGISTRY as Record<string, { configSchema: object }>)["output.commit"].configSchema }];
    const nodes = [{ id: "o1", type: "output.commit", config: { outputName: "{{sub_lang}}", srtName: "{{sub_lang}}" } }];
    const problems = input.varProblems(nodes, defs as never, input.withOwn(vars.mockVariables().variables, []));
    expect(problems.map((p) => [p.field, p.code])).toEqual([["outputName", "unknown_variable"]]);
    expect(problems[0].error).toBe("{{sub_lang}} isn't a variable for this field.");
  });

  it("Use {{suggest}} replaces every use of the name in the field", () => {
    expect(input.renameIn("{{titel}} – {{ titel }} and {{title}}", "titel", "title")).toBe("{{title}} – {{title}} and {{title}}");
  });
});

describe("refused saves", () => {
  it("a draft save with {{titel}} is saved (200) with the problem on its field", async () => {
    const { etag, layout, doc } = draft("w2");
    const out = outputNode(doc);
    out.config = { ...out.config, outputName: "{{titel}}" };
    const r = mock.saveDraft("w2", doc, etag);
    expect(r.refused).toBeUndefined();
    expect(r.draft!.validation).toContainEqual(expect.objectContaining({ code: "unknown_variable", severity: "error", nodeId: out.id, configPath: "/outputName", name: "titel", suggest: "title" }));
    expect(r.draft!.validation.find((v) => v.name === "titel")!.message).not.toContain("Nothing was changed.");
    // the page's own call: a 200, so no error
    const answer = await api.saveDraft("w2", doc, layout, r.draft!.etag);
    expect("error" in answer).toBe(false);
  });

  it("a publish 400 unknown_variable lands on the field, with its suggestion and 'Nothing was changed.'", async () => {
    const { etag, doc } = draft("w2");
    const out = outputNode(doc);
    out.config = { ...out.config, outputName: "{{titel}}" };
    const saved = mock.saveDraft("w2", doc, etag);
    const r = mock.publish("w2", saved.draft!.etag);
    expect(r.refused?.[0]).toMatchObject({ code: "unknown_variable", nodeId: out.id, configPath: "/outputName", name: "titel", suggest: "title" });
    expect(r.refused?.[0].message).toContain("Nothing was changed.");
    // the page's own call: the error carries the field and the suggestion beside the issues
    const answer = await api.publish("w2", saved.draft!.etag);
    expect(answer).toMatchObject({ error: { code: "unknown_variable", field: "outputName", name: "titel", suggest: "title" } });
    expect((answer as unknown as { error: { issues: unknown[] } }).error.issues).toHaveLength(1);
  });

  it("an unclosed {{ in a field is saved with the problem on that field, and publish refuses it", () => {
    const { etag, doc } = draft("w2");
    const out = outputNode(doc);
    out.config = { ...out.config, outputName: "{{title" };
    const r = mock.saveDraft("w2", doc, etag);
    expect(r.refused).toBeUndefined();
    expect(r.draft!.validation).toContainEqual(expect.objectContaining({ code: "unclosed_variable", nodeId: out.id, configPath: "/outputName" }));
    expect(mock.publish("w2", r.draft!.etag).refused?.[0]).toMatchObject({ code: "unclosed_variable", configPath: "/outputName" });
  });
});
