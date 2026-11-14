import { useEffect } from "react";
import { MOCK, SignedOut, failed } from "./api";
import { getSettings } from "./libraryApi";
import { SignIn } from "./screens/SignIn";
import sprite from "./icons.svg?raw";
import { itemFor } from "./nav";
import { AppearanceSettings, Coming, NotFound } from "./pages";
import { Dashboard } from "./screens/Dashboard";
import { Libraries, LibraryPage } from "./screens/Libraries";
import { ProcessingSettings } from "./screens/Processing";
import { AboutSettings, BackupSettings, GeneralSettings, IntegrationsSettings, SafetySettings } from "./screens/SettingsMore";
import { SharesSettings } from "./screens/Shares";
import { Start } from "./screens/Start";
import { Jobs } from "./screens/Jobs";
import { WorkerPage, Workers } from "./screens/Workers";
import { Workflows } from "./screens/Workflows";
import { WorkflowStack } from "./screens/WorkflowStack";
import { WorkflowGraph } from "./screens/WorkflowGraph";
import { navigate, usePath } from "./router";
import { Shell, ThemeMenu } from "./Shell";
import { Icon } from "./Icon";
import { WorkerHome } from "./screens/Enroll";
import { getSetup } from "./setupApi";
import { useData } from "./useData";

export function App() {
  const raw = usePath();
  const path = raw.length > 1 ? raw.replace(/\/+$/, "") : raw; // "/settings/" is "/settings"
  const settings = ["/settings", "/settings/general", "/settings/shares", "/settings/appearance", "/settings/processing", "/settings/safety", "/settings/integrations", "/settings/backup", "/settings/about"].includes(path); // the tabs built so far
  // Other pages' deeper paths (/workflows/42/graph) are their future routes; Settings' sub-pages are known, so any
  // other /settings/... is not found.
  const item = path.startsWith("/settings/") && !settings ? undefined : itemFor(path);
  const start = path === "/start", signin = path === "/signin"; // not in the menu (IA.md)
  // Sample data pill: built screens on the mock only; a page not built yet says so in its empty state (design, 00:02Z).
  // Screens on the real server (no Sample data pill): Dashboard, Libraries, first run, Workflows, the step list, Jobs.
  const real = !MOCK && (start || signin || path === "/libraries" || path.startsWith("/libraries/") || path === "/workflows" || /^\/workflows\/[^/]+(\/graph)?$/.test(path) || path === "/jobs" || path === "/" || path === "/workers" || /^\/workers\/[^/]+$/.test(path));
  const title = settings ? "Settings" : start ? "Set up" : signin ? "Sign in" : item ? item.label : "Page not found";
  // A computer that joined another server shows only its worker page, at every address but Settings (Change role).
  const su = useData(getSetup, []), joined = su.data?.role === "worker" && (!su.data.join || su.data.join.state === "allowed");
  useEffect(() => { document.title = title + " · Shrink Your Shit"; }, [title]);
  // A server whose first run isn't done opens on it (IA.md: shown until done). Upgrades from v1 have it done.
  useEffect(() => {
    if (MOCK || path !== "/") return;
    Promise.all([getSettings(), getSetup()]).then(([r, s]) => { // a joined worker stays on its own page
      if (!failed(r) && !r.firstRunDone && !(!failed(s) && s.role === "worker" && (!s.join || s.join.state === "allowed")) && (location.pathname === "/" || location.pathname === "")) navigate("/start"); })
      .catch((e) => { if (!(e instanceof SignedOut)) throw e; });
  }, [path]);
  if (joined && !settings && !start && !signin) return (
    <>
      <div dangerouslySetInnerHTML={{ __html: sprite }} />
      <header className="topbar"><span className="brand"><span className="logo"><Icon id="i-layers" /></span><span className="t">Shrink Your Shit</span></span><div className="sp" /><ThemeMenu /></header>
      <main className="content" id="main"><WorkerHome setup={su.data!} /></main>
    </>
  );
  return (
    <>
      <div dangerouslySetInnerHTML={{ __html: sprite }} />
      <Shell title={title} proto={!real && (start || path === "/" || path === "/libraries" || path.startsWith("/libraries/") || path === "/workflows" || /^\/workflows\/[^/]+(\/graph)?$/.test(path) || path === "/jobs" || path === "/workers" || /^\/workers\/[^/]+$/.test(path))}>
        {settings ? (path === "/settings/shares" ? <SharesSettings /> : path === "/settings/processing" ? <ProcessingSettings /> : path === "/settings/appearance" ? <AppearanceSettings /> : path === "/settings/safety" ? <SafetySettings /> : path === "/settings/integrations" ? <IntegrationsSettings /> : path === "/settings/backup" ? <BackupSettings /> : path === "/settings/about" ? <AboutSettings /> : <GeneralSettings />) : path === "/" ? <Dashboard /> : start ? <Start /> : path === "/jobs" ? <Jobs /> : path === "/workers" ? <Workers /> : /^\/workers\/[^/]+$/.test(path) ? <WorkerPage key={path} id={decodeURIComponent(path.split("/")[2])} /> : signin ? <SignIn /> : path === "/libraries" ? <Libraries /> : /^\/libraries\/[^/]+$/.test(path) ? <LibraryPage key={path} id={path.split("/")[2]} />: path === "/workflows" ? <Workflows /> : /^\/workflows\/[^/]+\/graph$/.test(path) ? <WorkflowGraph key={path} id={path.split("/")[2]} /> : /^\/workflows\/[^/]+$/.test(path) ? <WorkflowStack key={path} id={path.split("/")[2]} /> : item ? <Coming item={item} /> : <NotFound />}
      </Shell>
    </>
  );
}
