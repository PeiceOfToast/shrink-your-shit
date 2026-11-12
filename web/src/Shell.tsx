// The shell (spec 2; DESIGN/v2 IA.md and mockups/shell.html): the grouped rail, the top bar and the page.
// Not here yet, until their data exists: nav badges, the activity link, the "New" menu and the connection-lost bar.
import { SampleDataPage } from "./PageHead";
import { useLiveStatus } from "./live";
import { clock } from "./format";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { Icon } from "./Icon";
import { NAV, itemFor } from "./nav";
import { Palette } from "./Palette";
import { ACCENTS, THEMES, keep, keepRail, railCollapsed, useAppearance } from "./prefs";
import { Link, usePath } from "./router";

const mac = /Mac|iPhone|iPad/.test(navigator.platform);

// The top bar's quick Theme menu: the same two settings as Settings > Appearance, applied at once.
function ThemeMenu() {
  const a = useAppearance();
  const ref = useRef<HTMLDetailsElement>(null);
  const close = () => ref.current?.removeAttribute("open");
  useEffect(() => {
    const outside = (e: MouseEvent) => !ref.current?.contains(e.target as Node) && close();
    const esc = (e: KeyboardEvent) => e.key === "Escape" && close();
    document.addEventListener("mousedown", outside);
    document.addEventListener("keydown", esc);
    return () => (document.removeEventListener("mousedown", outside), document.removeEventListener("keydown", esc));
  }, []);
  return (
    // Tabbing out closes it. Only a real element outside counts: Safari and Firefox on macOS don't focus a clicked
    // button, so a click inside can blur with relatedTarget null, and closing then would lose the click.
    <details className="menu" ref={ref} onBlur={(e) => e.relatedTarget instanceof Node && !e.currentTarget.contains(e.relatedTarget) && close()}>
      <summary className="btn icon" aria-label="Theme" title="Theme"><Icon id="i-drop" /></summary>
      <div className="pop" role="menu">
        <div className="hd">Theme</div>
        {THEMES.map(([v, label, icon]) => (
          <button key={v} type="button" className="it" role="menuitemradio" aria-checked={a.theme === v} onClick={() => keep({ ...a, theme: v })}>
            <Icon id={icon} />{label}
          </button>
        ))}
        <hr />
        <div className="hd">Accent</div>
        {ACCENTS.map(([v, label, swatch]) => (
          <button key={v} type="button" className="it" role="menuitemradio" aria-checked={a.accent === v} onClick={() => keep({ ...a, accent: v })}>
            <span className="swatch" style={{ background: swatch }} />{label}
          </button>
        ))}
        <hr />
        <Link className="it" role="menuitem" to="/settings/appearance" onClick={close}><Icon id="i-settings" />More appearance settings</Link>
      </div>
    </details>
  );
}

export function Shell({ title, proto, children }: { title: string; proto: boolean; children: ReactNode }) {
  // A menu's popup opens from its button's right edge; near the left edge of a phone that pushes it off the screen
  // (review 308da3f P1). When any menu opens, keep its popup on the screen: from the left if it would start off it.
  useEffect(() => {
    const fit = (e: Event) => {
      const d = e.target as HTMLDetailsElement;
      if (!(d instanceof HTMLDetailsElement) || !d.open || !d.classList.contains("menu")) return;
      const pop = d.querySelector<HTMLElement>(":scope > .pop");
      if (!pop) return;
      pop.style.left = pop.style.right = "";
      const r = pop.getBoundingClientRect();
      if (r.left < 8) { pop.style.right = "auto"; pop.style.left = "0"; }
      const r2 = pop.getBoundingClientRect();
      if (r2.right > innerWidth - 8) pop.style.left = Math.round(innerWidth - 8 - r2.right) + "px";
    };
    document.addEventListener("toggle", fit, true);
    return () => document.removeEventListener("toggle", fit, true);
  }, []);
  const [collapsed, setCollapsed] = useState(railCollapsed);
  const [navOpen, setNavOpen] = useState(false);
  const [palette, setPalette] = useState(false);
  const path = usePath();
  const current = itemFor(path);
  useEffect(() => setNavOpen(false), [path]);
  useEffect(() => {
    const k = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "k") (e.preventDefault(), setPalette(true));
      else if (e.key === "Escape") setNavOpen(false);
    };
    window.addEventListener("keydown", k);
    return () => window.removeEventListener("keydown", k);
  }, []);
  const toggleRail = () => (keepRail(!collapsed), setCollapsed(!collapsed));
  return (
    // On a narrow screen the open menu's scrim is shell.css's .app.nav-open::after, so a tap on it is a click on .app
    // itself (review P2): that closes the menu. Escape closes it too.
    <div className={"app" + (collapsed ? " rail-collapsed" : "") + (navOpen ? " nav-open" : "")} onClick={(e) => navOpen && e.target === e.currentTarget && setNavOpen(false)}>
      <nav className="rail" aria-label="Main">
        <Link className="brand" to="/"><span className="logo"><Icon id="i-layers" /></span><span className="t">Shrink Your Shit</span></Link>
        <div className="nav">
          {NAV.map(([group, items]) => [
            <div className="grp" key={group}>{group}</div>,
            ...items.map((n) => (
              <Link key={n.to} to={n.to} title={n.label} aria-current={current === n ? "page" : undefined}>
                <Icon id={n.icon} /><span className="lbl">{n.label}</span>
              </Link>
            )),
          ])}
        </div>
        <div className="foot">
          <button className="btn ghost" type="button" aria-expanded={!collapsed} title={collapsed ? "Expand menu" : "Collapse menu"} onClick={toggleRail}>
            <Icon id="i-panel" /><span className="lbl">{collapsed ? "Expand menu" : "Collapse menu"}</span>
          </button>
        </div>
      </nav>
      <div className="main">
        <header className="topbar">
          <button className="btn icon ghost menubtn" type="button" aria-label="Open menu" aria-expanded={navOpen} onClick={(e) => (e.stopPropagation(), setNavOpen(!navOpen))}><Icon id="i-menu" /></button>
          <div className="crumb" aria-label="Breadcrumb"><span className="cur">{title}</span></div>
          <div className="sp" />
          <button className="search" type="button" aria-label="Search or jump to" onClick={() => setPalette(true)}>
            <Icon id="i-search" /><span className="txt">Search or jump to...</span><span className="k">{mac ? "⌘ K" : "Ctrl K"}</span>
          </button>
          <ThemeMenu />
        </header>
        <ConnBar />
        {/* the prototype bar became the Sample data pill in each page's header (DESIGN-DIRECTION-clean.md P2) */}
        <main className="content" id="main"><SampleDataPage.Provider value={proto}>{children}</SampleDataPage.Provider></main>
      </div>
      {palette && <Palette onClose={() => setPalette(false)} />}
    </div>
  );
}

// The connection-lost bar (spec 12): shown while the live stream (Jobs) has been down for 3 s; it goes when it is back.
function ConnBar() {
  const { lost, lastAt } = useLiveStatus();
  if (!lost) return null;
  return <div className="connbar" role="status" style={{ display: "flex" }}><Icon id="i-wifi-off" /><span>Lost connection to the server. Trying again...{lastAt && " (last update " + clock(lastAt) + ")"}</span></div>;
}
