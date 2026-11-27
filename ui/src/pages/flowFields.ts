// The flow editor's fields, choices and defaults for each step, as the cards draw them (page 07). FlowEditor takes its
// option lists from this table, and flowFields.test.ts checks it against the step definitions golden, so the test
// checks what is drawn. A choice's value is the stored value; null is the unset choice.
export interface Choice {
  value: string | number | null;
  label: string;
}

// A choice's value as a string key: the unset choice (null) is "none".
export function choiceKey(v: unknown): string {
  return v === null || v === undefined ? "none" : String(v);
}

// The line under a field whose stored value no choice matches (an imported or upgraded flow; the server rejects it on save).
export const NO_LONGER_OFFERED = "This setting is no longer offered; pick one.";

// The options to show for a stored value. A value no choice matches is kept as one more option, labelled as no longer
// offered, and stale is its key, so the control shows what is stored instead of the first choice.
export function offeredChoices(choices: Choice[], value: unknown): { options: Choice[]; stale: string | null } {
  const key = choiceKey(value);
  if (choices.some((c) => choiceKey(c.value) === key)) return { options: choices, stale: null };
  const shown = value === null || value === undefined ? "none" : String(value);
  return { options: [...choices, { value: value as Choice["value"], label: `${shown} (no longer offered)` }], stale: key };
}

export interface StepFields {
  fields: string[];
  choices: Record<string, Choice[]>;
  defaults: Record<string, unknown>;
}

export const DRAWN: Record<"video.convert" | "audio.tracks" | "file.save", StepFields> = {
  "video.convert": {
    fields: ["format", "quality", "useGpu", "ifGpuFails", "size", "width", "height", "rateControl", "bitrateLevel", "avgMbps", "maxMbps", "twoPass", "preset", "fps", "bitDepth", "hdr", "keepDolbyVision"],
    choices: {
      format: [
        { value: "hevc", label: "HEVC" },
        { value: "h264", label: "H.264" },
        { value: "av1", label: "AV1" },
      ],
      ifGpuFails: [
        { value: "cpu", label: "Use the processor" },
        { value: "stop", label: "Stop and tell me" },
      ],
      size: [
        { value: null, label: "Same as the source" },
        { value: "2160", label: "2160p" },
        { value: "1080", label: "1080p" },
        { value: "720", label: "720p" },
        { value: "480", label: "480p" },
        { value: "custom", label: "Custom (width × height)" },
      ],
      rateControl: [
        { value: null, label: "Quality (the slider)" },
        { value: "vbr", label: "Variable bitrate" },
        { value: "cbr", label: "Constant bitrate" },
      ],
      bitrateLevel: [
        { value: "low", label: "Low" },
        { value: "medium", label: "Medium" },
        { value: "high", label: "High" },
        { value: "custom", label: "Custom" },
      ],
      preset: [
        { value: "faster", label: "Faster" },
        { value: "normal", label: "Balanced" },
        { value: "slower", label: "Slower and smaller" },
      ],
      bitDepth: [
        { value: null, label: "Same as the source" },
        { value: 8, label: "8-bit" },
        { value: 10, label: "10-bit" },
      ],
      hdr: [
        { value: "keep", label: "Keep" },
        { value: "tonemap", label: "Convert to SDR" },
        { value: "skip", label: "Leave HDR files alone" },
      ],
    },
    defaults: {
      format: "hevc",
      quality: 3,
      useGpu: true,
      ifGpuFails: "cpu",
      size: null,
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
    },
  },
  "audio.tracks": {
    fields: ["format", "channels", "languages", "codec", "bitrateKbps", "downmix", "loudnorm", "commentary", "keepOriginal", "subtitles", "subtitleLanguages", "bitratePer", "sampleRate", "defaultTrack"],
    choices: {
      format: [
        { value: "aac", label: "AAC" },
        { value: "opus", label: "Opus" },
        { value: "copy", label: "Same as the source" },
      ],
      bitratePer: [
        { value: "channel", label: "Each channel" },
        { value: "track", label: "The whole track" },
      ],
      channels: [
        { value: "stereo", label: "Stereo" },
        { value: "source", label: "Same as the source" },
        { value: "mono", label: "Mono" },
        { value: "5.1", label: "5.1" },
      ],
      codec: [
        { value: null, label: "As picked above" },
        { value: "aac", label: "AAC" },
        { value: "opus", label: "Opus" },
        { value: "ac3", label: "AC-3" },
        { value: "eac3", label: "E-AC-3" },
        { value: "flac", label: "FLAC" },
        { value: "copy", label: "Copy the original" },
      ],
      downmix: [
        { value: "speech", label: "Clear speech" },
        { value: "plain", label: "Plain mix" },
        { value: "dpl2", label: "Dolby Pro Logic II" },
      ],
      commentary: [
        { value: "keep", label: "Keep" },
        { value: "drop", label: "Drop" },
      ],
      subtitles: [
        { value: "keep_all", label: "Keep all" },
        { value: "my_languages", label: "Keep my languages" },
        { value: "text_only", label: "Text only" },
        { value: "srt", label: "Make SRT files" },
      ],
    },
    defaults: {
      format: "copy",
      channels: "source",
      languages: [],
      codec: null,
      bitrateKbps: null,
      bitratePer: "channel",
      sampleRate: null,
      downmix: "plain",
      loudnorm: false,
      commentary: "keep",
      defaultTrack: null,
      keepOriginal: false,
      subtitles: "keep_all",
      subtitleLanguages: [],
    },
  },
  "file.save": {
    fields: ["container", "where", "folder", "nameSuffix"],
    choices: {
      container: [
        { value: "mkv", label: "MKV" },
        { value: "mp4", label: "MP4" },
        { value: "webm", label: "WebM" },
        { value: "keep", label: "Keep the same" },
      ],
      where: [
        { value: "replace", label: "Replace the original" },
        { value: "next_to", label: "Next to the original" },
        { value: "folder", label: "A different folder" },
      ],
    },
    defaults: { container: "mkv", where: "next_to", folder: "", nameSuffix: "" },
  },
};

// The choices a step definition greys out: choice key -> the server's reason. Available choices are not in it.
export function greyedChoices(
  def: { form: { schema: { properties: Record<string, { "x-ui": { choices?: { value: string | number | null; available: boolean; why?: string }[] } }> } } },
  field: string,
): Record<string, string> {
  const out: Record<string, string> = {};
  for (const c of def.form.schema.properties[field]?.["x-ui"].choices ?? []) {
    if (!c.available) out[choiceKey(c.value)] = c.why ?? "";
  }
  return out;
}

// The check's disabled entries for one field that rule out a value (a choice the server refuses), by choice key, with its
// reason. An entry with no value is a whole-field grey and is not a choice's grey.
export function valueGreys(disabled: readonly { stepId: string; field: string; value?: string; why: string }[], stepId: string, field: string): Record<string, string> {
  const out: Record<string, string> = {};
  for (const d of disabled) if (d.stepId === stepId && d.field === field && d.value !== undefined) out[d.value] = d.why;
  return out;
}
