import { useRef, useState } from "react";
import { navigate } from "../router.tsx";
import { api, type WorkflowKind, type WorkflowRow } from "../api/index.ts";
import { useApi } from "../api/useApi.ts";
import { ago } from "../format/time.ts";
import { refusedText } from "../format/workflow.ts";
import { useKeys } from "../ui/useKeys.ts";

// Workflows list (page 04). Only the Video group is shown (decisions.md:57); the other groups come back later.
const GROUPS: { kind: WorkflowKind; title: string }[] = [{ kind: "video", title: "Video" }];

type Dialog = { kind: "refused"; names: string[] } | { kind: "confirm"; row: WorkflowRow };

function download(name: string, data: unknown) {
  const url = URL.createObjectURL(new Blob([JSON.stringify(data, null, 2)], { type: "application/json" }));
  const a = document.createElement("a");
  a.href = url;
  a.download = `${name}.json`;
  a.click();
  URL.revokeObjectURL(url);
}

function StatePill({ w }: { w: WorkflowRow }) {
  if (w.state === "problems") return <span className="pill warn">{w.problemCount} {w.problemCount === 1 ? "problem" : "problems"}</span>;
  if (w.state === "published") return <span className="pill ok">Published</span>;
  return <span className="pill">Draft</span>;
}

