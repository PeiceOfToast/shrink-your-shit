import { useState } from "react";
import { api, type SettingsData, type SettingsPatch } from "../../api/index.ts";
import { useApi } from "../../api/useApi.ts";
import { asProblem, useDirty, useDraft, type FieldProblem } from "./parts.tsx";

export type SettingsGroup = "general" | "appearance" | "processing" | "logs";

const READ_ONLY = new Set(["updateAvailable", "trashPath"]);

// Only the changed fields, nested by group.
export function settingsPatch(server: SettingsData, draft: SettingsData, groups: SettingsGroup[]): SettingsPatch {
  const out: Record<string, Record<string, unknown>> = {};
  for (const g of groups) {
    const a = server[g] as unknown as Record<string, unknown>;
    const b = draft[g] as unknown as Record<string, unknown>;
    for (const k of Object.keys(b)) {
      if (READ_ONLY.has(k)) continue;
      if (JSON.stringify(a[k]) !== JSON.stringify(b[k])) (out[g] ??= {})[k] = b[k];
    }
  }
  return out as SettingsPatch;
}

// The shared load, edit and save for the settings that GET /api/v2/settings returns.
export function useSettingsForm(key: string, groups: SettingsGroup[]) {
  const s = useApi(() => api.getSettings(), []);
  const d = useDraft(s.data);
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState<FieldProblem | null>(null);
  const [text, setText] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);
  useDirty(key, d.dirty);

  const edit = (next: SettingsData) => {
    setSaved(false);
    setProblem(null);
    setText(null);
    d.setDraft(next);
  };
  const undo = () => {
    setProblem(null);
    setText(null);
    d.undo();
  };
  const save = () => {
    const server = s.data;
    if (!server || !d.draft) return;
    setBusy(true);
    setProblem(null);
    setText(null);
    setSaved(false);
    api.patchSettings(settingsPatch(server, d.draft, groups)).then(
      () => {
        setSaved(true);
        s.reload();
      },
      (e: unknown) => {
        const r = asProblem(e);
        setProblem(r.problem);
        setText(r.text);
      },
    ).finally(() => setBusy(false));
  };
  return { s, draft: d.draft, dirty: d.dirty, busy, saved, problem, text, edit, undo, save };
}
