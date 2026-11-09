// The command palette (spec 12, Ctrl/Cmd K; SCREENS V1). Phase 1 searches what exists: the pages and settings.
// Libraries, workflows, jobs, workers and plugins join as their screens get data. Only matching results are listed.
import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from "react";
import { Icon } from "./Icon";
import { PAGES } from "./nav";
import { navigate } from "./router";

interface Result {
  group: string;
  label: string;
  icon: string;
  to: string;
}
const ALL: Result[] = [
  ...PAGES.map((p) => ({ group: "Pages", label: p.label, icon: p.icon, to: p.to })),
  { group: "Settings", label: "Appearance: theme, accent, density, text size, motion", icon: "i-drop", to: "/settings/appearance" },
];

function Mark({ text, q }: { text: string; q: string }) {
  const i = q ? text.toLowerCase().indexOf(q) : -1;
  if (i < 0) return <>{text}</>;
  return (
    <>
      {text.slice(0, i)}
      <b>{text.slice(i, i + q.length)}</b>
      {text.slice(i + q.length)}
    </>
  );
}

export function Palette({ onClose }: { onClose: () => void }) {
  const [q, setQ] = useState("");
  const [on, setOn] = useState(0);
  const input = useRef<HTMLInputElement>(null);
  const query = q.trim().toLowerCase();
  const results = useMemo(() => ALL.filter((r) => !query || r.label.toLowerCase().includes(query)), [query]);
  useEffect(() => input.current?.focus(), []);
  useEffect(() => setOn(0), [query]);
  const open = (r: Result | undefined) => {
    if (!r) return;
    onClose();
    navigate(r.to);
  };
  const key = (e: KeyboardEvent) => {
    if (e.key === "ArrowDown") (e.preventDefault(), setOn((i) => Math.min(i + 1, results.length - 1)));
    else if (e.key === "ArrowUp") (e.preventDefault(), setOn((i) => Math.max(i - 1, 0)));
    else if (e.key === "Enter") (e.preventDefault(), open(results[on]));
    else if (e.key === "Escape") (e.preventDefault(), onClose());
  };
  let group = "";
  return (
    <div className="palette-wrap open" role="dialog" aria-modal="true" aria-label="Search or jump to" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="palette" onKeyDown={key}>
        <div className="q">
          <Icon id="i-search" />
          <input
            ref={input}
            type="text"
            value={q}
            onChange={(e) => setQ(e.target.value)}
            placeholder="Search pages and settings"
            aria-label="Search"
            role="combobox"
            aria-expanded="true"
            aria-controls="palette-results"
            aria-activedescendant={results[on] ? "palette-r" + on : undefined}
          />
          <span className="chip">Esc</span>
        </div>
        <div className="res" id="palette-results" role="listbox" aria-label="Results">
          {results.length === 0 && <div className="g">Nothing matches "{q.trim()}".</div>}
          {results.map((r, i) => {
            const head = r.group !== group ? ((group = r.group), <div className="g" key={"g" + r.group} role="presentation">{r.group}</div>) : null;
            return [
              head,
              <div key={r.to} id={"palette-r" + i} role="option" aria-selected={i === on} className={"r" + (i === on ? " on" : "")} onMouseMove={() => setOn(i)} onClick={() => open(r)}>
                <Icon id={r.icon} />
                <span><Mark text={r.label} q={query} /></span>
              </div>,
            ];
          })}
        </div>
        <div className="ftr"><span>Up and down to move</span><span>Enter to open</span><span>Esc to close</span></div>
      </div>
    </div>
  );
}
