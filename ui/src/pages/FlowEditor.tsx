import { useEffect, useRef, useState, type ReactNode } from "react";
import { navigate, useLocation } from "../router.tsx";
import { api, type Flow, type FlowCheck, type FlowStep, type StepDefinition, type StepId } from "../api/index.ts";
import { useApi } from "../api/useApi.ts";
import { QUALITY_NAMES, Row, Seg, toErr, useBitrateLadder, useCardFamilies, type Err } from "./flowParts.tsx";
import { DRAWN, NO_LONGER_OFFERED, choiceKey, greyedChoices, offeredChoices, valueGreys, type Choice } from "./flowFields.ts";
import { qualityMeaning } from "../format/quality.ts";
import { fileTypeHint } from "../format/fileType.ts";
import { nameExample } from "../format/nameExample.ts";
import { LanguageChips } from "./LanguageChips.tsx";
import { publishFlowOnce } from "./flowPublish.ts";
import { useTryFile } from "./useTryFile.ts";

// Flow editor (07): the fixed walk Video → Audio → Save the result. Nothing is added or reordered.
// The Advanced parts replace the simple choices for the things the guided page doesn't cover.
// Option lists come from flowFields.ts (checked against the step definitions golden); the greyed choices come from
// the definitions the server sends.

const NEW = "new";
const ORDER: StepId[] = ["video", "audio", "save"];
type StepType = "video.convert" | "audio.tracks" | "file.save";
const TYPE: Record<StepId, StepType> = { video: "video.convert", audio: "audio.tracks", save: "file.save" };
const V: StepType = "video.convert";
const A: StepType = "audio.tracks";
const F: StepType = "file.save";
// What each Subtitles choice keeps, under the choice.
const SUBTITLE_HINT: Record<string, string> = {
  keep_all: "Every subtitle is kept.",
  my_languages: "Only the subtitles in the languages picked below are kept.",
  text_only: "Only text subtitles are kept; picture subtitles are dropped.",
  srt: "Subtitles are made into SRT files.",
};
// The two special tracks the default track can name, in page 07's words.
const SPECIAL_TRACKS = [
  { code: "orig", name: "Original language" },
  { code: "und", name: "Unknown" },
];
// The Advanced video fields that "Back to the defaults" resets to the golden's defaults.
const ADVANCED_VIDEO = ["size", "width", "height", "rateControl", "bitrateLevel", "avgMbps", "maxMbps", "twoPass", "preset", "fps", "bitDepth", "hdr", "keepDolbyVision"];
const STEP_NAME: Record<StepId, string> = { video: "Video", audio: "Audio", save: "Save the result" };
const FORMAT_NAME: Record<string, string> = { hevc: "HEVC", h264: "H.264", av1: "AV1" };
const ROW_FOR_SIZE: Record<string, string> = { "2160": "2160", "1440": "1440", "1080": "1080", "720": "720", "480": "sd" };

// A field's value as the card shows it: the stored value, or the golden's default when unset.
function valueOf(type: StepType, s: Record<string, unknown>, key: string): unknown {
  return s[key] ?? DRAWN[type].defaults[key];
}
function choicesOf(type: StepType, key: string): Choice[] {
  return DRAWN[type].choices[key] ?? [];
}
// Greyed choices by choice key, with the reason shown under the field.
type Off = Record<string, string>;

interface Loaded {
  flow: Flow;
  rev: number;
  name: string;
  usedBy: string[];
  defs: StepDefinition[];
}

// The graphics card, as page 07 words it: the stored useGpu bool, as "true" or "false".
const GPU_CHOICES: Choice[] = [
  { value: "true", label: "Automatic" },
  { value: "false", label: "Off" },
];

