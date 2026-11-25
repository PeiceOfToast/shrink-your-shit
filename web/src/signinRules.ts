// Sign-in choice (contract-signin-choice.md for the codes and routes; design-direction-signin-choice.md for the words).
// Pure rules, so they can be tested without a DOM.
export const MIN_PASSWORD = 8;
export type AuthMode = "home" | "password";
// GET /api/auth, answered without sign-in. Only these four fields.
// addressesHidden: the server runs behind its default gateway (Docker's bridge), so "home network" can't be told from outside
export interface AuthState { mode: AuthMode; passwordSet: boolean; signedIn: boolean; open: boolean; addressesHidden?: boolean }

// The Settings access-token line. Home devices get in without the token once set up, but when addresses are hidden the
// server can't tell home from outside, so the home sentence is not said.
export const accessTokenWords = (authRequired: boolean, addressesHidden: boolean) =>
  authRequired || addressesHidden ? "Needed to open the app from another device." : "Not needed on your home network. Needed to open the app from outside your home.";

// The field sentence for a password that can't be saved as typed (design's words).
export const passwordProblem = (pw: string, again: string): string | null =>
  pw === "" ? `Type a password: at least ${MIN_PASSWORD} characters.` : pw.length < MIN_PASSWORD ? `Too short: use at least ${MIN_PASSWORD} characters.` : pw !== again ? "The two passwords don't match." : null;

// Save may go ahead. Home needs nothing. Password needs valid fields, unless the password stays as it is: one is set
// already, or the mode is password now (an existing install keeps token sign-in with the fields empty).
export function canSave(cur: { mode: AuthMode; passwordSet: boolean }, want: { mode: AuthMode; pw: string; again: string }): boolean {
  if (want.mode === "home") return true;
  if (want.pw === "" && want.again === "") return cur.passwordSet || cur.mode === "password";
  return passwordProblem(want.pw, want.again) === null;
}

// The server's codes in design's words, or the contract's where design gives none. Anything else keeps the server's text.
export const AUTH_WORDS: Record<string, string> = {
  password_needed: "Pick a password first, so you can still sign in.",
  password_too_short: `Use at least ${MIN_PASSWORD} characters.`,
  would_lock_out: "You're connecting from outside your home network, so turning sign-in off would lock you out. Change it from a computer on your home network.",
  bad_password: "That password isn't right.",
  too_many_tries: "Too many tries. Wait a minute, then try again.",
};
export const authWord = (code: string, fallback: string): string => AUTH_WORDS[code] ?? fallback;

// too_many_tries at the field (design #713 answer 3). The alert holds only the first sentence, so the ticking count
// is outside the live region and isn't read out again. Without the server's wait, the sentence says a minute.
export const TOO_MANY = "Too many tries.";
export const WAIT_A_MINUTE = "Wait a minute, then try again.";
export const tryAgainIn = (n: number): string => `Try again in ${n} ${n === 1 ? "second" : "seconds"}.`;
