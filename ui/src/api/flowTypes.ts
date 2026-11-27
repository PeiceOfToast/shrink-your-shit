// The workflow flow (pages 05-07): the guided answers, the editor's three fixed steps, the check and the guide.
// Shapes follow the page specs' "Data and API" sections.

// One answers object, used by the guided page, a starter and the preview.
export type Answers = Record<string, unknown>;

export type StepId = "video" | "audio" | "save";
export type IfStepFails = "tell" | "move_aside" | "leave";

export interface FlowStep {
  id: StepId;
  type: "video.convert" | "audio.tracks" | "file.save";
  version: number;
  settings: Record<string, unknown>;
}

export interface Flow {
  name: string;
  description: string;
  ifStepFails: IfStepFails;
  variables: { name: string; type: string; value: string | number | boolean }[];
  steps: FlowStep[];
}

export interface FlowProblem {
  stepId?: StepId;
  field?: string;
  code: string;
  message: string;
}

export interface FlowDisabled {
  stepId: StepId;
  field: string;
  value?: string;
  why: string;
}

export interface FlowCheck {
  problems: FlowProblem[];
  disabled: FlowDisabled[];
  summaries: Partial<Record<StepId, string>>;
}

export interface GuideChoice {
  value: string;
  label: string;
  available: boolean;
  why?: string;
}

export interface GuideField {
  key: string;
  label: string;
  type: "choice" | "number" | "yesno" | "languages" | "folder";
  choices?: GuideChoice[];
}

export interface GuidePart {
  id: "video" | "audio" | "subtitles" | "file_type" | "original" | "if_fails";
  label: string;
  fields: GuideField[];
}

export interface GuideChip {
  id: string;
  label: string;
  always: boolean;
  on: Answers;
  off: Answers;
  available: boolean;
  why?: string;
}

export interface GuideDoc {
  chips: GuideChip[];
  parts: GuidePart[];
  defaults: Answers;
  languages: string[];
}

export interface GuidePreview {
  sentences: string[];
  flow: Flow;
  problems: FlowProblem[];
}

export interface TryLine {
  stepId: StepId;
  text: string;
}

// The recommended bitrates (Mb/s at 30 fps SDR) and the Quality slider's numbers, from the app's own tables.
export interface BitrateLadder {
  unit: "Mbps";
  rows: { row: string; label: string }[];
  // formats[format][row] = Low / Medium / High
  formats: Record<"hevc" | "h264" | "av1", Record<string, { low: number; medium: number; high: number }>>;
  vbrFormats: string[];
  factors: { fps60: number; hdr: number };
  vbrMaxFactor: number;
}
