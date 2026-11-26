import { type GeneralSettings } from "../../api/index.ts";
import { LoadState, Row, SaveBar, Seg, Switch } from "./parts.tsx";
import { useSettingsForm } from "./useSettingsForm.ts";

export function General() {
  const f = useSettingsForm("general", ["general"]);
  const draft = f.draft;
  if (!draft) return <LoadState s={f.s} />;
  const g = draft.general;
  const set = (patch: Partial<GeneralSettings>) => f.edit({ ...draft, general: { ...g, ...patch } });
  return (
    <>
      <h2 className="set-title">General</h2>
      <Row id="g-name" label="Server name" hint="Shown in the top bar and in messages." field="general.serverName" problem={f.problem}>
        <input id="g-name" className="inp" value={g.serverName} onChange={(e) => set({ serverName: e.target.value })} />
      </Row>
      <Row id="g-lang" label="Language" hint="English is the only language for now.">
        <select id="g-lang" className="sel" value={g.language} onChange={(e) => set({ language: e.target.value as "en" })}>
          <option value="en">English</option>
        </select>
      </Row>
      <Row label="Time format" hint="How times look, for example 14:05 or 2:05 pm.">
        <Seg
          label="Time format"
          value={g.timeFormat}
          options={[
            { value: "24h", label: "24-hour" },
            { value: "12h", label: "12-hour" },
          ]}
          onChange={(timeFormat) => set({ timeFormat })}
        />
      </Row>
      <Row id="g-date" label="Date format" hint="How dates look.">
        <select id="g-date" className="sel" value={g.dateFormat} onChange={(e) => set({ dateFormat: e.target.value as GeneralSettings["dateFormat"] })}>
          <option value="YYYY-MM-DD">2026-11-26</option>
          <option value="DD.MM.YYYY">26.11.2026</option>
          <option value="MM/DD/YYYY">11/26/2026</option>
        </select>
      </Row>
      <Row label="Theme for new browsers" hint="Used by a browser that hasn't picked one. The top bar's sun button changes it for one browser.">
        <Seg
          label="Theme default"
          value={g.theme}
          options={[
            { value: "system", label: "System" },
            { value: "light", label: "Light" },
            { value: "dark", label: "Dark" },
          ]}
          onChange={(theme) => set({ theme })}
        />
      </Row>
      <Row label="Check for updates" hint={g.updateAvailable ? `Version ${g.updateAvailable} is available.` : "Look for new versions of this app."}>
        <Switch label="Check for updates" on={g.checkForUpdates} onChange={(checkForUpdates) => set({ checkForUpdates })} />
      </Row>
      <SaveBar dirty={f.dirty} busy={f.busy} saved={f.saved} text={f.text} onSave={f.save} onUndo={f.undo} />
    </>
  );
}
