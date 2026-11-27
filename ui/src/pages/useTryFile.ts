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
  // The Try button. When a try ends while Stop had focus (Stop unmounts, so focus falls to the body), focus goes back to Try.
  // Focus on anything else is left where the user put it.
  const button = useRef<HTMLButtonElement>(null);
  const wasTrying = useRef(false);

  // Leaving the page stops a try in flight; nothing is shown for it.
  useEffect(() => () => current.current?.abort(), []);

  useEffect(() => {
    if (wasTrying.current && !trying) {
      const focused = document.activeElement;
      if (!focused || focused === document.body) button.current?.focus();
    }
    wasTrying.current = trying;
  }, [trying]);

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

  return { trying, lines, err, stopped, button, start, stop };
}
