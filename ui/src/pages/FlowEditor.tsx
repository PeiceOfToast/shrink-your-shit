import { useEffect, useRef, useState, type ReactNode } from "react";
import { navigate, useLocation } from "../router.tsx";
import { api, type Flow, type FlowCheck, type FlowStep, type StepId, type TryLine } from "../api/index.ts";
import { useApi } from "../api/useApi.ts";
import { ChoiceField, QUALITY_NAMES, Row, Seg, qualityMeaning, toErr, useBitrateLadder, type Err } from "./flowParts.tsx";
import { LanguageChips } from "./LanguageChips.tsx";

// Flow editor (07): the fixed walk Video → Audio → Save the result. Nothing is added or reordered.
// The Advanced parts replace the simple choices for the things the guided page doesn't cover.

const NEW = "new";
const ORDER: StepId[] = ["video", "audio", "save"];
// The file types, in the order the File type row shows them. Greyed ones come from the check's disabled list.
const CONTAINERS = [
  { value: "mkv", label: "MKV" },
  { value: "mp4", label: "MP4" },
  { value: "webm", label: "WebM" },
  { value: "keep", label: "Keep the same" },
];
// What each Subtitles choice keeps, under the choice.
const SUBTITLE_HINT: Record<string, string> = {
  keep_all: "Every subtitle is kept.",
  my_languages: "Only the subtitles in the languages picked below are kept.",
  text_only: "Only text subtitles are kept; picture subtitles are dropped.",
  srt: "Subtitles are made into SRT files.",
};
const STEP_NAME: Record<StepId, string> = { video: "Video", audio: "Audio", save: "Save the result" };
const FORMAT_NAME: Record<string, string> = { hevc: "HEVC", h264: "H.264", av1: "AV1" };
const ROW_FOR_SIZE: Record<string, string> = { "2160": "2160", "1440": "1440", "1080": "1080", "720": "720", "480": "sd" };
const SIZE_OPTIONS = [
  { value: "keep", label: "Same as the source" },
  { value: "2160", label: "2160p" },
  { value: "1440", label: "1440p" },
  { value: "1080", label: "1080p" },
  { value: "720", label: "720p" },
  { value: "480", label: "480p" },
  { value: "custom", label: "Custom" },
];

interface Loaded {
  flow: Flow;
  rev: number;
  name: string;
  usedBy: string[];
}

// The graphics card choice. The backend has no separate value for Automatic and Intel (QSV): both use the card.
type GpuPick = "auto" | "qsv" | "none";

