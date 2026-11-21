import { useEffect } from "react";
import { MOCK, SignedOut, failed } from "./api";
import { getSettings } from "./libraryApi";
import { SignIn } from "./screens/SignIn";
import sprite from "./icons.svg?raw";
import { itemFor } from "./nav";
import { AppearanceSettings, Coming, NotFound, PluginsPlanned } from "./pages";
import { Dashboard } from "./screens/Dashboard";
import { HealthSettings } from "./screens/Doctor";
import { Libraries, LibraryPage } from "./screens/Libraries";
import { ProcessingSettings } from "./screens/Processing";
import { AboutSettings, GeneralSettings, SafetySettings } from "./screens/SettingsMore";
import { BackupSettings } from "./screens/Backup";
import { SharesSettings } from "./screens/Shares";
import { Start } from "./screens/Start";
import { Jobs } from "./screens/Jobs";
import { WorkerPage, Workers } from "./screens/Workers";
import { Workflows } from "./screens/Workflows";
import { WorkflowStack } from "./screens/WorkflowStack";
import { WorkflowGraph } from "./screens/WorkflowGraph";
import { Link, navigate, usePath } from "./router";
import { SampleDataPage, ScreenGuard } from "./PageHead";
import { ConnBar, Shell, ThemeMenu } from "./Shell";
import { Icon } from "./Icon";
import { WorkerHome } from "./screens/Enroll";
import { ConnectionsSettings } from "./screens/Connections";
import { NotificationsSettings } from "./screens/Notifications";
import { VariablesSettings } from "./screens/Variables";
import { getSetup } from "./setupApi";
import { useData } from "./useData";

let firstRunDone = false;
export function App() {
  const raw = usePath();
  const p0 = raw.length > 1 ? raw.replace(/\/+$/, "") : raw; // "/settings/" is "/settings"
  const path = p0 === "/settings/integrations" ? "/settings/connections" : p0; // the tab's old address (V9e)
  useEffect(() => { if (p0 !== path) navigate(path, true); }, [p0, path]);
  const settings = ["/settings", "/settings/general", "/settings/shares", "/settings/appearance", "/settings/processing", "/settings/safety", "/settings/connections", "/settings/notifications", "/settings/variables", "/settings/backup", "/settings/health", "/settings/about"].includes(path); // the tabs built so far
  // Other pages' deeper paths (/workflows/42/graph) are their future routes; Settings' sub-pages are known, so any
  // other /settings/... is not found.
  const item = path.startsWith("/settings/") && !settings ? undefined : itemFor(path);
  const start = path === "/start", signin = path === "/signin"; // not in the menu (IA.md)
  // Sample data pill: built screens on the mock only; a page not built yet says so in its empty state (design, 00:02Z).
  // Screens on the real server (no Sample data pill): Dashboard, Libraries, first run, Workflows, the step list, Jobs.
  const real = !MOCK && (start || signin || path === "/libraries" || path.startsWith("/libraries/") || path === "/workflows" || /^\/workflows\/[^/]+(\/graph)?$/.test(path) || path === "/jobs" || path === "/" || path === "/settings/variables" || path === "/workers" || /^\/workers\/[^/]+$/.test(path));
  const title = settings ? "Settings" : start ? "Set up" : signin ? "Sign in" : item ? item.label : "Page not found";
  // A computer that joined another server shows only its worker page, at every address but Settings (Change role).
  const su = useData(getSetup, []), joined = su.data?.role === "worker" && (!su.data.join || su.data.join.state === "allowed");
  useEffect(() => { document.title = title + " · Shrink Your Shit"; }, [title]);
  // Until the first run is done, every page goes to it (IA.md; parity G8), except sign-in and the About/Health pages. Upgrades from v1 have it done.
  const open = (p: string) => p === "/start" || p === "/signin" || p === "/settings/about" || p === "/settings/health";
  useEffect(() => {
    if (MOCK || firstRunDone || open(path)) return;
    Promise.all([getSettings(), getSetup()]).then(([r, s]) => { // a joined worker stays on its own page
      if (!failed(r) && r.firstRunDone) firstRunDone = true; // stop asking on every page once it is done
      if (!failed(r) && !r.firstRunDone && !(!failed(s) && s.role === "worker" && (!s.join || s.join.state === "allowed")) && !open(location.pathname)) navigate("/start"); })
      .catch((e) => { if (!(e instanceof SignedOut)) throw e; });
  }, [path]);
  if (joined && !settings && !start && !signin) return (
    <>
      <div dangerouslySetInnerHTML={{ __html: sprite }} />
      <header className="topbar"><span className="brand"><span className="logo"><Icon id="i-layers" /></span><span className="t">Shrink Your Shit</span></span><div className="sp" /><ThemeMenu /></header>
      <main className="content" id="main"><WorkerHome setup={su.data!} /></main>
    </>
  );
  // U12: first run and sign-in are bare pages: no rail, menu, search or breadcrumb; the page sits in one 720 px column
  if (start || signin) return (
    <>
      <div dangerouslySetInnerHTML={{ __html: sprite }} />
      <header className="topbar"><Link className="brand" to="/"><span className="logo"><Icon id="i-layers" /></span><span className="t">Shrink Your Shit</span></Link><div className="sp" /><ThemeMenu /></header>
      <ConnBar />
      <main className="content bare" id="main"><SampleDataPage.Provider value={!real && start}><ScreenGuard key={path}>{start ? <Start /> : <SignIn />}</ScreenGuard></SampleDataPage.Provider></main>
    </>
  );
  return (
    <>
      <div dangerouslySetInnerHTML={{ __html: sprite }} />
      <Shell title={title} proto={!real && (start || path === "/" || path === "/libraries" || path.startsWith("/libraries/") || path === "/workflows" || /^\/workflows\/[^/]+(\/graph)?$/.test(path) || path === "/jobs" || path === "/workers" || /^\/workers\/[^/]+$/.test(path))}>
        {settings ? (path === "/settings/shares" ? <SharesSettings /> : path === "/settings/processing" ? <ProcessingSettings /> : path === "/settings/appearance" ? <AppearanceSettings /> : path === "/settings/safety" ? <SafetySettings /> : path === "/settings/connections" ? <ConnectionsSettings /> : path === "/settings/notifications" ? <NotificationsSettings /> : path === "/settings/variables" ? <VariablesSettings /> :path === "/settings/backup" ? <BackupSettings /> : path === "/settings/health" ? <HealthSettings /> : path === "/settings/about" ? <AboutSettings /> : <GeneralSettings />) : path === "/" ? <Dashboard /> : start ? <Start /> : path === "/jobs" ? <Jobs /> : path === "/workers" ? <Workers /> : /^\/workers\/[^/]+$/.test(path) ? <WorkerPage key={path} id={decodeURIComponent(path.split("/")[2])} /> : signin ? <SignIn /> : path === "/libraries" ? <Libraries /> : /^\/libraries\/[^/]+$/.test(path) ? <LibraryPage key={path} id={path.split("/")[2]} />: path === "/workflows" ? <Workflows /> : /^\/workflows\/[^/]+\/graph$/.test(path) ? <WorkflowGraph key={path} id={path.split("/")[2]} /> : /^\/workflows\/[^/]+$/.test(path) ? <WorkflowStack key={path} id={path.split("/")[2]} /> : path === "/plugins" ? <PluginsPlanned /> : item ? <Coming item={item} /> : <NotFound />}
      </Shell>
    </>
  );
}
