import { clock, clockPrecise, duration, timeLeft } from "../format/time.ts";
import { formatGB } from "../api/index.ts";

// Sizes read as GB in the job panel; bytes come from the server.
const gb = (bytes: number) => formatGB(bytes / 1e9);
// Media rows: text as is; durations (ms) and sizes (bytes) formatted here.
const mediaValue = (r: MediaRow, v: string | number) =>
  r.kind === "duration" ? duration(Number(v)) : r.kind === "bytes" ? gb(Number(v)) : String(v);
import { useEffect, useRef, useState, type ReactNode } from "react";
import { api, type Job, type JobDetail as Detail, type LogLine, type LogStep, type MediaRow, type StepOutcome } from "../api/index.ts";
import { useApi } from "../api/useApi.ts";
import { Icon } from "../ui/Icon.tsx";
import { useKeys } from "../ui/useKeys.ts";
import { Link, navigate, useLocation } from "../router.tsx";
import { PILL } from "./Jobs.tsx";

// The job panel over the Jobs list (page 03). Opens at /jobs/:id; /jobs/:id/log opens the Log tab.
type Tab = "summary" | "log" | "steps" | "before" | "history";
const TABS: { key: Tab; label: string }[] = [
  { key: "summary", label: "Summary" },
  { key: "log", label: "Log" },
  { key: "steps", label: "Steps" },
  { key: "before", label: "Before and after" },
  { key: "history", label: "History" },
];

const OUTCOME: Record<StepOutcome, { label: string; cls: string }> = {
  done: { label: "Done", cls: "ok" },
  nothing: { label: "Nothing to do", cls: "" },
  failed: { label: "Failed", cls: "bad" },
  running: { label: "Running", cls: "" },
  skipped: { label: "Skipped", cls: "" },
  waiting: { label: "Waiting", cls: "" },
};

const LEVELS = [
  { key: "all", label: "All" },
  { key: "info", label: "Info" },
  { key: "warn", label: "Warnings" },
  { key: "error", label: "Errors" },
  { key: "detail", label: "Details" },
] as const;
type Level = (typeof LEVELS)[number]["key"];

const LEVEL_LABEL: Record<LogLine["level"], string> = { info: "INFO", warn: "WARN", error: "ERROR", detail: "DETAIL" };
const GROUPS: MediaRow["group"][] = ["General", "Video", "Audio", "Subtitles"];

