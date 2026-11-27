// Real client, workflows group (contract section 1): the list, create, the editor's draft, publish,
// the check, the revisions and restore, duplicate and delete, the guided builder's answers and
// preview, trying the flow on one file, and the bitrate ladder. Export and import are not built
// and say so with a not-yet the page shows.
import type * as mock from "../mock.ts";
import { request, type Out } from "../http.ts";
import { ApiError } from "../errors.ts";

// The page shows this message as it is: the call has no server route yet.
const notYet = (message: string): Promise<never> => Promise.reject(new ApiError("not_yet", message));

export const listWorkflows: typeof mock.listWorkflows = () => request<Out<typeof mock.listWorkflows>>("GET", "/workflows");
export const duplicateWorkflow: typeof mock.duplicateWorkflow = (id) =>
  request<Out<typeof mock.duplicateWorkflow>>("POST", `/workflows/${encodeURIComponent(id)}/duplicate`);
export const deleteWorkflow: typeof mock.deleteWorkflow = (id) =>
  request<Out<typeof mock.deleteWorkflow>>("DELETE", `/workflows/${encodeURIComponent(id)}`);
export const getFlow: typeof mock.getFlow = (id) => request<Out<typeof mock.getFlow>>("GET", `/workflows/${encodeURIComponent(id)}`);
export const putDraft: typeof mock.putDraft = (id, flow, baseRev) =>
  request<Out<typeof mock.putDraft>>("PUT", `/workflows/${encodeURIComponent(id)}/draft`, { flow, baseRev });
export const publishFlow: typeof mock.publishFlow = (id, flow) =>
  request<Out<typeof mock.publishFlow>>("POST", `/workflows/${encodeURIComponent(id)}/publish`, { flow });
// The editor's first save, with a flow or with the guided page's answers; either makes the draft.
export const createDraft: typeof mock.createDraft = (input) => request<Out<typeof mock.createDraft>>("POST", "/workflows", input);
export const getStepDefinitions: typeof mock.getStepDefinitions = () =>
  request<Out<typeof mock.getStepDefinitions>>("GET", "/step-definitions");

// The guided builder: the answers a workflow opens on (id "new" is the defaults), the save that
// turns them into the draft, and "What this does", which saves nothing.
export const getGuideAnswers: typeof mock.getGuideAnswers = (id) =>
  request<Out<typeof mock.getGuideAnswers>>("GET", `/workflows/${encodeURIComponent(id)}/answers`);
export const putAnswers: typeof mock.putAnswers = (id, answers, baseRev) =>
  request<Out<typeof mock.putAnswers>>("PUT", `/workflows/${encodeURIComponent(id)}/answers`, { answers, baseRev });
export const guidePreview: typeof mock.guidePreview = (answers) =>
  request<Out<typeof mock.guidePreview>>("POST", "/workflows/preview", { answers });
// The editor's live check (S2-2's route). The check is pure: the body's flow is the whole input and
// the path's id only names the workflow, so an unsaved draft checks the same way.
export const validateFlow: typeof mock.validateFlow = (flow) =>
  request<Out<typeof mock.validateFlow>>("POST", "/workflows/0/validate", { flow });
// Try on one file (S2-5): the page's flow, unsaved, and the file's full path; the server runs it
// bounded on a sample and answers the lines. Nothing is kept either side. The page's Stop is the
// request's signal, so the run stops with it.
export const tryFlow: typeof mock.tryFlow = (flow, file, signal) =>
  request<Out<typeof mock.tryFlow>>("POST", "/workflows/try", { flow, file }, { signal });
// The bitrate ladder (S2-6): page 07's recommended numbers, served once and read by the editor and
// the engine alike.
export const getBitrateLadder: typeof mock.getBitrateLadder = () =>
  request<Out<typeof mock.getBitrateLadder>>("GET", "/bitrate-ladder");
// Out of scope for S2-3 (the brief says so): export and import.
export const exportWorkflow: typeof mock.exportWorkflow = () => notYet("Exporting a workflow is not built yet.");
export const importWorkflow: typeof mock.importWorkflow = () => notYet("Importing a workflow is not built yet.");
