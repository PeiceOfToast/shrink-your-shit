import { useEffect, useRef, useState } from "react";
import { Empty, PageHead } from "./PageHead";
import type { NavItem } from "./nav";
import { ACCENTS, DEFAULTS, DENSITIES, MOTIONS, TEXT_SIZES, THEMES, keep, preview, useAppearance, type Appearance } from "./prefs";
import { MOCK } from "./api";
import { Link, onLeave } from "./router";
import { sharesSupported } from "./sharesApi";
import { SettingRow } from "./SettingRow";

// What each page will hold (spec 4-9). Until its step lands it says so and offers nothing that looks like it works.
const WHAT: Record<string, string> = {
  "/": "Health, what is running and what failed, at a glance.",
  "/jobs": "The queue, running jobs and history, with each job's log.",
  "/workflows": "What happens to each file: a simple stack, or a graph for more control.",
  "/libraries": "The folders that are watched, and the workflow each one uses.",
  "/workers": "The computers that convert files, and starting and stopping them.",
};

export function Coming({ item }: { item: NavItem }) {
  return (
    <div className="stack">
      <PageHead icon={item.icon} title={item.label} help={WHAT[item.to]} />
      <Empty icon={item.icon} text={item.label + " comes in a later step."}><Link className="btn" to="/">Go to the Dashboard</Link></Empty>
    </div>
  );
}

// Plugins are planned: say so, and offer nothing that looks like it works (no list, no buttons, no sign-up).
export function PluginsPlanned() {
  return (
    <div className="stack">
      <PageHead icon="i-plugin" title="Plugins" help="Planned. Not available yet." />
      <p>Plugins aren't available yet. When they arrive, they'll let you add your own steps to a workflow, such as a custom ffmpeg command or a script that runs before or after each file.</p>
      <p className="muted">Ideas for plugins are welcome on <a href="https://github.com/PeiceOfToast/shrink-your-shit/issues" target="_blank" rel="noopener">GitHub issues</a>.</p>
    </div>
  );
}

export function NotFound() {
  return (
    <div className="stack">
      <PageHead title="Page not found" />
      <Empty icon="i-alert" text="There is no page at this address."><Link className="btn" to="/">Go to the Dashboard</Link></Empty>
    </div>
  );
}

function Choice<T extends string | number>({ legend, name, value, options, desc, onPick }: {
  legend: string; name: string; value: T; options: [T, string, string?][]; desc?: string; onPick: (v: T) => void;
}) {
  return (
    <SettingRow wide title={<span id={name + "-h"}>{legend}</span>} desc={desc} descId={name + "-d"}>
      <div className="choice prefs" role="radiogroup" aria-labelledby={name + "-h"} aria-describedby={desc ? name + "-d" : undefined}>
        <div className="row">
          {options.map(([v, label, swatch]) => (
            <label key={String(v)} className="radio">
              <input type="radio" name={name} checked={v === value} onChange={() => onPick(v)} />
              {swatch && <span className="swatch" style={{ background: swatch }} aria-hidden="true" />}
              <span>{label}</span>
            </label>
          ))}
        </div>
      </div>
    </SettingRow>
  );
}

// A segmented choice (the design's .seg look): real radios, so the arrow keys move the choice and Tab takes one stop.
// The input is hidden (.sr, still focusable); the label is the segment. .seg.radios is styled apart from .seg button.
function Seg<T extends string | number>({ legend, name, value, options, desc, onPick }: {
  legend: string; name: string; value: T; options: [T, string][]; desc?: string; onPick: (v: T) => void;
}) {
  return (
    <SettingRow title={<span id={name + "-h"}>{legend}</span>} desc={desc} descId={name + "-d"}>
      <div className="seg radios" role="radiogroup" aria-labelledby={name + "-h"} aria-describedby={desc ? name + "-d" : undefined}>
        {options.map(([v, label]) => (
          <label key={String(v)} className={v === value ? "on" : undefined}>
            <input className="sr" type="radio" name={name} checked={v === value} onChange={() => onPick(v)} />
            <span>{label}</span>
          </label>
        ))}
      </div>
    </SettingRow>
  );
}

