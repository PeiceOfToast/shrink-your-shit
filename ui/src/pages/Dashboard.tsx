import { jobHint, sizeText } from "../format/job.ts";
import { clock, dayClock, dayOf } from "../format/time.ts";
import { Fragment, useState, type MouseEvent } from "react";
import { ApiError, api, formatBytes, formatCount, formatGB, type Attention, type Job, type Library } from "../api/index.ts";
import type { DaySeries, Series } from "../api/types.ts";
import { useApi } from "../api/useApi.ts";
import { Bars } from "../ui/Bars.tsx";
import { Icon } from "../ui/Icon.tsx";
import { libraryStatus } from "../ui/libraryStatus.ts";
import { Link, navigate } from "../router.tsx";

// Built exactly as draft-b/dashboard.html (see that file for the markup this mirrors). Batch 1, 11-26.

function paceLabel(secPerGB: number): string {
  const m = Math.floor(secPerGB / 60);
  const s = Math.round(secPerGB % 60);
  return `${m}:${String(s).padStart(2, "0")}`;
}

// The charts draw GB and job counts; the day records are bytes and counts.
const savedBars = (days: DaySeries[]): Series[] => [
  { c: "ok", v: days.map((d) => (d.freedBytes == null ? null : d.freedBytes / 1e9)) },
  { c: "accent", v: days.map((d) => (d.trashBytes == null ? null : d.trashBytes / 1e9)) },
];
const filesBars = (days: DaySeries[]): Series[] => [
  { c: "ok", v: days.map((d) => d.done) },
  { c: "text2", v: days.map((d) => d.skipped) },
  { c: "bad", v: days.map((d) => d.failed) },
];

function Lines({ lines }: { lines: string[] }) {
  return (
    <>
      {lines.map((l, i) => (
        <Fragment key={i}>
          {i > 0 && <br />}
          {l}
        </Fragment>
      ))}
    </>
  );
}

type ActionState = { s: "idle" } | { s: "busy" } | { s: "done"; line?: string } | { s: "failed"; message: string };

// A Needs attention row shows the server's sentences as they are. Its level sets the tile and pill tone, and its
// action runs through the method and path the server named.
function AttentionRow({ a }: { a: Attention }) {
  const [state, setState] = useState<ActionState>({ s: "idle" });
  const tone = a.level === "error" ? "bad" : "warn";
  const run = () => {
    const act = a.action;
    if (!act) return;
    setState({ s: "busy" });
    api.runAction(act.method, act.path, act.body).then(
      (r) => setState({ s: "done", line: r.line }),
      (e: unknown) => setState({ s: "failed", message: e instanceof ApiError ? e.message : "Couldn't do that." }),
    );
  };
  const pill = a.count > 1 ? `${a.count} times · ${clock(a.latestAt)}` : clock(a.latestAt);
  return (
    <li>
      <span className={`tile ${tone}`}>
        <Icon name="alert" />
      </span>
      <div className="main">
        {a.link ? (
          <Link className="name" to={a.link}>
            {a.what}
          </Link>
        ) : (
          <span className="name">{a.what}</span>
        )}
        <span className="hint">
          {a.means}{" "}
          {state.s === "busy" && (a.action?.path.endsWith("/capabilities/test") ? "Testing…" : "Working…")}
          {state.s === "done" &&
            (state.line ? (
              <>
                {state.line} {a.link && <Link to={a.link}>See on Workers</Link>}
              </>
            ) : (
              "Started."
            ))}
          {(state.s === "idle" || state.s === "failed") && a.action && (
            <button type="button" className="linklike" onClick={run}>
              {a.action.label}
            </button>
          )}
          {state.s === "failed" && <> {state.message}</>}
        </span>
      </div>
      <div className="side">
        <span className={`pill ${tone}`}>{pill}</span>
      </div>
    </li>
  );
}

