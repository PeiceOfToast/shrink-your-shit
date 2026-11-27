import { useState } from "react";
import { navigate } from "../router.tsx";
import { api } from "../api/index.ts";
import { useApi } from "../api/useApi.ts";
import { useKeys } from "../ui/useKeys.ts";
import "../styles/workflows.css";

type Starter = Awaited<ReturnType<typeof api.getStarters>>[number];

// New workflow (page 05): a dialog over the Workflows list with one kind for now (Video), and a guided card and a Blank card.
// Start opens the guided page (06) for a guided card, or the editor (07) for every other card. Nothing is saved yet.

export function NewWorkflow() {
  const starters = useApi(() => api.getStarters(), []);
  const videos = (starters.data ?? []).filter((s) => s.kind === "video");
  const [picked, setPicked] = useState<string | null>(null);
  const selected = videos.find((s) => s.id === picked) ?? videos[0] ?? null;

  const start = (s: Starter) => {
    navigate(s.guided ? `/workflows/new/guided?starter=${encodeURIComponent(s.id)}` : `/workflows/new/editor?starter=${encodeURIComponent(s.id)}`);
  };

  const move = (by: number) => {
    if (videos.length === 0 || !selected) return;
    const i = videos.findIndex((s) => s.id === selected.id);
    const next = videos[(i + by + videos.length) % videos.length];
    setPicked(next.id);
  };

  useKeys((e) => {
    if (e.key === "Escape") navigate("/workflows");
    else if (e.key === "ArrowRight" || e.key === "ArrowDown") {
      e.preventDefault();
      move(1);
    } else if (e.key === "ArrowLeft" || e.key === "ArrowUp") {
      e.preventDefault();
      move(-1);
    } else if (e.key === "Enter" && selected && !(e.target instanceof HTMLButtonElement)) start(selected);
  });

  return (
    <>
      <div className="scrim" onClick={() => navigate("/workflows")} />
      <div className="dlg wf-dlg" role="dialog" aria-modal="true" aria-labelledby="new-wf-title">
        <div className="wf-dlg-head">
          <h2 id="new-wf-title">New workflow</h2>
          <button type="button" className="wf-close" aria-label="Close" onClick={() => navigate("/workflows")}>×</button>
        </div>

        <p className="wf-kicker wf-kind-label">Video</p>

        {starters.error && !starters.data && <p className="bad">Can't load the starting points. <button type="button" className="btn ghost sm" onClick={starters.reload}>Try now</button></p>}

        <div className="wf-cards" role="listbox" aria-label="Starting points">
          {videos.map((s) => {
            const on = selected?.id === s.id;
            return (
              <button
                key={s.id}
                type="button"
                role="option"
                aria-selected={on}
                className={`wf-card${on ? " on" : ""}`}
                onClick={() => setPicked(s.id)}
                onDoubleClick={() => start(s)}
              >
                <span className="wf-card-name">
                  {s.name}
                  {s.guided && <span className="wf-badge">guided</span>}
                </span>
                <span className="hint">{s.line}</span>
              </button>
            );
          })}
        </div>

        <div className="dlg-actions wf-actions">
          <button type="button" className="btn ghost" onClick={() => navigate("/workflows")}>Cancel</button>
          <button type="button" className="btn primary" disabled={!selected} onClick={() => selected && start(selected)}>Start</button>
        </div>
      </div>
    </>
  );
}
