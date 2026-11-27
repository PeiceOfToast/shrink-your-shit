// The one place that decides where data comes from. Pages import from here only.
// Today it is always the mock. When the backend is ready, switch the module below to the real client.
import * as mock from "./mock.ts";

export const api = mock;
export { setForcedState, getForcedState, onForcedStateChange } from "./mock.ts";
export type * from "./types.ts";
export type * from "./flowTypes.ts";
export { LOG_CHOICES } from "./mockSettings.ts";
export { ApiError } from "./errors.ts";
export { onSummaryChanged } from "./summaryBus.ts";
export { formatCount, formatGB, formatBytes, formatPct } from "./format.ts";
