// A Needs attention action's reply, read the same way by the real client and the mock. POST /jobs/actions answers
// {done, refused}: a refusal that nothing got past is an error with the server's message; a mix shows how many went
// through and what was refused; a reply with only a line shows that line; anything else shows nothing.
import { ApiError } from "./errors.ts";

export interface ActionRefusal {
  id: string;
  code: string;
  message: string;
}

export function actionOutcome(reply: unknown): { line?: string } {
  const r = (reply ?? {}) as { line?: unknown; done?: unknown; refused?: unknown };
  const refused = Array.isArray(r.refused) ? (r.refused as ActionRefusal[]) : [];
  const done = Array.isArray(r.done) ? r.done : [];
  if (refused.length > 0) {
    if (done.length === 0) {
      const more = refused.length > 1 ? ` (and ${refused.length - 1} more)` : "";
      throw new ApiError(refused[0].code, refused[0].message + more);
    }
    return { line: `${done.length} retrying. ${refused.length} couldn't be: ${refused[0].message}` };
  }
  return typeof r.line === "string" ? { line: r.line } : {};
}
