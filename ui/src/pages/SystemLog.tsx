import { useEffect, useRef, useState } from "react";
import { api, type LogEvent, type LogParams } from "../api/index.ts";
import { useApi } from "../api/useApi.ts";
import { clock, clockPrecise } from "../format/time.ts";
import { Link, navigate, useLocation } from "../router.tsx";
import { Icon } from "../ui/Icon.tsx";
import { LEVEL_LABEL, copyText, saveText, type LogLevelName } from "./logParts.ts";

// The System log (page 12): every app-wide event, newest last. Filters live in the URL, so a link can open a filtered log.
const LEVELS = [
  { key: "", label: "All" },
  { key: "info", label: "Info" },
  { key: "warn", label: "Warnings" },
  { key: "error", label: "Errors" },
] as const;

const RANGES = [
  { key: "hour", label: "Last hour" },
  { key: "today", label: "Today" },
  { key: "week", label: "Last 7 days" },
  { key: "custom", label: "Custom" },
] as const;
const DEFAULT_RANGE = "week";

const HOUR = 60 * 60_000;

// A datetime-local value ("2026-11-20T10:00") as an RFC 3339 UTC time, or undefined when it isn't a date yet.
function localToIso(v: string | null): string | undefined {
  if (!v) return undefined;
  const t = Date.parse(v);
  return Number.isNaN(t) ? undefined : new Date(t).toISOString();
}

function rangeFrom(range: string, now: number): string | undefined {
  if (range === "hour") return new Date(now - HOUR).toISOString();
  if (range === "week") return new Date(now - 7 * 24 * HOUR).toISOString();
  if (range === "today") {
    const d = new Date(now);
    d.setHours(0, 0, 0, 0);
    return d.toISOString();
  }
  return undefined;
}

// Writes the filters into the URL. Typing uses replace, so each keystroke doesn't add a history entry.
function writeQuery(patch: Record<string, string | null>, replace = false) {
  const q = new URLSearchParams(location.search);
  for (const [k, v] of Object.entries(patch)) {
    if (v == null || v === "") q.delete(k);
    else q.set(k, v);
  }
  const qs = q.toString();
  const url = location.pathname + (qs ? `?${qs}` : "");
  if (replace) {
    history.replaceState(null, "", url);
    window.dispatchEvent(new PopStateEvent("popstate"));
  } else {
    navigate(url);
  }
}

// A line the live stream adds is shown only if it matches the filters on screen.
function matches(e: LogEvent, p: LogParams): boolean {
  if (e.level === "debug" && !p.detail) return false;
  if (p.since != null && e.seq <= p.since) return false;
  if (p.level && e.level !== p.level) return false;
  if (p.source && e.source !== p.source) return false;
  if (p.library && e.libraryId !== p.library) return false;
  if (p.worker && e.workerId !== p.worker) return false;
  if (p.q && !e.message.toLowerCase().includes(p.q.toLowerCase())) return false;
  return true;
}

function Repeat({ e }: { e: LogEvent }) {
  const [show, setShow] = useState(false);
  if (!e.repeat) return null;
  const more = e.repeat.count - 1;
  return (
    <span className="rep">
      {" "}
      the same {more} more {more === 1 ? "time" : "times"} ·{" "}
      <button type="button" className="syslog-link" aria-expanded={show} onClick={() => setShow((s) => !s)}>
        {show ? "hide" : "show"}
      </button>
      {show && (
        <span className="rep-span">
          {" "}
          first {clockPrecise(e.repeat.firstAt)}, last {clockPrecise(e.repeat.lastAt)}
        </span>
      )}
    </span>
  );
}

function Line({ e }: { e: LogEvent }) {
  const lvl: LogLevelName = e.level === "debug" ? "detail" : e.level;
  const label = LEVEL_LABEL[lvl];
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    const ok = await copyText(`${clockPrecise(e.ts)} ${label} ${e.source} ${e.message}`);
    setCopied(ok);
    setTimeout(() => setCopied(false), 1500);
  };
  const links: { to: string; text: string }[] = [];
  if (e.jobId) links.push({ to: `/jobs/${e.jobId}/log`, text: "Open job" });
  if (e.libraryId) links.push({ to: `/libraries/${e.libraryId}`, text: "Open library" });
  if (e.workerId) links.push({ to: `/workers/${e.workerId}`, text: "Open worker" });
  return (
    <div className={`ln ${lvl}`}>
      <span className="t">{clockPrecise(e.ts)}</span>
      <span className="lv">{label}</span>
      <span className="src">{e.source}</span>
      <span className="msg">
        {e.message}
        {e.nextAction && <span className="next"> {e.nextAction}</span>}
        {links.map((l, i) => (
          <span key={l.to}>
            {i === 0 ? " " : " · "}
            <Link to={l.to}>{l.text}</Link>
          </span>
        ))}
        <Repeat e={e} />
      </span>
      <button type="button" className="btn ghost sm copy" onClick={copy} aria-label={copied ? "Copied" : "Copy line"} title="Copy line">
        <Icon name="copy" />
      </button>
    </div>
  );
}

