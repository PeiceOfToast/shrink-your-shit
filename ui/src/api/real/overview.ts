// Real client, overview (GET /overview): the Dashboard. Stage 1 fills every field from what exists (workshop, Q1).
import type * as mock from "../mock.ts";
import { request, type Out } from "../http.ts";

export const getOverview: typeof mock.getOverview = () => request<Out<typeof mock.getOverview>>("GET", "/overview");

// Needs attention (GET /attention): an empty list in stage 1; the Dashboard must not show the mock's rows beside real data.
export const getAttention: typeof mock.getAttention = () => request<Out<typeof mock.getAttention>>("GET", "/attention");
