// Real client, workflows group (contract section 1): the list, create, the editor's draft, publish,
// the check, the revisions and restore, duplicate and delete. The guided builder's answers and the
// try-it preview (items 4-5) answer a plain not-yet the page shows; export and import are not part
// of S2-3 and say so the same way.
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
// The editor's first save; with answers the server answers 400 not_yet, which the page shows.
export const createDraft: typeof mock.createDraft = (input) => request<Out<typeof mock.createDraft>>("POST", "/workflows", input);
export const getStepDefinitions: typeof mock.getStepDefinitions = () =>
  request<Out<typeof mock.getStepDefinitions>>("GET", "/step-definitions");

// Item 4: the guided builder (answers to a flow) is not built yet.
export const getGuideAnswers: typeof mock.getGuideAnswers = () => notYet("The guided builder is not built yet; use the editor.");
export const putAnswers: typeof mock.putAnswers = () => notYet("The guided builder is not built yet; use the editor.");
export const guidePreview: typeof mock.guidePreview = () => notYet("The guided builder is not built yet; use the editor.");
// The editor's live check (S2-2's route). The check is pure: the body's flow is the whole input and
// the path's id only names the workflow, so an unsaved draft checks the same way.
export const validateFlow: typeof mock.validateFlow = (flow) =>
  request<Out<typeof mock.validateFlow>>("POST", "/workflows/0/validate", { flow });
// Item 6: trying the flow on a file, and its bitrate table.
export const tryFlow: typeof mock.tryFlow = () => notYet("Trying a flow on a file is not built yet.");
export const getBitrateLadder: typeof mock.getBitrateLadder = () => notYet("The bitrate table is not built yet.");
// Out of scope for S2-3 (the brief says so): export and import.
export const exportWorkflow: typeof mock.exportWorkflow = () => notYet("Exporting a workflow is not built yet.");
export const importWorkflow: typeof mock.importWorkflow = () => notYet("Importing a workflow is not built yet.");
