// The guided builder's answers for ?mock=1, shaped as the server sends them (web/src/guide.ts). The defaults are the
// server's DefaultAnswers per goal (internal/workflow/guide.go); the summary is one example, not a preview of any answer.
import type { Answers, Guide, GuideInfo } from "../guide";

const base: Answers = {
  goal: "smaller", quality: 3, slower: false, format: "hevc", resolution: "keep", audio: "stereo_aac_surround",
  allLanguages: false, languages: [], subtitles: "keep_all", subtitleLanguages: [], fileType: "mkv",
  original: "beside_library", folder: "", ifBigger: "keep",
  skip: { minSizeGB: 0, minResolution: 0, minMinutes: 0 }, includeDolbyVision: false,
  after: { mediaServer: false, downloader: false, notify: false, notifyTarget: "" },
};

export const GUIDE_INFO: GuideInfo = {
  defaults: {
    smaller: base,
    every_device: { ...base, goal: "every_device", format: "h264", audio: "stereo_aac", subtitles: "text_only", fileType: "mp4" },
    both: { ...base, goal: "both", audio: "stereo_aac", subtitles: "text_only", fileType: "mp4" },
  },
  available: {
    formats: ["hevc", "h264", "av1"],
    audio: ["stereo_aac_surround", "stereo_aac", "eac3_surround", "as_is"],
    subtitles: ["keep_all", "my_languages", "text_only"],
    after: { mediaServer: false, downloader: false, notifyTargets: [{ id: "n1", name: "Phone" }] },
  },
  languages: ["eng", "deu"],
};

// A workflow the questions built in full (the Start Simple preset's answers).
export const GUIDE_FULL: Guide = { coverage: "full", answers: base, extra: [] };

export const GUIDE_SUMMARY = [
  "Shrink videos to HEVC at quality 3, keeping the picture size.",
  "Keep a stereo track and the surround track as it is, in English and German; tracks with no language set are kept.",
  "Keep every subtitle.",
  "Save as MKV in a folder beside each library; the original stays where it is, and is kept if the new file is bigger.",
  "Files already HEVC, AV1, VP9 or Dolby Vision are left alone.",
];