// Settings > Appearance (DESIGN/v2/THEMES.md): changes show at once and are kept on Save; leaving with unsaved
// changes asks. The advanced theme editor (custom colours, presets, import/export) comes later.
// Settings' tabs (SCREENS V9); the others come with their settings.
export function SettingsTabs({ at }: { at: "general" | "shares" | "appearance" | "processing" | "safety" | "connections" | "notifications" | "variables" | "backup" | "health" | "about" }) {
  const tab = (k: typeof at, label: string) => <Link to={"/settings/" + k} aria-current={at === k ? "page" : undefined}>{label}</Link>;
  // the shares tab shows only when the server has the shares API (404 = not built yet)
  const [shares, setShares] = useState(true);
  useEffect(() => { if (!MOCK) { const f = async () => setShares(await sharesSupported()); f(); } }, []); // eslint-disable-line react-hooks/exhaustive-deps
  // pass 5 #4: the active tab is in view on load and after a change. Scrolls the row sideways only, never the page.
  const nav = useRef<HTMLElement>(null);
  useEffect(() => { const n = nav.current, a = n?.querySelector<HTMLElement>("[aria-current=page]"); if (n && a) n.scrollLeft += a.getBoundingClientRect().left - n.getBoundingClientRect().left - (n.clientWidth - a.offsetWidth) / 2; }, [at, shares]);
  return (
    <nav className="tabs" ref={nav} aria-label="Settings sections">
      {tab("general", "General")}{tab("appearance", "Appearance")}{tab("processing", "Processing")}{tab("safety", "Safety")}{tab("connections", "Connections")}{tab("notifications", "Notifications")}{shares && tab("shares", "Network shares")}{tab("variables", "Variables")}{tab("backup", "Backup")}{tab("health", "Health")}{tab("about", "About")}
    </nav>);
}

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
      <SettingsTabs at="appearance" />
      <div className="card">
        <div className="hd"><h2>Appearance</h2></div>
        <div className="bd stack" style={{ padding: 0, gap: 0 }}>
          {/* descriptions say only what the app does now (checked in tokens.css, shell.css, prefs.ts) */}
          <Choice legend="Theme" name="theme" value={draft.theme} desc="Dark is the default." options={THEMES.map(([v, l]) => [v, l])} onPick={set("theme")} />
          <Choice legend="Accent" name="accent" value={draft.accent} desc="Used for the active item, links and the focus ring." options={ACCENTS} onPick={set("accent")} />
          <Seg legend="Density" name="density" value={draft.density} desc="Compact makes rows 44 px instead of 48." options={DENSITIES} onPick={set("density")} />
          <Seg legend="Text size" name="textSize" value={draft.textSize} desc="Scales the text in the app." options={TEXT_SIZES} onPick={set("textSize")} />
          <Seg legend="Motion" name="motion" value={draft.motion} desc="Reduce motion turns off transitions in the app." options={MOTIONS} onPick={set("motion")} />
        </div>
      </div>
      {/* one Save for this tab, left-aligned; the note says only what is true here */}
      <div className="stack" style={{ gap: 8 }}>
        <p className="muted" role="status" style={{ margin: 0 }}>{said}</p>
        <div className="row" style={{ justifyContent: "flex-start", flexWrap: "wrap", gap: 12 }}>
          <button className="btn primary" type="button" onClick={() => (keep(draft), setSaid(dirty ? "Saved." : "Nothing was changed."))}>Save</button>
          {dirty && <button className="btn" type="button" onClick={() => (setDraft(saved), setSaid("Changes undone."))}>Undo changes</button>}
          <button className="btn ghost" type="button" onClick={() => (setDraft(DEFAULTS), setSaid("Defaults shown. Press Save to keep them."))}>Restore defaults</button>
          <span className="muted">Kept in this browser. Another browser or computer keeps its own.</span>
        </div>
      </div>
    </div>
  );
}
