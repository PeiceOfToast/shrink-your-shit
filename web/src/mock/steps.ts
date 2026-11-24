// The Stack view's server side, mocked from V1 as built (origin/v2-workflows internal/workflow: registry.go for the
// step definitions and their configSchema, validate.go for the step summaries; "Next to each library" runs since V5a #133).
import REGISTRY from "./registry-4df3345.json";
import type { AddStep, Edge, ID, JSONSchema, Node, Port, PublishPreview, StepDefinition, StepPlace, ValidationIssue, WorkflowDoc, WorkflowRevision } from "../types/v2";
import type { Ladder } from "../workflowApi";
import { groupOf, order } from "../stepEdits";
import { mockVariables } from "./variables";

const inPort: Port = { name: "in", label: "In", kind: "file", outcome: "success" };
const success: Port = { name: "success", label: "Next", kind: "file", outcome: "success" };
const skipped: Port = { name: "skipped", label: "Left as it is", kind: "control", outcome: "skipped" };
const errPort: Port = { name: "error", label: "Didn't work", kind: "control", outcome: "error" };
const en = (values: unknown[], labels: string[]): JSONSchema => ({ enum: values, "x-enumLabels": labels });
const wd = (s: JSONSchema, def: unknown, title: string, widget = ""): JSONSchema => ({ default: def, title, ...s, ...(widget ? { "x-ui": { widget } } : {}) });
const obj = (properties: Record<string, JSONSchema>, required: string[] = []): JSONSchema => ({ type: "object", properties, additionalProperties: true, ...(required.length ? { required } : {}) });

export const QUALITY = ["Smallest file", "Smaller", "Balanced", "Better quality", "Best quality"];
const FORMATS: Record<string, string> = { hevc: "HEVC", h264: "H.264", av1: "AV1", vp9: "VP9" };
const AUDIO: [string, string][] = [["stereo_aac", "Stereo AAC"], ["stereo_aac_surround", "Stereo AAC, plus surround as it is"], ["eac3_surround", "Surround E-AC-3"],
  ["ac3_surround", "Surround AC-3"], ["stereo_opus", "Stereo Opus"], ["as_is", "As it is"]];
const SUBS: Record<string, string> = { keep_all: "Keep all", my_languages: "Only my languages", text_only: "Text only", srt: "As SRT files" };
const TYPES: Record<string, string> = { mkv: "MKV", mp4: "MP4", webm: "WebM", keep: "Keep as is" };

const def = (d: Omit<StepDefinition, "typeVersion" | "fileTypes" | "source" | "requires" | "inputs" | "configSchema"> & Partial<StepDefinition>): StepDefinition =>
  ({ typeVersion: 1, fileTypes: ["video"], source: { kind: "builtin" }, requires: [], inputs: [inPort], configSchema: obj({}), ...d });
const SEGMENT = ["filter.video_skips", "video.convert", "audio.tracks", "subtitles.tracks", "container.output"];

