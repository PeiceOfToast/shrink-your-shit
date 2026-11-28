// Real client, settings group (GET/PATCH /settings): the whole document, and a PATCH of only the changed fields, nested
// by group, saved all or nothing and answered with the whole document. Refusals: invalid_setting, unknown_setting and
// read_only, each with its field.
import type * as mock from "../mock.ts";
import { request, type Out } from "../http.ts";
import type { Doctor, Worker } from "../types.ts";
import { testFinished, waitUntil } from "../../pages/encoderTest.ts";

export const getSettings: typeof mock.getSettings = () => request<Out<typeof mock.getSettings>>("GET", "/settings");
export const patchSettings: typeof mock.patchSettings = (patch) =>
  request<Out<typeof mock.patchSettings>>("PATCH", "/settings", patch);

// Settings > Health: the checks (GET /doctor, or /doctor?retest=1 to run them again).
export const getDoctor: typeof mock.getDoctor = (retest = false) =>
  request<Out<typeof mock.getDoctor>>("GET", "/doctor" + (retest ? "?retest=1" : ""));

// A check's fix, posted at its own path. The encoder test answers {runId}: wait for that run on the worker (the worker
// page's own check), then read the checks again. Any other answer is the doctor; any refusal is its message.
export const runHealthFix: typeof mock.runHealthFix = async (path) => {
  const reply = await request<unknown>("POST", path.replace(/^\/api\/v2/, ""));
  const runId = (reply as { runId?: unknown } | null)?.runId;
  if (typeof runId !== "string") return reply as Doctor;
  const worker = path.match(/\/workers\/([^/]+)\//)?.[1] ?? "local";
  await waitUntil(async () => testFinished(await request<Worker>("GET", `/workers/${encodeURIComponent(worker)}`), runId), {
    tries: 30,
    delayMs: 1000,
    sleep: (ms) => new Promise((r) => setTimeout(r, ms)),
  });
  return getDoctor(true);
};