export function Workflows() {
  const list = useApi(() => api.listWorkflows(), []);
  const [q, setQ] = useState("");
  const [menuFor, setMenuFor] = useState<string | null>(null);
  const [dialog, setDialog] = useState<Dialog | null>(null);
  const [actErr, setActErr] = useState<string | null>(null);
  const fileRef = useRef<HTMLInputElement>(null);

  useKeys((e) => {
    if (e.key !== "Escape") return;
    if (dialog) setDialog(null);
    else setMenuFor(null);
  });

  const fail = (e: unknown) => setActErr(e instanceof Error ? e.message : String(e));

  const duplicate = async (w: WorkflowRow) => {
    setMenuFor(null);
    try {
      await api.duplicateWorkflow(w.id);
      list.reload();
    } catch (e) {
      fail(e);
    }
  };

  const exportOne = async (w: WorkflowRow) => {
    setMenuFor(null);
    try {
      download(w.name.toLowerCase().replace(/\s+/g, "-"), await api.exportWorkflow(w.id));
    } catch (e) {
      fail(e);
    }
  };

  // A workflow a library uses is refused before any request; the server refuses it again if the list was stale.
  const askDelete = (w: WorkflowRow) => {
    setMenuFor(null);
    setActErr(null);
    if (w.usedBy.length > 0) setDialog({ kind: "refused", names: w.usedBy.map((u) => u.name) });
    else setDialog({ kind: "confirm", row: w });
  };

  const doDelete = async (w: WorkflowRow) => {
    try {
      const r = await api.deleteWorkflow(w.id);
      if (r.ok) {
        setDialog(null);
        list.reload();
      } else {
        setDialog({ kind: "refused", names: r.usedBy.map((u) => u.name) });
      }
    } catch (e) {
      setDialog(null);
      fail(e);
    }
  };

  const importFile = async (file: File) => {
    setActErr(null);
    try {
      const r = await api.importWorkflow(await file.text());
      navigate(`/workflows/${r.id}`);
    } catch (e) {
      fail(e);
    }
  };

  const needle = q.trim().toLowerCase();
  const rows = (list.data ?? []).filter((w) => !needle || w.name.toLowerCase().includes(needle) || w.description.toLowerCase().includes(needle));

  return (
    <>
      <div className="pagehead">
        <h1>Workflows</h1>
        <span className="muted sm">How the videos in a library are converted.</span>
        <span className="sp"></span>
        <button type="button" className="btn primary sm" onClick={() => navigate("/workflows/new")}>New workflow</button>
        <button type="button" className="btn sm" onClick={() => fileRef.current?.click()}>Import</button>
        <input
          ref={fileRef}
          type="file"
          accept="application/json,.json"
          hidden
          data-testid="import-file"
          onChange={(e) => {
            const f = e.target.files?.[0];
            e.target.value = "";
            if (f) importFile(f);
          }}
        />
      </div>

      <div className="row" style={{ marginBottom: 12 }}>
        <input className="inp" type="search" placeholder="Filter workflows" aria-label="Filter workflows" value={q} onChange={(e) => setQ(e.target.value)} style={{ maxWidth: 320 }} />
      </div>

      {actErr && <p className="err" role="alert">{actErr}</p>}

      <section className="card" style={{ padding: 0 }}>
        {list.error && !list.data && (
          <p className="bad pad">
            Can't load workflows.{" "}
            <button type="button" className="btn ghost sm" onClick={list.reload}>Try now</button>
          </p>
        )}
        {list.loading && !list.data && (
          <table className="tbl" aria-busy="true">
            <tbody>
              {[0, 1, 2].map((i) => (
                <tr key={i}>
                  <td colSpan={5}><span className="skel" /></td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
        {list.data && list.data.length === 0 && (
          <p className="muted pad">
            No workflows yet. Most people start with Shrink videos.{" "}
            <button type="button" className="btn primary sm" onClick={() => navigate("/workflows/new")}>New workflow</button>
          </p>
        )}
        {list.data && list.data.length > 0 && (
          <>
            {GROUPS.map((g) => {
              const items = rows.filter((w) => w.kind === g.kind);
              return (
                <div key={g.kind}>
                  <h2 className="sm muted pad" style={{ margin: 0 }}>{g.title}</h2>
                  {items.length === 0 ? (
                    <p className="muted pad">No workflows match.</p>
                  ) : (
                    <table className="tbl wft">
                      <thead>
                        <tr>
                          <th>Name</th>
                          <th>Used by</th>
                          <th>Last changed</th>
                          <th>State</th>
                          <th><span className="sr-only">Actions</span></th>
                        </tr>
                      </thead>
                      <tbody>
                        {items.map((w) => (
                          <tr key={w.id} className="row-link" tabIndex={0} onClick={() => navigate(`/workflows/${w.id}`)} onKeyDown={(e) => e.key === "Enter" && navigate(`/workflows/${w.id}`)}>
                            <td className="full">
                              <b>{w.name}</b>
                              <br />
                              <span className="muted sm">{w.description}</span>
                            </td>
                            <td data-l="Used by">{w.usedBy.length > 0 ? w.usedBy.map((u) => u.name).join(", ") : <span className="muted">not used</span>}</td>
                            <td data-l="Last changed">{ago(w.updatedAt)}</td>
                            <td data-l="State"><StatePill w={w} /></td>
                            <td onClick={(e) => e.stopPropagation()} onKeyDown={(e) => e.stopPropagation()}>
                              <span style={{ position: "relative" }}>
                                <button
                                  type="button"
                                  className="btn ghost sm"
                                  aria-label={`Actions for ${w.name}`}
                                  aria-haspopup="menu"
                                  aria-expanded={menuFor === w.id}
                                  onClick={() => setMenuFor(menuFor === w.id ? null : w.id)}
                                >
                                  ⋯
                                </button>
                                {menuFor === w.id && (
                                  <span role="menu" className="card" style={{ position: "absolute", right: 0, top: "100%", zIndex: 20, display: "grid", padding: 4, minWidth: 160 }}>
                                    <button type="button" role="menuitem" className="btn ghost sm" onClick={() => { setMenuFor(null); navigate(`/workflows/${w.id}`); }}>Edit</button>
                                    <button type="button" role="menuitem" className="btn ghost sm" onClick={() => duplicate(w)}>Duplicate</button>
                                    <button type="button" role="menuitem" className="btn ghost sm" onClick={() => exportOne(w)}>Export</button>
                                    <button type="button" role="menuitem" className="btn ghost sm bad" onClick={() => askDelete(w)}>Delete</button>
                                  </span>
                                )}
                              </span>
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  )}
                </div>
              );
            })}
          </>
        )}
      </section>

      {menuFor && <div style={{ position: "fixed", inset: 0, zIndex: 5 }} onClick={() => setMenuFor(null)} />}

      {dialog && (
        <div className="scrim" onClick={() => setDialog(null)}>
          <section className="card" role="dialog" aria-modal="true" aria-labelledby="wf-dlg" style={{ width: "min(440px, 100%)", height: "fit-content" }} onClick={(e) => e.stopPropagation()}>
            {dialog.kind === "refused" ? (
              <>
                <h2 id="wf-dlg">Delete this workflow?</h2>
                <p>{refusedText(dialog.names)}</p>
                <div className="row" style={{ justifyContent: "flex-end" }}>
                  <button type="button" className="btn sm" onClick={() => setDialog(null)}>OK</button>
                </div>
              </>
            ) : (
              <>
                <h2 id="wf-dlg">Delete {dialog.row.name}?</h2>
                <p>This can't be undone.</p>
                <div className="row" style={{ justifyContent: "flex-end" }}>
                  <button type="button" className="btn sm" onClick={() => setDialog(null)}>Cancel</button>
                  <button type="button" className="btn bad sm" onClick={() => doDelete(dialog.row)}>Delete</button>
                </div>
              </>
            )}
          </section>
        </div>
      )}
    </>
  );
}