// GET /step-definitions?fileType=video (V1: steps 1, 2, 3, 7 and 8 are always on).
export const STEP_DEFS: StepDefinition[] = [
  def({ type: "input.library", category: "input", name: "Library files", icon: "folder", typeLabel: "Input", description: "Every video in the libraries that use this workflow.",
    inputs: [], outputs: [success], alwaysOn: true, sideEffects: "none",
    configSchema: obj({ ignore: { type: "array", items: { type: "string" }, default: [], description: "Folders, files or patterns in the library that are not looked at." } }) }),
  def({ type: "filter.video_skips", category: "filter", name: "Leave some files alone", icon: "filter", typeLabel: "Filter", description: "Skips Dolby Vision files, files already in this format, and AV1 or VP9 videos.",
    outputs: [success, skipped], alwaysOn: true, runsWith: SEGMENT, sideEffects: "none",
    configSchema: obj({ includeDolbyVision: { type: "boolean", default: false, title: "Include Dolby Vision files" } }) }),
  def({ type: "video.convert", category: "video", name: "Convert video", icon: "video", typeLabel: "Video", description: "Makes the video smaller at the quality you choose.",
    outputs: [success, skipped, errPort], alwaysOn: true, runsWith: SEGMENT, sideEffects: "writes_staging",
    requiresByConfig: { format: { hevc: ["encoder:libx265"], h264: ["encoder:libx264"], av1: ["encoder:libsvtav1"], vp9: ["encoder:libvpx-vp9"] } },
    configSchema: obj({
      quality: wd(en([1, 2, 3, 4, 5], QUALITY), 3, "Quality", "slider"),
      format: wd({ ...en(Object.keys(FORMATS), Object.values(FORMATS)), "x-available": ["hevc", "h264", "av1", "vp9"] }, "hevc", "Video format", "segmented"),
      resolution: wd(en(["keep", "1080", "720", "480"], ["Same as the original", "At most 1080p", "At most 720p", "At most 480p"]), "keep", "Resolution"),
      maxBitrate: wd(en(["none", "high", "medium", "low"], ["No limit", "High", "Medium", "Low"]), "none", "Max bitrate"),
      speed: wd(en(["faster", "normal", "slower"], ["Faster", "Normal", "Slower (smaller files)"]), "normal", "Speed"),
      kind: wd({ ...en(["slider", "selectors"], ["Slider", "Custom settings"]), readOnly: true }, "slider", "Kind"), // as #292's schema: the slider and More options write it; forms skip it
    }, ["quality", "format", "resolution", "maxBitrate", "speed"]) }),
  def({ type: "audio.tracks", category: "audio", name: "Audio", icon: "audio", typeLabel: "Audio", description: "Which sound tracks the new file has.",
    outputs: [success], alwaysOn: false, runsWith: SEGMENT, sideEffects: "none",
    configSchema: obj({ audio: wd(en(AUDIO.map((a) => a[0]), AUDIO.map((a) => a[1])), "stereo_aac", "Audio"), onlyMyLanguages: { type: "boolean", default: true, title: "Only my languages" } }, ["audio", "onlyMyLanguages"]) }),
  def({ type: "subtitles.tracks", category: "subtitles", name: "Subtitles", icon: "subtitles", typeLabel: "Subtitles", description: "Which subtitles the new file keeps.",
    outputs: [success], alwaysOn: false, runsWith: SEGMENT, sideEffects: "none",
    configSchema: obj({ subtitles: wd(en(Object.keys(SUBS), Object.values(SUBS)), "keep_all", "Subtitles") }, ["subtitles"]) }),
  def({ type: "container.output", category: "container", name: "File type", icon: "file", typeLabel: "Container", description: "The new file's type.",
    outputs: [success], alwaysOn: false, runsWith: SEGMENT, sideEffects: "none",
    configSchema: obj({ fileType: wd(en(Object.keys(TYPES), Object.values(TYPES)), "mkv", "File type", "segmented") }, ["fileType"]) }),
  def({ type: "verify.output", category: "verify", name: "Check the result", icon: "check", typeLabel: "Check",
    description: "The new file must play through, look like the original and hold what you chose. If not, the original stays.", outputs: [success, errPort], alwaysOn: true, sideEffects: "none" }),
  def({ type: "output.commit", category: "output", name: "Save the result", icon: "save", typeLabel: "Output", description: "Where the new file goes, and what happens to the original.",
    outputs: [success, errPort], alwaysOn: true, sideEffects: "commits",
    configSchema: obj({
      policy: wd(en(["separate_folder", "replace", "beside"], ["Save to a separate folder", "Replace the original", "Put the new file next to the original"]), "separate_folder", "Save the result"),
      folderMode: wd(en(["next_to_library", "one_folder"], ["Next to each library", "One folder for all"]), "next_to_library", "Where"),
      folder: { type: "string", title: "Folder", "x-ui": { widget: "folder" } },
    }, ["policy"]) }),
  // An earlier install's flow, migrated as one read-only step (V1: video.legacy_settings, deprecated).
  def({ type: "video.legacy_settings", category: "video", name: "Earlier install's settings", icon: "history", typeLabel: "Video",
    description: "The settings this workflow had before workflows existed. They run exactly as before; to change them, start from the Shrink videos steps.",
    outputs: [success, skipped, errPort], alwaysOn: true, sideEffects: "commits", deprecated: true, readOnly: true }), // empty configSchema, as #109 lists it
  def({ type: "filter.video", category: "filter", name: "Only some videos", icon: "cond", typeLabel: "Condition", description: "Sends matching videos one way and the rest another.",
    outputs: [{ name: "match", label: "Matches", kind: "file", outcome: "branch" }, { name: "else", label: "Doesn't match", kind: "file", outcome: "branch" }], alwaysOn: false, sideEffects: "none" }),
];
// The server's own words (#114: titles, descriptions, option hints, placeholders), as a 4df3345 server sends them, so the
// mock shows what the real page shows.
for (const d of STEP_DEFS) { const r = (REGISTRY as unknown as Record<string, Pick<StepDefinition, "name" | "description" | "configSchema">>)[d.type]; if (r) Object.assign(d, r); }
// The string fields that take {{name}} variables (x-variables). The v1 contract names four (output file name, .srt name, track
// title, move-to folder); this registry has only the move-to folder, so only that one is marked here.
{ const f = (STEP_DEFS.find((d) => d.type === "output.commit")?.configSchema.properties as Record<string, JSONSchema> | undefined)?.folder; if (f) f["x-variables"] = true; }
export const stepDef = (type: string) => STEP_DEFS.find((d) => d.type === type);

