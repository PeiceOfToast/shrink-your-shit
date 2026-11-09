// The shell (spec 2; DESIGN/v2 IA.md and mockups/shell.html): the grouped rail, the top bar and the page.
// Not here yet, until their data exists: nav badges, the activity link, the "New" menu and the connection-lost bar.
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
    <details className="menu" ref={ref} onBlur={(e) => !e.currentTarget.contains(e.relatedTarget as Node) && close()}>
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
        {proto && (
          <div className="proto" role="note">
            <Icon id="i-alert" /><span><strong>Prototype</strong> · this page is not built yet. Nothing here changes your files.</span>
          </div>
        )}
        <main className="content" id="main">{children}</main>
      </div>
      {palette && <Palette onClose={() => setPalette(false)} />}
    </div>
  );
}
