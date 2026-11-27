// Workflow flow calls for the mock (pages 06 and 07): the guide, the answers, the three-step flow, the check,
// the recommended bitrates, saves, publish and the try. A session's edits live in memory, as the app's do.
import type { Answers, BitrateLadder, Flow, FlowCheck, FlowProblem, FlowStep, GuideDoc, GuidePreview, StepDefinition, StepId, TryLine } from "./flowTypes.ts";
import { STEP_DEFINITIONS } from "./fixtures/stepDefinitions.ts";
import { ApiError } from "./errors.ts";
import { respond } from "./mockState.ts";
import { createWorkflowRow, peekWorkflowRow, updateWorkflowRow } from "./mockWorkflows.ts";
import { languageName } from "../format/language.ts";

// The answers a new workflow starts from (page 06 "New": sensible defaults ticked).
export const DEFAULT_ANSWERS: Answers = {
  quality: 3,
  slower: false,
  format: "hevc",
  resolution: "keep",
  useGpu: true,
  ifGpuFails: "cpu",
  audio: "stereo_aac",
  allLanguages: false,
  languages: ["eng", "ger"],
  languagesEverywhere: false,
  subtitles: "keep_all",
  subtitleLanguages: ["eng", "ger"],
  fileType: "mkv",
  includeDolbyVision: false,
  original: "next_to_original",
  folder: "",
};

const FORMAT_LABEL: Record<string, string> = { hevc: "HEVC", h264: "H.264", av1: "AV1" };
const CODEC_LABEL: Record<string, string> ={ aac: "AAC", opus: "Opus", ac3: "AC-3", eac3: "E-AC-3", flac: "FLAC", copy: "Same as the source" };
const CONTAINER_LABEL: Record<string, string> = { mkv: "MKV", mp4: "MP4", webm: "WebM", keep: "Keep the same" };
const CHANNEL_LABEL: Record<string, string> ={ stereo: "stereo", source: "same channels as the source", mono: "mono", "5.1": "5.1" };


const LADDER: BitrateLadder = {
  unit: "Mbps",
  rows: [
    { row: "2160", label: "2160p" },
    { row: "1440", label: "1440p" },
    { row: "1080", label: "1080p" },
    { row: "720", label: "720p" },
    { row: "sd", label: "480p (SD)" },
  ],
  formats: {
    hevc: {
      "2160": { low: 7.8, medium: 12, high: 18 },
      "1440": { low: 3.6, medium: 5.4, high: 7.8 },
      "1080": { low: 1.8, medium: 3.6, high: 5.4 },
      "720": { low: 1.5, medium: 2.4, high: 3.6 },
      sd: { low: 0.6, medium: 0.9, high: 1.5 },
    },
    h264: {
      "2160": { low: 13, medium: 20, high: 30 },
      "1440": { low: 6, medium: 9, high: 13 },
      "1080": { low: 3, medium: 6, high: 9 },
      "720": { low: 2.5, medium: 4, high: 6 },
      sd: { low: 1, medium: 1.5, high: 2.5 },
    },
    av1: {
      "2160": { low: 6.5, medium: 10, high: 15 },
      "1440": { low: 3, medium: 4.5, high: 6.5 },
      "1080": { low: 1.5, medium: 3, high: 4.5 },
      "720": { low: 1.25, medium: 2, high: 3 },
      sd: { low: 0.5, medium: 0.75, high: 1.25 },
    },
  },
  vbrFormats: ["hevc", "h264"],
  // The Quality slider (page 07, rev 4): the processor's CRF at 1080p, and the offsets from the app's own tables.
  slider: [
    { position: 1, name: "Smallest file", crf: 27 },
    { position: 2, name: "Smaller", crf: 25 },
    { position: 3, name: "Balanced", crf: 23 },
    { position: 4, name: "Better quality", crf: 21 },
    { position: 5, name: "Best quality", crf: 19 },
  ],
  crfOffset: { hevc: 0, h264: -5, av1: 9 },
  rowCrfShift: { "2160": 1, "1440": 0, "1080": 0, "720": -1, sd: -1 },
  resolutionRow: { "2160": "2160", "1080": "1080", "720": "720", "480": "sd" },
  cardOffsets: { qsv: { "1": -2, "2": -2, "3": -1, "4": -1, "5": -1 } },
  factors: { fps60: 1.4, hdr: 1.25 },
  vbrMaxFactor: 1.5,
};

