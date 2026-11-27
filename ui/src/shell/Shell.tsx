import { ago, dayClock } from "../format/time.ts";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { api, formatBytes, formatCount, onSummaryChanged, type Summary } from "../api/index.ts";
import { useApi } from "../api/useApi.ts";
import { useConnectionOk } from "../api/connection.ts";
import { Icon } from "../ui/Icon.tsx";
import type { IconName } from "../ui/icon-paths.ts";
import { Link, navigate, useLocation } from "../router.tsx";
import { WorkersSwitch } from "./WorkersSwitch.tsx";
import { applyAppearance, nextTheme, readAccent, readTheme, type ThemeMode } from "./theme.ts";

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
// An item goes to a route, or runs its own action (sign out ends the session first).
type MenuItem = { label: string; to?: string; onSelect?: () => void };

function Menu({ label, icon, primary, items, className }: { label: string; icon?: IconName; primary?: boolean; items: MenuItem[]; className?: string }) {
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
    <div className={`menu${className ? ` ${className}` : ""}`} ref={ref}>
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
                if (it.onSelect) it.onSelect();
                else if (it.to) navigate(it.to);
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
      {s?.workers.paused && <b className="warn-t">paused</b>}
      <Link to="/workers">
        <i></i>
        <b>converting</b> {s ? `${s.converting.running} of ${s.converting.workers} workers` : "—"}
      </Link>
      <Link to="/jobs?state=waiting">
        queue <b>{s ? formatCount(s.queue) : "—"}</b>
      </Link>
      <span className="lg-only">
        last done <b>{s ? (s.lastDoneAt ? ago(s.lastDoneAt) : "none yet") : "—"}</b>
      </span>
      <Link className="lg-only" to="/libraries">
        next scan <b>{s ? (s.nextScanAt ? dayClock(s.nextScanAt) : "off") : "—"}</b>
      </Link>
      <Link className="lg-only" to="/settings/processing">
        disk free <b>{s ? formatBytes(s.diskFreeBytes) : "—"}</b>
      </Link>
      {s && s.newErrors > 0 && (
        <Link className="lg-only" to={`/settings/log?level=error&since=${s.seenSeq}`}>
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
  // The install's appearance (settings > appearance): the sun button changes the theme for every browser.
  const [appearance, setAppearance] = useState<{ theme: ThemeMode; accent: string | null }>(() => ({ theme: readTheme(), accent: readAccent() }));
  const theme = appearance.theme;
  const active = activeKey(pathname);

  useEffect(() => {
    applyAppearance(appearance);
  }, [appearance.theme, appearance.accent]);

  useEffect(() => {
    api.getSettings().then((s) => setAppearance(s.appearance), () => {});
  }, []);

  // Settings' health check refreshes this summary after a fix or re-run, so the red dot clears at once.
  // The System log reports when it has been opened, so the new-errors count clears at once.
  const reloadSummary = summary.reload;
  useEffect(() => {
    window.addEventListener("summary:refresh", reloadSummary);
    const offLog = onSummaryChanged(reloadSummary);
    return () => {
      window.removeEventListener("summary:refresh", reloadSummary);
      offLog();
    };
  }, [reloadSummary]);

  // While files finish after the switch goes off, the count is read again so the label clears on its own.
  const finishingNow = summary.data?.workers.finishing ?? 0;
  useEffect(() => {
    if (finishingNow === 0) return;
    const t = setTimeout(reloadSummary, 1500);
    return () => clearTimeout(t);
  }, [finishingNow, summary.data, reloadSummary]);

  const failed = summary.data?.failed24h ?? 0;
  const offline = summary.data?.workersOffline ?? 0;
  const badges: Record<string, { n: number; bad: boolean } | undefined> = {
    jobs: failed > 0 ? { n: failed, bad: true } : undefined,
    workers: offline > 0 ? { n: offline, bad: false } : undefined,
  };
  // A failed health check shows a red dot on Settings, in the top bar and the bottom bar.
  const dots: Record<string, boolean | undefined> = { settings: summary.data?.healthFailing };

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
              {dots[s.key] && <span className="dot" role="img" aria-label="Problem found" />}
            </Link>
          ))}
        </nav>
        <span className="sp"></span>
        <WorkersSwitch summary={summary.data} />
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
        <Menu
          label="Account"
          className="acct"
          items={[
            { label: "Your access tokens", to: "/settings/access" },
            {
              label: "Sign out",
              onSelect: () => {
                // The session ends on the server first; the sign-in page then shows whether or not the call worked.
                api.signOut().finally(() => navigate("/signin"));
              },
            },
          ]}
        />
        <button
          type="button"
          className="btn ghost sm"
          aria-label={`Theme: ${theme}`}
          title={`Theme: ${theme}`}
          onClick={() => { const next = nextTheme(theme); setAppearance((a) => ({ ...a, theme: next })); api.patchSettings({ appearance: { theme: next } }).catch(() => {}); }}
        >
          <Icon name="sun" />
        </button>
      </header>
      <StatusLine summary={summary.data} ok={ok} />
      <main className="main-col">
        {children}
        <nav className="bottomnav" aria-label="Main">
          {SECTIONS.map((s) => (
            <Link key={s.key} to={s.to} aria-current={active === s.key ? "page" : undefined}>
              <Icon name={s.icon} />
              {s.label}
              {dots[s.key] && <span className="dot" role="img" aria-label="Problem found" />}
            </Link>
          ))}
        </nav>
      </main>
    </div>
  );
}
