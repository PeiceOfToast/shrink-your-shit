import { clock } from "../format/time.ts";
import { useEffect, useRef, useState } from "react";
import { navigate, Link } from "../router.tsx";
import { api, formatCount, formatGB, type LibraryDetail, type LibraryPatch, type Watch } from "../api/index.ts";
import { useApi } from "../api/useApi.ts";
import { libraryStatus } from "../ui/libraryStatus.ts";
import { useKeys } from "../ui/useKeys.ts";
import { SampleBanner } from "../ui/SampleBanner.tsx";
import { FolderBrowser } from "./FolderBrowser.tsx";

// Library panel (drafts/add-library/a-side-panel.html#lib). Opens on a row click and after Add. Save sends only the
// changed fields. Worker access is on Workers (page 10), so this panel only lists them.

type Form = { name: string; path: string; watch: Watch; paused: boolean; workflowId: string };

const WATCH: { value: Watch; label: string }[] = [
  { value: "hourly", label: "Hourly" },
  { value: "daily", label: "Daily" },
  { value: "weekly", label: "Weekly" },
];

function toForm(l: LibraryDetail): Form {
  return { name: l.name, path: l.path, watch: l.watch, paused: l.paused, workflowId: l.workflow?.id ?? "" };
}

export function LibraryPanel({ id, onChange }: { id: string; onChange?: () => void }) {
  const lib = useApi(() => api.getLibrary(id), [id]);
  const wfs = useApi(() => api.listWorkflows(), []);
  const [form, setForm] = useState<Form | null>(null);
  const [saved, setSaved] = useState<Form | null>(null);
  const [browsing, setBrowsing] = useState(false);
  const [confirmRemove, setConfirmRemove] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [msg, setMsg] = useState<string | null>(null);
  const wfRef = useRef<HTMLSelectElement>(null);

  const L = lib.data;
  const wfId = form?.workflowId ?? "";
  // The chosen workflow's three lines and its preview for this library.
  const sum = useApi(() => (wfId ? api.getWorkflow(wfId) : Promise.resolve(null)), [wfId]);
  const prev = useApi(() => (wfId ? api.getPreview(id, wfId) : Promise.resolve(null)), [wfId, id]);

  useEffect(() => {
    if (lib.data) {
      const f = toForm(lib.data);
      setForm(f);
      setSaved(f);
    }
  }, [lib.data]);

  // A library with no workflow opens with focus on the workflow dropdown.
  useEffect(() => {
    if (L && !L.workflow) wfRef.current?.focus();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [L?.id]);

  useKeys((e) => {
    if (e.key === "Escape") close();
  });

  const close = () => navigate("/libraries");
  const dirty = !!form && !!saved && JSON.stringify(form) !== JSON.stringify(saved);
  const st = L ? libraryStatus(L) : null;

  const save = async () => {
    if (!form || !saved) return;
    const patch: LibraryPatch = {};
    if (form.workflowId !== saved.workflowId) patch.workflowId = form.workflowId || null;
    if (form.name !== saved.name) patch.name = form.name.trim();
    if (form.path !== saved.path) patch.path = form.path.trim();
    if (form.watch !== saved.watch) patch.watch = form.watch;
    if (form.paused !== saved.paused) patch.paused = form.paused;
    // Nothing edited: no "Saved." and no request.
    if (Object.keys(patch).length === 0) return;
    setBusy(true);
    setErr(null);
    setMsg(null);
    try {
      const upd = await api.patchLibrary(id, patch);
      const f = toForm(upd);
      setForm(f);
      setSaved(f);
      setMsg("Saved.");
      lib.reload();
      onChange?.();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const scan = async () => {
    setBusy(true);
    setErr(null);
    try {
      await api.scanLibrary(id);
      lib.reload();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const retry = async () => {
    setBusy(true);
    setErr(null);
    try {
      await api.reconnectShare(id);
      lib.reload();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const remove = async () => {
    setBusy(true);
    setErr(null);
    try {
      await api.deleteLibrary(id);
      navigate("/libraries");
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
      setBusy(false);
    }
  };

  const cancel = () => {
    if (saved) setForm(saved);
    setErr(null);
    setMsg(null);
  };

  return (
    <>
      <div className="scrim" onClick={close} />
      <aside className="ov panel" role="dialog" aria-modal="true" aria-labelledby="lib-title">
        <div className="ovh">
          <div style={{ minWidth: 0 }}>
            <h2 id="lib-title">{form?.name ?? L?.name ?? "Library"}</h2>
            <div className="muted xs" style={{ fontFamily: "var(--mono)" }}>{L?.path}</div>
          </div>
          <span className="sp"></span>
          {st && <span className={`pill ${st.kind}`}>{st.label}</span>}
          <button type="button" className="btn sm" onClick={scan} disabled={busy || !L}>Scan now</button>
          <button type="button" className="btn ghost sm" onClick={close} aria-label="Close">Close</button>
        </div>

        <div className="ovb">
          <SampleBanner text="the workflow preview and reconnecting a share aren't connected yet." />
          {err && <p className="err" role="alert">{err}</p>}
          {lib.error && !L && (
            <p className="bad">
              Can't load this library.{" "}
              <button type="button" className="btn ghost sm" onClick={lib.reload}>Try now</button>
            </p>
          )}
          {!L && lib.loading && <span className="skel" style={{ height: 120 }} />}

          {L && form && (
            <>
              <div className="facts3">
                <div>
                  <b>{formatCount(L.files)}</b>
                  <span>videos</span>
                </div>
                <div>
                  <b>{formatCount(L.waiting)}</b>
                  <span>waiting</span>
                </div>
                <div>
                  <b>{L.savedBytes > 0 ? formatGB(L.savedBytes / 1e9) : "—"}</b>
                  <span>saved</span>
                </div>
              </div>

              {!form.workflowId && (
                <div className="note">
                  <span>
                    <b>No workflow yet.</b> Its videos are found and counted, but nothing is converted until you pick one.
                  </span>
                </div>
              )}
              {L.status === "share_down" && (
                <div className="note">
                  <span>
                    Share not connected{L.shareDownAt ? ` since ${clock(L.shareDownAt)}` : ""}.{" "}
                    <button type="button" className="btn sm" onClick={retry} disabled={busy}>Try again</button>
                  </span>
                </div>
              )}

              <div className="field">
                <label className="lbl" htmlFor="lp-wf">Workflow</label>
                <select
                  id="lp-wf"
                  ref={wfRef}
                  className="sel"
                  value={form.workflowId}
                  onChange={(e) => setForm({ ...form, workflowId: e.target.value })}
                >
                  <option value="">Pick a workflow…</option>
                  {(wfs.data ?? []).map((w) => (
                    <option key={w.id} value={w.id}>{w.name}</option>
                  ))}
                </select>
                {sum.data && (
                  <div className="wfsum">
                    <span>{sum.data.summary.converts}</span>
                    <span>{sum.data.summary.leavesAlone}</span>
                    <span>{sum.data.summary.original}</span>
                  </div>
                )}
                {prev.data && (
                  <span className="hint">
                    With this workflow: {formatCount(prev.data.wouldConvert)} of {formatCount(prev.data.total)} would be converted
                  </span>
                )}
                <div className="row">
                  {form.workflowId && <Link to={`/workflows/${form.workflowId}`}>Edit this workflow</Link>}
                  <Link to="/workflows/new">New workflow</Link>
                </div>
              </div>

              <div className="field">
                <span className="lbl">Folder</span>
                <div className="row">
                  <input
                    className="inp mono"
                    value={form.path}
                    onChange={(e) => setForm({ ...form, path: e.target.value })}
                    aria-label="Folder"
                  />
                  <button type="button" className="btn sm" onClick={() => setBrowsing((b) => !b)}>Change</button>
                </div>
                {browsing && (
                  <FolderBrowser
                    start={form.path}
                    onPick={(p) => {
                      setForm({ ...form, path: p });
                      setBrowsing(false);
                    }}
                  />
                )}
              </div>

              <div className="field">
                <label className="lbl" htmlFor="lp-name">Name</label>
                <input id="lp-name" className="inp" value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} />
              </div>

              <details className="adv">
                <summary>Watching</summary>
                <div>
                  <div className="field">
                    <span className="lbl">New files</span>
                    <button
                      type="button"
                      className="sw"
                      aria-pressed={!form.paused}
                      onClick={() => setForm({ ...form, paused: !form.paused })}
                    >
                      <span className={`k ${form.paused ? "" : "on"}`}></span>
                      Watch for new files
                    </button>
                    <div className="row">
                      <label className="sm muted" htmlFor="lp-watch">Look for new and changed files</label>
                      <select
                        id="lp-watch"
                        className="sel"
                        style={{ width: "auto" }}
                        value={form.watch}
                        onChange={(e) => setForm({ ...form, watch: e.target.value as Watch })}
                      >
                        {WATCH.map((w) => (
                          <option key={w.value} value={w.value}>{w.label}</option>
                        ))}
                      </select>
                    </div>
                    <span className="hint">Set for this library only. Scan now looks at once.</span>
                  </div>
                </div>
              </details>

              <p className="hint" style={{ margin: 0 }}>
                <Link to={`/settings/log?source=Scanner&library=${id}`}>Show its lines in the system log</Link>
              </p>

              <div className="danger">
                {!confirmRemove ? (
                  <button type="button" className="btn bad" onClick={() => setConfirmRemove(true)}>Remove library</button>
                ) : (
                  <div className="jd-confirm">
                    <span>Stop watching {L.name}? Files already converted stay as they are.</span>
                    <button type="button" className="btn bad sm" onClick={remove} disabled={busy}>Remove</button>
                    <button type="button" className="btn sm" onClick={() => setConfirmRemove(false)}>Cancel</button>
                  </div>
                )}
              </div>
            </>
          )}
        </div>

        <div className="ovf">
          <span className="hint">{dirty ? "Not saved yet." : msg ?? ""}</span>
          <span className="sp"></span>
          <button type="button" className="btn sm" onClick={cancel} disabled={!dirty || busy}>Cancel</button>
          <button type="button" className="btn primary sm" onClick={save} disabled={!dirty || busy}>Save</button>
        </div>
      </aside>
    </>
  );
}
