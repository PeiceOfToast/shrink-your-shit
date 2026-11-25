// The guided builder, Basic (slice 12, G3): New workflow's first screen (A0), the questions Q1-Q10, the summary "This will…",
// and a workflow's Basic view (/workflows/{id}/basic). The server builds every step from the answers; this screen holds
// the answers, shows the server's preview, and sends the answers back. Advanced opens today's steps editor (G3b replaces it).
import { useEffect, useRef, useState, type ReactNode } from "react";
import { LoadFailed, PageHead } from "../PageHead";
import { failed, SignedOut, type Answer } from "../api";
import { useData } from "../useData";
import { navigate } from "../router";
import { createWorkflow, getDraft, getGuideInfo, previewGuide, publish, saveAnswers } from "../workflowApi";
import { applyGoal, basicDoesntAsk, basicSave, canBasic, questionCount, QUESTION_OF_FIELD, skipOn, subtitleChoices, type Answers, type Goal, type GuideInfo } from "../guide";
import type { Issue } from "../types/v1";
import type { ValidationIssue, WorkflowRevision } from "../types/v2";

const FS = { border: 0, padding: 0, margin: 0 } as const;
const GOALS: [Goal, string][] = [["smaller", "Smaller files that still look the same"], ["every_device", "Files that play on every device"], ["both", "Both: small and plays on most devices"]];
const GOAL_HINT: Record<Goal, string> = { smaller: "", every_device: "", both: "HEVC needs a TV or phone from 2016 on." };
const FORMATS: Record<string, string> = { hevc: "HEVC: small, plays on most TVs and phones since 2016", h264: "H.264: plays on everything, files much larger", av1: "AV1: smallest, slow to make, newer devices only" };
const FORMAT_NAME: Record<string, string> = { hevc: "HEVC", h264: "H.264", av1: "AV1" };
const RESOLUTIONS: [string, string, string?][] = [["keep", "Keep as is", "Videos are only ever made smaller."], ["1080", "At most 1080p"], ["720", "At most 720p"]];
const AUDIO: Record<string, string> = { stereo_aac_surround: "A stereo track, and the surround track kept as it is", stereo_aac: "A stereo track for every device; surround becomes stereo", eac3_surround: "The best surround for sound bars", as_is: "Leave the sound exactly as it is" };
const SUBTITLES: Record<string, string> = { keep_all: "Keep every subtitle", my_languages: "Keep only my languages", text_only: "Keep text subtitles only; picture subtitles are left out", srt: "Save my languages as separate .srt files" };
// The choices the plan offers. The server may offer more (vp9, ac3, opus); those show only when the workflow already has one.
const PLAN_FORMATS = ["hevc", "h264", "av1"], PLAN_AUDIO = ["stereo_aac_surround", "stereo_aac", "eac3_surround", "as_is"];
const offered = (list: string[], planned: string[], current: string) => list.filter((x) => planned.includes(x) || x === current);
const QUALITY = ["Smallest file", "Smaller", "Balanced", "Better quality", "Best quality"];
const FILE_TYPES: [string, string][] = [["mkv", "MKV"], ["mp4", "MP4"], ["keep", "Keep as is"]];
const ORIGINALS: [string, string, string?][] = [
  ["beside_library", "Keep the original; put the new file in a folder beside the library", "For example /media/movies (shrunk). The original stays where it is."],
  ["one_folder", "Keep the original; one folder for all new files", "The original stays where it is."],
  ["next_to_original", "Put the new file next to the original", "The original stays where it is."],
  ["replace", "Replace the original", "The original is kept in the trash for the days set in Settings > Safety."],
];
const QUESTIONS = ["What do you want?", "How much quality for how much space?", "Which video format?", "Picture size", "Sound", "Subtitles", "File type", "What happens to the original?", "Leave some files alone", "Afterwards"];
const SKIP_RES: [number, string][] = [[720, "720p"], [1080, "1080p"], [2160, "4K"]];

type Step = "entry" | "summary" | number;
type Pick = [string, string, string?];

