// Workers for the mock. Writes change an in-memory copy, so a session behaves like the app.
import type { JoinRequest, Pairing, Worker, WorkerDetail, WorkerLibrary, WorkerState } from "./types.ts";
import { ApiError } from "./errors.ts";
import { getForcedState, respond } from "./mockState.ts";
import { WORKERS } from "./fixtures/workers.ts";
import { SUMMARY } from "./fixtures/index.ts";
import { currentLibraries } from "./mockLibraries.ts";

let rows: WorkerDetail[] = WORKERS.map(copy);

// A worker that asks to join after its pairing command is run. The mock asks after two seconds.
const JOINER: WorkerDetail = {
  id: "den-pc",
  name: "Den PC",
  address: "10.10.2.52",
  local: false,
  state: "idle",
  since: null,
  slots: 1,
  gpuSlots: 0,
  gpu: null,
  now: null,
  doneToday: 0,
  pausedGlobally: false,
  hours: null,
  primaryEncoder: null,
  encoders: [],
  libraries: [],
  newLibraries: [],
};

const summary = (w: WorkerDetail): Worker => ({
  id: w.id,
  name: w.name,
  address: w.address,
  local: w.local,
  state: w.state,
  since: w.since,
  slots: w.slots,
  gpuSlots: w.gpuSlots,
  gpu: w.gpu,
  now: w.now,
  doneToday: w.doneToday,
  pausedGlobally: globalPaused,
});

function copy(w: WorkerDetail): WorkerDetail {
  return { ...w, libraries: w.libraries.map((l) => ({ ...l })), newLibraries: w.newLibraries.map((l) => ({ ...l })), encoders: w.encoders.map((e) => ({ ...e })) };
}

function find(id: string): WorkerDetail {
  const w = rows.find((r) => r.id === id);
  if (!w) throw new ApiError("worker_not_found", "That worker doesn't exist.");
  return w;
}

// The all-workers switch. Off starts no new jobs; the files already running finish (finishing counts them, then 0).
// A worker keeps its own state, so when the switch goes back on each worker is as it was.
export const FINISH_MS = 8000;
let globalPaused = false;
let finishing = 0;
let finishTimer: ReturnType<typeof setTimeout> | undefined;

export function workersSwitch(): { paused: boolean; finishing: number } {
  return { paused: globalPaused, finishing };
}

// Each route answers with the workers list.
export function drainAllWorkers(): Promise<Worker[]> {
  return respond(() => {
    globalPaused = true;
    finishing = SUMMARY.converting.running;
    clearTimeout(finishTimer);
    finishTimer = setTimeout(() => {
      finishing = 0;
      // The finishing files are done, so no worker still shows a file on the Now column.
      rows.forEach((r) => {
        r.now = null;
      });
    }, FINISH_MS);
    return rows.map(summary);
  });
}

export function startAllWorkers(): Promise<Worker[]> {
  return respond(() => {
    globalPaused = false;
    finishing = 0;
    clearTimeout(finishTimer);
    return rows.map(summary);
  });
}

export function workerName(id: string): string | undefined {
  return rows.find((r) => r.id === id)?.name;
}

// The workers that take a library, for the library page: this computer takes every library.
export function workersTaking(libraryId: string): { id: string; name: string }[] {
  return rows.filter((w) => w.local || w.libraries.some((l) => l.libraryId === libraryId)).map((w) => ({ id: w.id, name: w.name }));
}

// A folder on the worker must be a full path there: from the root, or a drive letter or a network share.
const isAbsolute = (p: string) => /^\//.test(p) || /^[A-Za-z]:[\\/]/.test(p) || /^\\\\/.test(p);
const HHMM = /^([01]\d|2[0-3]):[0-5]\d$/;

export function getWorkers(): Promise<Worker[]> {
  return respond(() => {
    // Only this computer is left in the empty state, the one row the page explains.
    const list = getForcedState() === "empty" ? rows.filter((w) => w.local) : rows;
    return list.map(summary);
  });
}

export function getWorker(id: string): Promise<WorkerDetail> {
  return respond(() => ({ ...copy(find(id)), pausedGlobally: globalPaused }));
}

export type WorkerPatch = Partial<Pick<WorkerDetail, "slots" | "gpuSlots" | "hours" | "primaryEncoder">> & {
  libraries?: { libraryId: string; workerPath: string; mode: WorkerLibrary["mode"] }[];
};

