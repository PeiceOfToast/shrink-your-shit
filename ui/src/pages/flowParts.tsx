import type { ReactNode } from "react";
import { api, ApiError, type GuideChoice } from "../api/index.ts";

// Pieces the guided page (06) and the flow editor (07) share: a setting row, a segmented control, a choice field.

export interface Err {
  field?: string;
  message: string;
}

// The Quality slider's names, positions 1 to 5 (the mock's SLIDER table).
export const QUALITY_NAMES = ["Smallest file", "Smaller", "Balanced", "Better quality", "Best quality"];

// The one-line meaning of a Quality setting: the card's number when the card is used, then the processor CRF.
// `where` says which picture size the number is for.
export function qualityMeaning(value: number, format: string, card: boolean, where: string): string {
  const crf = api.crfFor(value, format, false);
  if (format === "hevc" && card) {
    return `Uses the Intel card (QSV) ${api.crfFor(value, format, true)} when it can, otherwise the processor CRF ${crf} ${where}. Lower means a better picture and a bigger file.`;
  }
  return `Uses the processor CRF ${crf} ${where}. Lower means a better picture and a bigger file.`;
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
export function ChoiceField({ label, value, choices, onChange, id }: { label: string; value: string; choices: GuideChoice[]; onChange: (v: string) => void; id?: string }) {
  const why = choices.filter((c) => !c.available && c.why).map((c) => `${c.label}: ${c.why}`);
  return (
    <Row label={label} hint={why.length ? why.join(" · ") : undefined} id={id}>
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