function ConvertingRow({ j }: { j: Job }) {
  const open = (e: MouseEvent<HTMLLIElement>) => {
    if ((e.target as HTMLElement).closest("a")) return;
    navigate(`/jobs/${j.id}/log`);
  };
  return (
    <li className="row-link" onClick={open}>
      <span className="tile">
        <Lines lines={j.tileLines ?? []} />
      </span>
      <div className="main">
        <Link className="name" to={`/jobs/${j.id}`}>
          {j.file}
        </Link>
        <div className="chips">
          <span>
            <Icon name="folder" />
            {j.library}
          </span>
          <span>
            <Icon name="worker" />
            {j.worker}
          </span>
          <span>started {j.startedAt ? clock(j.startedAt) : "—"}</span>
        </div>
        <span className="hint">{jobHint(j)}</span>
      </div>
      <div className="side">
        <span className="pill run">
          <Icon name="play" />
          Running · {j.progress?.pct ?? 0}%
        </span>
      </div>
      <div className="lbar bar">
        <i style={{ width: `${j.progress?.pct ?? 0}%` }}></i>
      </div>
    </li>
  );
}

function LibraryRow({ l }: { l: Library }) {
  const status = libraryStatus(l);
  return (
    <tr>
      <td className="full">
        <b>{l.name}</b> <span className="muted sm">{l.path}</span>
      </td>
      <td data-l="Workflow">{l.workflow?.name ?? "—"}</td>
      <td className="num tnum" data-l="Files">{formatCount(l.files)}</td>
      <td className="num tnum" data-l="Waiting">{formatCount(l.waiting)}</td>
      <td className="num tnum" data-l="Saved">{l.savedBytes ? `${formatGB(l.savedBytes / 1e9)} · ${l.savedPct}%` : "—"}</td>
      <td>
        <span className={`pill ${status.kind}`}>{status.label}</span>
      </td>
    </tr>
  );
}