// GET /api/v2/bitrate-ladder as A-API-SHAPE gives it: the registry's ladder in kbps, the slider table (CRF and the
// save range per position; the figures are the sample's, not the engine's), and the 60 fps and HDR factors.
export function mockLadder(): Ladder {
  const L = (REGISTRY as unknown as Record<string, { configSchema: { properties: { maxBitrate: { "x-bitrateLadder": Record<string, Record<string, Record<string, number>>> } } } }>)["video.convert"].configSchema.properties.maxBitrate["x-bitrateLadder"];
  const rows = ["sd", "720", "1080", "1440", "2160"];
  const crf = [28, 26, 23, 21, 18], save = [[60, 85], [40, 70], [10, 55], [5, 30], [0, 15]];
  return {
    unit: "kbps", rows: [{ row: "sd", label: "480p and below" }, { row: "720", label: "720p" }, { row: "1080", label: "1080p" }, { row: "1440", label: "1440p" }, { row: "2160", label: "2160p" }],
    formats: Object.fromEntries(Object.entries(L).map(([f, lv]) => [f, Object.fromEntries(rows.map((r) => [r, { low: lv.low[r] * 1000, medium: lv.medium[r] * 1000, high: lv.high[r] * 1000, rough: 0 }]))])),
    resolutionRow: { "480": "sd", "720": "720", "1080": "1080", "1440": "1440", "2160": "2160" }, vbrFormats: ["hevc", "h264"],
    factors: { fps60: 1.4, hdr: 1.25 },
    slider: Object.keys(FORMATS).flatMap((f) => QUALITY.map((name, i) => ({ position: i + 1, name, format: f, crf: crf[i], keepsResolution: true, capKbps: null, saveLowPct: save[i][0], saveHighPct: save[i][1] }))),
  };
}

const str = (c: Record<string, unknown>, k: string, d: string) => (typeof c[k] === "string" ? (c[k] as string) : d);
// Step summaries as V1's validate.go summary() words them (server-rendered stepSummaries).
export function summary(n: Node): string {
  const c = n.config;
  if (!n.enabled) return "Off: the original's are kept";
  switch (n.type) {
    case "input.library": return "Every video in the libraries that use this workflow";
    case "filter.video_skips": return c.includeDolbyVision === true ? "Skips files already in this format and AV1 or VP9 videos; Dolby Vision files become HDR10" : "Skips Dolby Vision files, files already in this format, and AV1 or VP9 videos";
    case "video.convert": {
      // A-API-SHAPE: a slider step names its position; custom settings name the resolution and the level or VBR max
      const r = str(c, "resolution", "keep"), b = str(c, "maxBitrate", "none"), f = FORMATS[str(c, "format", "hevc")] ?? "HEVC";
      // W7/F4: the same non-default choices the server names (validate.go summary)
      const gc = (c.removeBlackBars === true ? " · removes black bars" : "") + (c.hardware === "only" ? " · graphics card only" : c.hardware === "cpu" ? " · processor only" : "");
      if (c.kind === "selectors") return "Custom: " + f + " · " + (r === "keep" ? "same resolution" : r + "p") + " · " + (c.rateControl === "quality" ? "Quality only" : c.rateControl === "vbr" ? "VBR, max " + (Number(c.vbrMaxKbps) || 0).toLocaleString() + " kbps" : b[0].toUpperCase() + b.slice(1)) + gc;
      return [f, QUALITY[(Number(c.quality) || 3) - 1], r === "keep" ? "same resolution" : "at most " + r + "p"].join(" · ") + gc;
    }
    case "audio.tracks": return (AUDIO.find((a) => a[0] === str(c, "audio", "stereo_aac"))?.[1] ?? "") + (c.onlyMyLanguages === false ? "" : ", in your languages");
    case "subtitles.tracks": return SUBS[str(c, "subtitles", "keep_all")];
    case "container.output": return TYPES[str(c, "fileType", "mkv")];
    case "verify.output": return "The new file must play through, look like the original and hold what you chose";
    case "output.commit": {
      const p = str(c, "policy", "separate_folder");
      if (p === "replace") return "Replace the original (kept for a while, then deleted)";
      if (p === "beside") return "Next to the original · both stay";
      return str(c, "folderMode", "next_to_library") === "one_folder" ? "Save to " + str(c, "folder", "a folder") + " · originals untouched" : "Save next to each library · originals untouched";
    }
    case "filter.video": return "Videos over 10 Mbps go one way, the rest another";
    case "video.legacy_settings": return "Settings from an earlier install, run exactly as before";
  }
  return "";
}