export function FlowEditor({ id }: { id: string }) {
  const starter = useLocation().query.get("starter");
  const loaded = useApi<Loaded>(async () => {
    const [rows, defs] = await Promise.all([api.listWorkflows(), api.getStepDefinitions()]);
    const row = rows.find((r) => r.id === id);
    const name = row?.name ?? "New workflow";
    const usedBy = row?.usedBy.map((u) => u.name) ?? [];
    if (id === NEW) {
      const g = await api.getGuideAnswers(NEW);
      const extra = starter ? (await api.getStarter(starter)).answers : {};
      const p = await api.guidePreview({ ...g.answers, ...extra });
      return { flow: { ...p.flow, name }, rev: 0, name, usedBy, defs };
    }
    const f = await api.getFlow(id);
    return { flow: f.flow, rev: f.rev, name, usedBy, defs };
  }, [id, starter]);

  if (loaded.error && !loaded.data) {
    return <p className="bad">Can't load this workflow. <button type="button" className="btn ghost sm" onClick={loaded.reload}>Try now</button></p>;
  }
  if (!loaded.data) return <p className="muted">Loading…</p>;
  return <Editor key={id} id={id} initial={loaded.data} />;
}

function Editor({ id, initial }: { id: string; initial: Loaded }) {
  const [flow, setFlow] = useState<Flow>(initial.flow);
  const [rev, setRev] = useState(initial.rev);
  const [dirty, setDirty] = useState(false);
  const [check, setCheck] = useState<FlowCheck | null>(null);
  const [checkErr, setCheckErr] = useState<Err | null>(null);
  const [open, setOpen] = useState<StepId | null>("video");
  const [showProblems, setShowProblems] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<Err | null>(null);
  const [tryFile, setTryFile] = useState("");
  const tryRun = useTryFile();
  const [draftId, setDraftId] = useState(id);
  const [tryOpen, setTryOpen] = useState(false);
  const seq = useRef(0);
  const isNew = draftId === NEW;

  // The check follows the flow, 300 ms after the last change: the problems, the greyed fields and the summaries.
  useEffect(() => {
    const mine = ++seq.current;
    const t = setTimeout(() => {
      api
        .validateFlow(flow)
        .then((c) => {
          if (mine !== seq.current) return;
          setCheck(c);
          setCheckErr(null);
        })
        .catch((e) => {
          if (mine !== seq.current) return;
          setCheckErr(toErr(e));
        });
    }, 300);
    return () => clearTimeout(t);
  }, [flow]);

  const settings = (stepId: StepId): Record<string, unknown> => flow.steps.find((s) => s.id === stepId)?.settings ?? {};
  const set = (stepId: StepId, patch: Record<string, unknown>) => {
    setFlow((f) => ({ ...f, steps: f.steps.map((s: FlowStep) => (s.id === stepId ? { ...s, settings: { ...s.settings, ...patch } } : s)) }));
    setDirty(true);
  };
  const setMeta = (patch: Partial<Pick<Flow, "name" | "description">>) => {
    setFlow((f) => ({ ...f, ...patch }));
    setDirty(true);
  };
  const why = (stepId: StepId, field: string): string | undefined => check?.disabled.find((d) => d.stepId === stepId && d.field === field)?.why;
  // A choice the check rules out for its value (the file types): the reason, or undefined.
  const whyValue = (stepId: StepId, field: string, value: string): string | undefined =>
    check?.disabled.find((d) => d.stepId === stepId && d.field === field && d.value === value)?.why;
  // The choices greyed for a field, by choice key, with the server's reason: the step definitions' greys, then every
  // check entry for the field that rules out a value (the file types, Colour depth 10, and so on).
  const greyed = (stepId: StepId, field: string): Off => {
    const def = initial.defs.find((d) => d.type === TYPE[stepId]);
    return { ...(def ? greyedChoices(def, field) : {}), ...valueGreys(check?.disabled ?? [], stepId, field) };
  };
  const problemsFor = (stepId: StepId) => (check?.problems ?? []).filter((p) => p.stepId === stepId);
  const problemCount = check?.problems.length ?? 0;

  const save = async () => {
    setBusy(true);
    setErr(null);
    try {
      if (isNew) {
        const created = await api.createDraft({ name: flow.name, description: flow.description, flow });
        setDraftId(created.id);
        navigate(`/workflows/${created.id}`);
        return;
      }
      const r = await api.putDraft(draftId, flow, rev);
      setRev(r.rev);
      setDirty(false);
    } catch (e) {
      setErr(toErr(e));
    } finally {
      setBusy(false);
    }
  };

  const publish = async () => {
    setBusy(true);
    setErr(null);
    try {
      // A new workflow's draft is made once: a refused publish keeps it, so the retry publishes the same draft.
      await publishFlowOnce(api, { isNew, draftId, flow, makeDraft: () => api.createDraft({ name: flow.name, description: flow.description, flow }) }, (id, created) => {
        setDraftId(id);
        setRev(created);
      });
      navigate("/workflows");
    } catch (e) {
      setErr(toErr(e));
    } finally {
      setBusy(false);
    }
  };

  const onTry = () => {
    void tryRun.start((signal) => api.tryFlow(flow, tryFile, signal).then((r) => r.lines));
  };

  const video = settings("video");
  const audio = settings("audio");
  const audioLanguages = (audio.languages as string[] | undefined) ?? [];
  // Keep every language (#883). A flow saved before the flag has none: a list then means only those languages.
  const keepAll = audio.allLanguages === undefined ? audioLanguages.length === 0 : audio.allLanguages !== false;
  const save_ = settings("save");

  // The file types the picked video can't go in: the server's reasons, by choice key.
  const containerOff = (): Off => {
    const out: Off = { ...greyed("save", "container") };
    for (const c of choicesOf(F, "container")) {
      const w = whyValue("save", "container", String(c.value));
      if (w) out[choiceKey(c.value)] = w;
    }
    return out;
  };
  // Format is greyed as a whole when the check says so (a codec above it replaces it); otherwise the definitions' greys.
  const formatOff = (): Off => {
    const w = why("audio", "format");
    if (!w) return greyed("audio", "format");
    return Object.fromEntries(choicesOf(A, "format").map((c) => [choiceKey(c.value), w]));
  };

  const cards: Record<StepId, ReactNode> = {
    video: (
      <>
        <Choose
          label="Format"
          choices={choicesOf(V, "format")}
          value={valueOf(V, video, "format")}
          off={greyed("video", "format")}
          hint={valueOf(V, video, "format") === "hevc" ? "Small files that play on most TVs and phones since 2016." : undefined}
          onChange={(v) => set("video", { format: v, ...(v === "av1" && video.rateControl === "vbr" ? { rateControl: null } : {}) })}
        />
        <QualityRow
          value={Number(valueOf(V, video, "quality") ?? 3)}
          format={String(valueOf(V, video, "format"))}
          card={valueOf(V, video, "useGpu") !== false}
          size={video.size == null ? null : String(video.size)}
          off={video.rateControl != null ? "Not used: Advanced sets a bitrate instead." : why("video", "quality")}
          onChange={(q) => set("video", { quality: q })}
        />
        <Choose
          widget="select"
          label="Graphics card"
          hint="Made on the graphics card when a worker has one."
          choices={GPU_CHOICES}
          value={String(video.useGpu !== false)}
          onChange={(v) => set("video", { useGpu: v === "true" })}
        />
        <Choose
          label="If the graphics card can't do it"
          choices={choicesOf(V, "ifGpuFails")}
          value={valueOf(V, video, "ifGpuFails")}
          off={greyed("video", "ifGpuFails")}
          onChange={(v) => set("video", { ifGpuFails: v })}
        />
        <Advanced summary="Advanced" description="Picture size, bitrate, speed, frame rate, bit depth, HDR">
          <VideoAdvanced
            s={video}
            format={String(valueOf(V, video, "format"))}
            set={(patch) => set("video", patch)}
            disabledRate={why("video", "rateControl")}
            greyed={(field) => greyed("video", field)}
          />
        </Advanced>
      </>
    ),
    audio: (
      <>
        <Choose label="Format" choices={choicesOf(A, "format")} value={valueOf(A, audio, "format")} off={formatOff()} onChange={(v) => set("audio", { format: v })} />
        <Choose label="Channels" widget="select" choices={choicesOf(A, "channels")} value={valueOf(A, audio, "channels")} onChange={(v) => set("audio", { channels: v })} />
        {audio.channels === "5.1" && (
          <div className="note" style={{ margin: "0 0 8px" }}>
            <span className="ic" aria-hidden="true">!</span>
            <p className="hint" style={{ margin: 0 }}>
              5.1 can't be made from stereo or mono. Files with stereo or mono sound keep the channels they have; only files with 5.1 or more come out as 5.1.
            </p>
          </div>
        )}
        <Row label="Languages">
          <label style={{ display: "inline-flex", gap: 8, alignItems: "center" }}>
            <input
              type="checkbox"
              checked={keepAll}
              onChange={(e) => set("audio", e.target.checked ? { allLanguages: true, languages: [] } : { allLanguages: false })}
            />
            Keep every language
          </label>
        </Row>
        {!keepAll && (
          <Row
            label="Keep these languages"
            full
            hint={
              audioLanguages.length === 0
                ? "None picked keeps my languages (Settings, General)."
                : "Other tracks are removed; if none match, the main track stays. Original language keeps the film's own language, whatever it is."
            }
          >
            <LanguageChips label="Keep these languages" value={audioLanguages} onChange={(languages) => set("audio", { languages })} />
          </Row>
        )}
        <Advanced summary="Advanced" description="Codec, bitrate, sample rate, stereo from surround, loudness, commentary, default track, original track">
          <AudioAdvanced s={audio} set={(patch) => set("audio", patch)} greyed={(field) => greyed("audio", field)} />
        </Advanced>
        <Choose
          label="Subtitles"
          full
          choices={choicesOf(A, "subtitles")}
          value={valueOf(A, audio, "subtitles")}
          off={greyed("audio", "subtitles")}
          hint={SUBTITLE_HINT[String(valueOf(A, audio, "subtitles"))]}
          onChange={(v) => set("audio", { subtitles: v })}
        />
        {audio.subtitles === "my_languages" && (
          <Row label="Subtitle languages" full hint="None picked keeps the same languages as the audio.">
            <LanguageChips label="Subtitle languages" value={(audio.subtitleLanguages as string[] | undefined) ?? []} onChange={(subtitleLanguages) => set("audio", { subtitleLanguages })} />
          </Row>
        )}
      </>
    ),
    save: (
      <>
        <p className="hint" style={{ marginTop: 0 }}>Every result is checked first: it must play and not be broken. A broken result is never saved, and the original stays as it was.</p>
        <Choose
          label="File type"
          full
          choices={choicesOf(F, "container")}
          value={valueOf(F, save_, "container")}
          off={containerOff()}
          hint={fileTypeHint(choicesOf(F, "container").map((c) => whyValue("save", "container", String(c.value))).filter((w): w is string => Boolean(w)))}
          onChange={(v) => set("save", { container: v })}
        />
        <Choose label="Where the result goes" choices={choicesOf(F, "where")} value={valueOf(F, save_, "where")} off={greyed("save", "where")} onChange={(v) => set("save", { where: v })} />
        {valueOf(F, save_, "where") === "folder" && (
          <Row label="Folder" hint="Sub-folders of the library are kept.">
            <input className="inp" aria-label="Folder" value={String(save_.folder ?? "")} onChange={(e) => set("save", { folder: e.target.value })} />
          </Row>
        )}
        {valueOf(F, save_, "where") === "replace" && (
          <p className="hint" style={{ margin: "0 0 8px" }}>The original goes to the trash and is kept 7 days. How long is set for all workflows in Settings › Processing.</p>
        )}
        <Row label="Add to the file name" hint={nameExample(String(valueOf(F, save_, "container")), String(save_.nameSuffix ?? ""))}>
          <input className="inp" aria-label="Add to the file name" value={String(save_.nameSuffix ?? "")} onChange={(e) => set("save", { nameSuffix: e.target.value })} />
        </Row>
      </>
    ),
  };

  return (
    <div className="wf-col">
      <div className="wf-head">
        <div>
          <button type="button" className="wf-back" onClick={() => navigate("/workflows")}>‹ Workflows</button>
          <div className="wf-head-meta">
            <h1>{flow.name || "Untitled"}</h1>
            {check ? (
              problemCount === 0 ? (
                <span className="pill ok">No problems</span>
              ) : (
                <button type="button" className="pill bad" aria-expanded={showProblems} onClick={() => setShowProblems((v) => !v)}>
                  {problemCount} {problemCount === 1 ? "problem" : "problems"} · Show
                </button>
              )
            ) : checkErr ? (
              <span className="pill warn">Can't check</span>
            ) : (
              <span className="pill">Checking…</span>
            )}
          </div>
          <span className="muted sm">
            Video{initial.usedBy.length > 0 ? ` · used by ${initial.usedBy.join(", ")}` : ""}
            {dirty ? <span className="wf-dirty" aria-label="Unsaved changes" /> : null}
          </span>
        </div>
        <span className="sp" />
        <button type="button" className="btn" aria-expanded={tryOpen} onClick={() => setTryOpen((v) => !v)}>Try on one file</button>
        <button type="button" className="btn" disabled={busy} onClick={save}>Save as a draft</button>
        <button type="button" className="btn primary" disabled={busy || problemCount > 0 || !check} onClick={publish}>Publish</button>
      </div>

      {tryOpen && (
        <section className="card" style={{ marginBottom: 14 }} aria-label="Try on one file">
          <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
            <input className="inp" aria-label="File to try" placeholder="Full path" aria-describedby="try-file-hint" value={tryFile} onChange={(e) => setTryFile(e.target.value)} style={{ flex: "1 1 220px", minWidth: 0 }} />
            <button ref={tryRun.button} type="button" className="btn sm" onClick={onTry} disabled={!check || tryRun.trying}>
              {tryRun.trying ? "Trying… about a minute" : "Try"}
            </button>
            {tryRun.trying && <button type="button" className="btn sm" onClick={tryRun.stop}>Stop</button>}
          </div>
          <p id="try-file-hint" className="muted sm">For example /media/Movies/film.mkv</p>
          {tryRun.err && <p className="bad">{tryRun.err.message}</p>}
          {tryRun.stopped && <p className="muted sm">Stopped. Nothing was kept.</p>}
          {tryRun.lines && (
            <ul className="wf-try-lines">
              {tryRun.lines.map((l) => (
                <li key={l.stepId + l.text}>
                  {l.text}
                </li>
              ))}
            </ul>
          )}
        </section>
      )}

      {showProblems && check && check.problems.length > 0 && (
        <ul className="card" style={{ margin: "0 0 14px", paddingLeft: 36 }} aria-label="Problems">
          {check.problems.map((p) => (
            <li key={p.code + p.message} className="bad">
              {p.stepId ? `${STEP_NAME[p.stepId]}: ` : ""}
              {p.message}
            </li>
          ))}
        </ul>
      )}
      {err && <p className="bad" role="alert">{err.message}</p>}

      <details className="wf-settings">
        <summary>Flow settings</summary>
        <Row label="Name">
          <input className="inp" aria-label="Name" value={flow.name} onChange={(e) => setMeta({ name: e.target.value })} />
        </Row>
        <Row label="Description">
          <input className="inp" aria-label="Description" value={flow.description} onChange={(e) => setMeta({ description: e.target.value })} />
        </Row>
      </details>

      <div className="wf-steps">
        {ORDER.map((stepId, i) => {
          const isOpen = open === stepId;
          const ps = problemsFor(stepId);
          const summary = check?.summaries[stepId] ?? "";
          return (
            <section key={stepId} className={`wf-step${isOpen ? " open" : ""}`} aria-label={STEP_NAME[stepId]}>
              <button type="button" className="wf-step-head" aria-expanded={isOpen} onClick={() => setOpen(isOpen ? null : stepId)}>
                <span className={`wf-num${ps.length === 0 && !isOpen ? " done" : ""}`}>{i + 1}</span>
                <span>
                  <span className="wf-step-title">{STEP_NAME[stepId]}</span>
                  {!isOpen && <span className="wf-step-sum">{summary}</span>}
                </span>
                <span className="wf-step-right">
                  {ps.length > 0 && <span className="pill bad">{ps.length === 1 ? "1 problem" : `${ps.length} problems`}</span>}
                  <span className="wf-step-of">Step {i + 1} of 3</span>
                </span>
              </button>
              {isOpen && (
                <div className="wf-step-body">
                  {cards[stepId]}
                  <div className="wf-step-nav">
                    {i > 0 && <button type="button" className="btn" onClick={() => setOpen(ORDER[i - 1])}>Back</button>}
                    {i < ORDER.length - 1 ? (
                      <button type="button" className="btn primary" onClick={() => setOpen(ORDER[i + 1])}>Next: {STEP_NAME[ORDER[i + 1]]}</button>
                    ) : (
                      <button type="button" className="btn primary" onClick={() => setOpen(null)}>Done</button>
                    )}
                  </div>
                </div>
              )}
            </section>
          );
        })}
      </div>

    </div>
  );
}

