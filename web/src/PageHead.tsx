// One header row per page (DESIGN-DIRECTION-clean.md P1, P2): icon, title, the Sample data pill on pages that show
// sample data (it replaced the full-width prototype bar), an optional "?" with the old subtitle, actions on the right.
import { Component, createContext, Fragment, useContext, useState, type ReactNode } from "react";
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

export function PageHead({ icon, title, sub, help, crumbs, after, children }: { icon?: string; title: ReactNode; sub?: ReactNode; help?: string; crumbs?: [string, string][]; after?: ReactNode; children?: ReactNode }) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <div className="pagehead">
        {icon && <span className="ptile"><Icon id={icon} /></span>}
        {crumbs?.map(([label, to]) => <Fragment key={to}><Link className="crumbl" to={to}>{label}</Link><span className="slash" aria-hidden="true">/</span></Fragment>)}
        {sub ? <div className="tt"><h1>{title}</h1><span className="sub">{sub}</span></div> : <h1>{title}</h1>}
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

// One empty state (clean pass E1): a centred 40 px icon, one sentence, one action.
export function Empty({ icon, text, children }: { icon: string; text: ReactNode; children?: ReactNode }) {
  return <div className="empty1"><Icon id={icon} /><p>{text}</p>{children}</div>;
}

// A first load that failed (pass 6 PR A): the Empty layout, what happened and one action. Shown only while there is no data;
// a failed refresh over data keeps the data and a red line above it instead.
export function LoadFailed({ error, reload }: { error?: { message: string }; reload: () => unknown }) {
  return <div className="empty1" role="alert"><Icon id="i-alert" /><p>{error?.message || "Couldn't load this."}</p><button className="btn primary" type="button" onClick={() => reload()}>Try again</button></div>;
}

// A page that fails to draw shows a message and a Reload, not a blank app (bug 11-19 23:00Z: one crash unmounted
// everything). The Shell keys it by the address, so moving to another page starts clean.
export class ScreenGuard extends Component<{ children: ReactNode }, { failed: boolean }> {
  state = { failed: false };
  static getDerivedStateFromError() { return { failed: true }; }
  render() {
    return this.state.failed
      ? <div className="empty1" role="alert"><Icon id="i-alert" /><p>This page hit a problem and can't be shown. Nothing was changed.</p><button className="btn primary" type="button" onClick={() => location.reload()}>Reload</button></div>
      : this.props.children;
  }
}