export function patchWorker(id: string, patch: WorkerPatch): Promise<WorkerDetail> {
  return respond(() => {
    const w = find(id);
    if (patch.slots !== undefined && (!Number.isInteger(patch.slots) || patch.slots < 1)) {
      throw new ApiError("invalid_setting", "Files at once is at least 1.", "slots");
    }
    if (patch.gpuSlots !== undefined && (!Number.isInteger(patch.gpuSlots) || patch.gpuSlots < 0)) {
      throw new ApiError("invalid_setting", "Graphics slots is 0 or more.", "gpuSlots");
    }
    if (patch.hours) {
      const { from, to } = patch.hours;
      if (!HHMM.test(from) || !HHMM.test(to) || from === to) {
        throw new ApiError("invalid_setting", "Pick two different times. From later than To runs overnight.", "hours");
      }
    }
    if (patch.primaryEncoder !== undefined && patch.primaryEncoder !== null && patch.primaryEncoder !== "cpu") {
      const enc = w.encoders.find((e) => e.name === patch.primaryEncoder);
      if (!enc) throw new ApiError("invalid_encoder", "This worker has no such encoder.", "primaryEncoder");
      if (!enc.testOk) throw new ApiError("encoder_not_working", "That encoder failed its test. Test the encoders, then pick it again.", "primaryEncoder");
    }
    if (patch.libraries) {
      if (w.local) throw new ApiError("local_worker", "This computer takes every library, at the same paths.");
      const known = currentLibraries();
      const next: WorkerLibrary[] = [];
      patch.libraries.forEach((l, i) => {
        const field = `libraries[${i}].workerPath`;
        if (!known.some((k) => k.id === l.libraryId)) throw new ApiError("invalid_setting", "That library doesn't exist.", `libraries[${i}].libraryId`);
        if (!isAbsolute(l.workerPath)) throw new ApiError("invalid_setting", "Enter the folder's full path on that machine.", field);
        const old = w.libraries.find((o) => o.libraryId === l.libraryId);
        const keep = old !== undefined && old.workerPath === l.workerPath;
        next.push({
          libraryId: l.libraryId,
          name: known.find((k) => k.id === l.libraryId)!.name,
          serverPath: known.find((k) => k.id === l.libraryId)!.path,
          workerPath: l.workerPath,
          mode: l.mode,
          test: keep ? old.test : { state: "untested" },
        });
      });
      w.libraries = next;
      // Saving the list is what makes the new libraries count as seen.
      w.newLibraries = [];
    }
    if (patch.slots !== undefined) w.slots = patch.slots;
    if (patch.gpuSlots !== undefined) w.gpuSlots = patch.gpuSlots;
    if (patch.hours !== undefined) w.hours = patch.hours;
    if (patch.primaryEncoder !== undefined) w.primaryEncoder = patch.primaryEncoder;
    return copy(w);
  });
}

// Pause or resume one worker. Pausing with finish: true lets the files it is on finish first.
function setState(id: string, state: WorkerState, since: string | null): Promise<Worker> {
  return respond(() => {
    const w = find(id);
    if (w.state === "offline") throw new ApiError("worker_offline", "This worker is offline.");
    w.state = state;
    w.since = since;
    return summary(w);
  });
}

export function pauseWorker(id: string, _finish: boolean): Promise<Worker> {
  return setState(id, "paused", new Date().toISOString());
}

export function resumeWorker(id: string): Promise<Worker> {
  return setState(id, "converting", null);
}

// The command the other machine runs. The mock's join request appears after the command runs (see waitForJoinRequest).
export function createPairing(): Promise<Pairing> {
  return respond(() => ({
    id: "pair-1",
    code: "4821-067",
    expiresAt: new Date(Date.now() + 10 * 60_000).toISOString(),
    command: "syp worker join 10.10.2.201 --code 4821-067",
    plainHTTP: true,
  }));
}

export function waitForJoinRequest(_pairingId: string): Promise<JoinRequest> {
  return new Promise((resolve) => {
    setTimeout(() => resolve({ id: "join-1", name: JOINER.name, address: JOINER.address }), 2000);
  });
}

// Allow adds the worker. It shows on the list from then on.
export function allowJoinRequest(_requestId: string): Promise<Worker> {
  return respond(() => {
    if (!rows.some((r) => r.id === JOINER.id)) rows = [...rows, copy(JOINER)];
    return summary(find(JOINER.id));
  });
}

export function denyJoinRequest(_requestId: string): Promise<void> {
  return respond(() => undefined);
}

export function removeWorker(id: string): Promise<void> {
  return respond(() => {
    const w = find(id);
    if (w.state !== "offline") throw new ApiError("online", "Stop the worker before you remove it.");
    rows = rows.filter((r) => r.id !== id);
  });
}

// Starts a folder test on one remote worker. The result arrives through waitForPathTest (the worker.path_test event).
export function testWorkerPath(id: string, libraryId: string): Promise<{ state: "pending" }> {
  return respond(() => {
    const w = find(id);
    if (w.state === "offline") throw new ApiError("worker_offline", "This worker is offline, so its folder can't be checked.");
    const l = w.libraries.find((x) => x.libraryId === libraryId);
    if (!l) throw new ApiError("invalid_setting", "Tick the library first.", "libraryId");
    l.test = { state: "pending" };
    return { state: "pending" as const };
  });
}

export function waitForPathTest(id: string, libraryId: string): Promise<WorkerLibrary["test"]> {
  return new Promise((resolve) => {
    setTimeout(() => {
      const l = find(id).libraries.find((x) => x.libraryId === libraryId);
      if (!l) return resolve({ state: "failed", message: "The library isn't taken any more." });
      // The mock fails a path that says "missing", so the failed state can be shown.
      l.test = l.workerPath.includes("missing")
        ? { state: "failed", at: new Date().toISOString(), message: `Folder not found on ${find(id).name}.` }
        : { state: "ok", at: new Date().toISOString() };
      resolve({ ...l.test });
    }, 1500);
  });
}