// One field: its choices from the editor table, as a segmented control or a select. A greyed choice is disabled, and its
// reason shows under the field (the explicit hint wins). children sit inside the row, under the control.
function Choose({
  label,
  choices,
  value,
  onChange,
  off = {},
  widget = "seg",
  hint,
  full,
  children,
}: {
  label: string;
  choices: Choice[];
  value: unknown;
  onChange: (v: string | number | null) => void;
  off?: Off;
  widget?: "seg" | "select";
  hint?: string;
  full?: boolean;
  children?: ReactNode;
}) {
  const { options, stale } = offeredChoices(choices, value);
  const key = choiceKey(value);
  const isOff = (k: string) => Boolean(off[k]) || k === stale;
  const reasons = options.map((c) => off[choiceKey(c.value)]).filter((w): w is string => Boolean(w));
  const shown = stale !== null ? NO_LONGER_OFFERED : (hint ?? (reasons.length ? reasons.join(" ") : undefined));
  const pick = (k: string) => {
    const c = options.find((x) => choiceKey(x.value) === k);
    if (c) onChange(c.value);
  };
  return (
    <Row label={label} hint={shown} full={full}>
      {widget === "seg" ? (
        <Seg
          label={label}
          value={key}
          options={options.map((c) => ({ value: choiceKey(c.value), label: c.label, disabled: isOff(choiceKey(c.value)), title: off[choiceKey(c.value)] }))}
          onChange={pick}
        />
      ) : (
        <select className="sel" aria-label={label} value={key} onChange={(e) => pick(e.target.value)}>
          {options.map((c) => (
            <option key={choiceKey(c.value)} value={choiceKey(c.value)} disabled={isOff(choiceKey(c.value))}>
              {c.label}
            </option>
          ))}
        </select>
      )}
      {children}
    </Row>
  );
}