// The guided page's questions (page 06, "Data and API"). Built from the step definitions in the app; here it is the same shape.
const GUIDE: GuideDoc = {
  chips: [
    { id: "shrink_hevc", label: "Shrink to HEVC", always: false, on: { format: "hevc" }, off: { format: "h264" }, available: true },
    { id: "keep_my_languages", label: "Keep only my languages", always: false, on: { allLanguages: false }, off: { allLanguages: true }, available: true },
    { id: "use_gpu", label: "Use the graphics card (QSV)", always: false, on: { useGpu: true }, off: { useGpu: false }, available: true },
    { id: "never_worse", label: "Never make anything worse", always: true, on: {}, off: {}, available: true },
    { id: "hdr_kept", label: "HDR kept, Dolby Vision left alone", always: true, on: {}, off: {}, available: true },
  ],
  parts: [
    {
      id: "video",
      label: "Video",
      fields: [
        {
          key: "format",
          label: "Format",
          type: "choice",
          choices: [
            { value: "hevc", label: "HEVC", available: true },
            { value: "h264", label: "H.264", available: true },
            { value: "av1", label: "AV1", available: true },
          ],
        },
        { key: "quality", label: "Quality", type: "number" },
        { key: "useGpu", label: "Use the graphics card", type: "yesno", more: true },
        {
          key: "ifGpuFails",
          more: true,
          label: "If the graphics card can't do it",
          type: "choice",
          choices: [
            { value: "cpu", label: "Use the processor", available: true },
            { value: "stop", label: "Stop and tell me", available: true },
          ],
        },
        {
          key: "resolution",
          label: "Picture size",
          type: "choice",
          choices: [
            { value: "keep", label: "Same as the source", available: true },
            { value: "1080", label: "1080p", available: true },
            { value: "720", label: "720p", available: true },
            { value: "480", label: "480p", available: true },
          ],
        },
      ],
    },
    {
      id: "audio",
      label: "Audio",
      fields: [
        {
          key: "audio",
          label: "Audio",
          type: "choice",
          choices: [
            { value: "stereo_aac", label: "Stereo AAC", available: true },
            { value: "stereo_aac_surround", label: "AAC, same channels as the source", available: true },
            { value: "eac3_surround", label: "E-AC-3, surround kept", available: true },
            { value: "ac3_surround", label: "AC-3, surround kept", available: true },
            { value: "stereo_opus", label: "Stereo Opus", available: true },
            { value: "as_is", label: "Leave as it is", available: true },
          ],
        },
        { key: "languages", label: "Keep these languages", type: "languages" },
      ],
    },
    {
      id: "subtitles",
      label: "Subtitles",
      fields: [
        {
          key: "subtitles",
          label: "Subtitles",
          type: "choice",
          choices: [
            { value: "keep_all", label: "Keep all", available: true },
            { value: "my_languages", label: "Keep my languages", available: true },
            { value: "text_only", label: "Text only", available: true },
            { value: "srt", label: "Make SRT files", available: true },
          ],
        },
        { key: "subtitleLanguages", label: "Subtitle languages", type: "languages" },
      ],
    },
    {
      id: "file_type",
      label: "File type",
      fields: [
        {
          key: "fileType",
          label: "File type",
          type: "choice",
          choices: [
            { value: "mkv", label: "MKV", available: true },
            { value: "mp4", label: "MP4", available: true },
            { value: "webm", label: "WebM", available: true },
            { value: "keep", label: "Keep the same", available: true },
          ],
        },
      ],
    },
    {
      id: "original",
      label: "The original",
      fields: [
        {
          key: "original",
          label: "Where the new file goes",
          type: "choice",
          choices: [
            { value: "replace", label: "Replace the original", available: false, why: "Replacing the original comes in a later update; for now the converted file is saved next to it." },
            { value: "next_to_original", label: "Next to the original", available: true },
            { value: "one_folder", label: "A different folder", available: true },
          ],
        },
        { key: "folder", label: "Folder", type: "folder" },
      ],
    },
  ],
  defaults: DEFAULT_ANSWERS,
  languages: ["eng", "ger"],
};

