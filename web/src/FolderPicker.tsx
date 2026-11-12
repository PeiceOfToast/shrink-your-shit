// The folder picker (one-UI gap G3; design SCREENS, 19:08Z) on GET /api/fs/dirs?path= -> {path, parent, dirs}: a
// "Browse..." button beside every folder field opens "Choose a folder". You open a folder, then press "Use <path>".
// Folders only, A-Z ignoring case, hidden ones (".") behind a checkbox, a filter over 50; the open folder as path
// segments (each a button) with Up; the server's errors in its own words (Up still works). On Windows the top is
// "This computer" with the drives. A dialog on desktop, a full-height sheet on phones; Escape closes it.
import { useEffect, useId, useRef, useState } from "react";
import { Icon } from "./Icon";
import { call, failed, MOCK, type Answer } from "./api";

interface Dirs { path: string; parent: string | null; dirs: string[] }
// the sample install's folders (?mock=1): the same shape, a small tree
const SAMPLE: Record<string, string[]> = { "/": ["media", "srv", ".snapshots"], "/media": ["movies", "shrunk", "tv"], "/media/movies": ["Arrival (2016)", "Dune (2021)"], "/media/tv": ["Severance"], "/srv": [] };
async function listDirs(path: string): Promise<Answer<Dirs>> {
  if (MOCK) {
    const p = path || "/";
    if (!(p in SAMPLE)) return { error: { code: "not_found", message: "That folder does not exist." } };
    return { path: p, parent: p === "/" ? null : p.slice(0, p.lastIndexOf("/")) || "/", dirs: SAMPLE[p] };
  }
  return call("GET", "/api/fs/dirs" + (path ? "?path=" + encodeURIComponent(path) : ""));
}
const sepOf = (p: string) => (/^[A-Za-z]:/.test(p) || p.includes("\\") ? "\\" : "/");
const join = (p: string, name: string) => (p === "" ? name : p.endsWith(sepOf(p)) ? p + name : p + sepOf(p) + name);
const parentOf = (p: string) => { const s = sepOf(p), t = p.replace(/[\\/]+$/, ""), i = t.lastIndexOf(s); return i <= 0 ? (s === "/" ? "/" : "") : /^[A-Za-z]:$/.test(t.slice(0, i)) ? t.slice(0, i) + s : t.slice(0, i); };
// "/media/movies" -> [["/", "/"], ["media", "/media"], ["movies", "/media/movies"]]; "C:\Videos" -> [["This computer", ""], ["C:", "C:\"], ["Videos", "C:\Videos"]]
function segments(p: string): [string, string][] {
  const s = sepOf(p), parts = p.split(/[\\/]+/).filter(Boolean), out: [string, string][] = s === "/" ? [["/", "/"]] : [["This computer", ""]];
  let acc = s === "/" ? "" : "";
  parts.forEach((x, i) => { acc = s === "/" ? acc + "/" + x : i === 0 ? x + "\\" : acc.replace(/\\$/, "") + "\\" + x; out.push([x, acc]); });
  return out;
}

export function Browse({ value, onPick, start, label = "Browse..." }: { value: string; onPick: (p: string) => void; start?: string; label?: string }) {
  const [open, setOpen] = useState(false), btn = useRef<HTMLButtonElement>(null);
  return <>
    <button ref={btn} className="btn browse" type="button" aria-haspopup="dialog" onClick={() => setOpen(true)}><Icon id="i-folder" />{label}</button>
    {open && <Picker from={value.trim() || start || ""} onClose={() => (setOpen(false), setTimeout(() => btn.current?.focus(), 0))} onPick={(p) => (onPick(p), setOpen(false), setTimeout(() => btn.current?.focus(), 0))} />}
  </>;
}

