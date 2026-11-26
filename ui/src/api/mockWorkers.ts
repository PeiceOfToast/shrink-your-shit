// Workers for the mock. Writes change an in-memory copy, so a session behaves like the app.
import type { Worker, WorkerDetail, WorkerLibrary, WorkerState } from "./types.ts";
import { ApiError } from "./errors.ts";
import { getForcedState, respond } from "./mockState.ts";
import { WORKERS } from "./fixtures/workers.ts";
import { currentLibraries } from "./mockLibraries.ts";

let rows: WorkerDetail[] = WORKERS.map((w) => ({ ...w, libraries: w.libraries.map((l) => ({ ...l })) }));

// A worker that joins after a pairing command is run. The mock joins one after a few seconds.
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
  hours: { from: 0, to: 24 },
  encoder: "auto",
  libraries: [],
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
});

const copy = (w: WorkerDetail): WorkerDetail => ({ ...w, libraries: w.libraries.map((l) => ({ ...l })) });

function find(id: string): WorkerDetail {
  const w = rows.find((r) => r.id === id);
  if (!w) throw new ApiError("worker_not_found", "That worker doesn't exist.");
  return w;
}

// The all on/off switch: every worker that is not offline converts, or pauses.
export function setAllWorkers(on: boolean): void {
  rows = rows.map((w) =>
    w.state === "offline" ? w : { ...w, state: on ? "converting" : "paused", since: on ? null : new Date().toISOString() },
  );
}

export function workerName(id: string): string | undefined {
  return rows.find((r) => r.id === id)?.name;
}

// A folder on the worker must be a full path there: from the root, or a drive letter or a network share.
const isAbsolute = (p: string) => /^\//.test(p) || /^[A-Za-z]:[\\/]/.test(p) || /^\\\\/.test(p);

export function getWorkers(): Promise<Worker[]> {
  return respond(() => {
    // Only this computer is left in the empty state, the one row the page explains.
    const list = getForcedState() === "empty" ? rows.filter((w) => w.local) : rows;
    return list.map(summary);
  });
}

export function getWorker(id: string): Promise<WorkerDetail> {
  return respond(() => copy(find(id)));
}

export type WorkerPatch = Partial<Pick<WorkerDetail, "slots" | "gpuSlots" | "hours" | "encoder">> & {
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
    if (patch.hours && (patch.hours.from < 0 || patch.hours.to > 24 || patch.hours.from >= patch.hours.to)) {
      throw new ApiError("invalid_setting", "Pick hours inside one day.", "hours");
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
    }
    if (patch.slots !== undefined) w.slots = patch.slots;
    if (patch.gpuSlots !== undefined) w.gpuSlots = patch.gpuSlots;
    if (patch.hours) w.hours = patch.hours;
    if (patch.encoder) w.encoder = patch.encoder;
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

// The command the other machine runs. The join is the mock's worker.joined event, see waitForJoin.
export function createPairing(): Promise<{ id: string; command: string }> {
  return respond(() => ({ id: "pair-1", command: "syp worker join 10.10.2.201 --code 4821-067" }));
}

export function waitForJoin(_pairingId: string): Promise<Worker> {
  return new Promise((resolve) => {
    setTimeout(() => {
      if (!rows.some((r) => r.id === JOINER.id)) rows = [...rows, { ...JOINER, libraries: [] }];
      resolve(summary(find(JOINER.id)));
    }, 3000);
  });
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
