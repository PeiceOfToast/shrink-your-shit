import { useEffect, useState, type ReactNode } from "react";
import { ApiError } from "../../api/index.ts";
import type { ApiState } from "../../api/useApi.ts";
import { setDirty } from "../../settings/leaveGuard.ts";

export interface FieldProblem {
  field: string;
  message: string;
}

// A save error that names a field shows next to that control; any other error is a sentence under the bar.
export function asProblem(e: unknown): { problem: FieldProblem | null; text: string | null } {
  if (e instanceof ApiError && e.field) return { problem: { field: e.field, message: e.message }, text: null };
  return { problem: null, text: e instanceof Error ? e.message : String(e) };
}

// Keeps an edit copy of what the server returned. Undo puts the server copy back.
export function useDraft<T>(server: T | undefined) {
  const [draft, setDraft] = useState<T | undefined>(server);
  useEffect(() => setDraft(server), [server]);
  const dirty = draft !== undefined && server !== undefined && JSON.stringify(draft) !== JSON.stringify(server);
  return { draft, setDraft, dirty, undo: () => setDraft(server) };
}

// Tells the leave guard that this section has unsaved changes while it is open.
export function useDirty(key: string, dirty: boolean): void {
  useEffect(() => {
    setDirty(key, dirty);
    return () => setDirty(key, false);
  }, [key, dirty]);
}

export function Row({
  id,
  label,
  hint,
  field,
  problem = null,
  children,
}: {
  id?: string;
  label: string;
  hint?: ReactNode;
  field?: string;
  problem?: FieldProblem | null;
  children: ReactNode;
}) {
  const msg = field && problem?.field === field ? problem.message : undefined;
  return (
    <div className="set-row">
      <div className="set-lab">
        <label htmlFor={id}>{label}</label>
        {hint && <div className="hint">{hint}</div>}
      </div>
      <div className="set-ctl">
        {children}
        {msg && (
          <p className="bad set-msg" role="alert">
            {msg}
          </p>
        )}
      </div>
    </div>
  );
}

export function Switch({ on, onChange, label }: { on: boolean; onChange: (on: boolean) => void; label: string }) {
  return (
    <button type="button" className="sw" role="switch" aria-checked={on} aria-label={label} onClick={() => onChange(!on)}>
      <span className={`k ${on ? "on" : ""}`}></span>
      <b>{on ? "On" : "Off"}</b>
    </button>
  );
}

export function Seg<T extends string | number>({
  value,
  options,
  onChange,
  label,
}: {
  value: T;
  options: { value: T; label: string }[];
  onChange: (v: T) => void;
  label: string;
}) {
  return (
    <div className="seg" role="group" aria-label={label}>
      {options.map((o) => (
        <button key={String(o.value)} type="button" aria-pressed={o.value === value} onClick={() => onChange(o.value)}>
          {o.label}
        </button>
      ))}
    </div>
  );
}

export function SaveBar({
  dirty,
  busy,
  saved,
  text,
  onSave,
  onUndo,
}: {
  dirty: boolean;
  busy: boolean;
  saved: boolean;
  text: string | null;
  onSave: () => void;
  onUndo: () => void;
}) {
  if (!dirty) return saved ? <p className="muted sm set-saved" role="status">Saved.</p> : null;
  return (
    <div className="savebar" role="region" aria-label="Unsaved changes">
      <span>Unsaved changes</span>
      {text && <span className="bad" role="alert">{text}</span>}
      <span className="sp"></span>
      <button type="button" className="btn ghost sm" onClick={onUndo} disabled={busy}>
        Undo changes
      </button>
      <button type="button" className="btn primary sm" onClick={onSave} disabled={busy}>
        {busy ? "Saving…" : "Save"}
      </button>
    </div>
  );
}

export function LoadState<T>({ s }: { s: ApiState<T> }) {
  if (s.data !== undefined) return null;
  if (s.error) {
    return (
      <p className="bad">
        Can't load this. <button type="button" className="btn ghost sm" onClick={s.reload}>Try now</button>
      </p>
    );
  }
  return <span className="skel" style={{ height: 80 }} aria-busy="true" />;
}

export const errText = (e: unknown): string => (e instanceof Error ? e.message : String(e));
