// Load a screen's data from libraryApi: again every `everyMs` on the real server (scans and conversions move on), and
// whenever the mock changes in ?mock=1. An error keeps the last data, so the page doesn't blank on a failed refresh.
import { useCallback, useEffect, useRef, useState } from "react";
import { MOCK, SignedOut, failed, type Answer } from "./api";
import { useMock } from "./mock";
import type { Issue } from "./types/v1";

export function useData<T>(load: () => Promise<Answer<T>>, deps: unknown[], everyMs = 0) {
  const changed = MOCK ? useMock() : null; // MOCK is fixed for the page's life, so the hook order never changes
  const [st, set] = useState<{ data?: T; error?: Issue; loaded: boolean }>({ loaded: false });
  const n = useRef(0);
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const reload = useCallback(async () => {
    const my = ++n.current;
    try {
      const r = await load();
      if (my === n.current) set((p) => (failed(r) ? { data: p.data, error: r.error, loaded: true } : { data: r, loaded: true }));
    } catch (e) {
      if (!(e instanceof SignedOut)) throw e; // signed out: the page is going to sign-in
    }
  }, deps);
  useEffect(() => {
    reload();
    if (!everyMs || MOCK) return;
    const t = setInterval(reload, everyMs);
    return () => clearInterval(t);
  }, [reload, changed, everyMs]);
  return { ...st, reload };
}
