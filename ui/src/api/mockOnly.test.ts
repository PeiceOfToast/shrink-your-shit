// Every mock function needs a real twin, or an entry in MOCK_ONLY with the reason it stays mock-only. The overlay in
// index.ts keeps the mock for any name no real module exports, so a missing twin shows sample data on a real server.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readdirSync } from "node:fs";
import * as mock from "./mock.ts";

const MOCK_ONLY: Record<string, string> = {
  // Helpers the mock uses inside itself. Pages do not call them.
  answersToFlow: "mock helper: builds a flow from the guided answers",
  assertThreeSteps: "mock helper: checks a flow has its three steps",
  checkFlow: "mock helper: the flow check the mock's validate answers with",
  createWorkflowRow: "mock helper: the mock's workflow table",
  peekWorkflowRow: "mock helper: the mock's workflow table",
  updateWorkflowRow: "mock helper: the mock's workflow table",
  clearLibraries: "mock helper: the mock's library table (dev state)",
  currentLibraries: "mock helper: the mock's library table (dev state)",
  insertLibrary: "mock helper: the mock's library table (dev state)",
  currentToken: "mock helper: the token the mock's sign-in checks",
  restoreCanRetry: "client-side rule for Trash (no server call)",
  workerName: "mock helper: the mock's worker table",
  workersTaking: "mock helper: the mock's worker table",
  workersSwitch: "mock helper: the mock's all-workers switch state",
  healthFailing: "mock helper: the sample health state the red dot reads",
  getSeenAt: "mock helper: the sample log's seen counter",
  getSeenSeq: "mock helper: the sample log's seen counter",
  newErrorCount: "mock helper: the sample log's new-errors count",
  getForcedState: "mock-only dev control: the state switcher",
  setForcedState: "mock-only dev control: the state switcher",
  onForcedStateChange: "mock-only dev control: the state switcher",
  // Real clients on open PRs, not on rebuild yet.
  changePassword: "real client on PR #911 (Access)",
  getToken: "real client on PR #911 (Access)",
  rotateToken: "real client on PR #911 (Access)",
  getDoctor: "real client on PR #913 (Health)",
  runHealthFix: "real client on PR #913 (Health)",
  getLog: "real client on PR #907 (System log)",
  getLogDownload: "real client on PR #907 (System log)",
  getLogSources: "real client on PR #907 (System log)",
  markLogSeen: "real client on PR #907 (System log)",
  subscribeAppEvents: "real client on PR #907 (System log Follow)",
  getPreview: "real client waits on workshop's library-preview PR #908",
  runAction: "real client on PR #901 (Needs attention actions)",
  getWorker: "real client on PR #898 (Workers)",
  getWorkers: "real client on PR #898 (Workers)",
  patchWorker: "real client on PR #898 (Workers)",
  pauseWorker: "real client on PR #898 (Workers): no server route yet for pausing",
  resumeWorker: "real client on PR #898 (Workers): no server route yet for resuming",
  removeWorker: "real client on PR #898 (Workers): no server route yet for removing",
  drainAllWorkers: "real client on PR #898 (Workers): no server route yet for the all-workers switch",
  startAllWorkers: "real client on PR #898 (Workers): no server route yet for the all-workers switch",
  testEncoders: "real client on PR #906 (Workers): the run-id reply",
  testWorkerPath: "real client on PR #898 (Workers): no server route yet for folder tests",
  waitForPathTest: "real client on PR #898 (Workers): no server route yet for folder tests",
  createPairing: "no server route yet (Add worker)",
  waitForJoinRequest: "no server route yet (Add worker)",
  allowJoinRequest: "no server route yet (Add worker)",
  denyJoinRequest: "no server route yet (Add worker)",
  applyBackup: "no server route yet (Settings > Backup)",
  createBackup: "no server route yet (Settings > Backup)",
  deleteBackup: "no server route yet (Settings > Backup)",
  downloadBackup: "no server route yet (Settings > Backup)",
  getBackups: "no server route yet (Settings > Backup)",
  previewBackup: "no server route yet (Settings > Backup)",
  createIntegration: "no server route yet (Settings > Connections)",
  deleteIntegration: "no server route yet (Settings > Connections)",
  getIntegrations: "no server route yet (Settings > Connections)",
  patchIntegration: "no server route yet (Settings > Connections)",
  testIntegration: "no server route yet (Settings > Connections)",
  createTarget: "no server route yet (Settings > Notifications)",
  deleteTarget: "no server route yet (Settings > Notifications)",
  patchTarget: "no server route yet (Settings > Notifications)",
  testTarget: "no server route yet (Settings > Notifications)",
  getNotifications: "no server route yet (Settings > Notifications)",
  getNotifyEvents: "no server route yet (Settings > Notifications)",
  patchNotifications: "no server route yet (Settings > Notifications)",
  createShare: "no server route yet (Libraries: share)",
  testShare: "no server route yet (Libraries: share)",
  reconnectShare: "no server route yet (Libraries: share)",
  getWorkflow: "not wired on rebuild yet (Workflows)",
};

test("every mock function has a real twin, or is named in MOCK_ONLY with its reason", async () => {
  const real = new Set<string>();
  for (const f of readdirSync(new URL("./real/", import.meta.url))) {
    if (!f.endsWith(".ts") || f.endsWith(".test.ts")) continue;
    for (const k of Object.keys(await import(`./real/${f}`))) real.add(k);
  }
  const fns = Object.keys(mock).filter((k) => typeof (mock as Record<string, unknown>)[k] === "function");
  const unnamed = fns.filter((k) => !real.has(k) && !(k in MOCK_ONLY));
  assert.deepEqual(unnamed, [], "mock functions with no real twin and no MOCK_ONLY entry");
  const stale = Object.keys(MOCK_ONLY).filter((k) => real.has(k) || !fns.includes(k));
  assert.deepEqual(stale, [], "MOCK_ONLY entries that are now real, or are not mock functions");
});
