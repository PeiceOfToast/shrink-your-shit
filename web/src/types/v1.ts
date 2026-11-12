// v1's library routes (/api/libraries...), which the Libraries page uses until /api/v2/libraries comes with V5
// (contract v2, "Libraries: what exists now"). Field names exactly as internal/web/libraries.go, doctor.go and
// preview/firstfiles.go send them.
import type { ID, Time } from "./v2";

export interface Issue { code: string; message: string } // also an error answer's body
export interface LibraryItem { // GET /api/libraries -> {items, total}; GET/POST/PATCH answer one
  id: ID;
  name: string;
  path: string;
  state: "working" | "idle" | "needs_attention" | "not_started";
  started: boolean; // a new library isn't: POST /api/libraries/{id}/scan starts it
  workingFiles: number;
  waitingFiles: number;
  nextScan: Time | null;
  freedBytes: number;
  waitingBytes: number;
  flowId: ID; // the workflow's id
  settingLabel: string;
  settingKind: string;
  status: { kind: "converting" | "waiting_for_worker" | "scanning" | "scanned" | "not_scanned"; videos: number; foundSoFar: number | null;
    lastScan: Time | null; nextScan: Time | null; converting: number; toDo: number };
  failedFiles: number;
  madeWith: string;
  tag: string;
  freeBytes?: number | null; // workshop library-free-space 5053a73: the disk's free space; null when the folder can't be read
}
export interface FolderReport { ok: boolean; blockers: Issue[]; notes: Issue[]; videoCount: number; totalBytes: number } // POST /api/libraries/check {path}
export interface FirstFile { path: string; folder: string; name: string; bytes: number; state: "checking" | "ready" | "unreadable"; codec: string | null; resolution: string | null; durationSec: number | null }
export interface FirstFiles { items: FirstFile[]; total: number; stillLooking: boolean } // POST /api/libraries/first-files {path}, GET /api/libraries/{id}/first-files
export interface FlowItem { id: ID; name: string; usedBy: string[]; madeWith: string; changedAt: Time; kind: string; tag: string } // GET /api/flows -> {items, total}
export interface WorkerV1 { id: ID; name: string; host: string; state: string; filesAtOnce: number; gpu: { found: boolean; vendor: string | null }; converting: unknown[]; quietUntil: Time | null } // GET /api/workers -> {workers}
// GET/PATCH /api/settings: the fields first run uses (the server sends more).
export interface FirstRunSettings { firstRunDone: boolean; firstRunStep: number; firstRunLibrary: { name: string; path: string } | null;
  scanEvery?: number; scanEverySource?: "flag" | "saved"; scanEveryMin?: number; scanEveryMax?: number } // #136/#146: how often libraries are looked at, in seconds, and what PATCH accepts
