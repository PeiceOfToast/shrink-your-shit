// Real client, settings group (GET/PATCH /settings): the whole document, and a PATCH of only the changed fields, nested
// by group, saved all or nothing and answered with the whole document. Refusals: invalid_setting, unknown_setting and
// read_only, each with its field.
import type * as mock from "../mock.ts";
import { request, type Out } from "../http.ts";

export const getSettings: typeof mock.getSettings = () => request<Out<typeof mock.getSettings>>("GET", "/settings");
export const patchSettings: typeof mock.patchSettings = (patch) =>
  request<Out<typeof mock.patchSettings>>("PATCH", "/settings", patch);
