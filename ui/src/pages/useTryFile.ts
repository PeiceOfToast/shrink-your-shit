// The "Try on one file" run, shared by the guided page and the editor: one try at a time, a Stop that aborts it, and
// a stopped try shows only "Stopped. Nothing was kept." (no error line).
import { useEffect, useRef, useState } from "react";
import type { TryLine } from "../api/index.ts";
import { toErr, type Err } from "./flowParts.tsx";

export function useTryFile() {
  const [trying, setTrying] = useState(false);
  const [lines, setLines] = useState<TryLine[] | null>(null);
  const [err, setErr] = useState<Err | null>(null);
  const [stopped, setStopped] = useState(false);
  const current = useRef<AbortController | null>(null);

  // Leaving the page stops a try in flight; nothing is shown for it.
  useEffect(() => () => current.current?.abort(), []);

  const start = async (run: (signal: AbortSignal) => Promise<TryLine[]>) => {
    current.current?.abort();
    const ctl = new AbortController();
    current.current = ctl;
    setTrying(true);
    setErr(null);
    setLines(null);
    setStopped(false);
    try {
      const out = await run(ctl.signal);
      if (!ctl.signal.aborted) setLines(out);
    } catch (e) {
      if (!ctl.signal.aborted) {
        setErr(toErr(e));
      }
    } finally {
      if (current.current === ctl) {
        current.current = null;
        setTrying(false);
      }
    }
  };

  const stop = () => {
    if (!current.current) return;
    setStopped(true);
    current.current.abort();
  };

  return { trying, lines, err, stopped, start, stop };
}
