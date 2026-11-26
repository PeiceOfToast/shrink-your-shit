import { navigate, Link, useLocation } from "../router.tsx";
import { api, formatCount, type Job, type JobCounts, type JobState } from "../api/index.ts";
import { useApi } from "../api/useApi.ts";

// Built exactly as draft-b/jobs.html. The only addition is the row click (opens /jobs/:id).

const TABS: { key: JobState | "all"; label: string; count: keyof JobCounts }[] = [
  { key: "all", label: "All", count: "all" },
  { key: "waiting", label: "Waiting", count: "waiting" },
  { key: "running", label: "Running", count: "running" },
  { key: "failed", label: "Failed", count: "failed" },
  { key: "done", label: "Done", count: "done" },
  { key: "skipped", label: "Skipped", count: "skipped" },
];

const PILL: Record<JobState, { cls: string; label: string }> = {
  waiting: { cls: "", label: "Waiting" },
  running: { cls: "run", label: "Running" },
  done: { cls: "ok", label: "Done" },
  failed: { cls: "bad", label: "Failed" },
  skipped: { cls: "skip", label: "Skipped" },
};

function JobRow({ j }: { j: Job }) {
  const open = () => navigate(`/jobs/${j.id}`);
  return (
    <tr className="row-link" tabIndex={0} onClick={open} onKeyDown={(e) => e.key === "Enter" && open()}>
      <td className="full">
        <span className={`pill ${PILL[j.state].cls}`}>{PILL[j.state].label}</span>
      </td>
      <td className="full">
        <b>{j.file}</b>
        <br />
        <span className="muted sm">{[j.library, j.workflow, j.worker].filter(Boolean).join(" · ")}</span>
      </td>
      <td data-l="Step">{j.step}</td>
      <td className="num tnum" data-l="Progress">{j.progress != null ? `${j.progress} %` : "—"}</td>
      <td className="num tnum" data-l="When">{j.when}</td>
      <td className="num tnum" data-l="Result">
        <span className={j.resultTone}>{j.result}</span>
      </td>
    </tr>
  );
}

export function Jobs() {
  const { query } = useLocation();
  const raw = query.get("state");
  const state = (TABS.find((t) => t.key === raw)?.key ?? "all") as JobState | "all";
  const page = useApi(() => api.getJobs({ state }), [state]);
  const counts = page.data?.counts;

  return (
    <>
      <div className="pagehead">
        <h1>Jobs</h1>
        <span className="muted sm">Every file the workers picked up, and what happened to it.</span>
        <span className="sp"></span>
        <button type="button" className="btn ghost sm">Filter</button>
        <button type="button" className="btn ghost sm">Columns</button>
      </div>

      <div className="tabs">
        {TABS.map((t) => (
          <Link key={t.key} to={t.key === "all" ? "/jobs" : `/jobs?state=${t.key}`} aria-current={state === t.key ? "page" : undefined}>
            {t.label}
            <b>{counts ? formatCount(counts[t.count]) : "—"}</b>
          </Link>
        ))}
      </div>

      <section className="card" style={{ padding: 0 }}>
        {page.loading && !page.data && <p className="muted pad">Loading…</p>}
        {page.error && !page.data && (
          <p className="bad pad">
            Can't load jobs. <button type="button" className="btn ghost sm" onClick={page.reload}>Try now</button>
          </p>
        )}
        {page.data && page.data.items.length === 0 && (
          <p className="muted pad">
            {counts?.all === 0 ? "Nothing has been converted yet. Jobs appear here once a library is scanned." : "Nothing in this tab."}
          </p>
        )}
        {page.data && page.data.items.length > 0 && (
          <table className="tbl">
            <thead>
              <tr>
                <th>Status</th>
                <th>File</th>
                <th>Step</th>
                <th className="num">Progress</th>
                <th className="num">When</th>
                <th className="num">Result</th>
              </tr>
            </thead>
            <tbody>
              {page.data.items.map((j) => (
                <JobRow key={j.id} j={j} />
              ))}
            </tbody>
          </table>
        )}
      </section>
    </>
  );
}
