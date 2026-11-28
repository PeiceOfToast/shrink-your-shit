import type { ReactNode } from "react";
import { api, ApiError, sampleOnly, type GuideChoice } from "../api/index.ts";
import type { BitrateLadder } from "../api/flowTypes.ts";
import type { GpuFamily } from "../api/types.ts";
import { useApi } from "../api/useApi.ts";
import { cardFamilies } from "../format/quality.ts";

// Pieces the guided page (06) and the flow editor (07) share: a setting row, a segmented control, a choice field.

export interface Err {
  field?: string;
  message: string;
}

// The Quality slider's names, positions 1 to 5 (the ladder's slider).
export const QUALITY_NAMES = ["Smallest file", "Smaller", "Balanced", "Better quality", "Best quality"];

// The server's bitrate ladder, which carries the Quality slider's numbers. null until it has loaded.
export function useBitrateLadder(): BitrateLadder | null {
  return useApi(() => api.getBitrateLadder(), []).data ?? null;
}

// The graphics-card families the workers have, for a Quality line. `useGpu` false (the editor's None, or Guided's
// no-card answer) gives none, so the line shows only the processor CRF. null while the workers load.
export function useCardFamilies(useGpu: boolean): GpuFamily[] {
  const workers = useApi(() => api.getWorkers(), []).data;
  // The workers list has no server route yet; the sample cards would add made-up GPU lines.
  if (!useGpu || !workers || sampleOnly()) return [];
  return cardFamilies(workers);
}

export const toErr =(e: unknown): Err => (e instanceof ApiError ? { field: e.field, message: e.message } : { message: e instanceof Error ? e.message : String(e) });

// `full` takes the whole line of a two-column card; short fields pair up beside each other.
export function Row({ label, hint, children, id, full }: { label: string; hint?: ReactNode; children: ReactNode; id?: string; full?: boolean }) {
  return (
    <div className={`wf-row${full ? " wf-full" : ""}`} id={id}>
      <span className="wf-row-label">{label}</span>
      {children}
      {hint && <p className="hint wf-hint">{hint}</p>}
    </div>
  );
}

export interface SegOption<T extends string> {
  value: T;
  label: string;
  disabled?: boolean;
  title?: string;
}

export function Seg<T extends string>({ label, value, options, onChange }: { label: string; value: T; options: SegOption<T>[]; onChange: (v: T) => void }) {
  return (
    <div className="seg wf-seg" role="group" aria-label={label}>
      {options.map((o) => (
        <button key={o.value} type="button" aria-pressed={value === o.value} disabled={o.disabled} title={o.title} onClick={() => onChange(o.value)}>
          {o.label}
        </button>
      ))}
    </div>
  );
}

// A choice from the guide or the step definitions: a segmented control for up to four, a list beyond that.
// A choice that can't work here is greyed with the server's reason.
// `hint` replaces the default line, which names each greyed choice's reason.
export function ChoiceField({ label, value, choices, onChange, id, full, hint }: { label: string; value: string; choices: GuideChoice[]; onChange: (v: string) => void; id?: string; full?: boolean; hint?: string }) {
  const why = choices.filter((c) => !c.available && c.why).map((c) => `${c.label}: ${c.why}`);
  return (
    <Row label={label} hint={hint ?? (why.length ? why.join(" · ") : undefined)} id={id} full={full}>
      {choices.length <= 4 ? (
        <Seg
          label={label}
          value={value}
          options={choices.map((c) => ({ value: c.value, label: c.label, disabled: !c.available, title: c.why }))}
          onChange={onChange}
        />
      ) : (
        <select className="sel" aria-label={label} value={value} onChange={(e) => onChange(e.target.value)}>
          {choices.map((c) => (
            <option key={c.value} value={c.value} disabled={!c.available}>
              {c.label}
            </option>
          ))}
        </select>
      )}
    </Row>
  );
}