// Quality: the 1–5 slider with its name, and the number it resolves to for the picture size.
function QualityRow({ value, format, card, size, off, onChange }: { value: number; format: string; card: boolean; size: string | null; off?: string; onChange: (q: number) => void }) {
  const ladder = useBitrateLadder();
  const families = useCardFamilies(card);
  return (
    <div className={off ? "wf-dim" : undefined}>
      <Row label={`Quality · ${value} · ${QUALITY_NAMES[value - 1]}`} hint={off ?? (ladder ? qualityMeaning(ladder, value, format, families, size) : undefined)}>
        <div className="wf-quality">
          <input type="range" min={1} max={5} step={1} value={value} disabled={Boolean(off)} aria-label="Quality" aria-valuetext={`${value} · ${QUALITY_NAMES[value - 1]}`} onChange={(e) => onChange(Number(e.target.value))} />
        </div>
      </Row>
    </div>
  );
}

function Advanced({ summary, description, children }: { summary: string; description: string; children: ReactNode }) {
  return (
    <details className="adv wf-advbox">
      <summary>
        {summary} <span className="hint">{description}</span>
      </summary>
      <div>{children}</div>
    </details>
  );
}

function VideoAdvanced({
  s,
  format,
  set,
  disabledRate,
  greyed,
}: {
  s: Record<string, unknown>;
  format: string;
  set: (patch: Record<string, unknown>) => void;
  disabledRate?: string;
  greyed: (field: string) => Off;
}) {
  const rate = (s.rateControl as string | null) ?? null;
  const level = String(valueOf(V, s, "bitrateLevel"));
  const size = valueOf(V, s, "size");
  const row = ROW_FOR_SIZE[String(s.size ?? "1080")] ?? "1080";
  const rec = useLadder(format, row);
  const recLine = rec?.line;
  const avgShown = level === "custom" ? (s.avgMbps ?? "") : recLine ? recLine[level as "low" | "medium" | "high"] : "";
  // Variable bitrate doesn't work with AV1 yet: greyed here, whatever the definitions say.
  const rateOff: Off = { ...greyed("rateControl") };
  if (format === "av1") rateOff.vbr = "Variable bitrate doesn't work with AV1 yet. Pick Constant bitrate or Quality, or another format.";

  return (
    <>
      <Choose widget="select" label="Picture size" choices={choicesOf(V, "size")} value={size} off={greyed("size")} onChange={(v) => set({ size: v })} />
      {size === "custom" && (
        <Row label="Custom size">
          <span style={{ display: "flex", gap: 8, alignItems: "center" }}>
            <input className="inp narrow" aria-label="Width" type="number" value={String(s.width ?? "")} onChange={(e) => set({ width: e.target.value ? Number(e.target.value) : null })} /> ×
            <input className="inp narrow" aria-label="Height" type="number" value={String(s.height ?? "")} onChange={(e) => set({ height: e.target.value ? Number(e.target.value) : null })} />
          </span>
        </Row>
      )}
      <Choose label="Bitrate" choices={choicesOf(V, "rateControl")} value={rate} off={rateOff} hint={disabledRate} onChange={(v) => set({ rateControl: v })} />
      {rate != null && (
        <>
          <Choose label="Bitrate level" choices={choicesOf(V, "bitrateLevel")} value={valueOf(V, s, "bitrateLevel")} off={greyed("bitrateLevel")} onChange={(v) => set({ bitrateLevel: v })}>
            {rec && recLine && (
              <p className="hint wf-hint">
                Recommended for {rec.rowLabel} {FORMAT_NAME[format] ?? format}: {recLine.low}–{recLine.high} Mb/s (Medium {recLine.medium}). More for HDR (+25%) and 50–60 fps (+40%), never above the source.
              </p>
            )}
          </Choose>
          <Row label={rate === "cbr" ? "Bitrate Mb/s" : "Average Mb/s"}>
            <input
              className="inp narrow"
              aria-label={rate === "cbr" ? "Bitrate Mb/s" : "Average Mb/s"}
              type="number"
              step="0.1"
              value={String(avgShown)}
              onChange={(e) => set({ bitrateLevel: "custom", avgMbps: e.target.value ? Number(e.target.value) : null })}
            />
          </Row>
          {rate === "vbr" && (
            <>
              <Row label="At most Mb/s">
                <input className="inp narrow" aria-label="At most Mb/s" type="number" step="0.1" value={String(s.maxMbps ?? "")} onChange={(e) => set({ maxMbps: e.target.value ? Number(e.target.value) : null })} />
              </Row>
              <Row label="Variable bitrate">
                <label style={{ display: "inline-flex", gap: 8, alignItems: "center" }}>
                  <input type="checkbox" checked={Boolean(valueOf(V, s, "twoPass"))} onChange={(e) => set({ twoPass: e.target.checked })} />
                  Two passes
                </label>
              </Row>
            </>
          )}
        </>
      )}
      <Choose widget="select" label="Speed" choices={choicesOf(V, "preset")} value={valueOf(V, s, "preset")} off={greyed("preset")} onChange={(v) => set({ preset: v })} />
      <Row label="Frame rate" hint="Left empty, each file keeps its own frame rate.">
        <input className="inp narrow" aria-label="Frame rate" type="number" min={1} max={240} value={String(s.fps ?? "")} onChange={(e) => set({ fps: e.target.value ? Number(e.target.value) : null })} />
      </Row>
      <Choose widget="select" label="Colour depth" choices={choicesOf(V, "bitDepth")} value={valueOf(V, s, "bitDepth")} off={greyed("bitDepth")} onChange={(v) => set({ bitDepth: v })} />
      <Choose widget="select" label="HDR" choices={choicesOf(V, "hdr")} value={valueOf(V, s, "hdr")} off={greyed("hdr")} onChange={(v) => set({ hdr: v })} />
      <Row label="Dolby Vision" hint="Only with HEVC.">
        <label style={{ display: "inline-flex", gap: 8, alignItems: "center" }}>
          <input
            type="checkbox"
            checked={Boolean(valueOf(V, s, "keepDolbyVision"))}
            disabled={format !== "hevc"}
            onChange={(e) => set({ keepDolbyVision: e.target.checked })}
          />
          Keep Dolby Vision
        </label>
      </Row>
      <button type="button" className="btn ghost sm" onClick={() => set(Object.fromEntries(ADVANCED_VIDEO.map((k) => [k, DRAWN[V].defaults[k]])))}>
        Back to the defaults
      </button>
    </>
  );
}

