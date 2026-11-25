// The sign-in choice's routes (contract-signin-choice.md): GET /api/auth (answered without sign-in), POST /api/auth/mode,
// POST /api/login. The server half isn't built yet, so ?mock=1 answers from a mock that follows the contract, steered by
// URL switches: authMode=home|password, passwordSet=true|false, open=true|false, and authErr=<code> (password_needed,
// password_too_short or would_lock_out on a mode save; bad_password or too_many_tries on a password sign-in).
import { MOCK, call, type Answer } from "./api";
import { MIN_PASSWORD, authWord, type AuthMode, type AuthState } from "./signinRules";
import type { Issue } from "./types/v1";

// a 429 carries the wait in seconds (contract 3); read from the error object, where the server's error body keeps it
export type SignInError = Issue & { retryAfter?: number };

const q = new URLSearchParams(location.search);
let mock: AuthState = {
  mode: q.get("authMode") === "password" ? "password" : "home",
  passwordSet: q.get("passwordSet") !== "false",
  signedIn: true,
  open: q.get("open") !== "false",
  addressesHidden: q.get("addressesHidden") === "true",
};
const M = <T,>(v: T) => Promise.resolve(v);
const fail = (code: string, extra: Partial<SignInError> = {}): { error: SignInError } => ({ error: { code, message: authWord(code, code), ...extra } });

export const getAuth = (): Promise<Answer<AuthState>> => (MOCK ? M({ ...mock }) : call<AuthState>("GET", "/api/auth"));

export async function setAuthMode(body: { mode: AuthMode; password?: string }): Promise<Answer<AuthState>> {
  if (!MOCK) return call<AuthState>("POST", "/api/auth/mode", body);
  const e = q.get("authErr");
  if (e === "would_lock_out" && body.mode === "home") return fail(e);
  if (e === "password_needed" || e === "password_too_short") return fail(e);
  if (body.mode === "password" && !body.password && !mock.passwordSet) return fail("password_needed");
  if (body.password !== undefined && body.password.length < MIN_PASSWORD) return fail("password_too_short");
  mock = { ...mock, mode: body.mode, passwordSet: body.password ? true : mock.passwordSet };
  return { ...mock };
}

// A sign-in answers 204 with the cookie; the caller only checks for an error.
export async function signIn(body: { password: string } | { token: string }): Promise<Answer<unknown>> {
  if (!MOCK) return call("POST", "/api/login", body);
  const e = q.get("authErr");
  // too_many_tries carries the server's wait unless retryAfter=none (the sentence without a count)
  if (e === "too_many_tries") return fail(e, q.get("retryAfter") !== "none" ? { retryAfter: 60 } : {});
  if ("password" in body && e === "bad_password") return fail(e);
  return {};
}
