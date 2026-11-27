import { useEffect, useState } from "react";
import { getForcedState, setForcedState, type ForcedState } from "./api/mock.ts";

// Dev-only state switcher: shows in `npm run dev`, and in a preview with ?states=1. It drives the mock API, not the page.
const STATES: { key: ForcedState; label: string }[] = [
  { key: "none", label: "Normal" },
  { key: "empty", label: "Empty" },
  { key: "loading", label: "Loading" },
  { key: "error", label: "Error" },
  { key: "running", label: "Running" },
  { key: "fresh", label: "Fresh install" },
  { key: "setup", label: "First run" },
  { key: "starting", label: "Starting up" },
];

// ?states=0 hides the switcher; in dev it shows, or in a preview with ?states=1.
export function showDevStates(): boolean {
  const v = new URLSearchParams(location.search).get("states");
  if (v === "0") return false;
  return import.meta.env.DEV || v !== null;
}

export function DevStates() {
  const [current, setCurrent] = useState<ForcedState>(() => getForcedState());
  // Collapsed to a small chip in the bottom-left corner, clear of the panel footers, until opened.
  const [open, setOpen] = useState(false);
  useEffect(() => {
    // Reset to normal when the page changes, so a state never leaks across screens.
    const onPop = () => {
      setForcedState("none");
      setCurrent("none");
    };
    window.addEventListener("popstate", onPop);
    return () => window.removeEventListener("popstate", onPop);
  }, []);
  if (!open) {
    return (
      <button type="button" className="btn sm ghost dev-chip" aria-expanded={false} onClick={() => setOpen(true)}>
        states
      </button>
    );
  }
  return (
    <div className="dev-states" role="group" aria-label="Dev state">
      <button type="button" className="btn sm ghost" aria-expanded={true} onClick={() => setOpen(false)}>
        hide
      </button>
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