export function FlowEditor({ id }: { id: string }) {
  const starter = useLocation().query.get("starter");
  const loaded = useApi<Loaded>(async () => {
    const rows = await api.listWorkflows();
    const row = rows.find((r) => r.id === id);
    const name = row?.name ?? "New workflow";
    const usedBy = row?.usedBy.map((u) => u.name) ?? [];
    if (id === NEW) {
      const g = await api.getGuideAnswers(NEW);
      const extra = starter ? (await api.getStarter(starter)).answers : {};
      const p = await api.guidePreview({ ...g.answers, ...extra });
      return { flow: { ...p.flow, name }, rev: 0, name, usedBy };
    }
    const f = await api.getFlow(id);
    return { flow: f.flow, rev: f.rev, name, usedBy };
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
  const [tryLines, setTryLines] = useState<TryLine[] | null>(null);
  const [tryErr, setTryErr] = useState<Err | null>(null);
  const [draftId, setDraftId] = useState(id);
  const [tryOpen, setTryOpen] = useState(false);
  const [gpu, setGpu] = useState<GpuPick>(() => (initial.flow.steps.find((s) => s.id === "video")?.settings.useGpu === false ? "none" : "auto"));
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
      const useId = isNew ? (await api.createDraft({ name: flow.name, description: flow.description, flow })).id : draftId;
      await api.publishFlow(useId, flow);
      navigate("/workflows");
    } catch (e) {
      setErr(toErr(e));
    } finally {
      setBusy(false);
    }
  };

  const onTry = async () => {
    setTryErr(null);
    try {
      setTryLines((await api.tryFlow(flow, tryFile)).lines);
    } catch (e) {
      setTryErr(toErr(e));
      setTryLines(null);
    }
  };

  const video = settings("video");
  const audio = settings("audio");
  const save_ = settings("save");

  const cards: Record<StepId, ReactNode> = {
    video: (
      <>
        <Row label="Format" hint={video.format === "hevc" ? "Small files that play on most TVs and phones since 2016." : undefined}>
          <Seg
            label="Format"
            value={String(video.format)}
            options={[
              { value: "hevc", label: "HEVC" },
              { value: "h264", label: "H.264" },
              { value: "av1", label: "AV1" },
            ]}
            onChange={(v) => set("video", { format: v, ...(v === "av1" && video.rateControl === "vbr" ? { rateControl: null } : {}) })}
          />
        </Row>
        <QualityRow
          value={Number(video.quality ?? 3)}
          format={String(video.format)}
          card={video.useGpu !== false}
          size={video.size == null ? null : String(video.size)}
          off={video.rateControl != null ? "Not used: Advanced sets a bitrate instead." : why("video", "quality")}
          onChange={(q) => set("video", { quality: q })}
        />
        <Row label="Graphics card" hint="Made on your card (QSV) when it can.">
          <select
            className="sel"
            aria-label="Graphics card"
            value={gpu}
            onChange={(e) => {
              const v = e.target.value as GpuPick;
              setGpu(v);
              set("video", { useGpu: v !== "none" });
            }}
          >
            <option value="auto">Automatic</option>
            <option value="qsv">Intel (QSV)</option>
            <option value="none">None, use the processor</option>
          </select>
        </Row>
        <Row label="If the graphics card can't do it">
          <Seg
            label="If the graphics card can't do it"
            value={String(video.ifGpuFails ?? "cpu")}
            options={[
              { value: "cpu", label: "Use the processor" },
              { value: "stop", label: "Stop and tell me" },
            ]}
            onChange={(v) => set("video", { ifGpuFails: v })}
          />
        </Row>
        <Advanced summary="Advanced" description="Picture size, bitrate, speed, frame rate, bit depth, HDR">
          <VideoAdvanced
            s={video}
            format={String(video.format)}
            set={(patch) => set("video", patch)}
            disabledRate={why("video", "rateControl")}
          />
        </Advanced>
      </>
    ),
    audio: (
      <>
        <ChoiceField
          label="Format"
          value={String(audio.format ?? "aac")}
          choices={[
            { value: "aac", label: "AAC", available: why("audio", "format") === undefined, why: why("audio", "format") },
            { value: "opus", label: "Opus", available: why("audio", "format") === undefined, why: why("audio", "format") },
            { value: "copy", label: "Same as the source", available: why("audio", "format") === undefined, why: why("audio", "format") },
          ]}
          onChange={(v) => set("audio", { format: v })}
        />
        <Row label="Channels">
          <select className="sel" aria-label="Channels" value={String(audio.channels ?? "stereo")} onChange={(e) => set("audio", { channels: e.target.value })}>
            <option value="stereo">Stereo</option>
            <option value="source">Same as the source</option>
            <option value="mono">Mono</option>
            <option value="5.1">5.1</option>
          </select>
        </Row>
        {audio.channels === "5.1" && (
          <div className="note" style={{ margin: "0 0 8px" }}>
            <span className="ic" aria-hidden="true">!</span>
            <p className="hint" style={{ margin: 0 }}>
              5.1 can't be made from stereo or mono. Files with stereo or mono sound keep the channels they have; only files with 5.1 or more come out as 5.1.
            </p>
          </div>
        )}
        <Row
          label="Languages"
          full
          hint={
            ((audio.languages as string[] | undefined) ?? []).length === 0
              ? "None picked keeps every language."
              : "Other tracks are removed; if none match, the main track stays. Original language keeps the film's own language, whatever it is."
          }
        >
          <LanguageChips label="Languages" value={(audio.languages as string[] | undefined) ?? []} onChange={(languages) => set("audio", { languages })} />
        </Row>
        <Advanced summary="Advanced" description="Codec, bitrate, downmix, loudness, commentary, original track">
          <AudioAdvanced s={audio} set={(patch) => set("audio", patch)} />
        </Advanced>
        <Row label="Subtitles" full hint={SUBTITLE_HINT[String(audio.subtitles ?? "keep_all")]}>
          <Seg
            label="Subtitles"
            value={String(audio.subtitles ?? "keep_all")}
            options={[
              { value: "keep_all", label: "Keep all" },
              { value: "my_languages", label: "Keep my languages" },
              { value: "text_only", label: "Text only" },
              { value: "srt", label: "Make SRT files" },
            ]}
            onChange={(v) => set("audio", { subtitles: v })}
          />
        </Row>
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
        <Row label="File type" full>
          <div className="wf-save-options" role="radiogroup" aria-label="File type">
            {CONTAINERS.map((o) => {
              const off = whyValue("save", "container", o.value);
              return (
                <label key={o.value}>
                  <input
                    type="radio"
                    name={`container-${draftId}`}
                    checked={(save_.container ?? "mkv") === o.value}
                    disabled={off !== undefined}
                    onChange={() => set("save", { container: o.value })}
                  />
                  {o.label}
                  {off && <span className="hint">{off}</span>}
                </label>
              );
            })}
          </div>
        </Row>
        <Row label="Where the result goes">
          <div className="wf-save-options" role="radiogroup" aria-label="Where the result goes">
            {[
              { value: "replace", label: "Replace the original" },
              { value: "next_to", label: "Next to the original" },
              { value: "folder", label: "A different folder" },
            ].map((o) => (
              <label key={o.value}>
                <input type="radio" name={`where-${draftId}`} checked={save_.where === o.value} onChange={() => set("save", { where: o.value })} />
                {o.label}
              </label>
            ))}
          </div>
        </Row>
        {save_.where === "folder" && (
          <Row label="Folder" hint="Sub-folders of the library are kept.">
            <input className="inp" aria-label="Folder" value={String(save_.folder ?? "")} onChange={(e) => set("save", { folder: e.target.value })} />
          </Row>
        )}
        {save_.where === "replace" && (
          <p className="hint" style={{ margin: "0 0 8px" }}>The original goes to the trash and is kept 7 days. How long is set for all workflows in Settings › Processing.</p>
        )}
        <Row label="Add to the file name" hint={`media.mp4 becomes media-${save_.nameSuffix || "…"}.mp4. Empty keeps the name.`}>
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
            <input className="inp" aria-label="File to try" placeholder="File name" value={tryFile} onChange={(e) => setTryFile(e.target.value)} style={{ flex: "1 1 220px", minWidth: 0 }} />
            <button type="button" className="btn sm" onClick={onTry} disabled={!check}>Try</button>
          </div>
          {tryErr && <p className="bad">{tryErr.message}</p>}
          {tryLines && (
            <ul className="wf-try-lines">
              {tryLines.map((l) => (
                <li key={l.stepId + l.text}>
                  {STEP_NAME[l.stepId]}: {l.text}
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

// Quality: the 1–5 slider with its name, and the number it resolves to for the picture size.
function QualityRow({ value, format, card, size, off, onChange }: { value: number; format: string; card: boolean; size: string | null; off?: string; onChange: (q: number) => void }) {
  const ladder = useBitrateLadder();
  return (
    <div className={off ? "wf-dim" : undefined}>
      <Row label={`Quality · ${value} · ${QUALITY_NAMES[value - 1]}`} hint={off ?? (ladder ? qualityMeaning(ladder, value, format, card, size) : undefined)}>
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

function VideoAdvanced({ s, format, set, disabledRate }: { s: Record<string, unknown>; format: string; set: (patch: Record<string, unknown>) => void; disabledRate?: string }) {
  const rate = (s.rateControl as string | null) ?? null;
  const level = String(s.bitrateLevel ?? "medium");
  const sizeKey = s.size == null ? "keep" : String(s.size);
  const row = ROW_FOR_SIZE[String(s.size ?? "1080")] ?? "1080";
  const rec = useLadder(format, row);
  const recLine = rec?.line;
  const avgShown = level === "custom" ? (s.avgMbps ?? "") : recLine ? recLine[level as "low" | "medium" | "high"] : "";

  return (
    <>
      <Row label="Picture size">
        <select
          className="sel"
          aria-label="Picture size"
          value={sizeKey}
          onChange={(e) => set({ size: e.target.value === "keep" ? null : e.target.value })}
        >
          {SIZE_OPTIONS.map((o) => (
            <option key={o.value} value={o.value}>
              {o.label}
            </option>
          ))}
        </select>
      </Row>
      {sizeKey === "custom" && (
        <Row label="Custom size">
          <span style={{ display: "flex", gap: 8, alignItems: "center" }}>
            <input className="inp narrow" aria-label="Width" type="number" value={String(s.width ?? "")} onChange={(e) => set({ width: e.target.value ? Number(e.target.value) : null })} /> ×
            <input className="inp narrow" aria-label="Height" type="number" value={String(s.height ?? "")} onChange={(e) => set({ height: e.target.value ? Number(e.target.value) : null })} />
          </span>
        </Row>
      )}
      <Row label="Bitrate" hint={disabledRate}>
        <Seg
          label="Bitrate"
          value={rate ?? "quality"}
          options={[
            { value: "quality", label: "Quality" },
            { value: "vbr", label: format === "av1" ? "Variable bitrate (not with AV1)" : "Variable bitrate", disabled: format === "av1", title: format === "av1" ? "Not with AV1" : undefined },
            { value: "cbr", label: "Constant bitrate" },
          ]}
          onChange={(v) => set({ rateControl: v === "quality" ? null : v })}
        />
      </Row>
      {rate != null && (
        <>
          <Row label="Bitrate level">
            <Seg
              label="Bitrate level"
              value={level}
              options={[
                { value: "low", label: "Low" },
                { value: "medium", label: "Medium" },
                { value: "high", label: "High" },
                { value: "custom", label: "Custom" },
              ]}
              onChange={(v) => set({ bitrateLevel: v })}
            />
            {rec && recLine && (
              <p className="hint wf-hint">
                Recommended for {rec.rowLabel} {FORMAT_NAME[format] ?? format}: {recLine.low}–{recLine.high} Mb/s (Medium {recLine.medium}). More for HDR (+25%) and 50–60 fps (+40%), never above the source.
              </p>
            )}
          </Row>
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
            <Row label="At most Mb/s">
              <input className="inp narrow" aria-label="At most Mb/s" type="number" step="0.1" value={String(s.maxMbps ?? "")} onChange={(e) => set({ maxMbps: e.target.value ? Number(e.target.value) : null })} />
            </Row>
          )}
        </>
      )}
      <Row label="Speed">
        <select className="sel" aria-label="Speed" value={String(s.preset ?? "")} onChange={(e) => set({ preset: e.target.value || null })}>
          <option value="">Balanced (medium)</option>
          <option value="slow">Slower, smaller</option>
          <option value="fast">Faster, larger</option>
        </select>
      </Row>
      <Row label="Frame rate">
        <Seg
          label="Frame rate"
          value={s.fps == null ? "keep" : String(s.fps)}
          options={[
            { value: "keep", label: "Same as the source" },
            { value: "24", label: "24" },
            { value: "25", label: "25" },
            { value: "30", label: "30" },
            { value: "60", label: "60" },
          ]}
          onChange={(v) => set({ fps: v === "keep" ? null : Number(v) })}
        />
      </Row>
      <Row label="Colour depth">
        <select className="sel" aria-label="Colour depth" value={String(s.bitDepth ?? "")} onChange={(e) => set({ bitDepth: e.target.value ? Number(e.target.value) : null })}>
          <option value="">Same as the source</option>
          <option value="8">8-bit</option>
          <option value="10">10-bit</option>
        </select>
      </Row>
      <Row label="HDR">
        <select className="sel" aria-label="HDR" value={String(s.hdr ?? "keep")} onChange={(e) => set({ hdr: e.target.value === "keep" ? null : e.target.value })}>
          <option value="keep">Keep</option>
          <option value="sdr">Convert to SDR</option>
          <option value="leave">Leave HDR files alone</option>
        </select>
      </Row>
      <button
        type="button"
        className="btn ghost sm"
        onClick={() => set({ rateControl: null, size: null, width: null, height: null, bitrateLevel: "medium", avgMbps: null, maxMbps: null, preset: null, fps: null, bitDepth: null, hdr: null })}
      >
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

function AudioAdvanced({ s, set }: { s: Record<string, unknown>; set: (patch: Record<string, unknown>) => void }) {
  return (
    <>
      <Row label="Codec">
        <select className="sel" aria-label="Codec" value={String(s.codec ?? "")} onChange={(e) => set({ codec: e.target.value || null })}>
          <option value="">As picked above</option>
          <option value="aac">AAC</option>
          <option value="opus">Opus</option>
          <option value="ac3">AC-3</option>
          <option value="eac3">E-AC-3</option>
          <option value="flac">FLAC</option>
          <option value="copy">Copy the original</option>
        </select>
      </Row>
      <Row label="Bitrate (kb/s)" hint="Empty uses the codec's default.">
        <input className="inp narrow" aria-label="Bitrate (kb/s)" type="number" value={String(s.bitrateKbps ?? "")} onChange={(e) => set({ bitrateKbps: e.target.value ? Number(e.target.value) : null })} />
      </Row>
      <Row label="Downmix">
        <select className="sel" aria-label="Downmix" value={String(s.downmix ?? "")} onChange={(e) => set({ downmix: e.target.value || null })}>
          <option value="">None</option>
          <option value="clear_speech">Clear speech</option>
          <option value="plain_mix">Plain mix</option>
          <option value="pro_logic_ii">Dolby Pro Logic II</option>
        </select>
      </Row>
      <Row label="Loudness">
        <label style={{ display: "inline-flex", gap: 8, alignItems: "center" }}>
          <input type="checkbox" checked={Boolean(s.loudnorm)} onChange={(e) => set({ loudnorm: e.target.checked })} />
          Even out loudness
        </label>
      </Row>
      <Row label="Commentary">
        <Seg
          label="Commentary"
          value={String(s.commentary ?? "keep")}
          options={[
            { value: "keep", label: "Keep" },
            { value: "drop", label: "Drop" },
          ]}
          onChange={(v) => set({ commentary: v })}
        />
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