// The recommended-bitrate ladder for one picture size, read once from the server's table.
function useLadder(format: string, row: string) {
  const ladder = useApi(() => api.getBitrateLadder(), []);
  if (!ladder.data) return null;
  const table = ladder.data.formats[format as "hevc"] ?? ladder.data.formats.hevc;
  return { line: table[row], rowLabel: ladder.data.rows.find((r) => r.row === row)?.label ?? row };
}

function AudioAdvanced({ s, set, greyed }: { s: Record<string, unknown>; set: (patch: Record<string, unknown>) => void; greyed: (field: string) => Off }) {
  const langs = useApi(() => api.getLanguages(), []);
  return (
    <>
      <Choose widget="select" label="Codec" choices={choicesOf(A, "codec")} value={valueOf(A, s, "codec")} off={greyed("codec")} onChange={(v) => set({ codec: v })} />
      <Row label="Bitrate (kb/s)" hint="Empty uses the codec's default.">
        <input className="inp narrow" aria-label="Bitrate (kb/s)" type="number" min={8} max={1536} value={String(s.bitrateKbps ?? "")} onChange={(e) => set({ bitrateKbps: e.target.value ? Number(e.target.value) : null })} />
      </Row>
      {s.bitrateKbps != null && <Choose label="Bitrate is for" choices={choicesOf(A, "bitratePer")} value={valueOf(A, s, "bitratePer")} off={greyed("bitratePer")} onChange={(v) => set({ bitratePer: v })} />}
      <Row label="Sample rate" hint="Left empty, each file keeps its own sample rate.">
        <span style={{ display: "flex", gap: 8, alignItems: "center" }}>
          <input className="inp narrow" aria-label="Sample rate" type="number" min={8000} max={192000} value={String(s.sampleRate ?? "")} onChange={(e) => set({ sampleRate: e.target.value ? Number(e.target.value) : null })} /> Hz
        </span>
      </Row>
      <Choose widget="select" label="Making stereo from surround" choices={choicesOf(A, "downmix")} value={valueOf(A, s, "downmix")} off={greyed("downmix")} onChange={(v) => set({ downmix: v })} />
      <Row label="Loudness">
        <label style={{ display: "inline-flex", gap: 8, alignItems: "center" }}>
          <input type="checkbox" checked={Boolean(s.loudnorm)} onChange={(e) => set({ loudnorm: e.target.checked })} />
          Even out loudness
        </label>
      </Row>
      <Choose label="Commentary tracks" choices={choicesOf(A, "commentary")} value={valueOf(A, s, "commentary")} off={greyed("commentary")} onChange={(v) => set({ commentary: v })} />
      <Row label="Default track">
        <select
          className="sel"
          aria-label="Default track"
          value={s.defaultTrack == null ? "none" : String(s.defaultTrack)}
          onChange={(e) => set({ defaultTrack: e.target.value === "none" ? null : e.target.value })}
        >
          <option value="none">As in the file</option>
          {SPECIAL_TRACKS.map((o) => (
            <option key={o.code} value={o.code}>
              {o.name}
            </option>
          ))}
          {(langs.data ?? []).map((l) => (
            <option key={l.code} value={l.code}>
              {l.name}
            </option>
          ))}
        </select>
      </Row>
      <Row label="Original track">
        <label style={{ display: "inline-flex", gap: 8, alignItems: "center" }}>
          <input type="checkbox" checked={Boolean(s.keepOriginal)} onChange={(e) => set({ keepOriginal: e.target.checked })} />
          Keep the original track as well
        </label>
      </Row>
    </>
  );
}
