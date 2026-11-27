import { useEffect, useRef, useState } from "react";
import { navigate, useLocation } from "../router.tsx";
import { api, type Answers, type FlowDisabled, type GuideChoice, type GuideDoc, type GuideField, type GuidePart, type GuidePreview } from "../api/index.ts";
import { languageName } from "../format/language.ts";
import { useApi } from "../api/useApi.ts";
import { ChoiceField, QUALITY_NAMES, Row, toErr, useBitrateLadder, useCardFamilies, type Err } from "./flowParts.tsx";
import { useTryFile } from "./useTryFile.ts";
import { qualityMeaning } from "../format/quality.ts";
import { fileTypeHint } from "../format/fileType.ts";

// Guided builder (06): a short set of questions that build the flow. Nothing reaches the library until
// "Save and use it". The three panels are the parts index, the part cards and "What this does".

const NEW = "new";

// A chip is on when every key in its `on` answers matches.
const chipOn = (on: Answers, answers: Answers) => Object.entries(on).every(([k, v]) => answers[k] === v);

function choiceLabel(field: GuideField, value: unknown): string {
  return field.choices?.find((c) => c.value === String(value))?.label ?? String(value ?? "");
}

// The part's value as short text for the index and the card's top right: "HEVC · 3 · eng ger".
function partValue(part: GuidePart, answers: Answers): string {
  return part.fields
    .filter((f) => !f.more && (f.type !== "folder" || answers.original === "one_folder") && (f.key !== "subtitleLanguages" || answers.subtitles === "my_languages"))
    .map((f) => {
      if (f.type === "choice") return choiceLabel(f, answers[f.key]);
      if (f.type === "number") return String(answers[f.key] ?? "");
      if (f.type === "yesno") return answers[f.key] ? "yes" : "no";
      if (f.type === "languages") return (answers[f.key] as string[] | undefined)?.map(languageName).join(" and ") || "all";
      return String(answers[f.key] ?? "");
    })
    .filter(Boolean)
    .join(" · ");
}


interface Loaded {
  guide: GuideDoc;
  answers: Answers;
  fitsGuided: boolean;
  // The draft revision the page read. Every save sends it back as baseRev, so an editor save since
  // the page opened is refused instead of silently overwritten.
  rev: number;
  name: string;
}

