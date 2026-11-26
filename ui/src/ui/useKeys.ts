import { useEffect, useRef } from "react";

// Calls onKey for each key press on the page while mounted. Keeps the latest callback, so callers can pass inline functions.
export function useKeys(onKey: (e: KeyboardEvent) => void): void {
  const ref = useRef(onKey);
  ref.current = onKey;
  useEffect(() => {
    const h = (e: KeyboardEvent) => ref.current(e);
    window.addEventListener("keydown", h);
    return () => window.removeEventListener("keydown", h);
  }, []);
}
