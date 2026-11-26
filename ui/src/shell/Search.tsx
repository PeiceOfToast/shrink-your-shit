import { useEffect, useMemo, useState } from "react";
import { api, type Job } from "../api/index.ts";
import { useApi } from "../api/useApi.ts";
import { Icon } from "../ui/Icon.tsx";
import { navigate } from "../router.tsx";

// Search over pages and jobs. Files and workflows come with their own screens later.
const PAGES = [
  { label: "Dashboard", to: "/" },
  { label: "Libraries", to: "/libraries" },
  { label: "Workflows", to: "/workflows" },
  { label: "Jobs", to: "/jobs" },
  { label: "Workers", to: "/workers" },
  { label: "Settings", to: "/settings" },
];

export function Search({ onClose }: { onClose: () => void }) {
  const [q, setQ] = useState("");
  const jobs = useApi(() => api.getJobs({ state: "all" }), []);
  const hits = useMemo(() => {
    const needle = q.trim().toLowerCase();
    const pages = PAGES.filter((p) => !needle || p.label.toLowerCase().includes(needle)).map((p) => ({ key: p.to, label: p.label, to: p.to, kind: "Page" }));
    const items = (jobs.data?.items ?? []) as Job[];
    const found = items
      .filter((j) => needle && j.file.toLowerCase().includes(needle))
      .map((j) => ({ key: j.id, label: j.file, to: `/jobs/${j.id}`, kind: "Job" }));
    return [...pages, ...found].slice(0, 12);
  }, [q, jobs.data]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  const go = (to: string) => {
    onClose();
    navigate(to);
  };

  return (
    <div className="scrim" onMouseDown={onClose}>
      <div className="palette card" role="dialog" aria-modal="true" aria-label="Search" onMouseDown={(e) => e.stopPropagation()}>
        <div className="palette-input">
          <Icon name="search" />
          <input
            autoFocus
            value={q}
            onChange={(e) => setQ(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && hits[0]) go(hits[0].to);
            }}
            placeholder="Search pages and jobs"
            aria-label="Search"
          />
        </div>
        <ul className="palette-list">
          {hits.map((h) => (
            <li key={h.key}>
              <button type="button" onClick={() => go(h.to)}>
                <span>{h.label}</span>
                <span className="muted sm">{h.kind}</span>
              </button>
            </li>
          ))}
          {hits.length === 0 && <li className="muted sm">No match.</li>}
        </ul>
      </div>
    </div>
  );
}
