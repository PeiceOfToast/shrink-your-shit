// Workflows on the real server's /api/v2 (#109: workflows, drafts, validate, publish, step definitions), or the mock
// module with ?mock=1. Each function answers in the contract's shapes either way.
import { call, failed, MOCK, type Answer } from "./api";
import * as mock from "./mock";
import { STEP_DEFS, stackable, summary, validate as checkDoc } from "./mock/steps";
import type { Layout, PublishPreview, StepDefinition, Template, ValidationIssue, Workflow, WorkflowDoc, WorkflowRevision } from "./types/v2";

const enc = encodeURIComponent, base = (id: string) => "/api/v2/workflows/" + enc(id);
const no = (code: string, message: string) => ({ error: { code, message } });
type Items<T> = { items: T[] };
const items = async <T,>(p: Promise<Answer<Items<T>>>): Promise<Answer<T[]>> => { const r = await p; return failed(r) ? r : r.items; };

export async function listWorkflows(): Promise<Answer<Workflow[]>> {
  return MOCK ? mock.mockState().wf : items(call<Items<Workflow>>("GET", "/api/v2/workflows"));
}
export async function getWorkflow(id: string): Promise<Answer<Workflow>> {
  if (MOCK) return mock.mockState().wf.find((w) => w.id === id) ?? no("not_found", "There is no such workflow.");
  return call("GET", base(id));
}
export async function listTemplates(): Promise<Answer<Template[]>> {
  return MOCK ? mock.TEMPLATES : items(call<Items<Template>>("GET", "/api/v2/templates"));
}
export async function stepDefinitions(): Promise<Answer<StepDefinition[]>> {
  return MOCK ? STEP_DEFS : items(call<Items<StepDefinition>>("GET", "/api/v2/step-definitions"));
}
// POST /workflows {name, fileType, templateId}: the Start Simple preset is the only template; a description goes in a
// PATCH after (POST doesn't take one).
export async function createWorkflow(name: string, description = ""): Promise<Answer<Workflow>> {
  if (MOCK) return mock.createWorkflow(name, description);
  const w = await call<Workflow>("POST", "/api/v2/workflows", { name, fileType: "video", templateId: "shrink_videos" });
  if (failed(w) || !description) return w;
  return call("PATCH", base(w.id), { description });
}
export async function patchWorkflow(id: string, patch: Partial<Pick<Workflow, "name" | "status" | "favourite" | "description" | "tags">>): Promise<Answer<Workflow>> {
  if (MOCK) { mock.setWorkflow(id, patch as Parameters<typeof mock.setWorkflow>[1]); return mock.mockState().wf.find((w) => w.id === id)!; }
  return call("PATCH", base(id), patch);
}
export async function duplicateWorkflow(id: string): Promise<Answer<Workflow>> {
  if (MOCK) { mock.duplicateWorkflow(id); return mock.mockState().wf.at(-1)!; }
  return call("POST", base(id) + "/duplicate");
}
export async function deleteWorkflow(id: string): Promise<Answer<unknown>> {
  if (MOCK) { const e = mock.deleteWorkflow(id); return e ? no("in_use", e) : {}; }
  return call("DELETE", base(id));
}
// The published document (what libraries run): GET /workflows/{id}, then /revisions/{publishedRevisionId}. null: never published.
export async function getPublishedDoc(id: string): Promise<Answer<WorkflowDoc | null>> {
  if (MOCK) return mock.getPublished(id);
  const w = await getWorkflow(id);
  if (failed(w)) return w;
  if (!w.publishedRevisionId) return null;
  const r = await call<WorkflowRevision>("GET", base(id) + "/revisions/" + enc(w.publishedRevisionId));
  return failed(r) ? r : r.workflow;
}
export async function getDraft(id: string): Promise<Answer<WorkflowRevision>> {
  if (MOCK) return mock.getDraft(id) ?? no("not_found", "There is no such workflow.");
  return call("GET", base(id) + "/draft");
}
// PUT /workflows/{id}/draft with If-Match: a draft changed elsewhere answers 409 stale_draft.
export async function saveDraft(id: string, workflow: WorkflowDoc, layout: Layout, etag: string): Promise<Answer<WorkflowRevision>> {
  if (MOCK) { const r = mock.saveDraft(id, workflow, etag); return r.draft ?? no("stale_draft", r.error ?? ""); }
  return call("PUT", base(id) + "/draft", { workflow, layout }, { "If-Match": etag });
}
// POST /workflows/{id}/validate: nothing saved; the server's problems, step words and whether the list can show it.
export interface Checked { issues: ValidationIssue[]; stackable: boolean; stepSummaries: Record<string, string> }
export async function validateDoc(id: string, workflow: WorkflowDoc): Promise<Answer<Checked>> {
  if (MOCK) return { issues: checkDoc(workflow), stackable: stackable(workflow), stepSummaries: Object.fromEntries(workflow.nodes.map((n) => [n.id, summary(n)])) };
  return call("POST", base(id) + "/validate", { workflow });
}
export async function publishPreview(id: string): Promise<Answer<PublishPreview>> {
  if (MOCK) return mock.publishPreview(id) ?? no("not_found", "There is no such workflow.");
  return call("GET", base(id) + "/publish-preview");
}
// POST /workflows/{id}/publish {etag}: the published revision (its number is the new version).
export async function publish(id: string, etag: string): Promise<Answer<{ number: number }>> {
  if (MOCK) { const r = mock.publish(id, etag); return r.version != null ? { number: r.version } : no("stale_draft", r.error ?? ""); }
  const r = await call<WorkflowRevision>("POST", base(id) + "/publish", { etag });
  return failed(r) ? r : { number: r.number ?? 0 };
}