// One group of choices, as radio cards with the legend as the question.
// hide: the question heading already says it; the legend stays for screen readers.
function Choice({ name, legend, value, options, onChange, hide }: { name: string; legend: string; value: string; options: Pick[]; onChange: (v: string) => void; hide?: boolean }) {
  return (
    <fieldset className="stack choice" style={FS}>
      <legend className={hide ? "sr" : "lab"}>{legend}</legend>
      {options.map(([v, label, hint]) => (
        <div className="radio" key={v}>
          <input type="radio" id={name + "-" + v} name={name} checked={value === v} onChange={() => onChange(v)} />
          <div style={{ minWidth: 0, flex: 1 }}>
            <label htmlFor={name + "-" + v}>{label}</label>
            {hint && <div className="hint" style={{ fontSize: "var(--fs)" }}>{hint}</div>}
          </div>
        </div>
      ))}
    </fieldset>
  );
}

// The languages for the sound and the subtitles: chips with a remove button, and a list to add one. Its codes are
// the person's languages for every workflow until per-workflow languages land (G2).
function LanguagePicker({ codes, set }: { codes: string[]; set: (c: string[]) => void }) {
  const [all, setAll] = useState<[string, string][]>([]);
  useEffect(() => { fetch("/languages.json").then((r) => r.json()).then((j: [string, string][]) => setAll(j.map(([c, n]) => [c, n] as [string, string]))).catch(() => {}); }, []);
  const name = (c: string) => all.find((l) => l[0] === c)?.[1] ?? c;
  const rest = all.filter((l) => !codes.includes(l[0]));
  return (
    <div className="stack" style={{ gap: 6 }}>
      <div className="row" style={{ flexWrap: "wrap", gap: 6 }}>
        {codes.map((c) => (
          <span className="mchip" key={c}><span>{name(c)}</span>
            <button type="button" className="btn sm ghost" aria-label={"Remove " + name(c)} onClick={() => set(codes.filter((x) => x !== c))}>×</button></span>
        ))}
        {codes.length === 0 && <span className="hint">No languages chosen: tracks with no language set are kept.</span>}
      </div>
      <div className="field"><label htmlFor="g-lang">Add a language</label>
        <select id="g-lang" className="input" value="" onChange={(e) => e.target.value && set([...codes, e.target.value])}>
          <option value="">Choose…</option>
          {rest.map(([c, n]) => <option key={c} value={c}>{n}</option>)}
        </select></div>
      <p className="hint">These are your languages for every workflow. Changing them here changes them everywhere (Settings &gt; General).</p>
    </div>
  );
}

