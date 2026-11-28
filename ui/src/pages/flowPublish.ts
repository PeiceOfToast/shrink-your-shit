// Publishing from the editor. A new workflow's draft is made once, and its id is handed back before the publish, so a
// refused publish leaves the one draft in place and a retry publishes that same draft.
import type { Flow } from "../api/flowTypes.ts";

export interface PublishApi {
  createDraft(input: { name: string; description?: string; flow?: Flow }): Promise<{ id: string; rev: number }>;
  publishFlow(id: string, flow: Flow): Promise<unknown>;
}

export async function publishFlowOnce(
  api: PublishApi,
  input: { isNew: boolean; draftId: string; flow: Flow },
  onCreated: (id: string, rev: number) => void,
): Promise<void> {
  let id = input.draftId;
  if (input.isNew) {
    const created = await api.createDraft({ name: input.flow.name, description: input.flow.description, flow: input.flow });
    onCreated(created.id, created.rev);
    id = created.id;
  }
  await api.publishFlow(id, input.flow);
}
