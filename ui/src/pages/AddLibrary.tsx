import { useState } from "react";
import { api, ApiError, formatBytes, formatCount, type FolderCheck, type Library, type Watch } from "../api/index.ts";
import { useKeys } from "../ui/useKeys.ts";
import { FolderBrowser } from "./FolderBrowser.tsx";

// Add library, a side panel (draft A, drafts/add-library/a-side-panel.html#open). No workflow here: the new library's
// panel opens after Add so one can be picked.

const WATCH: { value: Watch; label: string }[] = [
  { value: "hourly", label: "Hourly" },
  { value: "daily", label: "Daily" },
  { value: "weekly", label: "Weekly" },
];

type Problem = { field?: string; message: string };

function toProblem(e: unknown): Problem {
  if (e instanceof ApiError) return { field: e.field, message: e.message };
  return { message: e instanceof Error ? e.message : String(e) };
}

function baseName(p: string): string {
  return p.replace(/\/+$/, "").split("/").pop() ?? "";
}

export function AddLibrary({ onClose, onAdded }: { onClose: () => void; onAdded: (l: Library) => void }) {
  const [where, setWhere] = useState<"local" | "share">("local");
  const [path, setPath] = useState("");
  const [browsing, setBrowsing] = useState(false);
  const [address, setAddress] = useState("");
  const [user, setUser] = useState("");
  const [password, setPassword] = useState("");
  const [shareId, setShareId] = useState<string | null>(null);
  const [testRes, setTestRes] = useState<string | null>(null);
  const [name, setName] = useState("");
  const [nameTouched, setNameTouched] = useState(false);
  const [watch, setWatch] = useState<Watch>("daily");
  const [check, setCheck] = useState<FolderCheck | null>(null);
  const [problem, setProblem] = useState<Problem | null>(null);
  const [busy, setBusy] = useState(false);

  useKeys((e) => {
    if (e.key === "Escape") onClose();
  });

  // Choosing a folder fills the name from it, unless the person has typed one.
  const pick = (p: string) => {
    setPath(p);
    if (!nameTouched) setName(baseName(p));
    setCheck(null);
    setProblem(null);
  };

  const test = async () => {
    setBusy(true);
    setProblem(null);
    try {
      const r = await api.testShare({ address, user: user || undefined, password: password || undefined });
      const created = await api.createShare({ address, user: user || undefined, password: password || undefined });
      setShareId(created.id);
      setTestRes(`Connected · ${r.folders.length} folders`);
    } catch (e) {
      setShareId(null);
      setTestRes(null);
      setProblem(toProblem(e));
    } finally {
      setBusy(false);
    }
  };

  const runCheck = async () => {
    setBusy(true);
    setProblem(null);
    try {
      if (where === "local") setCheck(await api.checkFolder({ path: path.trim() }));
      else if (shareId) setCheck(await api.checkFolder({ shareId }));
      else throw new ApiError("share_untested", "Test the connection first.");
    } catch (e) {
      setProblem(toProblem(e));
    } finally {
      setBusy(false);
    }
  };

  const add = async () => {
    setBusy(true);
    setProblem(null);
    try {
      if (!name.trim()) throw new ApiError("name_required", "Give the library a name.", "name");
      if (where === "share" && !shareId) throw new ApiError("share_untested", "Test the connection first.");
      const lib = await api.createLibrary({
        name: name.trim(),
        path: where === "local" ? path.trim() : undefined,
        shareId: where === "share" ? shareId! : undefined,
        watch,
      });
      onAdded(lib);
    } catch (e) {
      setProblem(toProblem(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <div className="scrim" onClick={onClose} />
      <aside className="ov panel" role="dialog" aria-modal="true" aria-labelledby="add-lib-title">
        <div className="ovh">
          <h2 id="add-lib-title">Add library</h2>
          <span className="sp"></span>
          <button type="button" className="btn ghost sm" onClick={onClose} aria-label="Close">
            Close
          </button>
        </div>

        <div className="ovb">
          {problem && <p className="err" role="alert">{problem.message}</p>}

          <div className="field">
            <span className="lbl" id="where-lbl">Where the videos are</span>
            <div className="seg" role="group" aria-labelledby="where-lbl">
              <button type="button" aria-pressed={where === "local"} onClick={() => setWhere("local")}>On this server</button>
              <button type="button" aria-pressed={where === "share"} onClick={() => setWhere("share")}>Network share</button>
            </div>
          </div>

          {where === "local" ? (
            <div className="field">
              <span className="lbl">Folder</span>
              <div className="row">
                <input
                  className="inp mono"
                  value={path}
                  onChange={(e) => pick(e.target.value)}
                  placeholder="/media/…"
                  aria-label="Folder"
                />
                <button type="button" className="btn sm" onClick={() => setBrowsing((b) => !b)}>Browse</button>
              </div>
              {browsing && (
                <FolderBrowser
                  start={path || undefined}
                  onPick={(p) => {
                    pick(p);
                    setBrowsing(false);
                  }}
                />
              )}
            </div>
          ) : (
            <div className="field">
              <input
                className="inp mono"
                value={address}
                onChange={(e) => {
                  setAddress(e.target.value);
                  setShareId(null);
                  setTestRes(null);
                }}
                placeholder="//nas/videos or smb://nas/videos"
                aria-label="Share address"
              />
              <div className="row">
                <input className="inp" value={user} onChange={(e) => setUser(e.target.value)} placeholder="User name" aria-label="User name" />
                <input
                  className="inp"
                  type="password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  placeholder="Password"
                  aria-label="Password"
                />
              </div>
              <div className="row">
                <button type="button" className="btn sm" onClick={test} disabled={busy || !address.trim()}>Test connection</button>
                <span className="hint">{testRes}</span>
              </div>
            </div>
          )}

          <div className="field">
            <span className="lbl">Name</span>
            <input
              className="inp"
              value={name}
              onChange={(e) => {
                setName(e.target.value);
                setNameTouched(true);
              }}
              placeholder="Filled in from the folder"
              aria-label="Name"
            />
          </div>

          <div className="field">
            <div className="row">
              <span className="lbl">Check this folder</span>
              <span className="sp" style={{ flex: 1 }}></span>
              <button type="button" className="btn sm" onClick={runCheck} disabled={busy}>Check</button>
            </div>
            {check ? (
              <div className="check">
                <div className="nums">
                  <div>
                    <b>{formatCount(check.videos)}</b>
                    <span>videos found</span>
                  </div>
                  <div>
                    <b>{formatBytes(check.sizeBytes)}</b>
                    <span>in total</span>
                  </div>
                </div>
                {check.problems.map((p) => (
                  <span key={p.code} className="err">{p.message}</span>
                ))}
                <span className="hint">Checking reads the folder only; nothing is queued.</span>
              </div>
            ) : (
              <span className="hint">Counts the videos in it before you add it.</span>
            )}
          </div>

          <details className="adv">
            <summary>
              More options <span className="muted">· watching</span>
            </summary>
            <div>
              <div className="field">
                <label className="lbl" htmlFor="add-watch">Look for new and changed files</label>
                <select id="add-watch" className="sel" style={{ width: "auto" }} value={watch} onChange={(e) => setWatch(e.target.value as Watch)}>
                  {WATCH.map((w) => (
                    <option key={w.value} value={w.value}>{w.label}</option>
                  ))}
                </select>
              </div>
            </div>
          </details>

          <p className="hint" style={{ margin: 0 }}>
            You pick its workflow next. Until then its videos are counted, but nothing is converted.
          </p>
        </div>

        <div className="ovf">
          <span className="sp"></span>
          <button type="button" className="btn sm" onClick={onClose}>Cancel</button>
          <button type="button" className="btn primary sm" onClick={add} disabled={busy}>Add library</button>
        </div>
      </aside>
    </>
  );
}
