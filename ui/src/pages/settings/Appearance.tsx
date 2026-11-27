import { useEffect, useRef } from "react";
import { type AppearanceSettings } from "../../api/index.ts";
import { PRESETS, accentVars } from "../../format/accent.ts";
import { applyAppearance } from "../../shell/theme.ts";
import { LoadState, Row, SaveBar, Seg } from "./parts.tsx";
import { useSettingsForm } from "./useSettingsForm.ts";

const DEFAULT_ACCENT = "#22C55E";

// The Green swatch is the default, so it stores null.
function sameColour(a: string | null, b: string): boolean {
  return (a ?? DEFAULT_ACCENT).toLowerCase() === b.toLowerCase();
}

// Settings > Appearance: one theme and one accent for the install, applied in every browser that signs in.
// The page follows the draft as you pick, and goes back to the saved look when you leave without saving.
export function Appearance() {
  const f = useSettingsForm("appearance", ["appearance"]);
  const draft = f.draft;
  const saved = f.s.data?.appearance;
  const a = draft?.appearance;
  const savedRef = useRef(saved);
  savedRef.current = saved;
  // The page follows the draft; leaving it without saving puts back the saved look.
  useEffect(() => {
    if (a) applyAppearance(a);
  }, [a?.theme, a?.accent]);
  useEffect(() => () => {
    if (savedRef.current) applyAppearance(savedRef.current);
  }, []);
  if (!draft || !a) return <LoadState s={f.s} />;
  const set = (patch: Partial<AppearanceSettings>) => f.edit({ ...draft, appearance: { ...a, ...patch } });
  const custom = accentVars(a.accent);
  const previewDark = custom?.vars["--acc-dark"] ?? DEFAULT_ACCENT;
  const previewLight = custom?.vars["--acc-light"] ?? "#15803D";
  const onDark = custom?.vars["--on-dark"] ?? "#06130B";
  const onLight = custom?.vars["--on-light"] ?? "#FFFFFF";
  const movedDark = custom?.movedDark ?? false;
  const movedLight = custom?.movedLight ?? false;
  const current = a.accent ?? DEFAULT_ACCENT;
  return (
    <>
      <h2 className="set-title">Appearance</h2>
      <p className="hint">Applies in every browser that signs in. The top bar's sun button changes the theme too.</p>
      <Row label="Theme" hint="Match system follows the browser or the computer.">
        <Seg
          label="Theme"
          value={a.theme}
          options={[
            { value: "system", label: "Match system" },
            { value: "dark", label: "Dark" },
            { value: "light", label: "Light" },
          ]}
          onChange={(theme) => set({ theme })}
        />
      </Row>
      <Row label="Accent colour" hint="Colours buttons, links, the selected tab, chosen segments and the on and off switches. Done, warning and error colours never change, so a status always means the same thing.">
        <div className="ap-sw" role="group" aria-label="Accent colour">
          {PRESETS.map((p) => (
            <button
              key={p.name}
              type="button"
              aria-label={p.name}
              aria-pressed={sameColour(a.accent, p.hex)}
              title={p.name}
              style={{ background: p.hex }}
              onClick={() => set({ accent: p.hex === DEFAULT_ACCENT ? null : p.hex })}
            />
          ))}
        </div>
        <div className="set-own">
          <label>
            Your own
            <input type="color" aria-label="Your own colour" value={current} onChange={(e) => set({ accent: e.target.value })} />
          </label>
          <input
            className="inp"
            aria-label="Hex colour"
            value={current}
            onChange={(e) => set({ accent: e.target.value.trim() === "" ? null : e.target.value.trim() })}
          />
        </div>
        {f.problem?.field === "appearance.accent" && <p className="field-problem">{f.problem.message}</p>}
        {(movedDark || movedLight) && (
          <p className="hint">
            {movedDark && "Made a little lighter in the dark theme so text stays readable. "}
            {movedLight && "Made a little darker in the light theme so text stays readable."}
          </p>
        )}
      </Row>
      <Row label="Preview" hint="A dark and a light sample, and the whole page follows as you pick. Undo changes puts it back.">
        <div className="ap-preview">
          <div className="ap-sample ap-dark" style={{ ["--ap-acc" as string]: previewDark, ["--ap-on" as string]: onDark }}>
            <span className="ap-btn">Save</span> <a href="#preview">A link</a>
            <div className="ap-tabs"><span className="on">Tab</span><span>Other</span></div>
            <div className="ap-chips"><span className="chip ok">Done</span><span className="chip warn">Warning</span><span className="chip bad">Error</span></div>
          </div>
          <div className="ap-sample ap-light" style={{ ["--ap-acc" as string]: previewLight, ["--ap-on" as string]: onLight }}>
            <span className="ap-btn">Save</span> <a href="#preview">A link</a>
            <div className="ap-tabs"><span className="on">Tab</span><span>Other</span></div>
            <div className="ap-chips"><span className="chip ok">Done</span><span className="chip warn">Warning</span><span className="chip bad">Error</span></div>
          </div>
        </div>
        {custom && <span className="hint">Accent {a.accent}</span>}
        <div style={{ marginTop: 8 }}>
          <button type="button" className="btn" onClick={() => set({ theme: "system", accent: null })}>Reset to default</button>
        </div>
      </Row>
      <SaveBar dirty={f.dirty} busy={f.busy} saved={f.saved} text={f.text} onSave={f.save} onUndo={f.undo} />
    </>
  );
}
