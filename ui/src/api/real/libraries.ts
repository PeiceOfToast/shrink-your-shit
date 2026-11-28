// Real client, libraries group (contract section 1): list, one, create, scan one, scan all.
import type * as mock from "../mock.ts";
import { request, type Out } from "../http.ts";
import { ApiError } from "../errors.ts";

const notYet = (message: string): Promise<never> => Promise.reject(new ApiError("not_yet", message));

export const getLibraries: typeof mock.getLibraries = () => request<Out<typeof mock.getLibraries>>("GET", "/libraries");
export const getLibrary: typeof mock.getLibrary = (id) =>
  request<Out<typeof mock.getLibrary>>("GET", `/libraries/${encodeURIComponent(id)}`);
export const createLibrary: typeof mock.createLibrary = (input) =>
  request<Out<typeof mock.createLibrary>>("POST", "/libraries", input);
export const scanLibrary: typeof mock.scanLibrary = (id) =>
  request<Out<typeof mock.scanLibrary>>("POST", `/libraries/${encodeURIComponent(id)}/scan`);
export const scanAllLibraries: typeof mock.scanAllLibraries = () =>
  request<Out<typeof mock.scanAllLibraries>>("POST", "/libraries/scan");
export const patchLibrary: typeof mock.patchLibrary = (id, patch) =>
  request<Out<typeof mock.patchLibrary>>("PATCH", `/libraries/${encodeURIComponent(id)}`, patch);
export const deleteLibrary: typeof mock.deleteLibrary = (id) =>
  request<Out<typeof mock.deleteLibrary>>("DELETE", `/libraries/${encodeURIComponent(id)}`);
// Reconnecting a share has no server route yet: the panel says so, not the mock's "doesn't exist".
export const reconnectShare: typeof mock.reconnectShare = () => notYet("Reconnecting a share isn't connected yet.");
