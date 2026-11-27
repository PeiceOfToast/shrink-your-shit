import { useEffect } from "react";
import { navigate, useLocation } from "../router.tsx";
import { api, formatCount, onSummaryChanged, type Worker } from "../api/index.ts";
import { useApi } from "../api/useApi.ts";
import { AddWorker } from "./AddWorker.tsx";
import { StatePill } from "./workerParts.tsx";

// Built from pages/10-workers.md. A row click opens the worker's page; ?add=1 opens Add worker.
// On a phone (see .wkr in app.css) each worker is two lines: name and state, then address · graphics card · Now.

function WorkerRow({ w }: { w: Worker }) {
  const open = () => navigate(`/workers/${w.id}`);
  const where = w.local ? "this computer" : w.address;
  return (
    <tr className="row-link" tabIndex={0} onClick={open} onKeyDown={(e) => e.key === "Enter" && open()}>
      <td className="wk-name">
        <b>{w.name}</b>
        <div className="muted sm wk-addr">{where}</div>
      </td>
      <td className="wk-state">
        <StatePill w={w} />
      </td>
      <td className="num tnum wk-slots">{w.slots}</td>
      <td className="wk-gpu">{w.gpu ? <span className="mono sm">{w.gpu.name}</span> : "None found"}</td>
      <td className="wk-now">
        {w.now ? (
          <>
            {w.now.file}
            <br />
            <span className="muted sm">
              {w.now.step.name} · step {w.now.step.index} of {w.now.step.count}
            </span>
          </>
        ) : (
          "—"
        )}
      </td>
      <td className="num tnum wk-done">{formatCount(w.doneToday)}</td>
      <td className="wk-sub muted sm">
        {where}
        {w.gpu && (
          <>
            {" · "}
            <span className="mono">{w.gpu.name}</span>
          </>
        )}
        {w.now && ` · Now: ${w.now.file}`}
      </td>
    </tr>
  );
}

export function Workers() {
  const { query } = useLocation();
  const adding = query.get("add") === "1";
  // Reload when Add worker opens or closes, so a joined worker shows at once.
  const list = useApi(() => api.getWorkers(), [adding]);
  // The all-workers switch changes every worker's paused-for-all line, so the list reads again when it changes.
  useEffect(() => onSummaryChanged(list.reload), [list.reload]);
  const data = list.data;
  const addWorker = () => navigate("/workers?add=1");

  return (
    <>
      <div className="pagehead">
        <h1>Workers</h1>
        <span className="muted sm">The machines that convert files, and what each can do.</span>
        <span className="sp"></span>
        <button type="button" className="btn primary sm" onClick={addWorker}>Add worker</button>
      </div>

      {data && data.length === 1 && data[0].local && (
        <div className="note">
          <span>
            Add a worker to convert files on another computer or a NAS as well. Each worker converts its own files, so more
            workers get more done at once.{" "}
            <button type="button" className="btn sm" onClick={addWorker}>Add worker</button>
          </span>
        </div>
      )}

      <section className="card" style={{ padding: 0 }}>
        {list.error && !data && (
          <p className="bad pad">
            Can't load workers.{" "}
            <button type="button" className="btn ghost sm" onClick={list.reload}>Try now</button>
          </p>
        )}
        {list.loading && !data && (
          <table className="tbl" aria-busy="true">
            <tbody>
              {[0, 1].map((i) => (
                <tr key={i}>
                  <td colSpan={6}>
                    <span className="skel" />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
        {data && data.length > 0 && (
          <table className="tbl wkr">
            <thead>
              <tr>
                <th>Worker</th>
                <th>State</th>
                <th className="num">Files at once</th>
                <th>Graphics card</th>
                <th>Now</th>
                <th className="num">Done today</th>
              </tr>
            </thead>
            <tbody>
              {data.map((w) => (
                <WorkerRow key={w.id} w={w} />
              ))}
            </tbody>
          </table>
        )}
      </section>

      {adding && <AddWorker onClose={() => navigate("/workers")} />}
    </>
  );
}
