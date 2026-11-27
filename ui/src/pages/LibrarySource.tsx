import { useState } from "react";
import { api, ApiError } from "../api/index.ts";
import { FolderBrowser } from "./FolderBrowser.tsx";

// Where the videos are (page 09). On this server: a folder, typed or browsed. Or a network share: its address is
// tested, and the test saves the share so a library can use it. Add library and first run both show this field.

export type Problem = { field?: string; message: string };

export function toProblem(e: unknown): Problem {
  if (e instanceof ApiError) return { field: e.field, message: e.message };
  return { message: e instanceof Error ? e.message : String(e) };
}

export function baseName(p: string): string {
  return p.replace(/\/+$/, "").split("/").pop() ?? "";
}

export type Source = {
  where: "local" | "share";
  path: string;
  address: string;
  user: string;
  password: string;
  shareId: string | null;
  testRes: string | null;
};

export const NO_SOURCE: Source = { where: "local", path: "", address: "", user: "", password: "", shareId: null, testRes: null };

// The owner passes changes as a patch, so a share test that finishes late never undoes what was typed meanwhile.
export function LibrarySource({
  value,
  onChange,
  onProblem,
  onBusy,
}: {
  value: Source;
  onChange: (patch: Partial<Source>) => void;
  onProblem: (p: Problem | null) => void;
  onBusy?: (busy: boolean) => void;
}) {
  const [browsing, setBrowsing] = useState(false);
  const [testing, setTesting] = useState(false);

  const test = async () => {
    setTesting(true);
    onBusy?.(true);
    onProblem(null);
    try {
      const auth = { address: value.address, user: value.user || undefined, password: value.password || undefined };
      const r = await api.testShare(auth);
      const created = await api.createShare(auth);
      onChange({ shareId: created.id, testRes: `Connected · ${r.folders.length} folders` });
    } catch (e) {
      onChange({ shareId: null, testRes: null });
      onProblem(toProblem(e));
    } finally {
      setTesting(false);
      onBusy?.(false);
    }
  };

  return (
    <>
      <div className="field">
        <span className="lbl" id="where-lbl">Where the videos are</span>
        <div className="seg" role="group" aria-labelledby="where-lbl">
          <button type="button" aria-pressed={value.where === "local"} onClick={() => onChange({ where: "local" })}>On this server</button>
          <button type="button" aria-pressed={value.where === "share"} onClick={() => onChange({ where: "share" })}>Network share</button>
        </div>
      </div>

      {value.where === "local" ? (
        <div className="field">
          <span className="lbl">Folder</span>
          <div className="row">
            <input
              className="inp mono"
              value={value.path}
              onChange={(e) => onChange({ path: e.target.value })}
              placeholder="/media/…"
              aria-label="Folder"
            />
            <button type="button" className="btn sm" onClick={() => setBrowsing((b) => !b)}>Browse</button>
          </div>
          {browsing && (
            <FolderBrowser
              start={value.path || undefined}
              onPick={(p) => {
                onChange({ path: p });
                setBrowsing(false);
              }}
            />
          )}
        </div>
      ) : (
        <div className="field">
          <input
            className="inp mono"
            value={value.address}
            onChange={(e) => onChange({ address: e.target.value, shareId: null, testRes: null })}
            placeholder="//nas/videos or smb://nas/videos"
            aria-label="Share address"
          />
          <div className="row">
            <input className="inp" value={value.user} onChange={(e) => onChange({ user: e.target.value })} placeholder="User name" aria-label="User name" />
            <input
              className="inp"
              type="password"
              value={value.password}
              onChange={(e) => onChange({ password: e.target.value })}
              placeholder="Password"
              aria-label="Password"
            />
          </div>
          <div className="row">
            <button type="button" className="btn sm" onClick={test} disabled={testing || !value.address.trim()}>Test connection</button>
            <span className="hint">{value.testRes}</span>
          </div>
        </div>
      )}
    </>
  );
}
