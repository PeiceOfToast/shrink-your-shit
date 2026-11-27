import { useEffect, useState } from "react";
import { onForcedStateChange } from "./mock.ts";
import { setConnection } from "./connection.ts";
import { ApiError } from "./errors.ts";
import { onRefresh } from "./refresh.ts";

export interface ApiState<T> {
  data?: T;
  error?: string;
  // The ApiError code, so a page can tell "not found" from a load failure.
  code?: string;
  loading: boolean;
  reload: () => void;
}

// Runs one API call and refetches when the dev state switcher changes the mock state.
export function useApi<T>(load: () => Promise<T>, deps: unknown[] = []): ApiState<T> {
  const [state, setState] = useState<{ data?: T; error?: string; code?: string; loading: boolean }>({ loading: true });
  const [tick, setTick] = useState(0);

  useEffect(() => onForcedStateChange(() => setTick((t) => t + 1)), []);
  // A live event or a resync asks every mounted load to run again.
  useEffect(() => onRefresh(() => setTick((t) => t + 1)), []);

  useEffect(() => {
    let alive = true;
    setState((s) => ({ data: s.data, loading: true }));
    load().then(
      (data) => {
        if (!alive) return;
        setConnection(true);
        setState({ data, loading: false });
      },
      (e: unknown) => {
        if (!alive) return;
        setConnection(false);
        setState({ loading: false, error: e instanceof Error ? e.message : String(e), code: e instanceof ApiError ? e.code : undefined });
      },
    );
    return () => {
      alive = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tick, ...deps]);

  return { ...state, reload: () => setTick((t) => t + 1) };
}