// The three fixed steps and their settings, from the answers the guided page sends. The server does this in the app.
const AUDIO_FROM_GUIDED: Record<string, { format: string; channels: string; codec: string | null }> = {
  stereo_aac: { format: "aac", channels: "stereo", codec: null },
  stereo_aac_surround: { format: "aac", channels: "source", codec: null },
  eac3_surround: { format: "aac", channels: "source", codec: "eac3" },
  ac3_surround: { format: "aac", channels: "source", codec: "ac3" },
  stereo_opus: { format: "opus", channels: "stereo", codec: null },
  as_is: { format: "copy", channels: "source", codec: null },
};

// The file types and what each can hold (page 07; dev's selectors.go). MKV and Keep the same take anything.
// The rules are one table: a new rule goes here, not into the editor or the guide.
const CONTAINER_RULES: Record<string, { why: string; ok: (video: string, audio: string) => boolean }> = {
  mkv: { why: "", ok: () => true },
  mp4: { why: "MP4 can't hold Opus audio.", ok: (_v, audio) => audio !== "opus" },
  webm: { why: "WebM needs AV1 video and Opus audio.", ok: (video, audio) => video === "av1" && audio === "opus" },
  keep: { why: "", ok: () => true },
};

// The audio codec the step will write: Advanced's codec when set, else the Format choice.
function audioCodec(au: Record<string, unknown>): string {
  return String(au.codec ?? au.format);
}

function step(id: StepId, type: FlowStep["type"], settings: Record<string, unknown>): FlowStep {
  return { id, type, version: 1, settings };
}

export function answersToFlow(a: Answers, meta: { name: string; description: string }): Flow {
  const audio = AUDIO_FROM_GUIDED[String(a.audio ?? "stereo_aac")] ?? AUDIO_FROM_GUIDED.stereo_aac;
  const original = String(a.original ?? "replace");
  const where = original === "replace" ? "replace" : original === "one_folder" ? "folder" : "next_to";
  const size = a.resolution && a.resolution !== "keep" ? String(a.resolution) : null;
  return {
    name: meta.name,
    description: meta.description,
    steps: [
      step("video", "video.convert", {
        format: a.format ?? "hevc",
        quality: a.quality ?? 3,
        useGpu: a.useGpu ?? true,
        ifGpuFails: a.ifGpuFails ?? "cpu",
        size,
        width: null,
        height: null,
        rateControl: null,
        bitrateLevel: "medium",
        avgMbps: null,
        maxMbps: null,
        twoPass: false,
        preset: "normal",
        fps: null,
        bitDepth: null,
        hdr: "keep",
        keepDolbyVision: false,
      }),
      step("audio", "audio.tracks", {
        format: audio.format,
        channels: audio.channels,
        languages: Array.isArray(a.languages) ? a.languages : [],
        codec: audio.codec,
        bitrateKbps: null,
        bitratePer: "channel",
        sampleRate: null,
        downmix: null,
        loudnorm: false,
        commentary: "keep",
        defaultTrack: null,
        keepOriginal: false,
        subtitles: a.subtitles ?? "keep_all",
        subtitleLanguages: Array.isArray(a.subtitleLanguages) ? a.subtitleLanguages : [],
      }),
      step("save", "file.save", {
        where,
        folder: where === "folder" ? String(a.folder ?? "") : null,
        nameSuffix: "",
        container: a.fileType ?? "mkv",
      }),
    ],
  };
}

// The Recommended bitrate for a video step, in Mb/s. A set custom value wins only when the level is "custom".
function bitrateFor(s: Record<string, unknown>): number {
  const row = ({ "2160": "2160", "1440": "1440", "1080": "1080", "720": "720", "480": "sd" } as Record<string, string>)[String(s.size ?? "1080")] ?? "1080";
  const format = String(s.format ?? "hevc") as "hevc" | "h264" | "av1";
  const level = String(s.bitrateLevel ?? "medium");
  if (level === "custom") return Number(s.avgMbps ?? 0);
  return LADDER.formats[format][row][level as "low" | "medium" | "high"];
}

