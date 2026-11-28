// The one place that decides where data comes from. Pages import from here only.
// Every group starts on the mock. A group whose flag is on (groups.ts) takes its real module over the mock.
import * as mock from "./mock.ts";
import * as realSummary from "./real/summary.ts";
import * as realAccess from "./real/access.ts";
import * as realLibraries from "./real/libraries.ts";
import * as realJobs from "./real/jobs.ts";
import * as realWorkflows from "./real/workflows.ts";
import * as realOverview from "./real/overview.ts";
import * as realEvents from "./real/events.ts";
import * as realSettings from "./real/settings.ts";
import * as realTrash from "./real/trash.ts";
import * as realWorkers from "./real/workers.ts";
import { isReal, type Group } from "./groups.ts";

const realByGroup: Partial<Record<Group, object>> = {
  summary: realSummary,
  access: realAccess,
  libraries: realLibraries,
  jobs: realJobs,
  workflows: realWorkflows,
  overview: realOverview,
  events: realEvents,
  settings: realSettings,
  trash: realTrash,
  workers: realWorkers,
};

function build(): typeof mock {
  const out: Record<string, unknown> = { ...mock };
  for (const group of Object.keys(realByGroup) as Group[]) {
    if (isReal(group)) Object.assign(out, realByGroup[group]);
  }
  return out as typeof mock;
}

export const api = build();
export { setForcedState, getForcedState, onForcedStateChange } from "./mock.ts";
export type * from "./types.ts";
export type * from "./flowTypes.ts";
export { LOG_CHOICES } from "./mockSettings.ts";
export { ApiError } from "./errors.ts";
export { onSummaryChanged } from "./summaryBus.ts";
export { isReal, sampleOnly, type Group } from "./groups.ts";
export { startLiveWire, startJobWire } from "./live.ts";
export { formatCount, formatGB, formatBytes, formatPct } from "./format.ts";
