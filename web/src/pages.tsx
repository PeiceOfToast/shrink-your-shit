import { useEffect, useRef, useState, type ReactNode } from "react";
import { Empty, PageHead } from "./PageHead";
import type { NavItem } from "./nav";
import { ACCENTS, DEFAULTS, DENSITIES, MOTIONS, TEXT_SIZES, THEMES, keep, preview, useAppearance, type Appearance } from "./prefs";
import { Link, onLeave, useHash } from "./router";
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
// Settings' four tabs (U-C): General, Processing, Connections, System. Each tab page has one "Settings" head and one tab row.
export type SettingsTab = "general" | "processing" | "connections" | "system";
const TABS: [SettingsTab, string][] = [["general", "General"], ["processing", "Processing"], ["connections", "Connections"], ["system", "System"]];
export function SettingsTabs({ at }: { at: SettingsTab }) {
  return (
    <nav className="tabs" aria-label="Settings sections">
      {TABS.map(([k, label]) => <Link key={k} to={"/settings/" + k} aria-current={at === k ? "page" : undefined}>{label}</Link>)}
    </nav>);
}

// A tab page: the head, the tab row, then its sections. Arriving on #anchor scrolls that section below the sticky bar
// and moves focus to its heading (no smooth scroll under reduced motion).
export function SettingsPage({ at, children }: { at: SettingsTab; children: ReactNode }) {
  const hash = decodeURIComponent(useHash().slice(1));
  useEffect(() => {
    if (!hash) return;
    // The landing holds while the page settles: a section above the target can grow after it lands (its data loads),
    // so each resize re-lands at once, until 800 ms pass with no change, or the person scrolls or presses a key.
    const USER = ["wheel", "touchstart", "keydown", "pointerdown"] as const;
    const reduced = document.documentElement.dataset.motion === "reduced";
    let tries = 0, look = 0, settle = 0, done = false, sec: HTMLElement | null = null, bodyH = -1;
    const end = () => {
      done = true;
      window.clearTimeout(look);
      window.clearTimeout(settle);
      resized.disconnect();
      USER.forEach((t) => window.removeEventListener(t, end));
    };
    const hold = () => {
      window.clearTimeout(settle);
      settle = window.setTimeout(end, 800);
    };
    const resized = new ResizeObserver(() => {
      const h = document.body.getBoundingClientRect().height;
      if (!sec || done || h === bodyH) return; // the first call only reports the size we started from
      bodyH = h;
      sec.scrollIntoView({ block: "start", behavior: "auto" }); // corrections are instant: no smooth scroll fighting a growing page
      hold();
    });
    // the section can render after its data loads, so look for it for a moment; an h2 takes focus only with tabIndex
    const land = () => {
      if (done) return;
      const s = document.getElementById(hash);
      const h = s?.querySelector<HTMLElement>("h2");
      if (!s || !h) {
        if (tries++ < 40) look = window.setTimeout(land, 50);
        return;
      }
      sec = s;
      s.scrollIntoView({ block: "start", behavior: reduced ? "auto" : "smooth" });
      h.tabIndex = -1;
      h.focus({ preventScroll: true });
      bodyH = document.body.getBoundingClientRect().height;
      resized.observe(document.body);
      hold();
    };
    USER.forEach((t) => window.addEventListener(t, end, { passive: true }));
    land();
    return end;
  }, [hash]);
  return (
    <div className="stack">
      <PageHead icon="i-settings" title="Settings" />
      <SettingsTabs at={at} />
      {children}
    </div>);
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
    <section id="appearance" aria-labelledby="appearance-h" className="stack settings-sec">
      <div className="card">
        <div className="hd"><h2 id="appearance-h">Appearance</h2></div>
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
          <button className="btn primary" type="button" aria-label="Save appearance" onClick={() => (keep(draft), setSaid(dirty ? "Saved." : "Nothing was changed."))}>Save</button>
          {dirty && <button className="btn" type="button" onClick={() => (setDraft(saved), setSaid("Changes undone."))}>Undo changes</button>}
          <button className="btn ghost" type="button" onClick={() => (setDraft(DEFAULTS), setSaid("Defaults shown. Press Save to keep them."))}>Restore defaults</button>
          <span className="muted">Kept in this browser. Another browser or computer keeps its own.</span>
        </div>
      </div>
    </section>
  );
}
