import { useEffect, useRef, useState } from "react";
import { PageHead } from "./PageHead";
import type { NavItem } from "./nav";
import { ACCENTS, DEFAULTS, DENSITIES, MOTIONS, TEXT_SIZES, THEMES, keep, preview, useAppearance, type Appearance } from "./prefs";
import { Link, onLeave } from "./router";

// What each page will hold (spec 4-9). Until its step lands it says so and offers nothing that looks like it works.
const WHAT: Record<string, string> = {
  "/": "Health, what is running and what failed, at a glance.",
  "/jobs": "The queue, running jobs and history, with each job's log.",
  "/workflows": "What happens to each file: a simple stack, or a graph for more control.",
  "/libraries": "The folders that are watched, and the workflow each one uses.",
  "/plugins": "Installing and updating plugins.",
  "/workers": "The computers that convert files, and starting and stopping them.",
};

export function Coming({ item }: { item: NavItem }) {
  return (
    <div className="stack">
      <PageHead icon={item.icon} title={item.label} help={WHAT[item.to]} />
      <div className="card"><div className="bd">
        <p>This page comes in a later step of the new interface. Until then, use <a href="/">the current interface</a>.</p>
      </div></div>
    </div>
  );
}

export function NotFound() {
  return (
    <div className="stack">
      <PageHead title="Page not found" />
      <div className="card"><div className="bd"><p>There is no page at this address. <Link to="/">Go to the Dashboard</Link>.</p></div></div>
    </div>
  );
}

function Choice<T extends string | number>({ legend, name, value, options, onPick }: {
  legend: string; name: string; value: T; options: [T, string, string?][]; onPick: (v: T) => void;
}) {
  return (
    <fieldset className="field choice">
      <legend className="lab">{legend}</legend>
      <div className="row">
        {options.map(([v, label, swatch]) => (
          <label key={String(v)} className="radio">
            <input type="radio" name={name} checked={v === value} onChange={() => onPick(v)} />
            {swatch && <span className="swatch" style={{ background: swatch }} aria-hidden="true" />}
            <span>{label}</span>
          </label>
        ))}
      </div>
    </fieldset>
  );
}

// Settings > Appearance (DESIGN/v2/THEMES.md): changes show at once and are kept on Save; leaving with unsaved
// changes asks. The advanced theme editor (custom colours, presets, import/export) comes later.
export function AppearanceSettings() {
  const saved = useAppearance();
  const [draft, setDraft] = useState<Appearance>(saved);
  const [said, setSaid] = useState("");
  const dirty = JSON.stringify(draft) !== JSON.stringify(saved);
  const now = useRef({ draft, saved, dirty });
  now.current = { draft, saved, dirty };
  // Saved changed from outside (the top bar's Theme menu): take the fields that changed into the draft and keep the
  // user's own unsaved edits, so Save never undoes a choice made elsewhere (shell QA F2).
  const before = useRef(saved);
  useEffect(() => {
    const was = before.current;
    before.current = saved;
    setDraft((d) => ({ ...d, ...Object.fromEntries((Object.keys(saved) as (keyof Appearance)[]).filter((k) => saved[k] !== was[k]).map((k) => [k, saved[k]])) }));
  }, [saved]);
  useEffect(() => preview(draft), [draft]);
  useEffect(() => {
    onLeave(() => {
      const { draft, saved, dirty } = now.current;
      if (!dirty) return;
      if (window.confirm("Keep these colours?\n\nOK keeps them. Cancel goes back to what you had.")) keep(draft);
      else preview(saved);
    });
    return () => onLeave(null);
  }, []);
  const set = <K extends keyof Appearance>(k: K) => (v: Appearance[K]) => (setSaid(""), setDraft((d) => ({ ...d, [k]: v })));
  return (
    <div className="stack">
      <PageHead icon="i-settings" title="Settings" />
      <nav className="tabs" aria-label="Settings sections"><Link to="/settings/appearance" aria-current="page">Appearance</Link></nav>
      <div className="card">
        <div className="hd"><h2>Appearance</h2></div>
        <div className="bd stack">
          <Choice legend="Theme" name="theme" value={draft.theme} options={THEMES.map(([v, l]) => [v, l])} onPick={set("theme")} />
          <Choice legend="Accent" name="accent" value={draft.accent} options={ACCENTS} onPick={set("accent")} />
          <Choice legend="Density" name="density" value={draft.density} options={DENSITIES} onPick={set("density")} />
          <Choice legend="Text size" name="textSize" value={draft.textSize} options={TEXT_SIZES} onPick={set("textSize")} />
          <Choice legend="Motion" name="motion" value={draft.motion} options={MOTIONS} onPick={set("motion")} />
          <p className="muted sm">Kept in this browser. Another browser or computer keeps its own.</p>
          <div className="row">
            <button className="btn primary" type="button" onClick={() => (keep(draft), setSaid(dirty ? "Saved." : "Nothing was changed."))}>Save</button>
            {dirty && <button className="btn" type="button" onClick={() => (setDraft(saved), setSaid("Changes undone."))}>Undo changes</button>}
            <button className="btn ghost" type="button" onClick={() => (setDraft(DEFAULTS), setSaid("Defaults shown. Press Save to keep them."))}>Restore defaults</button>
            <span className="muted" role="status">{said}</span>
          </div>
        </div>
      </div>
    </div>
  );
}