function chain(types: string[], configs: Record<string, Record<string, unknown>> = {}): WorkflowDoc {
  const nodes: Node[] = types.map((t, i) => ({ id: "n" + (i + 1), type: t, typeVersion: 1, enabled: true, config: configs[t] ?? defaults(t) }));
  const edges: Edge[] = nodes.slice(1).map((n, i) => ({ id: "e" + (i + 1), from: nodes[i].id, fromPort: "success", to: n.id, toPort: "in" }));
  return { schemaVersion: 1, nodes, edges, variables: [] };
}
function defaults(type: string): Record<string, unknown> {
  const props = (stepDef(type)?.configSchema.properties ?? {}) as Record<string, JSONSchema>;
  return Object.fromEntries(Object.entries(props).filter(([, s]) => "default" in s).map(([k, s]) => [k, s.default]));
}
const PRESET = ["input.library", "filter.video_skips", "video.convert", "audio.tracks", "subtitles.tracks", "container.output", "verify.output", "output.commit"];
// The Shrink videos preset (SCREENS V3; review rule 2: Max bitrate No limit, Subtitles Keep all).
export const presetDoc = () => chain(PRESET);
// "Home videos - my rules": a condition with two branches, so the stack can't show it (read-only summary).
export function branchedDoc(): WorkflowDoc {
  const d = chain(["input.library", "filter.video", "video.convert", "verify.output", "output.commit"], { "output.commit": { policy: "replace" }, "video.convert": { ...defaults("video.convert"), quality: 4 } });
  d.edges = d.edges.filter((e) => e.from !== "n2");
  d.nodes.push({ id: "n6", type: "output.commit", typeVersion: 1, enabled: true, name: "Keep as it is", config: { policy: "beside", ifBigger: "keep" } });
  d.edges.push({ id: "e10", from: "n2", fromPort: "match", to: "n3", toPort: "in", label: "Over 10 Mbps" }, { id: "e11", from: "n2", fromPort: "else", to: "n6", toPort: "in", label: "The rest" });
  return d;
}

// An earlier install's flow after the upgrade: input.library > video.legacy_settings (V1, Built in V1).
export function legacyDoc(): WorkflowDoc {
  return chain(["input.library", "video.legacy_settings"], { "video.legacy_settings": { template: { quality: 2, audio: "keep_all" } } });
}

// W11 part (review2 gap 2): a new workflow with only its Library files step, for the empty state
export function emptyDoc(): WorkflowDoc {
  return chain(["input.library"], {});
}

export { order }; // the running order: the chain of success edges (stepEdits.ts)
export const stackable = (d: WorkflowDoc) => order(d).length === d.nodes.length && d.edges.every((e) => e.fromPort === "success");

// W11 (validate's stepPlaces and addSteps, contract final 05:58Z). Mock: every step is fixed in place unless ?movable=1
// (the server marks movable steps once W10 lands); each step type either can be added after a step, or is refused.
const MOVABLE: string[] = ["filter.video_skips", "filter.video", "audio.tracks", "subtitles.tracks", "container.output"];
export function stepPlacesOf(d: WorkflowDoc): Record<ID, StepPlace> {
  const movable = new URLSearchParams(location.search).has("movable");
  return Object.fromEntries(d.nodes.map((n) => [n.id, { fixedPlace: !(movable && MOVABLE.includes(n.type)) }]));
}
export function addStepsOf(d: WorkflowDoc): Record<string, AddStep> {
  const ids = order(d).map((n) => n.id), out: Record<string, AddStep> = {};
  for (const s of STEP_DEFS) {
    if (s.readOnly || s.deprecated) continue;
    if (s.type === "container.output") { out[s.type] = { addable: false, reason: "Can't run among the steps that convert yet." }; continue; }
    // after the last step of the list whose group comes first (an add lands in the group's place)
    let after: ID | undefined;
    for (const id of ids) {
      const n = d.nodes.find((x) => x.id === id)!, def = STEP_DEFS.find((x) => x.type === n.type);
      if (def && def.type !== "output.commit" && groupOf(def).index <= groupOf(s).index) after = id; // the save stays last: an add never lands after it
    }
    out[s.type] = { addable: true, after: after ?? ids[0] };
  }
  return out;
}

