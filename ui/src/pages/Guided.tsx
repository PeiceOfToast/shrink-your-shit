import { useEffect, useRef, useState } from "react";
import { navigate } from "../router.tsx";
import { api, type Answers, type GuideDoc, type GuideField, type GuidePart, type GuidePreview, type TryLine } from "../api/index.ts";
import { useApi } from "../api/useApi.ts";
import { ChoiceField, QUALITY_NAMES, Row, toErr, type Err } from "./flowParts.tsx";

// Guided builder (06): a short set of questions that build the flow. Nothing reaches the library until
// "Save and use it". The three panels are the parts index, the part cards and "What this does".

const NEW = "new";

// A chip is on when every key in its `on` answers matches.
const chipOn = (on: Answers, answers: Answers) => Object.entries(on).every(([k, v]) => answers[k] === v);

function choiceLabel(field: GuideField, value: unknown): string {
  return field.choices?.find((c) => c.value === String(value))?.label ?? String(value ?? "");
}

// The part's current value as text, for the index: "HEVC · quality 3 · Keep the size".
function partValue(part: GuidePart, answers: Answers): string {
  return part.fields
    .map((f) => {
      if (f.type === "choice") return choiceLabel(f, answers[f.key]);
      if (f.type === "number") return `${f.label.toLowerCase()} ${answers[f.key]}`;
      if (f.type === "yesno") return answers[f.key] ? "Yes" : "No";
      if (f.type === "languages") return (answers[f.key] as string[] | undefined)?.join(", ") || "any";
      return String(answers[f.key] ?? "");
    })
    .filter(Boolean)
    .join(" · ");
}

interface Loaded {
  guide: GuideDoc;
  answers: Answers;
  fitsGuided: boolean;
  name: string;
}

export function Guided({ id }: { id: string }) {
  const loaded = useApi<Loaded>(
    async () => {
      const [guide, a, rows] = await Promise.all([api.getGuide(), api.getGuideAnswers(id), api.listWorkflows()]);
      const row = rows.find((r) => r.id === id);
      return { guide, answers: a.answers, fitsGuided: a.fitsGuided, name: row?.name ?? "New workflow" };
    },
    [id],
  );

  if (loaded.error && !loaded.data) {
    return <p className="bad">Can't load this workflow. <button type="button" className="btn ghost sm" onClick={loaded.reload}>Try now</button></p>;
  }
  if (!loaded.data) return <p className="muted">Loading…</p>;
  return <GuidedForm key={id} id={id} initial={loaded.data} />;
}

