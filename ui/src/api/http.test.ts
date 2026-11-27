// The request passes its signal to fetch, and a stopped request rejects with the AbortError, not "unreachable".
import assert from "node:assert/strict";
import { test } from "node:test";
import { request } from "./http.ts";

// A fetch that never answers and rejects when its signal aborts, as the browser's does.
function hangingFetch(seen: { signal?: AbortSignal }) {
  return (_url: string | URL | Request, init?: RequestInit) => {
    seen.signal = init?.signal ?? undefined;
    return new Promise<Response>((_resolve, reject) => {
      init?.signal?.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")));
    });
  };
}

test("request passes the signal to fetch and a stop rejects with AbortError", async () => {
  const realFetch = globalThis.fetch;
  const seen: { signal?: AbortSignal } = {};
  globalThis.fetch = hangingFetch(seen) as typeof fetch;
  try {
    const ctl = new AbortController();
    const p = request("POST", "/workflows/0/try", { x: 1 }, { signal: ctl.signal });
    assert.equal(seen.signal, ctl.signal);
    ctl.abort();
    await assert.rejects(p, { name: "AbortError" });
  } finally {
    globalThis.fetch = realFetch;
  }
});

test("a request without a signal still has no signal on fetch", async () => {
  const realFetch = globalThis.fetch;
  const seen: { signal?: AbortSignal } = {};
  globalThis.fetch = (async (_url: string | URL | Request, init?: RequestInit) => {
    seen.signal = init?.signal ?? undefined;
    return new Response("{}", { status: 200 });
  }) as typeof fetch;
  try {
    await request("GET", "/x");
    assert.equal(seen.signal, undefined);
  } finally {
    globalThis.fetch = realFetch;
  }
});
