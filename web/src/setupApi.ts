// Enrollment (first page, ONE-COMMAND-SETUP.md section 11) on the real /api/v2/setup, or the mock with ?mock=1.
import { call, failed, MOCK, type Answer } from "./api";
import * as mock from "./mock";
import type { FoundServer, RoleBody, Setup } from "./types/v2";

export async function getSetup(): Promise<Answer<Setup>> {
  if (MOCK) return mock.mockState().setup;
  return call("GET", "/api/v2/setup");
}
// The server looks for others on the network (about 3 s) before it answers.
export async function listServers(): Promise<Answer<FoundServer[]>> {
  if (MOCK) return new Promise((ok) => setTimeout(() => ok(mock.foundServers.map((s) => ({ ...s }))), 1500));
  const r = await call<{ items: FoundServer[] }>("GET", "/api/v2/setup/servers");
  return failed(r) ? r : Array.isArray(r?.items) ? r.items : []; // an answer this page can't read reads as none found, not a crash
}
// {role:'server'} goes on to the normal first run; {role:'worker', serverUrl} asks to join (join.state waiting);
// {role:'unset'} cancels a request (Cancel request, Back). Errors: server_unreachable, not_a_server,
// pair_code_invalid, pair_code_expired, 409 role_change_busy.
export async function setRole(b: RoleBody): Promise<Answer<Setup>> {
  if (MOCK) return mock.setRole(b);
  return call("POST", "/api/v2/setup/role", b);
}
