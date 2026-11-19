// Notifications settings (contract "## Notifications"). The server routes aren't on dev yet (workshop PR #330), so ?mock=1
// keeps an in-memory copy: a Discord place and a webhook that failed. ?mock=fresh: nothing set up yet.
import { call, MOCK, type Answer } from "./api";
import type { NotifySettings, NotifySummary, NotifyTarget, NotifyTargetInput, NotifyTestInput, NotifyTestResult } from "./types/notifications";

const ago = (min: number) => new Date(Date.now() - min * 60000).toISOString();
const EVENTS_ON = { jobFailed: true, needsYou: true, fileFinished: false, batchFinished: true, workerOffline: true, encoderFellBack: false };
const fresh = new URLSearchParams(location.search).get("mock") === "fresh";
let mock: NotifySettings = fresh ? { targets: [], summary: { mode: "drained", at: "09:00" }, linkBase: null } : {
  targets: [
    { id: "1", name: "#media", kind: "discord", urlShown: "https://discord.com/…", enabled: true, events: { ...EVENTS_ON },
      lastResult: { ok: true, message: "Sent. Discord answered 204.", at: ago(14) }, failingSince: null },
    { id: "2", name: "Home Assistant", kind: "webhook", urlShown: "http://192.168.1.30/…", enabled: true,
      events: { ...EVENTS_ON, fileFinished: true, batchFinished: false },
      lastResult: { ok: false, message: "The address answered 500 Internal Server Error, so the message wasn't accepted.", at: ago(14) }, failingSince: ago(14) },
  ],
  summary: { mode: "daily", at: "09:00" },
  linkBase: "http://nas:8484",
};
const wait = <T,>(v: T) => new Promise<T>((r) => setTimeout(() => r(v), 250));
const bad = (code: string, message: string) => ({ error: { code, message } }) as Answer<never>;
const urlOk = (u: string) => /^https?:\/\/[^\s/]+/.test(u);
const shown = (kind: string, u: string) => (kind === "discord" ? "https://discord.com/…" : u.replace(/^(https?:\/\/[^/]+).*/, "$1/…"));

export async function getNotifications(): Promise<Answer<NotifySettings>> {
  if (MOCK) return wait(structuredClone(mock));
  return call("GET", "/api/v2/notifications");
}

export async function patchNotifications(p: { summary?: NotifySummary; linkBase?: string | null }): Promise<Answer<NotifySettings>> {
  if (MOCK) {
    if (p.linkBase && !urlOk(p.linkBase)) return bad("invalid", "The link address has to start with http:// or https://.");
    mock = { ...mock, ...(p.summary && { summary: p.summary }), ...(p.linkBase !== undefined && { linkBase: p.linkBase }) };
    return wait(structuredClone(mock));
  }
  return call("PATCH", "/api/v2/notifications", p);
}

export async function addTarget(i: NotifyTargetInput): Promise<Answer<NotifyTarget>> {
  if (MOCK) {
    if (!urlOk(i.url)) return bad("invalid_url", "That address isn't a web address. It starts with http:// or https://.");
    const t: NotifyTarget = { id: String(Date.now()), name: i.name, kind: i.kind, urlShown: shown(i.kind, i.url), enabled: i.enabled ?? true,
      events: i.events ?? { jobFailed: true, needsYou: true, fileFinished: false, batchFinished: true, workerOffline: true, encoderFellBack: false },
      lastResult: null, failingSince: null };
    mock = { ...mock, targets: [...mock.targets, t] };
    return wait(structuredClone(t));
  }
  return call("POST", "/api/v2/notifications/targets", i);
}

export async function patchTarget(id: string, p: { name?: string; url?: string; enabled?: boolean; events?: NotifyTarget["events"] }): Promise<Answer<NotifyTarget>> {
  if (MOCK) {
    const s = mock.targets.find((x) => x.id === id);
    if (!s) return bad("not_found", "That place is gone.");
    if (p.url !== undefined && !urlOk(p.url)) return bad("invalid_url", "That address isn't a web address. It starts with http:// or https://.");
    Object.assign(s, { ...(p.name !== undefined && { name: p.name }), ...(p.url !== undefined && { urlShown: shown(s.kind, p.url) }),
      ...(p.enabled !== undefined && { enabled: p.enabled }), ...(p.events && { events: p.events }) });
    return wait(structuredClone(s));
  }
  return call("PATCH", "/api/v2/notifications/targets/" + encodeURIComponent(id), p);
}

export async function removeTarget(id: string): Promise<Answer<null>> {
  if (MOCK) { mock = { ...mock, targets: mock.targets.filter((x) => x.id !== id) }; return wait(null); }
  return call("DELETE", "/api/v2/notifications/targets/" + encodeURIComponent(id));
}

// Mock answers: an address with "nothing" isn't reached, one with "wrong" is refused; otherwise sent.
function mockTest(kind: string, url: string): NotifyTestResult {
  if (/nothing/.test(url)) return { ok: false, message: "Couldn't reach " + new URL(url).host + ". Check this server's internet connection." };
  if (/wrong/.test(url)) return { ok: false, message: "Discord answered 401: the webhook URL is wrong or was deleted." };
  return { ok: true, message: kind === "discord" ? "Sent. Discord answered 204." : "Sent. The address answered 200." };
}

export async function testNotification(t: NotifyTestInput): Promise<Answer<NotifyTestResult>> {
  if (MOCK) {
    if ("id" in t) {
      const s = mock.targets.find((x) => x.id === t.id);
      if (!s) return bad("not_found", "That place is gone.");
      const r = mockTest(s.kind, t.url ?? "https://ok.example/");
      if (t.url === undefined) s.lastResult = { ...r, at: new Date().toISOString() }; // only a plain Test sets lastResult
      return wait(r);
    }
    if (!urlOk(t.url)) return bad("invalid_url", "That address isn't a web address. It starts with http:// or https://.");
    return wait(mockTest(t.kind, t.url));
  }
  return call("POST", "/api/v2/notifications/test", t);
}