// The check (page 07 "Data and API"): the problems, the greyed fields and the closed cards' one-liners.
// A flow is exactly the three steps, Video, Audio and Save the result. Returns the steps by id.
export function assertThreeSteps(flow: Flow): Record<StepId, FlowStep> {
  const byId = Object.fromEntries(flow.steps.map((s) => [s.id, s])) as Partial<Record<StepId, FlowStep>>;
  if (flow.steps.length !== 3 || !byId.video || !byId.audio || !byId.save) {
    throw new ApiError("invalid_flow", "A flow has three steps: Video, Audio and Save the result.");
  }
  return byId as Record<StepId, FlowStep>;
}

export function checkFlow(flow: Flow): FlowCheck {
  const byId = assertThreeSteps(flow);
  const v = byId.video.settings;
  const au = byId.audio.settings;
  const sv = byId.save.settings;
  const problems: FlowProblem[] = [];
  const disabled: FlowCheck["disabled"] = [];

  if (v.rateControl === "vbr" && !LADDER.vbrFormats.includes(String(v.format))) {
    problems.push({ stepId: "video", field: "rateControl", code: "vbr_av1", message: "Variable bitrate doesn't work with AV1 yet. Pick Constant bitrate or Quality, or another format." });
  }
  if (sv.where === "folder" && !sv.folder) {
    problems.push({ stepId: "save", field: "folder", code: "folder_required", message: "Choose the folder for the new file." });
  }
  if (v.rateControl) {
    disabled.push({ stepId: "video", field: "quality", why: "Not used: Advanced sets a bitrate instead." });
  }
  if (au.codec) {
    disabled.push({ stepId: "audio", field: "format", why: `Advanced is set: ${CODEC_LABEL[String(au.codec)]}. It replaces Format above.` });
  }
  // The file types the video and audio can go into. A pick the table rules out is greyed, and a saved pick that is ruled out is a problem.
  for (const value of Object.keys(CONTAINER_RULES)) {
    const rule = CONTAINER_RULES[value];
    if (rule.ok(String(v.format), audioCodec(au))) continue;
    disabled.push({ stepId: "save", field: "container", value, why: rule.why });
    if (sv.container === value) {
      problems.push({ stepId: "save", field: "container", code: "container_unsupported", message: `${rule.why} Pick another file type.` });
    }
  }

  const videoSummary = v.rateControl
    ? `${FORMAT_LABEL[String(v.format)]} · ${v.rateControl === "vbr" ? "variable" : "constant"} bitrate about ${bitrateFor(v)} Mb/s`
    : `${FORMAT_LABEL[String(v.format)]} · quality ${v.quality} · graphics card ${v.useGpu ? "automatic" : "off"}`;
  const langs = Array.isArray(au.languages) && au.languages.length ? (au.languages as string[]).map(languageName).join(" and ") : "my languages";
  // The subtitles part follows the Subtitles choice; with no subtitle languages picked, they are the audio's.
  const subLangs = Array.isArray(au.subtitleLanguages) && au.subtitleLanguages.length ? (au.subtitleLanguages as string[]).map(languageName).join(" and ") : langs;
  const subPart = au.subtitles === "srt" ? "SRT files"
    : au.subtitles === "text_only" ? "text only"
    : au.subtitles === "my_languages" ? `subtitles: ${subLangs}`
    : "subtitles: keep all";
  const audioSummary = `${CODEC_LABEL[String(au.codec ?? au.format)]} · ${CHANNEL_LABEL[String(au.channels)]} · ${langs} · ${subPart}`;
  // The file type, then where the result goes. The trash line is only for replacing the original.
  const where = String(sv.where);
  const placed = where === "replace"
    ? "replace the original · old file in the trash 7 days"
    : where === "folder"
      ? `a different folder${sv.folder ? `: ${String(sv.folder)}` : ""}`
      : "next to the original";
  const saveSummary = `${CONTAINER_LABEL[String(sv.container ?? "mkv")] ?? "MKV"} · ${placed}`;

  return { problems, disabled, summaries: { video: videoSummary, audio: audioSummary, save: saveSummary } };
}

