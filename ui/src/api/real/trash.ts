// Real client, trash group (GET /trash, POST /trash/{id}/restore): the files kept after a Replace, newest first, and a
// restore that puts the original back. Refusals are 409s with their code (original_gone, converted_changed, name_taken);
// an unknown id is 404 trash_not_found.
import type * as mock from "../mock.ts";
import { request, type Out } from "../http.ts";

export const getTrash: typeof mock.getTrash = () => request<Out<typeof mock.getTrash>>("GET", "/trash");
export const restoreTrash: typeof mock.restoreTrash = (id) =>
  request<Out<typeof mock.restoreTrash>>("POST", `/trash/${encodeURIComponent(id)}/restore`);