// The tabs on a workflow's page: Basic (this screen's questions, greyed with its reason when they can't show it) and
// Advanced (the steps editor for now).
export function FlowTabs({ id, on }: { id: string; on: "basic" | "advanced" }) {
  const d = useData(() => getDraft(id), [id]);
  const g = d.data?.guide;
  const can = g ? canBasic(g) : true;
  return (
    <div className="stack" style={{ gap: 6 }}>
      <div className="tabs" role="tablist" aria-label="Edit as">
        <button role="tab" type="button" aria-selected={on === "basic"} aria-disabled={!can} title={!can ? g?.why : undefined}
          onClick={() => can && on !== "basic" && navigate("/workflows/" + id + "/basic")}>Basic</button>
        <button role="tab" type="button" aria-selected={on === "advanced"} onClick={() => on !== "advanced" && navigate("/workflows/" + id)}>Advanced</button>
      </div>
      {!can && g?.why && <p className="hint">Basic can't show this workflow. {g.why}</p>}
    </div>
  );
}

// Basic: the answers of a new workflow (no id) or of an existing one's draft (id).
export function Guided({ id }: { id?: string }) {
  const editing = id != null;
  const info = useData<GuideInfo>(getGuideInfo, []);
  const draft = useData<WorkflowRevision | null>(() => (id ? getDraft(id) : Promise.resolve(null)), [id]);
  const loaded = info.loaded && (!editing || draft.loaded);
  const g = draft.data?.guide;

  const [a, setA] = useState<Answers | null>(null);
  const [readBack, setReadBack] = useState<Answers | null>(null);
  const [etag, setEtag] = useState("");
  const [name, setName] = useState("Shrink videos");
  const [flow, setFlow] = useState<"basic" | "advanced">("basic");
  const [step, setStep] = useState<Step>(editing ? "summary" : "entry");
  const [err, setErr] = useState(""), [note, setNote] = useState(""), [busy, setBusy] = useState(false);
  const [lines, setLines] = useState<string[]>([]), [issues, setIssues] = useState<ValidationIssue[]>([]);
  const [done, setDone] = useState<{ id: string } | null>(null);
  const nameRef = useRef<HTMLInputElement>(null), headRef = useRef<HTMLHeadingElement>(null);

  // Start the answers: a new workflow with the Simple goal's defaults; an existing one as its draft reads.
  useEffect(() => {
    if (!loaded || a || !info.data) return;
    if (!editing) return setA({ ...info.data.defaults.smaller, languages: info.data.languages });
    if (g?.answers && canBasic(g) && draft.data) { setA(g.answers); setReadBack(g.answers); setEtag(draft.data.etag); }
  }, [loaded, a, info.data, draft.data, editing, g]);

  // Focus the new step once it is drawn: the first screen waits for the answers, so `a` is in the deps too.
  useEffect(() => { (step === "entry" ? nameRef : headRef).current?.focus(); }, [step, loaded, a]);

  // The summary is the server's sentences for the answers as they are now.
  useEffect(() => {
    if (step !== "summary" || !a) return;
    let live = true;
    previewGuide(payload()).then((r) => { if (live && !failed(r)) { setLines(r.summary); setIssues(r.validation); } });
    return () => { live = false; };
    // payload() reads only a, readBack and the languages list
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [step, a]);

  if (info.error && !info.data) return <LoadFailed error={info.error} reload={info.reload} />;
  if (editing && draft.error && !draft.data) return <LoadFailed error={draft.error} reload={draft.reload} />;

  const total = info.data ? questionCount(info.data) : 9;
  const picked = (x: Answers) => (x.languages.length ? x.languages : info.data?.languages ?? []);
  const payload = (): Answers => ({ ...basicSave(readBack, a!), languages: picked(a!), languagesEverywhere: true });
  const set = (p: Partial<Answers>) => setA((cur) => (cur ? { ...cur, ...p } : cur));
  const goTo = (s: Step) => { setStep(s); setErr(""); setNote(""); };

  // A refusal says what is wrong, and opens the question it belongs to.
  const ask = async <T,>(p: Promise<Answer<T>>): Promise<T | null> => {
    try {
      const r = await p;
      if (failed(r)) {
        const e = r.error as Issue & { field?: string };
        setErr(e.message);
        const q = e.field ? QUESTION_OF_FIELD[e.field] : undefined;
        if (q) setStep(q);
        return null;
      }
      return r;
    } catch (e) {
      if (e instanceof SignedOut) return null;
      throw e;
    }
  };

  const next = () => {
    if (step === "entry") return flow === "advanced" ? makeAdvanced() : goTo(1);
    if (typeof step !== "number") return;
    goTo(step < total ? step + 1 : "summary");
  };
  const back = () => {
    if (step === "summary") return goTo(total);
    if (step === 1) return goTo(editing ? "summary" : "entry");
    if (typeof step === "number") return goTo(step - 1);
  };

  // Advanced: the workflow as today's Simple preset, opened in the steps editor.
  const makeAdvanced = async () => {
    if (!name.trim()) { setErr("Give the workflow a name."); return nameRef.current?.focus(); }
    setBusy(true);
    const w = await ask(createWorkflow(name.trim(), ""));
    setBusy(false);
    if (w) navigate("/workflows/" + w.id);
  };

  // New workflow from the answers. "Create and use it" publishes it; "Save as a draft" stops at the draft.
  const make = async (then: "use" | "draft") => {
    if (!name.trim()) { setErr("Give the workflow a name."); return nameRef.current?.focus(); }
    setBusy(true); setErr("");
    const w = await ask(createWorkflow(name.trim(), "", payload()));
    if (!w) return setBusy(false);
    if (then === "draft") return navigate("/workflows/" + w.id);
    const d = await ask(getDraft(w.id));
    const p = d ? await ask(publish(w.id, d.etag)) : null;
    setBusy(false);
    if (p) return navigate("/workflows/" + w.id);
    setDone({ id: w.id }); // the draft exists; the message says why it isn't published
  };

  // Edit: the answers into the draft, with its etag. A draft changed elsewhere is refused (its message says so).
  const save = async () => {
    setBusy(true); setErr(""); setNote("");
    const r = await ask(saveAnswers(id!, payload(), etag));
    setBusy(false);
    if (!r) return;
    setEtag(r.etag); setReadBack(payload()); setNote("Saved.");
  };

  // A workflow the questions can't read: Basic says why and offers Advanced.
  if (editing && g && !canBasic(g)) {
    return (
      <div className="stack">
        <PageHead icon="i-workflow" title="Basic" sub="Answer the questions; the steps are made for you." />
        <FlowTabs id={id!} on="basic" />
        <section className="card"><div className="bd stack">
          <p className="st warn" role="status">Basic can't show this workflow.</p>
          {g.why && <p>{g.why}</p>}
          <div className="row"><button className="btn primary" type="button" onClick={() => navigate("/workflows/" + id)}>Open in Advanced</button></div>
        </div></section>
      </div>
    );
  }
  if (!loaded || !a) return <p className="muted" role="status">Loading…</p>;

  const q = typeof step === "number" ? step : 0;
  const sk = skipOn(a.skip);

  const questionBody = (n: number): ReactNode => {
    const av = info.data!.available;
    switch (n) {
      case 1: return <Choice name="goal" legend="What do you want?" hide value={a.goal} onChange={(v) => setA((cur) => applyGoal(cur!, v as Goal, info.data!))}
        options={GOALS.map(([v, l]) => [v, l, GOAL_HINT[v] || undefined] as Pick)} />;
      case 2: return (
        <fieldset className="stack" style={FS}><legend className="sr">How much quality for how much space?</legend>
          <div className="stack" style={{ gap: 6 }}>
            <input type="range" min={1} max={5} step={1} value={a.quality} aria-label="Quality" aria-valuetext={QUALITY[a.quality - 1]} onChange={(e) => set({ quality: Number(e.target.value) })} />
            <div className="hint" style={{ fontSize: "var(--fs)" }}>{QUALITY[a.quality - 1]}</div>
          </div>
          <label className="row"><input type="checkbox" checked={a.slower} onChange={(e) => set({ slower: e.target.checked })} /> Take longer for smaller files</label>
        </fieldset>);
      case 3: return <Choice name="format" legend="Which video format?" hide value={a.format}
        options={offered(info.data!.available.formats, PLAN_FORMATS, a.format).map((f) => [f, FORMAT_NAME[f] ?? f, FORMATS[f]] as Pick)} onChange={(v) => set({ format: v })} />;
      case 4: return <Choice name="resolution" legend="Picture size" hide value={a.resolution} options={RESOLUTIONS} onChange={(v) => set({ resolution: v })} />;
      case 5: return (
        <div className="stack">
          <Choice name="audio" legend="What happens to the sound" value={a.audio} options={offered(av.audio, PLAN_AUDIO, a.audio).map((v) => [v, AUDIO[v] ?? v] as Pick)} onChange={(v) => set({ audio: v })} />
          <Choice name="langs" legend="Which languages to keep" value={a.allLanguages ? "all" : "mine"}
            options={[["mine", "Only these languages", "Tracks with no language set are always kept."], ["all", "Every language"]]} onChange={(v) => set({ allLanguages: v === "all" })} />
          {!a.allLanguages && <LanguagePicker codes={picked(a)} set={(languages) => set({ languages })} />}
        </div>);
      case 6: return (
        <div className="stack">
          <Choice name="subtitles" legend="Subtitles" hide value={a.subtitles} options={subtitleChoices(av.subtitles).map((v) => [v, SUBTITLES[v] ?? v, v === "keep_all" ? "Subtitle pictures are never burned into the video." : undefined] as Pick)} onChange={(v) => set({ subtitles: v })} />
          {a.subtitles === "my_languages" && <p className="hint">Uses the languages from the sound question.</p>}
        </div>);
      case 7: return (
        <div className="stack">
          <Choice name="fileType" legend="File type" hide value={a.fileType} options={FILE_TYPES} onChange={(v) => set({ fileType: v })} />
          {a.fileType === "mp4" && a.subtitles === "keep_all" && (
            <div className="stack" style={{ gap: 6 }}>
              <p className="hint">Files with picture subtitles are left alone: MP4 can't hold them.</p>
              <div className="row"><button className="btn sm" type="button" onClick={() => set({ subtitles: "text_only" })}>Switch to text subtitles only</button></div>
            </div>)}
        </div>);
      case 8: return (
        <div className="stack">
          <Choice name="original" legend="What happens to the original?" hide value={a.original} options={ORIGINALS} onChange={(v) => set({ original: v })} />
          {a.original === "one_folder" && <div className="field"><label htmlFor="g-folder">Folder for the new files</label>
            <input id="g-folder" className="input" value={a.folder} onChange={(e) => set({ folder: e.target.value })} /></div>}
          <Choice name="ifBigger" legend="If the new file turns out bigger" value={a.ifBigger}
            options={[["keep", "Keep the original", "The new file is not used."], ["replace", "Use it anyway", "The new file replaces the original even when it is bigger."]]} onChange={(v) => set({ ifBigger: v })} />
        </div>);
      case 9: return (
        <div className="stack">
          <p className="hint">Always left alone: files already in the chosen format, AV1 and VP9 files, Dolby Vision, files already efficient, files shared with another folder, and files still changing.</p>
          <label className="row"><input type="checkbox" checked={sk.size} onChange={(e) => set({ skip: { ...a.skip, minSizeGB: e.target.checked ? 2 : 0 } })} /> Skip files smaller than</label>
          {sk.size && <div className="field"><label htmlFor="g-size">Size in GB</label>
            <input id="g-size" className="input sm" type="number" min={0} step={0.5} value={a.skip.minSizeGB} onChange={(e) => set({ skip: { ...a.skip, minSizeGB: Number(e.target.value) } })} /></div>}
          <label className="row"><input type="checkbox" checked={sk.res} onChange={(e) => set({ skip: { ...a.skip, minResolution: e.target.checked ? 1080 : 0 } })} /> Only files at least</label>
          {sk.res && <div className="field"><label htmlFor="g-res">Resolution</label>
            <select id="g-res" className="input sm" value={a.skip.minResolution} onChange={(e) => set({ skip: { ...a.skip, minResolution: Number(e.target.value) } })}>
              {SKIP_RES.map(([v, l]) => <option key={v} value={v}>{l}</option>)}</select></div>}
          <label className="row"><input type="checkbox" checked={sk.minutes} onChange={(e) => set({ skip: { ...a.skip, minMinutes: e.target.checked ? 30 : 0 } })} /> Only files longer than</label>
          {sk.minutes && <div className="field"><label htmlFor="g-min">Minutes</label>
            <input id="g-min" className="input sm" type="number" min={0} value={a.skip.minMinutes} onChange={(e) => set({ skip: { ...a.skip, minMinutes: Number(e.target.value) } })} /></div>}
          <label className="row"><input type="checkbox" checked={a.includeDolbyVision} onChange={(e) => set({ includeDolbyVision: e.target.checked })} /> Include Dolby Vision files (they become HDR10)</label>
        </div>);
      case 10: return (
        <div className="stack">
          {av.after.mediaServer && <label className="row"><input type="checkbox" checked={a.after.mediaServer} onChange={(e) => set({ after: { ...a.after, mediaServer: e.target.checked } })} /> Tell the media server</label>}
          {av.after.downloader && <label className="row"><input type="checkbox" checked={a.after.downloader} onChange={(e) => set({ after: { ...a.after, downloader: e.target.checked } })} /> Tell the downloader</label>}
          {av.after.notifyTargets.length > 0 && <>
            <label className="row"><input type="checkbox" checked={a.after.notify} onChange={(e) => set({ after: { ...a.after, notify: e.target.checked } })} /> Send me a message</label>
            {a.after.notify && <div className="field"><label htmlFor="g-target">Message to</label>
              <select id="g-target" className="input" value={a.after.notifyTarget} onChange={(e) => set({ after: { ...a.after, notifyTarget: e.target.value } })}>
                <option value="">Every target that is on</option>
                {av.after.notifyTargets.map((t) => <option key={t.id} value={t.name}>{t.name}</option>)}</select></div>}
          </>}
          <p className="hint">Without these, the connections are told the way Settings &gt; Connections says.</p>
        </div>);
      default: return null;
    }
  };

  const extra = editing && g ? basicDoesntAsk(g.extra, readBack?.more) : [];
  const heading = (text: string) => <h2 ref={headRef} tabIndex={-1} style={{ outline: "none" }}>{text}</h2>;

  return (
    <div className="stack">
      <PageHead icon="i-workflow" title={editing ? "Basic" : "New workflow"} sub="Answer the questions; the steps are made for you.">{null}</PageHead>
      {editing && <FlowTabs id={id!} on="basic" />}
      {err && <p className="st bad" role="alert">{err}</p>}
      {note && <p className="muted" role="status">{note}</p>}
      {done && <section className="card"><div className="bd stack"><p role="status">{name} is ready, but it isn't published yet: {err || "open it to see why."} <a href={"/workflows/" + done.id} onClick={(e) => (e.preventDefault(), navigate("/workflows/" + done.id))}>Open it</a></p></div></section>}

      {step === "entry" && (
        <section className="card" aria-label="New workflow"><div className="bd stack">
          {heading("Start")}
          <div className="field"><label htmlFor="g-name">Name</label>
            <input id="g-name" ref={nameRef} className="input" value={name} aria-invalid={!!err && !name.trim()} onChange={(e) => (setName(e.target.value), setErr(""))} /></div>
          <Choice name="flow" legend="How do you want to build it?" value={flow}
            options={[["basic", "Basic", "Answer a few questions. The steps are made for you."], ["advanced", "Advanced", "Open the steps and set each one yourself."]]} onChange={(v) => setFlow(v as "basic" | "advanced")} />
          <div className="row">
            <button className="btn primary" type="button" disabled={busy} onClick={next}>Next</button>
            <button className="btn ghost" type="button" onClick={() => navigate("/workflows")}>Cancel</button>
          </div>
        </div></section>
      )}

      {typeof step === "number" && (
        <section className="card" aria-label={"Question " + q + " of " + total}><div className="bd stack">
          <p className="muted">Question {q} of {total}</p>
          {heading(QUESTIONS[q - 1])}
          {questionBody(q)}
          <div className="row">
            <button className="btn ghost" type="button" onClick={back}>Back</button>
            <button className="btn primary" type="button" onClick={next}>Next</button>
          </div>
        </div></section>
      )}

      {step === "summary" && (
        <section className="card" aria-label="Summary"><div className="bd stack">
          {heading("This will…")}
          {extra.length > 0 && <p className="hint">Basic doesn't ask about: {extra.join(", ")}. They stay as they are.</p>}
          {g?.coverage === "partial" && <p className="hint">This workflow also has: {g.extra.join(", ")}. They are kept as they are; change them in the steps.</p>}
          {lines.length > 0 ? <ul className="stack" style={{ gap: 4 }}>{lines.map((l, i) => <li key={i}>{l}</li>)}</ul> : <p className="muted">Working out the sentences…</p>}
          {issues.length > 0 && <ul>{issues.map((x, i) => <li key={i} className="bad">{x.message}</li>)}</ul>}
          <div className="row" style={{ flexWrap: "wrap" }} aria-label="Change an answer">
            {Array.from({ length: total }, (_, i) => i + 1).map((n) => (
              <button key={n} className="btn sm ghost" type="button" onClick={() => goTo(n)}>Change {n}</button>))}
          </div>
          <div className="row" style={{ flexWrap: "wrap", alignItems: "flex-end" }}>
            {editing ? (
              <button className="btn primary" type="button" disabled={busy} onClick={save}>Save changes</button>
            ) : (<>
              <div className="field" style={{ flex: "1 1 240px", minWidth: 0 }}><label htmlFor="g-name2">Name</label>
                <input id="g-name2" className="input" value={name} onChange={(e) => (setName(e.target.value), setErr(""))} /></div>
              <button className="btn primary" type="button" disabled={busy} onClick={() => make("use")}>Create and use it</button>
              <button className="btn ghost" type="button" disabled={busy} onClick={() => make("draft")}>Save as a draft</button>
            </>)}
            <button className="btn ghost" type="button" onClick={back}>Back</button>
          </div>
        </div></section>
      )}
    </div>
  );
}