function Picker({ from, onPick, onClose }: { from: string; onPick: (p: string) => void; onClose: () => void }) {
  const [cur, setCur] = useState<Dirs | null>(null), [err, setErr] = useState(""), [at, setAt] = useState(""), [hidden, setHidden] = useState(false), [q, setQ] = useState("");
  const id = useId(), title = useRef<HTMLHeadingElement>(null), box = useRef<HTMLDivElement>(null);
  const go = async (p: string) => {
    setErr(""); setQ(""); setAt(p);
    const r = await listDirs(p);
    if (failed(r)) { setCur(null); setErr(r.error.message); return false; }
    setCur(r); setAt(r.path); return true;
  };
  // start: the field's folder if it exists, else its nearest existing parent, else the top
  useEffect(() => { (async () => { let p = from; for (let i = 0; i < 30; i++) { if (await go(p)) break; if (!p || p === "/") { await go(""); break; } const up = parentOf(p); if (up === p) { await go(""); break; } p = up; } title.current?.focus(); })(); }, []); // eslint-disable-line react-hooks/exhaustive-deps
  // opening a folder replaces the list, and the button that had the focus with it: keep the focus in the dialog (the
  // new list's first folder, else the title), so Enter keeps going down and Escape still closes
  useEffect(() => { if (box.current && !box.current.contains(document.activeElement)) (box.current.querySelector<HTMLElement>(".list li button") ?? title.current)?.focus(); }, [cur, err]);
  const shown = (cur?.dirs ?? []).filter((d) => hidden || !d.startsWith(".")).filter((d) => !q || d.toLowerCase().includes(q.toLowerCase())).sort((a, b) => a.localeCompare(b, undefined, { sensitivity: "base" }));
  const top = at === "" || at === "/", upTo = cur?.parent ?? (top ? null : parentOf(at));
  const usable = !!cur && cur.path !== "";
  return (
    <div className="fpick" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div ref={box} className="dlg" role="dialog" aria-modal="true" aria-labelledby={id + "-t"} onKeyDown={(e) => e.key === "Escape" && (e.stopPropagation(), onClose())}>
        <div className="row hdr"><h2 id={id + "-t"} ref={title} tabIndex={-1}>Choose a folder</h2><span className="sp" />
          <button className="btn sm icon ghost" type="button" aria-label="Close" onClick={onClose}><Icon id="i-x" /></button></div>
        <div className="row crumbs">
          <button className="btn sm" type="button" disabled={upTo == null} onClick={() => upTo != null && go(upTo)}><Icon id="i-chev-l" />Up</button>
          <nav aria-label="Open folder" className="segs">{segments(at).map(([n, p], i, all) => i === all.length - 1
            ? <strong key={p} aria-current="location">{n}</strong>
            : <button key={p} className="linkbtn" type="button" onClick={() => go(p)}>{n}</button>)}</nav>
        </div>
        {(cur?.dirs.length ?? 0) > 50 && <input className="input" placeholder="Filter folders" aria-label="Filter folders" value={q} onChange={(e) => setQ(e.target.value)} />}
        <div className="list" aria-live="polite">
          {err ? <p className="st bad" role="alert" style={{ whiteSpace: "normal" }}><Icon id="i-alert" /><span>{err}</span></p>
            : !cur ? <p className="muted">Opening...</p>
            : !shown.length ? <p className="muted">{cur.path === "" ? "No drives found." : "No folders inside. You can use this one."}</p>
            : <ul aria-label="Folders">{shown.map((d) => <li key={d}><button type="button" onClick={() => go(join(cur.path, d))}><Icon id="i-folder" /><span>{d}</span></button></li>)}</ul>}
        </div>
        {cur?.dirs.some((d) => d.startsWith(".")) && <label className="row nw sm"><input type="checkbox" checked={hidden} onChange={(e) => setHidden(e.target.checked)} />Show hidden folders</label>}
        <div className="row ftr"><button className="btn primary" type="button" disabled={!usable} onClick={() => cur && onPick(cur.path)}>{usable ? "Use " + cur!.path : "Use this folder"}</button>
          <button className="btn ghost" type="button" onClick={onClose}>Cancel</button></div>
      </div>
    </div>
  );
}
