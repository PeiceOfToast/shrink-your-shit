// The Stack view's server side, mocked from V1 as built (origin/v2-workflows internal/workflow: registry.go for the
// step definitions and their configSchema, validate.go for the step summaries and the not_runnable_yet rule).
import type { Edge, JSONSchema, Node, Port, PublishPreview, StepDefinition, ValidationIssue, WorkflowDoc, WorkflowRevision } from "../types/v2";

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
export const stepDef = (type: string) => STEP_DEFS.find((d) => d.type === type);

const str = (c: Record<string, unknown>, k: string, d: string) => (typeof c[k] === "string" ? (c[k] as string) : d);
// Step summaries as V1's validate.go summary() words them (server-rendered stepSummaries).
export function summary(n: Node): string {
  const c = n.config;
  if (!n.enabled) return "Off: the original's are kept";
  switch (n.type) {
    case "input.library": return "Every video in the libraries that use this workflow";
    case "filter.video_skips": return c.includeDolbyVision === true ? "Skips files already in this format and AV1 or VP9 videos; Dolby Vision files become HDR10" : "Skips Dolby Vision files, files already in this format, and AV1 or VP9 videos";
    case "video.convert": {
      const r = str(c, "resolution", "keep"), b = str(c, "maxBitrate", "none");
      return [FORMATS[str(c, "format", "hevc")] ?? "HEVC", QUALITY[(Number(c.quality) || 3) - 1] + " quality", r === "keep" ? "same resolution" : "at most " + r + "p",
        ...(b !== "none" ? [b[0].toUpperCase() + b.slice(1) + " bitrate limit"] : [])].join(" · ");
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
  d.nodes.push({ id: "n6", type: "output.commit", typeVersion: 1, enabled: true, name: "Keep as it is", config: { policy: "beside" } });
  d.edges.push({ id: "e10", from: "n2", fromPort: "match", to: "n3", toPort: "in", label: "Over 10 Mbps" }, { id: "e11", from: "n2", fromPort: "else", to: "n6", toPort: "in", label: "The rest" });
  return d;
}

// An earlier install's flow after the upgrade: input.library > video.legacy_settings (V1, Built in V1).
export function legacyDoc(): WorkflowDoc {
  return chain(["input.library", "video.legacy_settings"], { "video.legacy_settings": { template: { quality: 2, audio: "keep_all" } } });
}

// The order the stack shows: the chain of success edges from the input (contract "Stack ⇄ graph").
export function order(d: WorkflowDoc): Node[] {
  const out: Node[] = [];
  let n = d.nodes.find((x) => x.type === "input.library");
  while (n && !out.includes(n)) {
    out.push(n);
    const e = d.edges.find((x) => x.from === n!.id && x.fromPort === "success");
    n = e && d.nodes.find((x) => x.id === e.to);
  }
  return out;
}
export const stackable = (d: WorkflowDoc) => order(d).length === d.nodes.length && d.edges.every((e) => e.fromPort === "success");

export function validate(d: WorkflowDoc): ValidationIssue[] {
  const issues: ValidationIssue[] = [];
  for (const n of d.nodes.filter((x) => x.type === "output.commit")) {
    const c = n.config, policy = str(c, "policy", "separate_folder");
    if (policy !== "separate_folder") continue;
    if (str(c, "folderMode", "next_to_library") === "next_to_library")
      issues.push({ code: "not_runnable_yet", severity: "error", nodeId: n.id, configPath: "/folderMode",
        message: "Next to each library can't run yet. For now, choose One folder for all, Replace the original, or Put the new file next to the original." });
    else if (!str(c, "folder", "").trim()) issues.push({ code: "missing_config", severity: "error", nodeId: n.id, configPath: "/folder", message: "Choose the folder for new files." });
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
