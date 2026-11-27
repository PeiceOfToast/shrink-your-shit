import { useState } from "react";
import { api, ApiError, formatBytes, formatCount, type FolderCheck, type Library, type Watch } from "../api/index.ts";
import { useKeys } from "../ui/useKeys.ts";
import { LibrarySource, NO_SOURCE, baseName, toProblem, type Problem, type Source } from "./LibrarySource.tsx";

// Add library, a side panel (draft A, drafts/add-library/a-side-panel.html#open). No workflow here: the new library's
// panel opens after Add so one can be picked.

const WATCH: { value: Watch; label: string }[] = [
  { value: "hourly", label: "Hourly" },
  { value: "daily", label: "Daily" },
  { value: "weekly", label: "Weekly" },
];

export function AddLibrary({ onClose, onAdded }: { onClose: () => void; onAdded: (l: Library) => void }) {
  const [src, setSrc] = useState<Source>(NO_SOURCE);
  const [name, setName] = useState("");
  const [nameTouched, setNameTouched] = useState(false);
  const [watch, setWatch] = useState<Watch>("daily");
  const [check, setCheck] = useState<FolderCheck | null>(null);
  const [problem, setProblem] = useState<Problem | null>(null);
  const [busy, setBusy] = useState(false);

  useKeys((e) => {
    if (e.key === "Escape") onClose();
  });

  // Changing the folder fills the name from it, unless the person has typed one, and clears the last check.
  const change = (patch: Partial<Source>) => {
    setSrc((s) => ({ ...s, ...patch }));
    if (patch.path !== undefined) {
      if (!nameTouched) setName(baseName(patch.path));
      setCheck(null);
      setProblem(null);
    }
  };

  const runCheck = async () => {
    setBusy(true);
    setProblem(null);
    try {
      if (src.where === "local") setCheck(await api.checkFolder({ path: src.path.trim() }));
      else if (src.shareId) setCheck(await api.checkFolder({ shareId: src.shareId }));
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
      if (src.where === "share" && !src.shareId) throw new ApiError("share_untested", "Test the connection first.");
      const lib = await api.createLibrary({
        name: name.trim(),
        path: src.where === "local" ? src.path.trim() : undefined,
        shareId: src.where === "share" ? src.shareId! : undefined,
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

          <LibrarySource value={src} onChange={change} onProblem={setProblem} onBusy={setBusy} />

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