// The guide's sentences ("What this does") from the answers. The flow and the check come from the same mapping as the editor.
function sentencesFor(flow: Flow, a: Answers): string[] {
  const v = flow.steps[0].settings;
  const au = flow.steps[1].settings;
  const sv = flow.steps[2].settings;
  const list = (xs: unknown) => (Array.isArray(xs) ? (xs as string[]).map(languageName).join(" and ") : "");
  const sub = list(a.subtitleLanguages) || "every language";
  const subtitles =
    a.subtitles === "srt" ? "Subtitles are made into SRT files."
    : a.subtitles === "text_only" ? "Only text subtitles are kept."
    : a.subtitles === "my_languages" ? `Keeps subtitles in ${sub}.`
    : "Keeps every subtitle.";
  const out = [
    `Converts video to ${FORMAT_LABEL[String(v.format)]} at quality ${v.quality}, ${v.size ? `at most ${String(v.size)}p` : "keeping the picture size"}${v.useGpu ? ", on the graphics card when it can" : ""}.`,
    `Audio: ${CODEC_LABEL[String(au.codec ?? au.format)]}, ${CHANNEL_LABEL[String(au.channels)]}${list(au.languages) ? `, in ${list(au.languages)}` : ", every language"}.`,
    subtitles,
    a.fileType === "keep" ? "The file keeps its container." : `Saves as ${({ mkv: "MKV", mp4: "MP4", webm: "WebM" } as Record<string, string>)[String(a.fileType)] ?? String(a.fileType)}.`,
  ];
  if (sv.where === "replace") out.push("Replaces the original. The old file goes to the trash and is kept 7 days.");
  else if (sv.where === "folder") out.push(`Puts the new file in ${sv.folder ? String(sv.folder) : "the folder you pick"}; the original stays where it is.`);
  else out.push("Puts the new file next to the original; the original stays where it is.");
  return out;
}

// ---- Documents: one per workflow id, kept in memory for the session.
interface Doc {
  flow: Flow;
  answers: Answers;
  rev: number;
  fitsGuided: boolean;
}
const DOCS = new Map<string, Doc>();

function docFor(id: string): Doc {
  const hit = DOCS.get(id);
  if (hit) return hit;
  const row = peekWorkflowRow(id);
  if (!row) throw new ApiError("workflow_not_found", "That workflow doesn't exist.");
  const doc: Doc = {
    flow: answersToFlow(DEFAULT_ANSWERS, { name: row.name, description: row.description }),
    answers: { ...DEFAULT_ANSWERS },
    rev: 1,
    fitsGuided: row.fitsGuided,
  };
  DOCS.set(id, doc);
  return doc;
}

function checkQuality(a: Answers): void {
  const q = Number(a.quality);
  if (!Number.isInteger(q) || q < 1 || q > 5) throw new ApiError("invalid_answer", "Quality is 1 to 5.", "quality");
}

// The cards' settings: the step definitions, a copy of the golden (the server's GET /api/v2/step-definitions).
export function getStepDefinitions(): Promise<StepDefinition[]> {
  return Promise.resolve(STEP_DEFINITIONS);
}

export function getGuide(): Promise<GuideDoc> {
  return respond(() => GUIDE);
}

// Opening the guided page: the saved answers (or the defaults for a new one), and whether the guide still fits.
export function getGuideAnswers(id: string): Promise<{ answers: Answers; fitsGuided: boolean }> {
  return respond(() => {
    if (id === "new") return { answers: { ...DEFAULT_ANSWERS }, fitsGuided: true };
    const d = docFor(id);
    return { answers: { ...d.answers }, fitsGuided: d.fitsGuided };
  });
}

// "What this does": the server turns the answers into the flow and sentences. Nothing is saved.
export function guidePreview(answers: Answers): Promise<GuidePreview> {
  return respond(() => {
    checkQuality(answers);
    const flow = answersToFlow(answers, { name: "", description: "" });
    const check = checkFlow(flow);
    return { sentences: sentencesFor(flow, answers), flow, problems: check.problems, disabled: check.disabled };
  });
}

