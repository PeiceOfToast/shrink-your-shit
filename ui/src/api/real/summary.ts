// Real client for the summary group (GET /summary). Its signatures must match the mock's: tsc checks that below.
import type * as mock from "../mock.ts";
import { request } from "../http.ts";
import type { Summary } from "../types.ts";

export const getSummary = (): Promise<Summary> => request<Summary>("GET", "/summary");

// Checked against the mock: the same signature or tsc fails.
({ getSummary }) satisfies Pick<typeof mock, "getSummary">;