export function SystemLog() {
  const { pathname, query } = useLocation();
  const level = query.get("level") ?? "";
  const source = query.get("source") ?? "";
  const worker = query.get("worker") ?? "";
  const library = query.get("library") ?? "";
  const range = query.get("range") ?? DEFAULT_RANGE;
  const from = query.get("from") ?? "";
  const to = query.get("to") ?? "";
  const q = query.get("q") ?? "";
  const since = query.get("since") ?? "";
  // The time of the last line the person saw, for the "New errors since" hint.
  const sinceAt = since ? api.getLogLineTime(Number(since)) : null;
  const detail = query.get("detail") === "1";
  const paramKey = query.toString();

  // The filters as the API takes them. The time range is read at load, so "last hour" moves with the clock.
  const paramsFor = (): LogParams => {
    const now = Date.now();
    return {
      level: (level || undefined) as LogParams["level"],
      source: source || undefined,
      worker: worker || undefined,
      library: library || undefined,
      q: q || undefined,
      detail: detail || undefined,
      since: since ? Number(since) : undefined,
      from: range === "custom" ? localToIso(from) : rangeFrom(range, now),
      to: range === "custom" ? localToIso(to) : undefined,
    };
  };

  const log = useApi(() => api.getLog({ ...paramsFor(), limit: api.LOG_PAGE_SIZE }), [paramKey]);
  const sources = useApi(() => api.getLogSources(), []);

  const [older, setOlder] = useState<LogEvent[]>([]);
  const [hasMore, setHasMore] = useState(true);
  const [live, setLive] = useState<LogEvent[]>([]);
  const [follow, setFollow] = useState(true);
  const [paused, setPaused] = useState(false);
  const [text, setText] = useState(q);
  const [filtersOpen, setFiltersOpen] = useState(false);
  const [msg, setMsg] = useState("");
  const listRef = useRef<HTMLDivElement>(null);

  // A new filter starts the list again.
  useEffect(() => {
    setOlder([]);
    setLive([]);
    setHasMore(true);
  }, [paramKey]);

  // Typing in the search box: the URL follows after a short pause.
  useEffect(() => {
    setText(q);
  }, [q]);
  useEffect(() => {
    if (text === q) return;
    const t = setTimeout(() => writeQuery({ q: text }, true), 300);
    return () => clearTimeout(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [text]);

  // Opening the page marks the errors up to its newest line as seen, which clears the status line's count.
  useEffect(() => {
    api
      .getLog({ limit: 1, detail: true })
      .then((p) => {
        const last = p.items[p.items.length - 1];
        if (last) return api.markLogSeen(last.seq);
      })
      .catch(() => {});
  }, []);

  // Follow: new lines arrive while the page is open. Scrolling up pauses; Follow resumes.
  useEffect(() => {
    if (!follow) return;
    return api.subscribeAppEvents((e) => {
      const p = paramsFor();
      if (matches(e, p)) setLive((l) => [...l, e]);
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [follow, paramKey]);

  const base = log.data?.items ?? [];
  const items = [...older, ...base, ...live];
  const counts = { ...(log.data?.counts ?? { info: 0, warn: 0, error: 0 }) };
  for (const e of live) if (e.level !== "debug") counts[e.level]++;

  useEffect(() => {
    const el = listRef.current;
    if (el && follow && !paused) el.scrollTop = el.scrollHeight;
  }, [items.length, follow, paused]);

  const onScroll = () => {
    const el = listRef.current;
    if (!el || !follow) return;
    setPaused(el.scrollHeight - el.scrollTop - el.clientHeight > 40);
  };

  const loadOlder = async () => {
    const oldest = items[0]?.seq;
    if (oldest == null) return;
    try {
      const p = await api.getLog({ ...paramsFor(), before: oldest, limit: api.LOG_PAGE_SIZE });
      setOlder((o) => [...p.items, ...o]);
      if (p.items.length < api.LOG_PAGE_SIZE) setHasMore(false);
    } catch (e) {
      setMsg(e instanceof Error ? e.message : "Couldn't load older lines. Try again.");
    }
  };

  const download = async () => {
    const text = await api.getLogDownload(paramsFor());
    saveText("system-log.txt", text);
  };

  const followClick = () => {
    if (!follow) {
      setFollow(true);
      setPaused(false);
    } else if (paused) {
      setPaused(false);
      const el = listRef.current;
      if (el) el.scrollTop = el.scrollHeight;
    } else {
      setFollow(false);
    }
  };

  const hasFilters = Boolean(level || source || worker || library || q || detail || since || range !== DEFAULT_RANGE);
  const sourceNames = sources.data ?? [];
  const sourceMissing = source && !sourceNames.includes(source);

  const toolbar = (
    <div className={`syslog-filters ${filtersOpen ? "open" : ""}`}>
      <div className="jd-tools">
        <div className="jd-seg" role="group" aria-label="Level">
          {LEVELS.map((l) => {
            const n = l.key === "" ? counts.info + counts.warn + counts.error : counts[l.key as "info" | "warn" | "error"];
            return (
              <button key={l.key || "all"} type="button" aria-pressed={level === l.key} onClick={() => writeQuery({ level: l.key })}>
                {l.label} <span className="muted">{n}</span>
              </button>
            );
          })}
        </div>
        <select className="sel" aria-label="Source" value={source} onChange={(e) => writeQuery({ source: e.target.value })}>
          <option value="">All sources</option>
          {sourceMissing && <option value={source}>{source}</option>}
          {sourceNames.map((s) => (
            <option key={s} value={s}>
              {s}
            </option>
          ))}
        </select>
        <select className="sel" aria-label="Time range" value={range} onChange={(e) => writeQuery({ range: e.target.value })}>
          {RANGES.map((r) => (
            <option key={r.key} value={r.key}>
              {r.label}
            </option>
          ))}
        </select>
        {range === "custom" && (
          <>
            <input className="inp" type="datetime-local" aria-label="From" value={from} onChange={(e) => writeQuery({ from: e.target.value })} />
            <input className="inp" type="datetime-local" aria-label="To" value={to} onChange={(e) => writeQuery({ to: e.target.value })} />
          </>
        )}
        <label className="syslog-check">
          <input type="checkbox" checked={detail} onChange={(e) => writeQuery({ detail: e.target.checked ? "1" : null })} /> Show detail
        </label>
        <input className="inp jd-search" type="search" aria-label="Search the log" placeholder="Search" value={text} onChange={(e) => setText(e.target.value)} />
      </div>
    </div>
  );

  return (
    <div className="syslog-page">
      <div className="pagehead">
        <h1>System log</h1>
        <span className="sp" /><div className="syslog-tools">
          <button type="button" className="btn ghost sm syslog-filters-toggle" aria-expanded={filtersOpen} onClick={() => setFiltersOpen((o) => !o)}>
            Filters
          </button>
          <button type="button" aria-pressed={follow} className={`btn sm ${follow ? "" : "ghost"}`} onClick={followClick}>
            {!follow ? "Follow" : paused ? "Paused · jump to newest" : "Following"}
          </button>
          <button type="button" className="btn ghost sm" onClick={download} disabled={!log.data}>
            Download
          </button>
        </div>
      </div>

      {(worker || library) && (
        <p className="hint">
          Only lines for {worker ? "one worker" : "one library"}.{" "}
          <button type="button" className="syslog-link" onClick={() => writeQuery({ worker: null, library: null })}>
            Show all lines
          </button>
        </p>
      )}

      {toolbar}

      <span role="status" className="hint">
        {msg}
      </span>

      {log.loading && !log.data && <p className="muted">Loading the log…</p>}

      {log.error && !log.data && (
        <div className="syslog-empty">
          <p>{log.error}</p>
          <button type="button" className="btn sm" onClick={log.reload}>
            Retry
          </button>
        </div>
      )}

      {since && (
        <p className="syslog-since hint">
          {sinceAt ? `New errors since ${clock(sinceAt)}` : "New errors"}{" "}
          <button type="button" className="btn ghost sm" onClick={() => writeQuery({ since: "" })}>Show all lines</button>
        </p>
      )}
      {log.data && items.length === 0 && !hasFilters && <p className="muted">Nothing logged yet.</p>}

      {log.data && items.length === 0 && hasFilters && (
        <div className="syslog-empty">
          <p>No lines match these filters.</p>
          <button type="button" className="btn ghost sm" onClick={() => navigate(pathname)}>
            Clear filters
          </button>
        </div>
      )}

      {log.data && items.length > 0 && (
        <div className="log syslog syslog-list" ref={listRef} onScroll={onScroll} aria-label="System log lines" role="log">
          {hasMore && base.length >= api.LOG_PAGE_SIZE && (
            <button type="button" className="btn ghost sm" onClick={loadOlder}>
              Load older lines
            </button>
          )}
          {items.map((e) => (
            <Line key={e.seq} e={e} />
          ))}
        </div>
      )}
    </div>
  );
}
