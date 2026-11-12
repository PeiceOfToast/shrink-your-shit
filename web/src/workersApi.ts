// V7 Workers on the real /api/v2/workers (#132), or the mock's workers with ?mock=1 (the same Worker shape).
import { call, failed, MOCK, type Answer } from "./api";
import * as mock from "./mock";
import type { Worker } from "./types/v2";

const base = (id: string) => "/api/v2/workers/" + encodeURIComponent(id);
export async function listWorkers(): Promise<Answer<Worker[]>> {
  if (MOCK) return mock.mockState().workers;
  const r = await call<{ items: Worker[] }>("GET", "/api/v2/workers");
  return failed(r) ? r : r.items;
}
export async function getWorker(id: string): Promise<Answer<Worker>> {
  if (MOCK) return mock.mockState().workers.find((w) => w.id === id) ?? { error: { code: "not_found", message: "There is no such worker." } };
  return call("GET", base(id));
}
// POST /workers/{id}/start|stop|drain: drain finishes what it is converting, then stops.
// User review 11-12 item 2 (#171): every worker at once (the "Workers on" switch), answered with the workers list.
// drain = "Stop after these files". Never per worker: /workers/{id}/... becomes per-worker later.
export async function allWorkers(action: "start" | "stop" | "drain"): Promise<Answer<Worker[]>> {
  if (MOCK) { for (const w of mock.mockState().workers) if (w.status !== "offline" && w.status !== "stalled") mock.workerAction(w.id, action); return mock.mockState().workers; }
  const r = await call<{ items: Worker[] }>("POST", "/api/v2/workers/all/" + action);
  return failed(r) ? r : r.items;
}
export async function workerAction(id: string, action: "start" | "stop" | "drain"): Promise<Answer<unknown>> {
  if (MOCK) { const e = mock.workerAction(id, action); return e ? { error: { code: "invalid", message: e } } : {}; }
  return call("POST", base(id) + "/" + action);
}
export async function patchWorker(id: string, p: { name?: string; slots?: { cpu?: number }; tags?: string[] }): Promise<Answer<Worker>> {
  if (MOCK) { mock.patchWorker(id, p); return mock.mockState().workers.find((w) => w.id === id)!; }
  return call("PATCH", base(id), p);
}
// POST /workers/{id}/capabilities/test: a short real conversion with each encoder; answers the worker.
export async function testCapabilities(id: string): Promise<Answer<Worker>> {
  if (MOCK) return mock.mockState().workers.find((w) => w.id === id)!;
  return call("POST", base(id) + "/capabilities/test");
}
// v1's GET /api/workers says whether an update stopped the workers (the notice "Converting is stopped after the update").
export async function stoppedByUpdate(): Promise<Answer<boolean>> {
  if (MOCK) return false;
  const r = await call<{ stoppedByUpdate: boolean }>("GET", "/api/workers");
  return failed(r) ? r : r.stoppedByUpdate;
}
export const dismissUpdateNotice = (): Promise<Answer<unknown>> => (MOCK ? Promise.resolve({}) : call("POST", "/api/workers/notice/dismiss"));

// V7b (#170): Add a worker asks for a one-time pairing code (15 minutes, one use) with the command to run on the other
// computer; plainHTTP: the page isn't on https (fine on the LAN, refused off it). The sample install can't pair.
export interface Pairing { code: string; expiresAt: string; command: string; plainHTTP: boolean }
export async function newPairing(): Promise<Answer<Pairing>> {
  if (MOCK) return { error: { code: "not_available", message: "The sample install can't pair a worker." } };
  return call("POST", "/api/v2/workers/pairings", {});
}