export function Dashboard() {
  const overview = useApi(() => api.getOverview(), []);
  const attention = useApi(() => api.getAttention(), []);
  const o = overview.data;
  const down = !!overview.error;
  const UNREACHABLE = "Can't change this while the server can't be reached";

  return (
    <>
      <div className="pagehead">
        <h1>Dashboard</h1>
        <span className="sp"></span>
        <button
          type="button"
          className={`btn ghost sm${down ? " dis" : ""}`}
          disabled={down}
          title={down ? UNREACHABLE : undefined}
          onClick={() => void api.scanAllLibraries()}
        >
          <Icon name="refresh" />
          Scan all
        </button>
      </div>

      {overview.loading && !o && <p className="muted">Loading…</p>}
      {overview.error && !o && (
        <section className="card">
          <p className="bad">Can't load the dashboard. Trying again.</p>
          <button type="button" className="btn ghost sm" onClick={overview.reload}>
            Try now
          </button>
        </section>
      )}

      {o && (
        <>
          <div className="kpis">
            <div className="kpi">
              <span className="l">Freed</span>
              <span className="v ok">{formatGB(o.freed.totalBytes / 1e9)}</span>
              <span className="d">+{formatGB(o.freed.todayBytes / 1e9)} today</span>
            </div>
            <div className="kpi">
              <span className="l">Converted</span>
              <span className="v ">{formatCount(o.converted.total)}</span>
              <span className="d">{o.converted.perDay} / day</span>
            </div>
            <div className="kpi">
              <span className="l">Waiting</span>
              <span className="v ">{formatCount(o.waiting.count)}</span>
              <span className="d">{o.waiting.nextScanAt ? <>next {dayClock(o.waiting.nextScanAt)}</> : "no scan scheduled"}</span>
            </div>
            <div className="kpi">
              <span className="l">Failed 24 h</span>
              <span className={o.failed24h.count > 0 ? "v bad" : "v "}>{o.failed24h.count}</span>
              <span className="d">{o.failed24h.pct} %</span>
            </div>
            <div className="kpi">
              <span className="l">Avg shrink</span>
              <span className="v ">{o.avgShrink.pct} %</span>
              <span className="d">{paceLabel(o.avgShrink.secPerGB)} / GB</span>
            </div>
          </div>

          {o.libraries.length === 0 ? (
            // First run (no libraries): page 08 leads with this card; the other cards have nothing to show yet.
            <section className="card wide">
              <h2>Add a library to start</h2>
              <p>
                <Link className="btn primary" to="/libraries?add=1">
                  Add library
                </Link>
              </p>
            </section>
          ) : (
          <div className="grid2">
            <section className="card wide">
              <h2>
                Needs attention <span className="muted" style={{ textTransform: "none", letterSpacing: 0, fontFamily: "var(--sans)" }}>{attention.data?.length ?? 0}</span>
                <Link className="more" to="/jobs?state=failed">failed jobs →</Link>
              </h2>
              {attention.data && attention.data.length === 0 && <p className="muted">Nothing needs attention.</p>}
              {attention.data && attention.data.length > 0 && (
                <ul className="rows">
                  {attention.data.map((a) => (
                    <AttentionRow key={a.id} a={a} />
                  ))}
                </ul>
              )}
            </section>

            <section className="card">
              <h2>
                Space saved
                <Link className="more" to="/jobs?state=done">Done jobs</Link>
              </h2>
              <div className="big tnum ok">
                {formatGB(o.freed.totalBytes / 1e9)}
                <small>freed</small>
              </div>
              <div className="sub tnum">
                <span>
                  {o.freed.nextReleaseBytes != null && o.freed.nextReleaseAt && (
                    <>{sizeText(o.freed.nextReleaseBytes)} more on {dayOf(o.freed.nextReleaseAt)}, when originals leave the trash · </>
                  )}
                  <Link to="/settings/processing">{formatBytes(o.freed.inTrashBytes)} in the trash</Link>
                </span>
                <span>
                  New files are <b>{o.avgShrink.pct}% smaller</b> on average
                </span>
              </div>
              <ul className="legend">
                <li>
                  <i style={{ background: "var(--ok)" }}></i>Freed
                </li>
                <li>
                  <i style={{ background: "var(--accent)" }}></i>In the trash
                </li>
              </ul>
              <Bars series={savedBars(o.series.days)} />
              <div className="axis tnum">
                <span>27 Oct</span>
                <span>10 Nov</span>
                <span>Today</span>
              </div>
            </section>

            <section className="card">
              <h2>
                Files converted
                <Link className="more" to="/jobs">All jobs</Link>
              </h2>
              <div className="big tnum">
                {formatCount(o.converted.total)}
                <small>all time</small>
              </div>
              <div className="sub tnum">
                <span>
                  <b>{o.converted.perDay} per day</b> lately · last 24 h: {o.last24h.done} done, {o.last24h.skipped} skipped,{" "}
                  <Link to="/jobs?state=failed">{o.last24h.failed} failed</Link>
                </span>
                <span>
                  About <b>2 min 10 s</b> of converting per GB on this computer
                </span>
              </div>
              <ul className="legend">
                <li>
                  <i style={{ background: "var(--ok)" }}></i>Done
                </li>
                <li>
                  <i style={{ background: "var(--text2)" }}></i>Skipped
                </li>
                <li>
                  <i style={{ background: "var(--bad)" }}></i>Failed
                </li>
              </ul>
              <Bars series={filesBars(o.series.days)} />
              <div className="axis tnum">
                <span>27 Oct</span>
                <span>10 Nov</span>
                <span>Today</span>
              </div>
            </section>

            <section className="card wide">
              <h2>
                Converting now
                <span className="muted" style={{ textTransform: "none", letterSpacing: 0, fontFamily: "var(--sans)" }}>
                  {o.running.length} / 3 slots
                </span>
                <Link className="more" to="/jobs?state=running">all jobs →</Link>
              </h2>
              {o.running.length === 0 && <p className="muted">Nothing is converting right now.</p>}
              {o.running.length > 0 && (
                <ul className="rows">
                  {o.running.map((j) => (
                    <ConvertingRow key={j.id} j={j} />
                  ))}
                </ul>
              )}
            </section>

            <section className="card wide">
              <h2>
                Libraries
                <Link className="more" to="/libraries">libraries →</Link>
              </h2>
              {o.libraries.length > 0 && (
                <table className="tbl">
                  <thead>
                    <tr>
                      <th>Library</th>
                      <th>Workflow</th>
                      <th className="num">Files</th>
                      <th className="num">Waiting</th>
                      <th className="num">Saved</th>
                      <th>Status</th>
                    </tr>
                  </thead>
                  <tbody>
                    {o.libraries.map((l) => (
                      <LibraryRow key={l.id} l={l} />
                    ))}
                  </tbody>
                </table>
              )}
            </section>
          </div>
          )}
        </>
      )}
    </>
  );
}
