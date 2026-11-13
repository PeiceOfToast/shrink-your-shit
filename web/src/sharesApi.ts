// D: network shares API (D-API-SHAPE.md, workshop 11-13 02:20Z). Mock only (the engine isn't on dev yet).
import { call, MOCK, type Answer } from "./api";
import { mockState } from "./mock";
import type { Share, SharesResponse, ShareCreateInput, ShareTestInput, ShareTestResult } from "./types/shares";

let supportsShares: boolean | null = null;
export async function sharesSupported(): Promise<boolean> {
  if (MOCK) return true;
  if (supportsShares !== null) return supportsShares;
  const r = await call("GET", "/api/v2/shares?limit=1");
  supportsShares = !("error" in r);
  return supportsShares;
}

export async function getShares(): Promise<Answer<SharesResponse>> {
  if (MOCK) {
    const s = mockState();
    return { mode: s.mode, platform: "linux", nfsSupported: true, testedPlatform: true, serverAddress: "192.168.1.20", items: s.shares };
  }
  return call("GET", "/api/v2/shares");
}

export async function addShare(input: ShareCreateInput): Promise<Answer<Share>> {
  if (MOCK) {
    const s = mockState();
    const id = String(s.shares.reduce((m, x) => Math.max(m, Number(x.id)), 0) + 1);
    const mode = s.mode;
    const share: Share = {
      id, name: input.name, type: input.type, address: input.address,
      guest: input.guest, username: input.guest ? null : input.username ?? null,
      hasPassword: !input.guest && !!input.password,
      status: mode === "app" ? "connecting" : "waiting_setup", since: null, reason: null,
      path: "/var/lib/syp/shares/" + input.name.toLowerCase().replace(/\s+/g, "-"),
      libraries: [], setup: mode === "app" ? null : {
        kind: mode === "manual" ? "command" : "compose",
        text: mode === "manual"
          ? "sudo /opt/shrink-your-shit/bin/connect-share " + id
          : "volume:\n  - type: cifs\n    source: " + input.address + "\n    target: /mnt/shares/" + input.name.toLowerCase().replace(/\s+/g, "-") + "\n    options:\n      - credentials=/etc/syp/shares/" + id,
        removeText: mode === "manual"
          ? "sudo /opt/shrink-your-shit/bin/connect-share " + id + " --remove"
          : "# Remove the volume block for " + input.name + " from compose.yml",
      },
    };
    if (mode === "app") setTimeout(() => { const st = mockState(); const x = st.shares.find((x) => x.id === id); if (x) x.status = "connected"; }, 1500);
    return share;
  }
  return call("POST", "/api/v2/shares", input);
}

export async function testShare(input: ShareTestInput): Promise<Answer<ShareTestResult>> {
  if (MOCK) {
    if (input.address.includes("bad") || input.address.includes("fail")) return { ok: false, code: "auth_failed", message: "The username or password is wrong." };
    if (input.address.includes("notfound") || input.address.includes("missing")) return { ok: false, code: "share_not_found", message: "The server doesn't serve a folder with that name.", shares: ["Movies", "Public", "Media"] };
    if (input.address.includes("nfs") || input.type === "nfs") return { ok: true, address: input.address, portOnly: true, folders: [], moreFolders: 0 };
    return { ok: true, address: input.address, folders: ["Movies", "TV", "Music"], moreFolders: 4, portOnly: false };
  }
  return call("POST", "/api/v2/shares/test", input);
}

export async function patchShare(id: string, patch: Partial<ShareCreateInput>): Promise<Answer<Share>> {
  if (MOCK) {
    const s = mockState();
    const share = s.shares.find((x) => x.id === id);
    if (!share) return { error: { code: "not_found", message: "There is no such share." } };
    return { ...share, ...patch, hasPassword: !patch.guest && !!patch.password ? true : share.hasPassword };
  }
  return call("PATCH", "/api/v2/shares/" + encodeURIComponent(id), patch);
}

export async function deleteShare(id: string): Promise<Answer<void>> {
  if (MOCK) {
    const s = mockState();
    const share = s.shares.find((x) => x.id === id);
    if (share && share.libraries.length > 0) return { error: { code: "share_in_use", message: "Libraries use this share.", libraries: share.libraries } } as unknown as Answer<void>;
    s.shares = s.shares.filter((x) => x.id !== id);
    return undefined as unknown as Answer<void>;
  }
  return call("DELETE", "/api/v2/shares/" + encodeURIComponent(id));
}

export async function reconnectShare(id: string): Promise<Answer<Share>> {
  if (MOCK) {
    const s = mockState();
    const share = s.shares.find((x) => x.id === id);
    if (!share) return { error: { code: "not_found", message: "There is no such share." } };
    share.status = "connected"; share.reason = null; share.since = new Date().toISOString();
    return share;
  }
  return call("POST", "/api/v2/shares/" + encodeURIComponent(id) + "/reconnect");
}

export async function checkShare(id: string): Promise<Answer<Share>> {
  if (MOCK) {
    const s = mockState();
    const share = s.shares.find((x) => x.id === id);
    if (!share) return { error: { code: "not_found", message: "There is no such share." } };
    return share;
  }
  return call("POST", "/api/v2/shares/" + encodeURIComponent(id) + "/check");
}