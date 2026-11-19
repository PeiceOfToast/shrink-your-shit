// Sonarr / Radarr connections (contract "Sonarr / Radarr"). The server routes aren't on dev yet, so ?mock= keeps an
// in-memory list: a connected Sonarr and a Radarr that didn't answer (?mock=fresh: none).
import { call, MOCK, type Answer } from "./api";
import type { Integration, IntegrationInput, IntegrationTestInput, IntegrationTestResult } from "./types/connections";

const ago = (min: number) => new Date(Date.now() - min * 60000).toISOString();
let mock: Integration[] = new URLSearchParams(location.search).get("mock") === "fresh" ? [] : [
  { id: "1", kind: "sonarr", name: "Sonarr", url: "http://192.168.1.20:8989", hasKey: true, pathMappings: [{ theirPath: "/tv", ourPath: "/mnt/media/tv" }],
    enabled: true, lastResult: { ok: true, message: "Sonarr rescanned The Show.", at: ago(12) }, failingSince: null },
  { id: "2", kind: "radarr", name: "Radarr", url: "http://192.168.1.20:7878", hasKey: true, pathMappings: [],
    enabled: true, lastResult: { ok: false, message: "no answer in 60 s", at: ago(40) }, failingSince: null },
];
const wait = <T,>(v: T) => new Promise<T>((r) => setTimeout(() => r(v), 250));
const bad = (code: string, message: string) => ({ error: { code, message } }) as Answer<never>;

export async function listIntegrations(): Promise<Answer<Integration[]>> {
  if (MOCK) return wait(mock.map((x) => ({ ...x })));
  return call("GET", "/api/v2/integrations");
}

// Mock answers: a key "wrong" is refused, an address with "nothing" isn't reached; otherwise its root folders.
function mockTest(kind: string, url: string, key: string | undefined, maps: { theirPath: string; ourPath: string }[]): IntegrationTestResult {
  const app = kind === "sonarr" ? "Sonarr" : "Radarr";
  if (/nothing/.test(url)) return { ok: false, message: "Couldn't reach " + url + ".", version: null, folders: [] };
  if (key === "wrong") return { ok: false, message: app + " answered 401: the API key is wrong.", version: null, folders: [] };
  const roots = kind === "sonarr" ? ["/tv"] : ["/movies", "/movies-4k"];
  return { ok: true, message: "Connected to " + app + ".", version: kind === "sonarr" ? "4.0.10" : "5.14.0",
    folders: roots.map((p) => { const m = maps.find((x) => p === x.theirPath || p.startsWith(x.theirPath + "/"));
      return m ? { theirPath: p, ourPath: m.ourPath + p.slice(m.theirPath.length), inLibrary: true } : { theirPath: p, ourPath: null, inLibrary: false }; }) };
}

export async function testIntegration(t: IntegrationTestInput): Promise<Answer<IntegrationTestResult>> {
  if (MOCK) {
    if ("id" in t) {
      const s = mock.find((x) => x.id === t.id);
      if (!s) return bad("not_found", "That connection is gone.");
      const r = mockTest(s.kind, t.url ?? s.url, t.apiKey, t.pathMappings ?? s.pathMappings);
      if (t.url === undefined && t.apiKey === undefined && t.pathMappings === undefined) // only a plain Test sets lastResult
        s.lastResult = { ok: r.ok, message: r.message, at: new Date().toISOString() };
      return wait(r);
    }
    return wait(mockTest(t.kind, t.url, t.apiKey, t.pathMappings ?? []));
  }
  return call("POST", "/api/v2/integrations/test", t);
}

const urlOk = (u: string) => /^https?:\/\/[^\s/]+/.test(u);
const mapsOk = (m: { theirPath: string; ourPath: string }[] = []) => m.every((x) => x.theirPath.startsWith("/") && x.ourPath.startsWith("/"));

export async function addIntegration(i: IntegrationInput): Promise<Answer<Integration>> {
  if (MOCK) {
    if (!urlOk(i.url)) return bad("invalid_url", "That address isn't a web address. It starts with http:// or https://.");
    if (!i.apiKey) return bad("missing_key", "Enter the API key.");
    if (!mapsOk(i.pathMappings)) return bad("invalid_mapping", "Folders start with /.");
    const it: Integration = { id: String(Date.now()), kind: i.kind, name: i.name, url: i.url, hasKey: true, pathMappings: i.pathMappings ?? [],
      enabled: i.enabled ?? true, lastResult: null, failingSince: null };
    mock.push(it);
    return wait({ ...it });
  }
  return call("POST", "/api/v2/integrations", i);
}

export async function patchIntegration(id: string, p: Partial<IntegrationInput>): Promise<Answer<Integration>> {
  if (MOCK) {
    const s = mock.find((x) => x.id === id);
    if (!s) return bad("not_found", "That connection is gone.");
    if (p.url !== undefined && !urlOk(p.url)) return bad("invalid_url", "That address isn't a web address. It starts with http:// or https://.");
    if (!mapsOk(p.pathMappings)) return bad("invalid_mapping", "Folders start with /.");
    Object.assign(s, { ...(p.url !== undefined && { url: p.url }), ...(p.pathMappings && { pathMappings: p.pathMappings }), ...(p.enabled !== undefined && { enabled: p.enabled }) });
    return wait({ ...s });
  }
  return call("PATCH", "/api/v2/integrations/" + encodeURIComponent(id), p);
}

export async function removeIntegration(id: string): Promise<Answer<null>> {
  if (MOCK) { mock = mock.filter((x) => x.id !== id); return wait(null); }
  return call("DELETE", "/api/v2/integrations/" + encodeURIComponent(id));
}
