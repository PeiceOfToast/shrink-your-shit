// The guided builder's answers (slice 12), as the server sends and takes them: the JSON keys of internal/workflow/guide.go.
// The screen holds one Answers object and sends it back whole, so what Basic doesn't ask (`more`) comes back unchanged.
// The server builds every step; this file only decides what the screen shows and what it sends.

export type Goal = "smaller" | "every_device" | "both";

export interface Skip { minSizeGB: number; minResolution: number; minMinutes: number } // 0 = any
export interface After { mediaServer: boolean; downloader: boolean; notify: boolean; notifyTarget: string } // notifyTarget: a target's name, "" = every target on

export interface Answers {
  goal: Goal;
  quality: number; // 1-5
  slower: boolean; // take longer for smaller files
  format: string; // hevc | h264 | av1 | vp9
  resolution: string; // keep | 1080 | 720
  audio: string; // stereo_aac_surround | stereo_aac | eac3_surround | as_is
  allLanguages: boolean; // true = every language; false = only the languages below
  languages: string[];
  subtitles: string; // keep_all | my_languages | text_only | srt
  subtitleLanguages: string[];
  fileType: string; // mkv | mp4 | webm | keep
  original: string; // beside_library | one_folder | next_to_original | replace
  folder: string; // with one_folder
  ifBigger: string; // keep | replace ("use it anyway")
  skip: Skip;
  includeDolbyVision: boolean;
  after: After;
  more?: Record<string, Record<string, unknown>>; // Advanced's settings, by step type; read back and sent unchanged
  languagesEverywhere?: boolean; // also writes the list as the person's languages in Settings
}

export interface Guide {
  coverage: "full" | "partial" | "none";
  answers: Answers | null;
  extra: string[]; // steps the questions don't show, kept as they are
  why?: string; // coverage none: what Basic can't show
}

export interface NotifyTarget { id: string; name: string }
export interface GuideInfo {
  defaults: Record<Goal, Answers>;
  available: {
    formats: string[];
    audio: string[];
    subtitles: string[];
    after: { mediaServer: boolean; downloader: boolean; notifyTargets: NotifyTarget[] };
  };
  languages: string[];
}

// Q10 opens only when a media server or downloader connection, or a notification target, exists.
export const hasAfter = (a: GuideInfo["available"]["after"]) => a.mediaServer || a.downloader || a.notifyTargets.length > 0;
export const questionCount = (info: GuideInfo) => (hasAfter(info.available.after) ? 10 : 9);

// Basic can open a workflow only when the server could read it as answers.
export const canBasic = (g: Guide) => g.coverage !== "none" && g.answers != null;

// Choosing a goal sets the defaults of Q2-Q8 (the plan's Q1 table). The person's languages, folder, Advanced settings,
// Q9 and Q10 stay as they are; so does everything when the goal is the one already chosen.
export function applyGoal(cur: Answers, goal: Goal, info: GuideInfo): Answers {
  if (goal === cur.goal) return cur;
  const d = info.defaults[goal];
  return {
    ...cur, goal,
    quality: d.quality, slower: d.slower, format: d.format, resolution: d.resolution, audio: d.audio,
    subtitles: d.subtitles, fileType: d.fileType, original: d.original, ifBigger: d.ifBigger,
  };
}

// A Basic save: the answers the screen shows, with the Advanced settings the screen read back. Nothing else is added.
export function basicSave(readBack: Answers | null, edits: Answers): Answers {
  return { ...edits, more: readBack?.more };
}

// What Basic doesn't ask about, in words, for "Basic doesn't ask about: …". Step names come from the server; the
// `more` keys are step types, named here (an unknown type shows its key).
const MORE_WORDS: Record<string, string> = {
  "video.convert": "Convert video's rate control",
  "audio.tracks": "Audio's even loudness",
  "output.commit": "Save the result's file names",
  "input.library": "Library files' ignore list",
  "filter.video": "a condition on the file",
};
export function basicDoesntAsk(extra: string[], more?: Record<string, unknown>): string[] {
  return [...new Set([...extra, ...Object.keys(more ?? {}).map((k) => MORE_WORDS[k] ?? k)])];
}

// Which question a server refusal names (the answer's JSON key), so the screen can open it and show the message there.
export const QUESTION_OF_FIELD: Record<string, number> = {
  goal: 1, quality: 2, slower: 2, format: 3, resolution: 4, audio: 5, allLanguages: 5, languages: 5,
  subtitles: 6, subtitleLanguages: 6, fileType: 7, original: 8, folder: 8, ifBigger: 8,
  skip: 9, includeDolbyVision: 9, after: 10,
};

// Q6's subtitle choices: the ones the server offers, in the plan's order. srt is shown only when offered; an unknown
// value the server offers goes last, as it came.
const SUBTITLE_ORDER = ["keep_all", "my_languages", "text_only", "srt"];
export const subtitleChoices = (offered: string[]) => [
  ...SUBTITLE_ORDER.filter((v) => offered.includes(v)),
  ...offered.filter((v) => !SUBTITLE_ORDER.includes(v)),
];

// The languages a save sends. Ticked ("also use these as my languages everywhere") writes the list as the person's
// languages in Settings; unticked sends nothing, and the workflow follows Settings (no languages, no flag).
export function languagesSent(a: { languages: string[] }, settings: string[], everywhere: boolean) {
  return everywhere
    ? { languages: a.languages.length ? a.languages : settings, languagesEverywhere: true }
    : { languages: [] as string[], languagesEverywhere: false };
}

// Q9's conditions are all off by default: a zero value is "any".
export const skipOn = (s: Skip) => ({ size: s.minSizeGB > 0, res: s.minResolution > 0, minutes: s.minMinutes > 0 });
