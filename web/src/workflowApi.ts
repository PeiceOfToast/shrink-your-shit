// Workflows on the real server's /api/v2 (#109: workflows, drafts, validate, publish, step definitions), or the mock
// module with ?mock=1. Each function answers in the contract's shapes either way.
import { call, failed, MOCK, type Answer } from "./api";
import * as mock from "./mock";
import { STEP_DEFS, addStepsOf, mockLadder, stackable, summary, validate as checkDoc } from "./mock/steps";
import { mockVariables, type VarList } from "./mock/variables";
import { GUIDE_FULL, GUIDE_INFO, GUIDE_SUMMARY } from "./mock/guide";
import type { Answers, GuideInfo } from "./guide";
import type { AddStep, Layout, PublishPreview, StepDefinition, Template, ValidationIssue, Workflow, WorkflowDoc, WorkflowRevision } from "./types/v2";

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
// With answers (the guided builder), the server builds the steps from them; without, it is the Start Simple preset.
export async function createWorkflow(name: string, description = "", answers?: Answers): Promise<Answer<Workflow>> {
  if (MOCK) return mock.createWorkflow(name, description);
  const body: Record<string, unknown> = { name, fileType: "video", templateId: "shrink_videos" };
  if (answers) body.answers = answers;
  const w = await call<Workflow>("POST", "/api/v2/workflows", body);
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
  if (MOCK) { const e = mock.deleteWorkflow(id); return e ? no("in_use", e) : null; } // like the real server: 204, no body
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
  if (MOCK) { const d = mock.getDraft(id); return d ? { ...d, guide: d.guide ?? GUIDE_FULL } : no("not_found", "There is no such workflow."); }
  return call("GET", base(id) + "/draft");
}
// The guided builder (slice 12): the defaults per goal, what this computer can make, and the person's languages.
export async function getGuideInfo(): Promise<Answer<GuideInfo>> {
  if (MOCK) return GUIDE_INFO;
  return call("GET", "/api/v2/workflows/guide");
}
// The answers as the sentences the summary shows, and what validation says. Nothing is saved.
export async function previewGuide(answers: Answers): Promise<Answer<{ summary: string[]; validation: ValidationIssue[] }>> {
  if (MOCK) return { summary: GUIDE_SUMMARY, validation: [] };
  return call("POST", "/api/v2/workflows/guide/preview", { answers });
}
// Save answers into a draft (If-Match: its etag). The answer is the draft's revision, with its new etag.
export async function saveAnswers(id: string, answers: Answers, etag: string): Promise<Answer<WorkflowRevision>> {
  if (MOCK) return getDraft(id); // the mock keeps no answers: it returns the draft as it is
  return call("PUT", base(id) + "/answers", { answers }, { "If-Match": etag });
}
// Export the draft as a file (the server's GET /api/flows/{id}/export; the file's JSON, saved by the page).
export async function exportWorkflow(id: string): Promise<Answer<object>> {
  if (MOCK) return no("mock", "Export and import need the real server.");
  return call("GET", "/api/flows/" + enc(id) + "/export");
}
// Import a file as a new draft (POST /api/flows/import). A refused file saves nothing: 400 invalid, 413 too_large.
export type ImportReport = { missingGlobals: string[]; unknownSteps: string[]; issues: ValidationIssue[] };
export async function importWorkflow(file: unknown): Promise<Answer<Workflow & { import: ImportReport }>> {
  if (MOCK) return no("mock", "Export and import need the real server.");
  return call("POST", "/api/flows/import", file);
}
// PUT /workflows/{id}/draft with If-Match: a draft changed elsewhere answers 409 stale_draft.
export async function saveDraft(id: string, workflow: WorkflowDoc, layout: Layout, etag: string): Promise<Answer<WorkflowRevision>> {
  if (MOCK) {
    const r = mock.saveDraft(id, workflow, etag);
    if (r.refused) { const e = { error: { code: r.refused[0].code, message: r.refused[0].message, issues: r.refused } }; return e; } // the server's refusal: its issues ride along
    return r.draft ?? no("stale_draft", r.error ?? "");
  }
  return call("PUT", base(id) + "/draft", { workflow, layout }, { "If-Match": etag });
}
// POST /workflows/{id}/validate: nothing saved; the server's problems, step words and whether the list can show it.
export interface Checked { issues: ValidationIssue[]; stackable: boolean; stepSummaries: Record<string, string>; fileOn?: Record<string, Record<string, string>>; addSteps?: Record<string, AddStep> }
export async function validateDoc(id: string, workflow: WorkflowDoc): Promise<Answer<Checked>> {
  if (MOCK) return { issues: checkDoc(workflow), stackable: stackable(workflow), stepSummaries: Object.fromEntries(workflow.nodes.map((n) => [n.id, summary(n)])), addSteps: addStepsOf(workflow) };
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

// User review 11-12 item 1 (#176): the engine's own bitrate ladder, so the page shows real Mbps and never hard-codes them.
// rows: 2160 ... sd; formats[format][row] = {low, medium, high, rough}; resolutionRow maps the Standard choice (480 -> sd);
// vbrFormats: the formats VBR with a peak limit is offered for. The sample install has none (no numbers are made up).
export interface Ladder {
  unit: string; rows: { row: string; label: string }[];
  formats: Record<string, Record<string, { low: number; medium: number; high: number; rough: number }>>;
  resolutionRow: Record<string, string>; vbrFormats: string[];
  // the slider's five positions per format (its settings line), and the factors for the page's note (A-API-SHAPE)
  slider: { position: number; name: string; format: string; crf: number; keepsResolution: boolean; capKbps: number | null; saveLowPct: number; saveHighPct: number }[];
  factors: { fps60: number; hdr: number };
}
let ladder: Promise<Answer<Ladder>> | null = null; // one fetch per page load: the ladder is fixed for a server's life
// GET /api/v2/workflow/variables?workflow=<id>: the names the {{name}} fields take, with the sample file's values (the server
// builds the list; the page checks names itself)
export async function getVariables(workflowId: string): Promise<Answer<VarList>> {
  return MOCK ? mockVariables(mock.getDraft(workflowId)?.workflow.variables ?? []) : call("GET", "/api/v2/workflow/variables?workflow=" + enc(workflowId));
}
export function getLadder(): Promise<Answer<Ladder>> {
  if (MOCK) return Promise.resolve({ ...mockLadder() }); // the sample's ladder, shaped as the engine's
  return (ladder ??= call<Ladder>("GET", "/api/v2/bitrate-ladder").then((r) => (failed(r) && (ladder = null), r)));
}