// Save as a draft from the guided page: stores the answers and writes the flow into the draft.
export function putAnswers(id: string, answers: Answers): Promise<{ flow: Flow; rev: number }> {
  return respond(() => {
    checkQuality(answers);
    const d = docFor(id);
    d.answers = { ...answers };
    d.flow = answersToFlow(answers, { name: d.flow.name, description: d.flow.description });
    d.rev += 1;
    d.fitsGuided = true;
    updateWorkflowRow(id, {});
    return { flow: d.flow, rev: d.rev };
  });
}

// The editor's load: the flow and the draft's revision.
export function getFlow(id: string): Promise<{ flow: Flow; rev: number; fitsGuided: boolean }> {
  return respond(() => {
    const d = docFor(id);
    return { flow: d.flow, rev: d.rev, fitsGuided: d.fitsGuided };
  });
}

// Save as a draft from the editor. A draft saved elsewhere since baseRev is refused (dev's changed_since).
export function putDraft(id: string, flow: Flow, baseRev: number): Promise<{ rev: number }> {
  return respond(() => {
    const d = docFor(id);
    if (baseRev !== d.rev) {
      throw new ApiError("changed_since", "This workflow was saved somewhere else since you opened it. Reload to see the newer version.");
    }
    checkFlow(flow);
    d.flow = flow;
    d.fitsGuided = false;
    d.rev += 1;
    updateWorkflowRow(id, { name: flow.name.trim() || peekWorkflowRow(id)!.name, description: flow.description });
    return { rev: d.rev };
  });
}

export function validateFlow(flow: Flow): Promise<FlowCheck> {
  return respond(() => checkFlow(flow));
}

// Publish: refused while the check has an error, so libraries never get a broken flow.
export function publishFlow(id: string, flow: Flow): Promise<{ rev: number; published: true }> {
  return respond(() => {
    const check = checkFlow(flow);
    if (check.problems.length > 0) {
      throw new ApiError("has_problems", `Fix this first: ${check.problems[0].message}`);
    }
    const d = docFor(id);
    d.flow = flow;
    d.fitsGuided = false;
    d.rev += 1;
    updateWorkflowRow(id, { state: "published", problemCount: 0, name: flow.name.trim() || peekWorkflowRow(id)!.name, description: flow.description, fitsGuided: false });
    return { rev: d.rev, published: true as const };
  });
}

// The first save of a new workflow (the picker's Start, then Save as a draft): makes the row and returns its id and rev 1.
// Route: POST /api/v2/workflows with {name, description?, answers} (the guide) or {name, description?, flow} (the editor).
// A flow that isn't the three steps is refused with invalid_flow.
export function createDraft(input: { name: string; description?: string; answers?: Answers; flow?: Flow }): Promise<{ id: string; rev: number }> {
  return respond(() => {
    if (input.flow) assertThreeSteps(input.flow);
    const name = input.name.trim() || "New workflow";
    const description = input.description ?? "";
    const id = createWorkflowRow({ name, description, madeWith: input.answers ? "guided" : "editor" });
    const answers = input.answers ?? { ...DEFAULT_ANSWERS };
    const flow = input.flow ?? answersToFlow(answers, { name, description });
    DOCS.set(id, { flow: { ...flow, name, description }, answers, rev: 1, fitsGuided: !!input.answers });
    return { id, rev: 1 };
  });
}

// Try on one file: the mock returns the per-step lines at once (the app runs a preview job and follows its events).
export function tryFlow(flow: Flow, file: string): Promise<{ lines: TryLine[] }> {
  return respond(() => {
    checkFlow(flow);
    if (!file.trim()) throw new ApiError("file_required", "Pick a file to try.", "file");
    const v = flow.steps[0].settings;
    const au = flow.steps[1].settings;
    return {
      lines: [
        { stepId: "video", text: `Video: ${FORMAT_LABEL[String(v.format)]} at quality ${v.quality}, about 42% of the original size.` },
        { stepId: "audio", text: `Audio: ${CODEC_LABEL[String(au.codec ?? au.format)]}, ${CHANNEL_LABEL[String(au.channels)]}.` },
        { stepId: "save", text: "Save: the check that the new file plays passes. Nothing is saved." },
      ],
    };
  });
}

export function getBitrateLadder(): Promise<BitrateLadder> {
  return respond(() => LADDER);
}

