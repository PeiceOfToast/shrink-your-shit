import { ago, dayClock } from "../format/time.ts";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { api, formatBytes, formatCount, type Summary } from "../api/index.ts";
import { useApi } from "../api/useApi.ts";
import { useConnectionOk } from "../api/connection.ts";
import { Icon } from "../ui/Icon.tsx";
import type { IconName } from "../ui/icon-paths.ts";
import { Link, navigate, useLocation } from "../router.tsx";
import { Search } from "./Search.tsx";
import { applyTheme, nextTheme, readTheme, type ThemeMode } from "./theme.ts";

// Main sections in the order decided 11-26 (Dashboard · Libraries · Workflows · Jobs · Workers · Settings).
const SECTIONS: { key: string; label: string; to: string; icon: IconName }[] = [
  { key: "dashboard", label: "Dashboard", to: "/", icon: "dash" },
  { key: "libraries", label: "Libraries", to: "/libraries", icon: "lib" },
  { key: "workflows", label: "Workflows", to: "/workflows", icon: "flow" },
  { key: "jobs", label: "Jobs", to: "/jobs", icon: "jobs" },
  { key: "workers", label: "Workers", to: "/workers", icon: "worker" },
  { key: "settings", label: "Settings", to: "/settings", icon: "settings" },
];

function activeKey(pathname: string): string {
  const first = pathname.split("/").filter(Boolean)[0];
  return first ?? "dashboard";
}

// A small dropdown: opens on click, closes on outside click or Escape.
function Menu({ label, icon, primary, items }: { label: string; icon?: IconName; primary?: boolean; items: { label: string; to: string }[] }) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (!ref.current?.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    document.addEventListener("mousedown", onDown);
    window.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDown);
      window.removeEventListener("keydown", onKey);
    };
  }, [open]);
  return (
    <div className="menu" ref={ref}>
      <button type="button" className={`btn sm ${primary ? "primary" : "ghost"}`} aria-haspopup="menu" aria-expanded={open} onClick={() => setOpen((o) => !o)}>
        {icon && <Icon name={icon} />}
        {label}
      </button>
      {open && (
        <div className="menu-panel" role="menu">
          {items.map((it) => (
            <button
              key={it.label}
              type="button"
              role="menuitem"
              onClick={() => {
                setOpen(false);
                navigate(it.to);
              }}
            >
              {it.label}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

function StatusLine({ summary, ok }: { summary?: Summary; ok: boolean }) {
  const [open, setOpen] = useState(false);
  if (!ok) {
    return (
      <div className="statusbar statusbar-lost" role="status">
        <span className="lost">Can't reach the server · trying again</span>
      </div>
    );
  }
  const s = summary;
  return (
    <div className={`statusbar ${open ? "open" : ""}`} role="status" onClick={() => setOpen((o) => !o)}>
      <Link to="/workers">
        <i></i>
        <b>converting</b> {s ? `${s.converting.running} of ${s.converting.workers} workers` : "—"}
      </Link>
      <Link to="/jobs?state=waiting">
        queue <b>{s ? formatCount(s.queue) : "—"}</b>
      </Link>
      <span className="lg-only">
        last done <b>{s ? ago(s.lastDoneAt) : "—"}</b>
      </span>
      <Link className="lg-only" to="/libraries">
        next scan <b>{s ? dayClock(s.nextScanAt) : "—"}</b>
      </Link>
      <Link className="lg-only" to="/settings/processing">
        disk free <b>{s ? formatBytes(s.diskFreeBytes) : "—"}</b>
      </Link>
      {s && s.newErrors > 0 && (
        <Link className="lg-only" to="/settings/log">
          {s.newErrors} new errors
        </Link>
      )}
    </div>
  );
}

export function Shell({ children }: { children: ReactNode }) {
  const { pathname } = useLocation();
  const summary = useApi(() => api.getSummary(), []);
  const ok = useConnectionOk();
  const [theme, setTheme] = useState<ThemeMode>(() => readTheme());
  const [searching, setSearching] = useState(false);
  const active = activeKey(pathname);

  useEffect(() => {
    applyTheme(theme);
  }, [theme]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setSearching((v) => !v);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  const failed = summary.data?.failed24h ?? 0;
  const offline = summary.data?.workersOffline ?? 0;
  const badges: Record<string, { n: number; bad: boolean } | undefined> = {
    jobs: failed > 0 ? { n: failed, bad: true } : undefined,
    workers: offline > 0 ? { n: offline, bad: false } : undefined,
  };

  return (
    <div className="app">
      <header className="topbar">
        <Link className="brand" to="/">
          <span className="logo">
            <Icon name="film" />
          </span>
          syp
        </Link>
        <nav aria-label="Main">
          {SECTIONS.map((s) => (
            <Link key={s.key} to={s.to} aria-current={active === s.key ? "page" : undefined}>
              <Icon name={s.icon} />
              <span>{s.label}</span>
              {badges[s.key] && <span className={`badge ${badges[s.key]?.bad ? "bad" : ""}`}>{badges[s.key]?.n}</span>}
            </Link>
          ))}
        </nav>
        <span className="sp"></span>
        <button type="button" className="search" onClick={() => setSearching(true)}>
          <Icon name="search" />
          Search
          <kbd>Ctrl K</kbd>
        </button>
        <Menu
          label="New"
          icon="plus"
          primary
          items={[
            { label: "Add library", to: "/libraries?add=1" },
            { label: "New workflow", to: "/workflows/new" },
            { label: "Process one file now", to: "/process" },
            { label: "Add worker", to: "/workers?add=1" },
          ]}
        />
        <Menu label="Account" items={[{ label: "Your access tokens", to: "/settings/access" }, { label: "Sign out", to: "/signin" }]} />
        <button
          type="button"
          className="btn ghost sm"
          aria-label={`Theme: ${theme}`}
          title={`Theme: ${theme}`}
          onClick={() => setTheme((t) => nextTheme(t))}
        >
          <Icon name="sun" />
        </button>
      </header>
      <StatusLine summary={summary.data} ok={ok} />
      <main className="main-col">
        <div className="phonebar">
          <b>syp</b>
          <span className="sp"></span>
          <Menu label="New" icon="plus" primary items={[
            { label: "Add library", to: "/libraries?add=1" },
            { label: "New workflow", to: "/workflows/new" },
            { label: "Process one file now", to: "/process" },
            { label: "Add worker", to: "/workers?add=1" },
          ]} />
        </div>
        {children}
        <nav className="bottomnav" aria-label="Main">
          {SECTIONS.map((s) => (
            <Link key={s.key} to={s.to} aria-current={active === s.key ? "page" : undefined}>
              <Icon name={s.icon} />
              {s.label}
            </Link>
          ))}
        </nav>
      </main>
      {searching && <Search onClose={() => setSearching(false)} />}
    </div>
  );
}
