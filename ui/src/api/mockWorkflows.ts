// Workflow list calls for the mock (page 04 "Data and API"). Rows are an in-memory copy, so a session behaves like the app.
import type { WorkflowRow } from "./types.ts";
import { ApiError } from "./errors.ts";
import { getForcedState, respond } from "./mockState.ts";
import { WORKFLOW_ROWS } from "./fixtures/workflowRows.ts";
import { currentLibraries } from "./mockLibraries.ts";

const FORMAT = "shrink-your-shit.workflow";
let rows: Omit<WorkflowRow, "usedBy">[] = WORKFLOW_ROWS.map((r) => ({ ...r }));
let seq = 0;

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

// The file Export downloads. Only the fields a user would keep; the page saves it as JSON.
export function exportWorkflow(id: string): Promise<Record<string, unknown>> {
  return respond(() => {
    const r = find(id);
    return { format: FORMAT, version: 1, name: r.name, description: r.description, kind: r.kind, madeWith: r.madeWith };
  });
}

// Import checks the file is a workflow export, adds it as a draft, and returns its id for the editor placeholder.
export function importWorkflow(text: string): Promise<{ id: string }> {
  return respond(() => {
    let data: { format?: unknown; name?: unknown; description?: unknown; kind?: unknown };
    try {
      data = JSON.parse(text);
    } catch {
      throw new ApiError("not_a_workflow", "That file isn't a workflow export.");
    }
    if (!data || data.format !== FORMAT || typeof data.name !== "string" || !data.name.trim()) {
      throw new ApiError("not_a_workflow", "That file isn't a workflow export.");
    }
    const id = `imported-${++seq}`;
    rows = [
      ...rows,
      {
        id,
        name: data.name.trim(),
        description: typeof data.description === "string" ? data.description : "",
        kind: "video",
        updatedAt: new Date().toISOString(),
        state: "draft",
        problemCount: 0,
        madeWith: "editor",
        fitsGuided: false,
      },
    ];
    return { id };
  });
}
