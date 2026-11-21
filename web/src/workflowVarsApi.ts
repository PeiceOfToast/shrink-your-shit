// Server-wide workflow variables (Settings > Variables; the server contract is workflowvars.go). A step names one as
// {{name}}. ?mock=1 keeps an in-memory copy so the tab renders without a server.
import { call, MOCK, type Answer } from "./api";

export type VarKind = "string" | "number" | "bool" | "enum" | "path";
export interface WorkflowVar {
  name: string;
  type: VarKind | "boolean"; // the server also accepts "boolean" for yes or no
  value?: string | number | boolean | null;
  options?: string[];
  description?: string;
}
export interface VarIssue { code: string; message: string; name?: string }
export interface VarList { items: WorkflowVar[]; total: number }
export interface VarSave {
  variables: WorkflowVar[];
  total: number;
  saved: boolean;
  affects: { workflows: string[]; libraries: number };
}

let mock: WorkflowVar[] = [
  { name: "output_dir", type: "path", value: "/srv/media/out", description: "Where finished files go" },
  { name: "quality", type: "number", value: 23 },
];
const wait = <T,>(v: T) => new Promise<T>((r) => setTimeout(() => r(v), 250));

export async function getWorkflowVars(): Promise<Answer<VarList>> {
  if (MOCK) return wait({ items: structuredClone(mock), total: mock.length });
  return call("GET", "/api/v2/workflow-variables");
}

export async function saveWorkflowVars(variables: WorkflowVar[], preview: boolean): Promise<Answer<VarSave>> {
  if (MOCK) {
    if (!preview) mock = structuredClone(variables);
    return wait({ variables, total: variables.length, saved: !preview, affects: { workflows: [], libraries: 0 } });
  }
  return call("PUT", "/api/v2/workflow-variables", { variables, ...(preview && { preview: true }) });
}