export function Guided({ id }: { id: string }) {
  // A new workflow picked from a starter (?starter=) starts from that starter's answers over the defaults.
  const starter = useLocation().query.get("starter");
  const loaded = useApi<Loaded>(
    async () => {
      const [guide, a, rows, st] = await Promise.all([
        api.getGuide(),
        api.getGuideAnswers(id),
        api.listWorkflows(),
        id === "new" && starter ? api.getStarter(starter) : null,
      ]);
      const row = rows.find((r) => r.id === id);
      const answers = st ? { ...a.answers, ...st.answers } : a.answers;
      return { guide, answers, fitsGuided: a.fitsGuided, rev: a.rev, name: row?.name ?? "New workflow" };
    },
    [id, starter],
  );

  if (loaded.error && !loaded.data) {
    // A starter that doesn't exist says so (the server's not_found message); other failures use the generic line.
    if (loaded.code === "not_found") return <p className="bad">{loaded.error}</p>;
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
  const tryRun = useTryFile();
  const [draftId, setDraftId] = useState(id);
  const [rev, setRev] = useState(initial.rev);
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
      setRev(created.rev);
      setBaseline(JSON.stringify(answers));
      return created.id;
    }
    // The revision this page read goes back with the answers. If the editor saved the draft since,
    // the server refuses with changed_since and the page shows that message (as the editor does).
    const saved = await api.putAnswers(draftId, answers, rev);
    setRev(saved.rev);
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

  const onTry = () => {
    if (!preview) return;
    void tryRun.start((signal) => api.tryFlow(preview.flow, tryFile, signal).then((r) => r.lines));
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
          <section className="card wf-quick" aria-labelledby="wf-quick-h">
            <h2 id="wf-quick-h">What should it do?</h2>
            <ChipRow chips={initial.guide.chips} answers={answers} onToggle={toggleChip} />
          </section>
          {initial.guide.parts.map((p) => (
            <PartCard key={p.id} part={p} answers={answers} languages={initial.guide.languages} setAnswer={setAnswer} disabled={preview?.disabled ?? []} />
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
              <button ref={tryRun.button} type="button" className="btn sm" disabled={!preview || tryRun.trying} onClick={onTry}>
                {tryRun.trying ? "Trying… about a minute" : "Try"}
              </button>
              {tryRun.trying && <button type="button" className="btn sm" onClick={tryRun.stop}>Stop</button>}
            </div>
            {tryRun.err && <p className="bad">{tryRun.err.message}</p>}
            {tryRun.stopped && <p className="muted sm">Stopped. Nothing was kept.</p>}
            {tryRun.lines && (
              <ul className="wf-try-lines">
                {tryRun.lines.map((l) => (
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
  const tickable = chips.filter((c) => !c.always);
  const always = chips.filter((c) => c.always);
  return (
    <>
      <div className="wf-chips" role="group" aria-label="Quick choices">
        {tickable.map((c) => (
          <button key={c.id} type="button" className="wf-chip" aria-pressed={chipOn(c.on, answers)} disabled={!c.available} title={c.why} onClick={() => onToggle(c)}>
            <span className="k" aria-hidden="true" />
            <span>
              {c.label}
              {c.why && !c.available ? <span className="hint" style={{ display: "block" }}>{c.why}</span> : null}
            </span>
          </button>
        ))}
      </div>
      <div className="wf-always">
        <span>Always on:</span>
        {always.map((c) => (
          <span key={c.id} className="wf-mini">{c.label}</span>
        ))}
      </div>
      <p className="hint">A tick sets the parts below. Change a part and its tick follows.</p>
    </>
  );
}

function PartCard({ part, answers, languages, setAnswer, disabled }: { part: GuidePart; answers: Answers; languages: string[]; setAnswer: (key: string, v: unknown) => void; disabled: FlowDisabled[] }) {
  const shown = part.fields.filter((f) => (f.type !== "folder" || answers.original === "one_folder") && (f.key !== "subtitleLanguages" || answers.subtitles === "my_languages"));
  const field = (f: GuideField) => (
    <FieldRow key={f.key} field={f} value={answers[f.key]} answers={answers} languages={languages} disabled={disabled} onChange={(v) => setAnswer(f.key, v)} />
  );
  const rare = shown.filter((f) => f.more);
  return (
    <section id={`wf-part-${part.id}`} className="card wf-part" aria-labelledby={`wf-h-${part.id}`}>
      <div className="wf-part-head">
        <h2 id={`wf-h-${part.id}`}>{part.label}</h2>
        <span className="wf-part-sum">{partValue(part, answers)}</span>
      </div>
      <div className="wf-stack">{shown.filter((f) => !f.more).map(field)}</div>
      {rare.length > 0 && (
        <details className="wf-more">
          <summary>More</summary>
          <div className="wf-stack">{rare.map(field)}</div>
        </details>
      )}
    </section>
  );
}

// A file type the answers rule out is greyed with the same reason the editor shows (the check's disabled list).
function greyFileTypes(choices: GuideChoice[], disabled: FlowDisabled[]): GuideChoice[] {
  return choices.map((c) => {
    const why = disabled.find((d) => d.field === "container" && d.value === c.value)?.why;
    return why ? { ...c, available: false, why } : c;
  });
}

function FieldRow({ field, value, answers, languages, disabled, onChange }: { field: GuideField; value: unknown; answers: Answers; languages: string[]; disabled: FlowDisabled[]; onChange: (v: unknown) => void }) {
  const ladder = useBitrateLadder();
  const families = useCardFamilies(answers.useGpu !== false);
  if (field.type === "choice" && field.choices) {
    const isFileType = field.key === "fileType";
    const choices = isFileType ? greyFileTypes(field.choices, disabled) : field.choices;
    const reasons = choices.filter((c) => !c.available && c.why).map((c) => c.why ?? "");
    return (
      <>
        <ChoiceField label={field.label} value={String(value ?? "")} choices={choices} onChange={onChange} full={field.key === "subtitles" || field.key === "original"} hint={isFileType ? fileTypeHint(reasons) : undefined} />
        {field.key === "original" && value === "replace" && <p className="hint wf-full">The original goes to the trash and is kept 7 days.</p>}
      </>
    );
  }
  if (field.type === "number") {
    const n = Number(value ?? 3);
    const isQuality = field.label === "Quality";
    return (
      <Row
        label={isQuality ? `Quality · ${n} · ${QUALITY_NAMES[n - 1]}` : field.label}
        hint={isQuality && ladder ? qualityMeaning(ladder, n, String(answers.format ?? "hevc"), families, null) : undefined}
      >
        <div className="wf-quality">
          <input type="range" min={1} max={5} step={1} value={n} aria-label={field.label} aria-valuetext={isQuality ? `${n} · ${QUALITY_NAMES[n - 1]}` : `${n}`} onChange={(e) => onChange(Number(e.target.value))} />
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
    const toggle = (code: string) => onChange(list.includes(code) ? list.filter((c) => c !== code) : [...list, code]);
    // A field the app cannot act on yet is greyed with its reason, the same as a greyed choice.
    const greyed = field.available === false;
    return (
      <Row label={field.label} full hint={greyed ? field.why : list.length === 0 ? "None picked keeps every language." : undefined}>
        <div className="wf-togs" role="group" aria-label={field.label} aria-disabled={greyed || undefined}>
          {languages.map((code) => (
            <button key={code} type="button" aria-pressed={list.includes(code)} title={greyed ? field.why : undefined} disabled={greyed} onClick={() => toggle(code)}>
              {languageName(code)}
            </button>
          ))}
        </div>
      </Row>
    );
  }
  return (
    <Row label={field.label} full={field.type === "folder"}>
      <input className="inp" aria-label={field.label} value={String(value ?? "")} onChange={(e) => onChange(e.target.value)} />
    </Row>
  );
}
