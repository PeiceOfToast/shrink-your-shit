// Real client, overview (GET /overview): the Dashboard. Stage 1 fills every field from what exists (workshop, Q1).
import type * as mock from "../mock.ts";
import { request, type Out } from "../http.ts";

export const getOverview: typeof mock.getOverview = () => request<Out<typeof mock.getOverview>>("GET", "/overview");
