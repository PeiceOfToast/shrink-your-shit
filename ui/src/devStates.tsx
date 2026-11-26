import { useEffect, useState } from "react";
import { getForcedState, setForcedState, type ForcedState } from "./api/mock.ts";

// Dev-only state switcher: shows in `npm run dev`, and in a preview with ?states=1. It drives the mock API, not the page.
const STATES: { key: ForcedState; label: string }[] = [
  { key: "none", label: "Normal" },
  { key: "empty", label: "Empty" },
  { key: "loading", label: "Loading" },
  { key: "error", label: "Error" },
  { key: "running", label: "Running" },
];

export function showDevStates(): boolean {
  return import.meta.env.DEV || new URLSearchParams(location.search).has("states");
}

export function DevStates() {
  const [current, setCurrent] = useState<ForcedState>(() => getForcedState());
  useEffect(() => {
    // Reset to normal when the page changes, so a state never leaks across screens.
    const onPop = () => {
      setForcedState("none");
      setCurrent("none");
    };
    window.addEventListener("popstate", onPop);
    return () => window.removeEventListener("popstate", onPop);
  }, []);
  return (
    <div className="dev-states" role="group" aria-label="Dev state">
      <span className="muted sm">state</span>
      {STATES.map((s) => (
        <button
          key={s.key}
          type="button"
          className={`btn sm ${current === s.key ? "primary" : "ghost"}`}
          aria-pressed={current === s.key}
          onClick={() => {
            setForcedState(s.key);
            setCurrent(s.key);
          }}
        >
          {s.label}
        </button>
      ))}
    </div>
  );
}
