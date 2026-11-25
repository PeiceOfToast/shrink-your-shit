// Sign-in choice (contract-signin-choice.md for the codes and routes; design-direction-signin-choice.md for the words).
// Pure rules, so they can be tested without a DOM.
export const MIN_PASSWORD = 8;
export type AuthMode = "home" | "password";
// GET /api/auth, answered without sign-in. Only these four fields.
export interface AuthState { mode: AuthMode; passwordSet: boolean; signedIn: boolean; open: boolean }

// The field sentence for a password that can't be saved as typed (design's words).
export const passwordProblem = (pw: string, again: string): string | null =>
  pw.length < MIN_PASSWORD ? `Too short: use at least ${MIN_PASSWORD} characters.` : pw !== again ? "The two passwords don't match." : null;

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
