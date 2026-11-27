// First run and sign in (page 13). Shapes come from page 13 and page 05 (starters). The guide's chips (page 06) are not mocked yet,
// so a starter's answers are empty here (a gap in the handback).
import { ApiError } from "./errors.ts";
import { DELAY_MS, getForcedState, onForcedStateChange, respond, setFreshInstall } from "./mockState.ts";
import { currentToken } from "./mockSettings.ts";

export type Setup = { needsAccount: boolean; starting: boolean };

export type Starter = { id: string; kind: string; name: string; line: string; icon: string; guided: boolean };

export type StarterDetail = Starter & { answers: Record<string, string> };

export type SetupInput = {
  account: { password: string };
  library: { name: string; path: string } | { name: string; shareId: string } | null;
  starter: { id: string; answers: Record<string, unknown> } | null;
};

const STARTERS: Starter[] = [
  { id: "shrink-videos", kind: "video", name: "Shrink videos", line: "Converts big files to HEVC on the graphics card", icon: "flow", guided: true },
  { id: "blank-video", kind: "video", name: "Blank", line: "Start from nothing and build the steps yourself", icon: "plus", guided: false },
];

// Finishing setup ends the forced "First run" state for the rest of the session (the switcher can set it again).
let setupDone = false;
onForcedStateChange(() => {
  setupDone = false;
});

// Page 13: the screen choice. Dev states "setup" (no account yet) and "starting" (server still starting) drive it.
export function getSetup(): Promise<Setup> {
  return respond(() => {
    const s = getForcedState();
    return { needsAccount: s === "setup" && !setupDone, starting: s === "starting" };
  });
}

// Page 05: the starter cards. The first run shows only the guided ones (page 13).
export function getStarters(): Promise<Starter[]> {
  return respond(() => STARTERS);
}

// GET /api/v2/workflows/starters/{id}: the starter and its answers (the guide's chips fill from these).
export function getStarter(id: string): Promise<StarterDetail> {
  return respond(() => {
    const s = STARTERS.find((x) => x.id === id);
    if (!s) throw new ApiError("not_found", "That starting workflow doesn't exist.");
    return { ...s, answers: {} };
  });
}

// Page 13: the password is 8 characters or more and at most 72 bytes. The messages are placeholders (a gap in the handback).
function checkPassword(password: string): void {
  if ([...password].length < 8) throw new ApiError("invalid_password", "Use at least 8 characters.", "account.password");
  if (new TextEncoder().encode(password).length > 72) throw new ApiError("invalid_password", "Use 72 bytes or fewer.", "account.password");
}

// Page 13: one call, all or nothing. A refused folder is 409 with field library.path and saves nothing.
export function postSetup(input: SetupInput): Promise<{ ok: true; scanStarted: boolean }> {
  return respond(() => {
    checkPassword(input.account.password);
    if (input.library && "path" in input.library && input.library.path.endsWith("/refused")) {
      throw new ApiError("overlaps", "That folder is already in another library. Pick a different folder.", "library.path");
    }
    setFreshInstall(input.library === null);
    setupDone = true;
    return { ok: true as const, scanStarted: input.library !== null };
  });
}

// Page 13 sign-in lockout: 5 failures from one address within a minute, then every try is 429 until the minute is up.
const WINDOW_MS = 60_000;
let failures: number[] = [];
let lockedUntil = 0;

function checkLock(now: number): void {
  if (now < lockedUntil) throw new ApiError("too_many_tries", "Too many tries. Wait a minute, then try again.");
}

function recordFailure(now: number): void {
  failures = failures.filter((t) => now - t < WINDOW_MS);
  failures.push(now);
  if (failures.length >= 5) lockedUntil = now + WINDOW_MS;
}

// Page 13: each failed sign-in waits about a second before it answers, so the form's waiting state is real.
const FAILURE_MS = 1000;
function slowFailure<T>(p: Promise<T>): Promise<T> {
  return p.catch(async (e: unknown) => {
    await new Promise((r) => setTimeout(r, FAILURE_MS - DELAY_MS));
    throw e;
  });
}

// POST /api/v2/signin {password}. A wrong password says so in one line and gives no hint which part was wrong.
export function signIn(password: string): Promise<{ ok: true }> {
  return slowFailure(respond(() => {
    const now = Date.now();
    checkLock(now);
    if (password !== "correct") {
      recordFailure(now);
      throw new ApiError("bad_password", "That password isn't right.");
    }
    failures = [];
    return { ok: true as const };
  }));
}

// POST /api/v2/signin {token}: the sign-in link. The bad_token text is a placeholder (a gap in the handback).
export function signInWithToken(token: string): Promise<{ ok: true }> {
  return slowFailure(respond(() => {
    const now = Date.now();
    checkLock(now);
    if (token !== currentToken()) {
      recordFailure(now);
      throw new ApiError("bad_token", "That sign-in link isn't valid. Ask for a new one.");
    }
    failures = [];
    return { ok: true as const };
  }));
}

// POST /api/v2/signout.
export function signOut(): Promise<{ ok: true }> {
  return respond(() => ({ ok: true as const }));
}
