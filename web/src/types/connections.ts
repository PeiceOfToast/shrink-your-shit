// Sonarr / Radarr connections (UI-API-CONTRACT-v2.md "Sonarr / Radarr", workshop 11-19 02:44Z).
export type IntegrationKind = "sonarr" | "radarr";
export interface PathMapping { theirPath: string; ourPath: string }
export interface Integration {
  id: string; kind: IntegrationKind; name: string;
  url: string;
  hasKey: boolean; // the API key is write-only
  pathMappings: PathMapping[];
  enabled: boolean;
  lastResult: { ok: boolean; message: string; at: string } | null;
  failingSince: string | null;
}
export interface IntegrationInput { kind: IntegrationKind; name: string; url: string; apiKey?: string; pathMappings?: PathMapping[]; enabled?: boolean }
// {id, ...overrides} tests a saved one; {kind, url, apiKey, pathMappings?} a new one (workshop 02:55Z).
export type IntegrationTestInput = { id: string; url?: string; apiKey?: string; pathMappings?: PathMapping[] } | { kind: IntegrationKind; url: string; apiKey: string; pathMappings?: PathMapping[] };
export interface IntegrationTestResult {
  ok: boolean; message: string; version: string | null;
  folders: { theirPath: string; ourPath: string | null; inLibrary: boolean }[];
}
