// Real client, libraries group (contract section 1): list, one, create, scan one, scan all.
import type * as mock from "../mock.ts";
import { request, type Out } from "../http.ts";

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

// Network shares (contract section 1, shares): test a share's address, save it, reconnect a dropped one.
export const testShare: typeof mock.testShare = (input) => request<Out<typeof mock.testShare>>("POST", "/shares/test", input);
export const createShare: typeof mock.createShare = (input) => request<Out<typeof mock.createShare>>("POST", "/shares", input);
export const reconnectShare: typeof mock.reconnectShare = (id) =>
  request<Out<typeof mock.reconnectShare>>("POST", `/shares/${encodeURIComponent(id)}/reconnect`);
