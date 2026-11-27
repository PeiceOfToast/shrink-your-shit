// Workflow list calls for the mock (page 04 "Data and API"). Rows are an in-memory copy, so a session behaves like the app.
import type { WorkflowRow } from "./types.ts";
import { ApiError } from "./errors.ts";
import { getForcedState, respond } from "./mockState.ts";
import { WORKFLOW_ROWS } from "./fixtures/workflowRows.ts";
import { currentLibraries } from "./mockLibraries.ts";
import { assertThreeSteps, createDraft, getFlow } from "./mockFlow.ts";
import type { Flow } from "./flowTypes.ts";

// The export file: the workflow's own flow, in the format the workshop's API uses. appVersion is the mock's app version (the one its backups report).
const FORMAT = "syp-workflow";
const FORMAT_VERSION = 1;
const APP_VERSION = "0.9.4";
let rows: Omit<WorkflowRow, "usedBy">[] = WORKFLOW_ROWS.map((r) => ({ ...r }));
let seq = 0;

// Used by the picker's Start and by the flow pages' saves: a new draft row, and the state a publish or save sets.
export function createWorkflowRow(input: { name: string; description: string; madeWith: "guided" | "editor" }): string {
  const id = `${input.name.toLowerCase().replace(/[^a-z0-9]+/g, "-") || "workflow"}-${++seq}`;
  rows = [
    ...rows,
    { id, name: input.name, description: input.description, kind: "video", updatedAt: new Date().toISOString(), state: "draft", problemCount: 0, madeWith: input.madeWith, fitsGuided: input.madeWith === "guided" },
  ];
  return id;
}

export function peekWorkflowRow(id: string): Omit<WorkflowRow, "usedBy"> | null {
  return rows.find((r) => r.id === id) ?? null;
}

export function updateWorkflowRow(id: string, patch: Partial<Omit<WorkflowRow, "usedBy" | "id">>): void {
  const cur = find(id);
  rows = rows.map((r) => (r.id === cur.id ? { ...cur, ...patch, updatedAt: new Date().toISOString() } : r));
}

// The libraries that use a workflow, read from the library list at call time.
function usedBy(id: string): WorkflowRow["usedBy"] {
  return currentLibraries()
    .filter((l) => l.workflow?.id === id)
    .map((l) => ({ type: "library" as const, id: l.id, name: l.name }));
}

const withUsage = (r: Omit<WorkflowRow, "usedBy">): WorkflowRow => ({ ...r, usedBy: usedBy(r.id) });

function find(id: string): Omit<WorkflowRow, "usedBy"> {
  const r = rows.find((x) => x.id === id);
  if (!r) throw new ApiError("workflow_not_found", "That workflow doesn't exist.");
  return r;
}

export function listWorkflows(): Promise<WorkflowRow[]> {
  return respond(() => (getForcedState() === "empty" ? [] : rows.map(withUsage)));
}

export function duplicateWorkflow(id: string): Promise<WorkflowRow> {
  return respond(() => {
    const src = find(id);
    const copy = { ...src, id: `${id}-copy-${++seq}`, name: `${src.name} copy`, state: "draft" as const, problemCount: 0, fitsGuided: false, updatedAt: new Date().toISOString() };
    rows = [...rows, copy];
    return withUsage(copy);
  });
}

// Refused while a library uses the workflow (the 409 "in_use" of page 04); the page shows the names.
export function deleteWorkflow(id: string): Promise<{ ok: true } | { ok: false; code: "in_use"; usedBy: WorkflowRow["usedBy"] }> {
  return respond(() => {
    find(id);
    const users = usedBy(id);
    if (users.length > 0) return { ok: false as const, code: "in_use" as const, usedBy: users };
    rows = rows.filter((r) => r.id !== id);
    return { ok: true as const };
  });
}

// The file Export downloads: {format, formatVersion, appVersion, flow}. The flow carries its own name and description.
export async function exportWorkflow(id: string): Promise<Record<string, unknown>> {
  const { flow } = await getFlow(id);
  return { format: FORMAT, formatVersion: FORMAT_VERSION, appVersion: APP_VERSION, flow };
}

// Import accepts only a syp-workflow file (formatVersion 1) with a valid three-step flow, and adds it as a draft
// the editor can open. Anything else, including the old app's format, is not_a_workflow.
export async function importWorkflow(text: string): Promise<{ id: string }> {
  const flow = await respond((): Flow => {
    const notWorkflow = () => new ApiError("not_a_workflow", "That file isn't a workflow export.");
    let data: { format?: unknown; formatVersion?: unknown; flow?: unknown };
    try {
      data = JSON.parse(text);
    } catch {
      throw notWorkflow();
    }
    if (!data || data.format !== FORMAT || data.formatVersion !== FORMAT_VERSION || !data.flow || typeof data.flow !== "object") {
      throw notWorkflow();
    }
    const flow = data.flow as Flow;
    if (typeof flow.name !== "string" || !flow.name.trim() || !Array.isArray(flow.steps)) throw notWorkflow();
    try {
      assertThreeSteps(flow);
    } catch {
      throw notWorkflow();
    }
    return flow;
  });
  return createDraft({ name: flow.name, description: flow.description, flow });
}
