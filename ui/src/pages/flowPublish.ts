// Publishing from the editor and the guided page. A new workflow's draft is made once, and its id is handed back before the
// publish, so a refused publish leaves the one draft in place and a retry publishes that same draft.
import type { Flow } from "../api/flowTypes.ts";

export interface PublishApi {
  publishFlow(id: string, flow: Flow): Promise<unknown>;
}

// Returns the id of the draft that was published.
export async function publishFlowOnce(
  api: PublishApi,
  input: { isNew: boolean; draftId: string; flow: Flow; makeDraft: () => Promise<{ id: string; rev: number }> },
  onCreated: (id: string, rev: number) => void,
): Promise<string> {
  let id = input.draftId;
  if (input.isNew) {
    const created = await input.makeDraft();
    onCreated(created.id, created.rev);
    id = created.id;
  }
  await api.publishFlow(id, input.flow);
  return id;
}
