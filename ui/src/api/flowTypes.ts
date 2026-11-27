// The workflow flow (pages 05-07): the guided answers, the editor's three fixed steps, the check and the guide.
// Shapes follow the page specs' "Data and API" sections.

// One answers object, used by the guided page, a starter and the preview.
export type Answers = Record<string, unknown>;

export type StepId = "video" | "audio" | "save";

export interface FlowStep {
  id: StepId;
  type: "video.convert" | "audio.tracks" | "file.save";
  version: number;
  settings: Record<string, unknown>;
}

export interface Flow {
  name: string;
  description: string;
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
  // Rare settings: the guided page puts them under "More".
  more?: boolean;
  choices?: GuideChoice[];
}

export interface GuidePart {
  id: "video" | "audio" | "subtitles" | "file_type" | "original";
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

// The workflow routes the editor and the list use (pages 04 and 07).

// GET /workflows/{id}: the flow the editor shows and the draft revision it holds as baseRev.
export interface WorkflowDraft {
  flow: Flow;
  rev: number;
  fitsGuided: boolean;
}

// POST /workflows: the new workflow's id and the draft revision the editor starts from.
export interface WorkflowCreated {
  id: string;
  rev: number;
}

// PUT /workflows/{id}/draft: the draft's new revision, what the page sends back next time.
export interface DraftSaved {
  rev: number;
}

// POST /workflows/{id}/publish: the revision it became.
export interface WorkflowPublished {
  rev: number;
  published: true;
}

// GET /workflows/{id}/revisions: one published revision.
export interface WorkflowRevision {
  rev: number;
  createdAt: string;
}

// POST /workflows/{id}/revisions/{rev}/restore: the new revision the old flow was copied into.
export interface WorkflowRestored {
  rev: number;
}

export interface GuidePreview {
  sentences: string[];
  flow: Flow;
  problems: FlowProblem[];
  disabled: FlowDisabled[];
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
  // The Quality slider: the processor's CRF at 1080p by position, and what turns it into the number each format, row and card uses.
  slider: { position: number; name: string; crf: number }[];
  crfOffset: Record<string, number>;
  rowCrfShift: Record<string, number>;
  resolutionRow: Record<string, string>;
  cardOffsets: Partial<Record<"qsv" | "nvenc" | "vaapi" | "amf", Record<string, number>>>;
}

export type StepValue = string | number | boolean | null;
export interface StepChoice { value: string | number | null; label: string; available: boolean; why?: string }
export interface StepUi {
  widget: "segmented" | "select" | "slider" | "checkbox" | "text" | "number" | "languages";
  group: string; order: number; unit?: string; sliderLabels?: string[]; choices?: StepChoice[];
  showIf?: { field: string; equals?: StepValue; notEquals?: StepValue }; // exactly one of equals / notEquals
}
type JsonType = "string" | "integer" | "number" | "boolean" | "array" | "null";
export interface StepProperty {
  type: JsonType | JsonType[]; title: string; description?: string;
  enum?: (string | number | null)[]; default?: StepValue | string[]; minimum?: number; maximum?: number;
  items?: { type: "string" }; "x-ui": StepUi;
}
export interface StepForm {
  groups: { id: string; label: string; help?: string }[];
  schema: { type: "object"; additionalProperties: false; properties: Record<string, StepProperty> };
}
export interface StepEvent { code: string; level: "debug" | "info" | "warn" | "error"; text: string; fields?: string[]; keep: "log" | "job" }
export interface StepDefinition { type: FlowStep["type"]; version: number; name: string; help: string; events: StepEvent[]; form: StepForm }