function fmtMs(ms: number | null): string {
  if (ms == null) return "—";
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)} s`;
  return `${Math.floor(ms / 60_000)} min ${Math.round((ms % 60_000) / 1000)} s`;
}

async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    return false;
  }
}

// The text of the whole panel, for Copy all and the .txt download.
function logText(d: Detail): string {
  const head = `${d.job.file}\n${d.path}\n${d.job.result}\n`;
  const history = d.history.map((h) => `${clock(h.time)} ${h.text}`).join("\n");
  if (!d.logs) return `${head}\nThe detailed log is no longer kept.\n\nHistory\n${history}\n`;
  const steps = d.logs.map((s) =>
    [
      `${s.index}. ${s.name} · ${OUTCOME[s.outcome].label}`,
      `   ${s.decision}`,
      ...s.values.map((v) => `   ${v.name}: ${v.value} (${v.from})`),
      ...(s.command ? [`   $ ${s.command}`] : []),
      ...s.lines.map((l) => `   ${clockPrecise(l.time)} ${LEVEL_LABEL[l.level]} ${l.text}`),
    ].join("\n"),
  );
  return `${head}\n${steps.join("\n\n")}\n\nHistory\n${history}\n`;
}

export function JobDetail({ id, initialTab }: { id: string; initialTab: "summary" | "log" }) {
  const { query } = useLocation();
  const listState = query.get("state");
  const filterQs = listState ? `?state=${listState}` : "";
  const detail = useApi(() => api.getJob(id), [id]);
  const nb = useApi(() => api.getJobNeighbours(id, listState ?? "all"), [id, listState]);
  // The tab the reader picked stays put while ‹ › walks the list. Until they pick one, failed and running jobs open on Log.
  const [picked, setPicked] = useState<Tab | null>(null);
  // A "log" link on Steps opens the Log tab scrolled to that step.
  const [focusStep, setFocusStep] = useState<number | null>(null);
  const [confirm, setConfirm] = useState<"again" | "cancel" | null>(null);
  const [menu, setMenu] = useState(false);
  const [msg, setMsg] = useState("");
  const [busy, setBusy] = useState(false);
  // Phone width only: the actions after Retry collapse into a ⋯ menu.
  const [more, setMore] = useState(false);
  const titleRef = useRef<HTMLHeadingElement>(null);
  const acts = useRef<HTMLDivElement>(null);
  const moreBtn = useRef<HTMLButtonElement>(null);
  const d = detail.data;
  // A job that doesn't exist has no log, copy or workflow to open: only ‹ › and X stay.
  const missing = detail.code === "job_not_found";

  useEffect(() => {
    titleRef.current?.focus();
  }, [id]);

  const close = () => navigate(`/jobs${filterQs}`);
  // Escape closes an open menu first (focus back on ⋯); a second Escape closes the panel.
  useKeys((e) => {
    if (e.key !== "Escape") return;
    if (more || menu) {
      setMore(false);
      setMenu(false);
      if (more) moreBtn.current?.focus();
      return;
    }
    close();
  });
  // A press outside the actions row closes the ⋯ menu.
  useEffect(() => {
    if (!more) return;
    const onDown = (e: PointerEvent) => {
      if (!acts.current?.contains(e.target as Node)) setMore(false);
    };
    document.addEventListener("pointerdown", onDown);
    return () => document.removeEventListener("pointerdown", onDown);
  }, [more]);

  const fallback: Tab = initialTab === "log" || (d && (d.job.state === "failed" || d.job.state === "running")) ? "log" : "summary";
  const tab = picked ?? fallback;
  const go = (to: string | null) => {
    if (to) navigate(`/jobs/${to}${filterQs}`);
  };
  const openLog = (index: number) => {
    setFocusStep(index);
    setPicked("log");
  };

  const act = async (run: () => Promise<unknown>, done: string) => {
    setBusy(true);
    setMsg("");
    try {
      await run();
      setConfirm(null);
      setMsg(done);
      detail.reload();
    } catch (e) {
      setMsg(e instanceof Error ? e.message : "Something went wrong. Try again.");
    } finally {
      setBusy(false);
    }
  };

  const downloadLog = () => {
    if (!d) return;
    const url = URL.createObjectURL(new Blob([logText(d)], { type: "text/plain" }));
    const a = document.createElement("a");
    a.href = url;
    a.download = `job-${d.job.id}-log.txt`;
    a.click();
    URL.revokeObjectURL(url);
  };

  const copyAll = async () => {
    if (!d) return;
    const ok = await copyText(logText(d));
    setMsg(ok ? "Copied the whole log." : "Couldn't copy here. Select the text in the log instead.");
  };

  const job: Job | undefined = d?.job;
  const state = job?.state;

  return (
    <>
      <div className="scrim" onClick={(e) => e.target === e.currentTarget && close()} />
      <aside className="ov panel jd" role="dialog" aria-modal="true" aria-labelledby="jd-title">
        <header className="ovh">
          <div className="jd-title">
            {/* One line; the full path shows on hover and in Summary. */}
            <h2 id="jd-title" ref={titleRef} tabIndex={-1} title={d?.path} style={{ outline: "none" }}>
              {job ? job.file : "Job"}
            </h2>
          </div>
          {job && (
            <span className="jd-state">
              <span className={`pill ${PILL[job.state].cls}`}>{PILL[job.state].label}</span>
              <span className={`sm ${job.resultTone}`}>{job.result}</span>
              {job.state === "running" && d && (
                <span className="muted sm tnum">{job.progress ?? 0}% · {timeLeft(d.timeLeftMs) ?? "—"}</span>
              )}
            </span>
          )}
          <div className="jd-acts" ref={acts}>
            <button type="button" className="btn ghost sm" aria-label="Previous job" disabled={!nb.data?.prev} onClick={() => go(nb.data?.prev ?? null)}>‹</button>
            <button type="button" className="btn ghost sm" aria-label="Next job" disabled={!nb.data?.next} onClick={() => go(nb.data?.next ?? null)}>›</button>
            {state === "failed" && (
              <button type="button" className="btn sm" disabled={busy} onClick={() => act(() => api.runJobsAction("retry", [id]), "Queued to try again.")}>Retry</button>
            )}
            <div className={`jd-more${more ? " open" : ""}`}>
            {(state === "done" || state === "skipped") && (
              <button type="button" className="btn sm" disabled={busy} onClick={() => setConfirm("again")}>Run again</button>
            )}
            {(state === "running" || state === "waiting") && (
              <button type="button" className="btn sm" disabled={busy} onClick={() => setConfirm("cancel")}>Cancel</button>
            )}
            {!missing && (
            <span className="jd-menu-wrap">
              <button type="button" className="btn ghost sm" disabled={!d} aria-haspopup="menu" aria-expanded={menu} onClick={() => setMenu((m) => !m)}>
                Download log ▾
              </button>
              {menu && (
                <div className="jd-menu" role="menu">
                  <button
                    type="button"
                    role="menuitem"
                    onClick={() => {
                      setMenu(false);
                      downloadLog();
                    }}
                  >
                    .txt
                  </button>
                  {/* Placeholder: the zip of the step logs is not built in the mock. */}
                  <button
                    type="button"
                    role="menuitem"
                    onClick={() => {
                      setMenu(false);
                      setMsg("The .zip of the step logs comes with the backend. Nothing downloaded here.");
                    }}
                  >
                    .zip
                  </button>
                </div>
              )}
            </span>
            )}
            {!missing && (
              <button type="button" className="btn ghost sm" disabled={!d} onClick={copyAll}>Copy all</button>
            )}
            {!missing && <Link to="/workflows" className="btn ghost sm">Open in workflow</Link>}
            </div>
            {!missing && (
              <button ref={moreBtn} type="button" className="btn ghost sm jd-more-btn" aria-label="More actions" aria-expanded={more} onClick={() => setMore((m) => !m)}>
                ⋯
              </button>
            )}
          </div>
          <button type="button" className="btn ghost sm jd-close" aria-label="Close" onClick={close}>
            <Icon name="x" />
          </button>
        </header>

        <div className="ovb">
          {confirm === "again" && job && (
            <div className="jd-confirm" role="alert">
              <span>Run this file through {job.workflow} again? The current file is treated as the original.</span>
              <button type="button" className="btn primary sm" disabled={busy} onClick={() => act(() => api.convertAgain(id), "Queued again. This panel now shows the new attempt.")}>Run again</button>
              <button type="button" className="btn sm" onClick={() => setConfirm(null)}>Keep it</button>
            </div>
          )}
          {confirm === "cancel" && (
            <div className="jd-confirm" role="alert">
              <span>Stop converting? The original stays as it is.</span>
              <button type="button" className="btn primary sm" disabled={busy} onClick={() => act(() => api.runJobsAction("cancel", [id]), "Stopped.")}>Stop</button>
              <button type="button" className="btn sm" onClick={() => setConfirm(null)}>Keep going</button>
            </div>
          )}
          {msg && <p className="hint" role="status" style={{ margin: 0 }}>{msg}</p>}

          {detail.error && !d && detail.code === "job_not_found" && (
            <p className="bad">
              That job doesn't exist.{" "}
              <button type="button" className="btn ghost sm" onClick={() => navigate(`/jobs${filterQs}`)}>Back to jobs</button>
            </p>
          )}
          {detail.error && !d && detail.code !== "job_not_found" && (
            <p className="bad">
              {detail.error} <button type="button" className="btn ghost sm" onClick={detail.reload}>Try again</button>
            </p>
          )}
          {!d && !detail.error && <span className="skel" />}

          {d && (
            <>
              <nav className="tabs" role="tablist" aria-label="Job sections">
                {TABS.map((t) => (
                  <button key={t.key} type="button" role="tab" aria-selected={tab === t.key} onClick={() => setPicked(t.key)}>
                    {t.label}
                  </button>
                ))}
              </nav>
              {tab === "summary" && <SummaryTab d={d} busy={busy} onRetry={() => act(() => api.runJobsAction("retry", [id]), "Queued to try again.")} />}
              {tab === "log" && <LogTab d={d} focus={focusStep} />}
              {tab === "steps" && <StepsTab d={d} onOpenLog={openLog} />}
              {tab === "before" && <BeforeTab rows={d.media} />}
              {tab === "history" && <HistoryTab d={d} />}
            </>
          )}
        </div>
      </aside>
    </>
  );
}

function Fact({ label, value }: { label: string; value: ReactNode }) {
  return (
    <span>
      <span>{label}</span>
      <span className="tnum" style={{ textAlign: "right", overflowWrap: "anywhere" }}>{value}</span>
    </span>
  );
}

function SummaryTab({ d, busy, onRetry }: { d: Detail; busy: boolean; onRetry: () => void }) {
  // The track is the original size; the fill is how much the new file takes of it.
  const ratio = d.sizeAfterBytes != null ? Math.max(0, Math.min(1, d.sizeAfterBytes / d.sizeBeforeBytes)) : 0;
  return (
    <>
      {d.failure && (
        <div className="jd-fail" role="status">
          <b>Failed at {d.failure.step}</b>
          <span>{d.failure.reason}</span>
          <span className="muted sm">{d.failure.nextAction}</span>
          <div>
            <button type="button" className="btn sm" disabled={busy} onClick={onRetry}>{d.failure.actionLabel}</button>
          </div>
        </div>
      )}
      {d.skipReason && <p className="muted" style={{ margin: 0 }}>Skipped: {d.skipReason}</p>}

      <div className="jd-cards">
        <section className="card">
          <h3>File</h3>
          <div>
            <Fact label="Path" value={<span title={d.path}>{d.path}</span>} />
            <Fact label="Library" value={d.job.library} />
            <Fact label="Workflow" value={<Link to="/workflows">{d.workflow}</Link>} />
          </div>
          <div className="jd-bar-wrap">
            <span className="sm tnum">{gb(d.sizeBeforeBytes)} → {d.sizeAfterBytes != null ? gb(d.sizeAfterBytes) : "—"}</span>
            <div className="jd-bar" role="img" aria-label={`Size ${gb(d.sizeBeforeBytes)} before, ${d.sizeAfterBytes != null ? gb(d.sizeAfterBytes) : "not yet"} after`}>
              <span style={{ width: `${ratio * 100}%` }} />
            </div>
          </div>
        </section>
        <section className="card">
          <h3>Run</h3>
          <div>
            <Fact label="State" value={PILL[d.job.state].label} />
            <Fact label="Took" value={d.tookMs != null ? duration(d.tookMs) : "—"} />
            <Fact label="Worker" value={d.worker} />
            <Fact label="Started" value={d.startedAt ? clock(d.startedAt) : "—"} />
            <Fact label="Finished" value={d.finishedAt ? clock(d.finishedAt) : "—"} />
            <Fact label="Attempt" value={d.attempt} />
          </div>
        </section>
        <section className="card">
          <h3>Result</h3>
          <div>
            <Fact label="Written to" value={d.outputPath ?? "—"} />
            <Fact label="Written at" value={d.writtenAt ? clock(d.writtenAt) : "—"} />
            <Fact label="Original" value={d.original} />
          </div>
        </section>
      </div>

      <section className="card" style={{ padding: 0 }}>
        <table className="tbl">
          <thead>
            <tr>
              <th>#</th>
              <th>Step</th>
              <th>Outcome</th>
              <th className="num">Time</th>
            </tr>
          </thead>
          <tbody>
            {d.steps.map((s) => (
              <tr key={s.index}>
                <td className="tnum muted">{s.index}</td>
                <td>{s.name}</td>
                <td><span className={OUTCOME[s.outcome].cls}>{OUTCOME[s.outcome].label}</span></td>
                <td className="num tnum">{fmtMs(s.durationMs)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </section>
    </>
  );
}

function CopyButton({ text, label }: { text: string; label: string }) {
  const [done, setDone] = useState(false);
  return (
    <button type="button" className="btn ghost sm" style={{ alignSelf: "flex-start" }} onClick={async () => setDone(await copyText(text))}>
      {done ? "Copied" : label}
    </button>
  );
}

function LogLineRow({ l, step }: { l: LogLine; step?: LogStep }) {
  return (
    <div className={`ln ${l.level}`}>
      <span className="t">{clockPrecise(l.time)}</span>
      <span className="lv">{LEVEL_LABEL[l.level]}</span>
      <span>
        {step && <span className="muted">{step.index}. {step.name} · </span>}
        {l.text}
      </span>
    </div>
  );
}

function scrollToStep(index: number) {
  document.getElementById(`log-step-${index}`)?.scrollIntoView({ block: "start" });
}

function LogTab({ d, focus }: { d: Detail; focus: number | null }) {
  const [q, setQ] = useState("");
  const [level, setLevel] = useState<Level>("all");
  const [view, setView] = useState<"steps" | "one">("steps");
  const [closed, setClosed] = useState<Set<number>>(new Set());
  const logs = d.logs;

  useEffect(() => {
    if (focus != null) scrollToStep(focus);
  }, [focus]);

  if (!logs) {
    return <p className="muted">The detailed log is no longer kept. The steps and history are still here.</p>;
  }
  if (logs.length === 0) {
    return <p className="muted">Not started yet. The log fills in once a worker picks the file up.</p>;
  }

  const needle = q.trim().toLowerCase();
  const all = logs.flatMap((s) => s.lines.map((l) => ({ s, l })));
  const count = (k: Level) => (k === "all" ? all.length : all.filter((x) => x.l.level === k).length);
  const keep = (l: LogLine) => (level === "all" || l.level === level) && (!needle || l.text.toLowerCase().includes(needle));
  const sections = logs.map((s) => ({ s, lines: s.lines.filter(keep) })).filter((x) => x.lines.length > 0);
  const flat = all.filter((x) => keep(x.l));
  const allClosed = closed.size === logs.length;

  const toggle = (index: number) =>
    setClosed((prev) => {
      const next = new Set(prev);
      if (next.has(index)) next.delete(index);
      else next.add(index);
      return next;
    });
  const toggleAll = () => setClosed(allClosed ? new Set() : new Set(logs.map((s) => s.index)));

  return (
    <>
      <div className="jd-tools">
        <div className="jd-seg" role="group" aria-label="Level">
          {LEVELS.map((lv) => (
            <button key={lv.key} type="button" aria-pressed={level === lv.key} onClick={() => setLevel(lv.key)}>
              {lv.label} <span className="tnum">{count(lv.key)}</span>
            </button>
          ))}
        </div>
        <div className="jd-seg" role="group" aria-label="View">
          <button type="button" aria-pressed={view === "steps"} onClick={() => setView("steps")}>By step</button>
          <button type="button" aria-pressed={view === "one"} onClick={() => setView("one")}>As one log</button>
        </div>
        <input
          className="inp jd-search"
          type="search"
          placeholder="Search the log"
          aria-label="Search the log"
          value={q}
          onChange={(e) => setQ(e.target.value)}
        />
        {view === "steps" && (
          <button type="button" className="btn ghost sm" onClick={toggleAll}>{allClosed ? "Expand all" : "Collapse all"}</button>
        )}
      </div>

      <div className="jd-logwrap">
        <ol className="jd-timeline" aria-label="Steps">
          {d.steps.map((s) => {
            const logged = logs.some((l) => l.index === s.index);
            return (
              <li key={s.index} className={OUTCOME[s.outcome].cls}>
                <button type="button" disabled={!logged} onClick={() => scrollToStep(s.index)}>
                  <span className="jd-dot" aria-hidden />
                  <span>{s.index}. {s.name}</span>
                </button>
              </li>
            );
          })}
        </ol>

        <div className="jd-logmain">
          {sections.length === 0 && <p className="muted">Nothing matches.</p>}
          {view === "steps" &&
            sections.map(({ s, lines }) => {
              const open = !closed.has(s.index);
              return (
                <section className="log-step" id={`log-step-${s.index}`} key={s.index}>
                  <header>
                    <button type="button" className="jd-sec-toggle" aria-expanded={open} onClick={() => toggle(s.index)}>
                      {open ? "▾" : "▸"} <b>{s.index}. {s.name}</b>
                    </button>
                    <span className={OUTCOME[s.outcome].cls}>{OUTCOME[s.outcome].label}</span>
                    <span className="sp" />
                    <span className="muted sm tnum">{fmtMs(s.durationMs)}</span>
                  </header>
                  {open && (
                    <>
                      <span className="muted sm">{s.decision}</span>
                      {s.values.length > 0 && (
                        <dl className="jd-values">
                          {s.values.map((v) => (
                            <div key={v.name}>
                              <dt>{v.name}</dt>
                              <dd>{v.value} <span className="muted">· {v.from}</span></dd>
                            </div>
                          ))}
                        </dl>
                      )}
                      {s.command && (
                        <>
                          <pre>{s.command}</pre>
                          <CopyButton text={s.command} label="Copy command" />
                        </>
                      )}
                      <div className="log">
                        {lines.map((l, i) => (
                          <LogLineRow key={i} l={l} />
                        ))}
                      </div>
                    </>
                  )}
                </section>
              );
            })}
          {view === "one" && flat.length > 0 && (
            <div className="log">
              {flat.map((x, i) => (
                <LogLineRow key={i} l={x.l} step={x.s} />
              ))}
            </div>
          )}
        </div>
      </div>
    </>
  );
}

function StepsTab({ d, onOpenLog }: { d: Detail; onOpenLog: (index: number) => void }) {
  const logged = (index: number) => !!d.logs?.some((l) => l.index === index);
  return (
    <>
      <table className="tbl">
        <thead>
          <tr>
            <th>#</th>
            <th>Step</th>
            <th>Outcome</th>
            <th className="num">Time</th>
            <th>Log</th>
          </tr>
        </thead>
        <tbody>
          {d.steps.map((s) => (
            <tr key={s.index}>
              <td className="tnum muted">{s.index}</td>
              <td>{s.name}</td>
              <td><span className={OUTCOME[s.outcome].cls}>{OUTCOME[s.outcome].label}</span></td>
              <td className="num tnum">{fmtMs(s.durationMs)}</td>
              <td>
                <button type="button" className="btn ghost sm" disabled={!logged(s.index)} onClick={() => onOpenLog(s.index)}>
                  log
                </button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </>
  );
}

function BeforeTab({ rows }: { rows: MediaRow[] }) {
  const [changed, setChanged] = useState(false);
  return (
    <>
      <label className="sm"><input type="checkbox" checked={changed} onChange={(e) => setChanged(e.target.checked)} /> Changes only</label>
      <table className="tbl">
        <thead>
          <tr>
            <th>Field</th>
            <th>Before</th>
            <th>After</th>
          </tr>
        </thead>
        {GROUPS.map((g) => {
          const rs = rows.filter((r) => r.group === g && (!changed || r.changed));
          if (rs.length === 0) return null;
          return (
            <tbody key={g}>
              <tr className="jd-group">
                <th colSpan={3} scope="colgroup">{g}</th>
              </tr>
              {rs.map((r) => {
                const hit = r.changed && r.after !== null;
                return (
                  <tr key={r.field} className={hit ? "jd-changed" : undefined}>
                    <td>
                      {hit && <span className="jd-dot" aria-hidden />}
                      {r.field}
                    </td>
                    <td className="muted">{mediaValue(r, r.before)}</td>
                    <td>{r.after != null ? mediaValue(r, r.after) : "—"}</td>
                  </tr>
                );
              })}
            </tbody>
          );
        })}
      </table>
    </>
  );
}

function HistoryTab({ d }: { d: Detail }) {
  return (
    <div className="log">
      {d.history.map((h, i) => (
        <div key={i} className={`ln ${h.level}`}>
          <span className="t">{clock(h.time)}</span>
          <span className="lv">{LEVEL_LABEL[h.level]}</span>
          <span>{h.text}</span>
        </div>
      ))}
    </div>
  );
}