function GuidedForm({ id, initial }: { id: string; initial: Loaded }) {
  const [answers, setAnswers] = useState<Answers>(initial.answers);
  const [baseline, setBaseline] = useState(() => JSON.stringify(initial.answers));
  const [preview, setPreview] = useState<GuidePreview | null>(null);
  const [previewErr, setPreviewErr] = useState<Err | null>(null);
  const [active, setActive] = useState<string>(initial.guide.parts[0]?.id ?? "");
  const [busy, setBusy] = useState<"" | "draft" | "use" | "open">("");
  const [err, setErr] = useState<Err | null>(null);
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [tryFile, setTryFile] = useState("");
  const [tryLines, setTryLines] = useState<TryLine[] | null>(null);
  const [tryErr, setTryErr] = useState<Err | null>(null);
  const [draftId, setDraftId] = useState(id);
  const seq = useRef(0);

  const dirty = JSON.stringify(answers) !== baseline;
  const isNew = draftId === NEW;

  // "What this does" follows the answers, 300 ms after the last change. Only the newest answer is shown.
  useEffect(() => {
    const mine = ++seq.current;
    const t = setTimeout(() => {
      api
        .guidePreview(answers)
        .then((p) => {
          if (mine !== seq.current) return;
          setPreview(p);
          setPreviewErr(null);
        })
        .catch((e) => {
          if (mine !== seq.current) return;
          setPreviewErr(toErr(e));
        });
    }, 300);
    return () => clearTimeout(t);
  }, [answers]);

  const setAnswer = (key: string, value: unknown) => setAnswers((a) => ({ ...a, [key]: value }));

  const toggleChip = (chip: GuideDoc["chips"][number]) => {
    if (chip.always || !chip.available) return;
    const on = chipOn(chip.on, answers);
    setAnswers((a) => ({ ...a, ...(on ? chip.off : chip.on) }));
  };

  const saveDraft = async (): Promise<string> => {
    if (isNew) {
      const created = await api.createDraft({ name: initial.name, answers });
      setDraftId(created.id);
      setBaseline(JSON.stringify(answers));
      return created.id;
    }
    await api.putAnswers(draftId, answers);
    setBaseline(JSON.stringify(answers));
    return draftId;
  };

  const onSaveDraft = async () => {
    setBusy("draft");
    setErr(null);
    try {
      const newId = await saveDraft();
      if (isNew) navigate(`/workflows/${newId}/guided`);
    } catch (e) {
      setErr(toErr(e));
    } finally {
      setBusy("");
    }
  };

  const onSaveAndUse = async () => {
    if (!preview) return;
    if (preview.problems.length > 0) {
      setErr({ message: preview.problems[0].message });
      return;
    }
    setBusy("use");
    setErr(null);
    try {
      const useId = isNew ? await api.createDraft({ name: initial.name, answers }).then((c) => c.id) : draftId;
      await api.publishFlow(useId, preview.flow);
      navigate(`/workflows/${useId}`);
    } catch (e) {
      setErr(toErr(e));
    } finally {
      setBusy("");
    }
  };

  const onOpenEditor = async () => {
    if (dirty) {
      setConfirmOpen(true);
      return;
    }
    navigate(`/workflows/${draftId}`);
  };

  const onSaveAndOpen = async () => {
    setBusy("open");
    setErr(null);
    try {
      const newId = await saveDraft();
      navigate(`/workflows/${newId}`);
    } catch (e) {
      setErr(toErr(e));
      setBusy("");
    }
  };

  const onTry = async () => {
    if (!preview) return;
    setTryErr(null);
    try {
      const r = await api.tryFlow(preview.flow, tryFile);
      setTryLines(r.lines);
    } catch (e) {
      setTryErr(toErr(e));
      setTryLines(null);
    }
  };

  const problemOpen = (field?: string) => {
    const part = initial.guide.parts.find((p) => p.fields.some((f) => f.key === field));
    if (!part) return;
    setActive(part.id);
    document.getElementById(`wf-part-${part.id}`)?.scrollIntoView({ block: "start" });
  };

  return (
    <>
      <div className="wf-head">
        <div>
          <h1>{initial.name}</h1>
          <span className="muted sm">
            Video · guided{dirty ? <span className="wf-dirty" aria-label="Unsaved changes" /> : null}
          </span>
        </div>
        <span className="sp" />
        <button type="button" className="btn" onClick={onOpenEditor}>Open in the editor</button>
        <button type="button" className="btn" disabled={busy !== ""} onClick={onSaveDraft}>Save as a draft</button>
        <button type="button" className="btn primary" disabled={busy !== "" || !preview || preview.problems.length > 0} onClick={onSaveAndUse}>
          Save and use it
        </button>
      </div>

      {confirmOpen && (
        <div className="wf-confirm" role="alertdialog" aria-label="Save before opening the editor">
          <span>Save your answers as a draft first?</span>
          <button type="button" className="btn primary sm" disabled={busy !== ""} onClick={onSaveAndOpen}>Save and open</button>
          <button type="button" className="btn ghost sm" onClick={() => setConfirmOpen(false)}>Cancel</button>
        </div>
      )}

      {err && <p className="bad" role="alert">{err.message}</p>}

      <div className="wf-guided">
        <nav aria-label="Parts">
          <ol className="wf-index" style={{ listStyle: "none" }}>
            {initial.guide.parts.map((p) => (
              <li key={p.id}>
                <button
                  type="button"
                  aria-current={active === p.id ? "true" : undefined}
                  onClick={() => {
                    setActive(p.id);
                    document.getElementById(`wf-part-${p.id}`)?.scrollIntoView({ block: "start" });
                  }}
                >
                  <span>{p.label}</span>
                  <span className="wf-index-value">{partValue(p, answers)}</span>
                </button>
              </li>
            ))}
          </ol>
        </nav>

        <div className="wf-parts">
          <ChipRow chips={initial.guide.chips} answers={answers} onToggle={toggleChip} />
          {initial.guide.parts.map((p) => (
            <section key={p.id} id={`wf-part-${p.id}`} className="card wf-part" aria-labelledby={`wf-h-${p.id}`}>
              <h2 id={`wf-h-${p.id}`}>{p.label}</h2>
              {p.fields.map((f) => (
                <FieldRow key={f.key} field={f} value={answers[f.key]} languages={initial.guide.languages} onChange={(v) => setAnswer(f.key, v)} />
              ))}
            </section>
          ))}
        </div>

        <aside className="wf-side card" aria-label="What this does">
          <h2 style={{ margin: 0, fontSize: 15 }}>What this does</h2>
          {previewErr && <p className="bad">{previewErr.message}</p>}
          {!preview && !previewErr && <p className="muted sm">Working it out…</p>}
          {preview && (
            <>
              <ol>
                {preview.sentences.map((s) => (
                  <li key={s}>{s}</li>
                ))}
              </ol>
              {preview.problems.length > 0 ? (
                <div>
                  <span className="pill bad">{preview.problems.length} {preview.problems.length === 1 ? "problem" : "problems"}</span>
                  <ul className="wf-try-lines">
                    {preview.problems.map((p) => (
                      <li key={p.code + p.message}>
                        {p.message}{" "}
                        {p.field && (
                          <button type="button" className="btn ghost sm" onClick={() => problemOpen(p.field)}>Open the part</button>
                        )}
                      </li>
                    ))}
                  </ul>
                </div>
              ) : (
                <span className="pill ok">No problems</span>
              )}
            </>
          )}

          <div>
            <h3 style={{ margin: "0 0 6px", fontSize: 14 }}>Try on one file</h3>
            <div style={{ display: "flex", gap: 8 }}>
              <input className="inp" aria-label="File to try" placeholder="File name" value={tryFile} onChange={(e) => setTryFile(e.target.value)} style={{ flex: 1, minWidth: 0 }} />
              <button type="button" className="btn sm" disabled={!preview} onClick={onTry}>Try</button>
            </div>
            {tryErr && <p className="bad">{tryErr.message}</p>}
            {tryLines && (
              <ul className="wf-try-lines">
                {tryLines.map((l) => (
                  <li key={l.stepId + l.text}>{l.text}</li>
                ))}
              </ul>
            )}
          </div>
        </aside>
      </div>
    </>
  );
}