// The server's CheckVariables (W4a, #497), for the mock: a name a person may type, no built-in name, no two the same.
// Its messages are the server's. The built-ins are the mock's list (no own variables in it) plus the two field-only ones.
const BUILTIN_NAMES = new Set([...mockVariables().variables.map((v) => v.name), "sub_lang", "sub_kind"]);
export function checkVariables(d: WorkflowDoc): ValidationIssue[] {
  const out: ValidationIssue[] = [], seen = new Set<string>();
  for (const v of d.variables ?? []) {
    const named = v.name.trim() ? "“" + v.name + "”" : "A variable's empty name";
    if (!/^[a-z][a-z0-9_]{0,39}$/.test(v.name)) out.push({ code: "invalid_variable_name", severity: "error", name: v.name || undefined, message: `${named} can't be a variable name: use lowercase letters, numbers and _, starting with a letter, up to 40 characters.` });
    else if (BUILTIN_NAMES.has(v.name)) out.push({ code: "builtin_variable_name", severity: "error", name: v.name, message: `{{${v.name}}} is set by the server already, so a variable can't use that name.` });
    else if (seen.has(v.name)) out.push({ code: "duplicate_variable", severity: "error", name: v.name, message: `Two variables are called {{${v.name}}}. Give one of them another name.` });
    seen.add(v.name);
  }
  return out;
}

export function validate(d: WorkflowDoc): ValidationIssue[] {
  const issues: ValidationIssue[] = checkVariables(d);
  for (const n of d.nodes.filter((x) => x.type === "output.commit")) {
    const c = n.config, policy = str(c, "policy", "separate_folder");
    if (policy !== "separate_folder") continue;
    if (str(c, "folderMode", "next_to_library") === "next_to_library") continue; // each library's folder is checked when it joins
    if (!str(c, "folder", "").trim()) issues.push({ code: "missing_config", severity: "error", nodeId: n.id, configPath: "/folder", message: "Choose the folder for new files." });
    else if (!str(c, "folder", "").startsWith("/")) issues.push({ code: "invalid_config", severity: "error", nodeId: n.id, configPath: "/folder", message: "Give the folder's full path, starting with /." });
  }
  return issues;
}

let etagN = 0;
export function revision(workflowId: string, d: WorkflowDoc, number: number | null): WorkflowRevision {
  return {
    id: workflowId + (number == null ? "draft" : "r" + number), workflowId, number, status: number == null ? "draft" : "published", workflow: d,
    layout: { nodes: {}, groups: [], comments: [] }, logicHash: String(JSON.stringify(d).length), stackable: stackable(d),
    stepSummaries: Object.fromEntries(d.nodes.map((n) => [n.id, summary(n)])), validation: validate(d), etag: "e" + ++etagN,
    createdAt: new Date().toISOString(), publishedAt: number == null ? null : new Date().toISOString(),
  };
}

// GET /workflows/{id}/publish-preview: what changed against the published revision.
export function preview(draft: WorkflowDoc, published: WorkflowDoc | null, nextNumber: number, libraries: PublishPreview["libraries"]): PublishPreview {
  const changes: PublishPreview["changes"] = [];
  for (const n of draft.nodes) {
    const was = published?.nodes.find((x) => x.id === n.id);
    if (!was) changes.push({ nodeId: n.id, kind: "added", summary: (stepDef(n.type)?.name ?? n.type) + ": " + summary(n) });
    else if (JSON.stringify([was.config, was.enabled]) !== JSON.stringify([n.config, n.enabled])) changes.push({ nodeId: n.id, kind: "changed", summary: (stepDef(n.type)?.name ?? n.type) + ": " + summary(n) });
  }
  for (const n of published?.nodes ?? []) if (!draft.nodes.some((x) => x.id === n.id)) changes.push({ nodeId: n.id, kind: "removed", summary: stepDef(n.type)?.name ?? n.type });
  const commit = draft.nodes.find((n) => n.type === "output.commit");
  return { nextNumber, changes, libraries, changesOriginals: str(commit?.config ?? {}, "policy", "") === "replace", validation: validate(draft) };
}
