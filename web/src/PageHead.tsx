// One header row per page (DESIGN-DIRECTION-clean.md P1, P2): icon, title, the Sample data pill on pages that show
// sample data (it replaced the full-width prototype bar), an optional "?" with the old subtitle, actions on the right.
import { createContext, Fragment, useContext, useState, type ReactNode } from "react";
import { Icon } from "./Icon";
import { Link } from "./router";

export const SampleDataPage = createContext(false); // set by the Shell for pages on the mock

export function SampleData() {
  if (!useContext(SampleDataPage)) return null;
  return (
    <span className="pill sample" title="This page shows sample data. Nothing you do here changes your files.">
      Sample data<span className="sr">: this page shows sample data. Nothing you do here changes your files.</span>
    </span>
  );
}

export function PageHead({ icon, title, help, crumbs, after, children }: { icon?: string; title: ReactNode; help?: string; crumbs?: [string, string][]; after?: ReactNode; children?: ReactNode }) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <div className="pagehead">
        {icon && <Icon id={icon} />}
        {crumbs?.map(([label, to]) => <Fragment key={to}><Link className="crumbl" to={to}>{label}</Link><span className="slash" aria-hidden="true">/</span></Fragment>)}
        <h1>{title}</h1>
        <SampleData />
        {help && <button className="btn icon ghost help" type="button" title={help} aria-expanded={open} aria-label="About this page" onClick={() => setOpen(!open)}><Icon id="i-info" /></button>}
        {after}
        <span className="sp" />
        {children}
      </div>
      {help && open && <p className="muted pagehelp">{help}</p>}
    </>
  );
}