function ChipRow({ chips, answers, onToggle }: { chips: GuideDoc["chips"]; answers: Answers; onToggle: (c: GuideDoc["chips"][number]) => void }) {
  return (
    <div className="wf-chips" role="group" aria-label="Quick choices">
      {chips.map((c) => (
        <button
          key={c.id}
          type="button"
          className="wf-chip"
          aria-pressed={c.always ? true : chipOn(c.on, answers)}
          disabled={c.always || !c.available}
          title={c.why}
          onClick={() => onToggle(c)}
        >
          <span className="k" aria-hidden="true" />
          <span>
            {c.label}
            {c.always ? " (always on)" : ""}
            {c.why && !c.available ? <span className="hint" style={{ display: "block" }}>{c.why}</span> : null}
          </span>
        </button>
      ))}
    </div>
  );
}

function FieldRow({ field, value, languages, onChange }: { field: GuideField; value: unknown; languages: string[]; onChange: (v: unknown) => void }) {
  if (field.type === "choice" && field.choices) {
    return <ChoiceField label={field.label} value={String(value ?? "")} choices={field.choices} onChange={onChange} />;
  }
  if (field.type === "number") {
    const n = Number(value ?? 3);
    return (
      <Row label={field.label}>
        <div className="wf-quality">
          <input type="range" min={1} max={5} step={1} value={n} aria-label={field.label} aria-valuetext={`${n}`} onChange={(e) => onChange(Number(e.target.value))} />
          <output>{field.label === "Quality" ? `${n} · ${QUALITY_NAMES[n - 1]}` : n}</output>
        </div>
      </Row>
    );
  }
  if (field.type === "yesno") {
    return (
      <Row label={field.label}>
        <label style={{ display: "inline-flex", gap: 8, alignItems: "center" }}>
          <input type="checkbox" checked={Boolean(value)} onChange={(e) => onChange(e.target.checked)} />
          {Boolean(value) ? "Yes" : "No"}
        </label>
      </Row>
    );
  }
  if (field.type === "languages") {
    const list = (value as string[] | undefined) ?? [];
    return (
      <Row label={field.label} hint={`Codes from: ${languages.join(", ")}. Separate with commas.`}>
        <input
          className="inp"
          aria-label={field.label}
          value={list.join(", ")}
          onChange={(e) => onChange(e.target.value.split(",").map((s) => s.trim()).filter(Boolean))}
        />
      </Row>
    );
  }
  return (
    <Row label={field.label}>
      <input className="inp" aria-label={field.label} value={String(value ?? "")} onChange={(e) => onChange(e.target.value)} />
    </Row>
  );
}
