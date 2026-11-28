// Real client, workers group (contract section 1): the list, one worker, and its own settings (PATCH). The server has no
// route yet for pausing, removing, pairing or testing a worker, so those answer with a plain sentence and send nothing.
import type * as mock from "../mock.ts";
import { request, type Out } from "../http.ts";
import { ApiError } from "../errors.ts";

const notYet = (message: string): Promise<never> => Promise.reject(new ApiError("not_yet", message));

export const getWorkers: typeof mock.getWorkers = () => request<Out<typeof mock.getWorkers>>("GET", "/workers");
export const getWorker: typeof mock.getWorker = (id) =>
  request<Out<typeof mock.getWorker>>("GET", `/workers/${encodeURIComponent(id)}`);
export const patchWorker: typeof mock.patchWorker = (id, patch) =>
  request<Out<typeof mock.patchWorker>>("PATCH", `/workers/${encodeURIComponent(id)}`, patch);

export const pauseWorker: typeof mock.pauseWorker = () => notYet("Pausing a worker isn't connected yet.");
export const resumeWorker: typeof mock.resumeWorker = () => notYet("Resuming a worker isn't connected yet.");
export const removeWorker: typeof mock.removeWorker = () => notYet("Removing a worker isn't connected yet.");
export const createPairing: typeof mock.createPairing = () => notYet("Adding a worker isn't connected yet.");
export const waitForJoinRequest: typeof mock.waitForJoinRequest = () => notYet("Adding a worker isn't connected yet.");
export const allowJoinRequest: typeof mock.allowJoinRequest = () => notYet("Adding a worker isn't connected yet.");
export const denyJoinRequest: typeof mock.denyJoinRequest = () => notYet("Adding a worker isn't connected yet.");
export const testWorkerPath: typeof mock.testWorkerPath = () => notYet("Testing a worker's folder isn't connected yet.");
export const waitForPathTest: typeof mock.waitForPathTest = () => notYet("Testing a worker's folder isn't connected yet.");
export const testEncoders: typeof mock.testEncoders = () => notYet("Testing the encoders isn't connected yet.");
export const drainAllWorkers: typeof mock.drainAllWorkers = () => notYet("Pausing all workers isn't connected yet.");
export const startAllWorkers: typeof mock.startAllWorkers = () => notYet("Starting all workers isn't connected yet.");
