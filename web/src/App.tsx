import { useEffect } from "react";
import { MOCK, SignedOut, failed } from "./api";
import { getSettings } from "./libraryApi";
import { openBeforeFirstRun } from "./firstRun";
import { SignIn } from "./screens/SignIn";
import sprite from "./icons.svg?raw";
import { itemFor } from "./nav";
import { AppearanceSettings, Coming, NotFound, PluginsPlanned, SettingsPage } from "./pages";
import { pathOf, redirectTarget, settingsTarget } from "./settingsRoutes";
import { Dashboard } from "./screens/Dashboard";
import { HealthSettings } from "./screens/Doctor";
import { Libraries, LibraryPage } from "./screens/Libraries";
import { ProcessingSettings } from "./screens/Processing";
import { AboutSettings, GeneralSettings, SafetySettings, SignInSettings } from "./screens/SettingsMore";
import { BackupSettings } from "./screens/Backup";
import { SharesSettings } from "./screens/Shares";
import { Start } from "./screens/Start";
import { Jobs } from "./screens/Jobs";
import { WorkerPage, Workers } from "./screens/Workers";
import { Workflows } from "./screens/Workflows";
import { WorkflowStack } from "./screens/WorkflowStack";
import { Guided } from "./screens/Guided";
import { WorkflowGraph } from "./screens/WorkflowGraph";
import { Link, navigate, useSearch, usePath } from "./router";
import { SampleDataPage, ScreenGuard } from "./PageHead";
import { ConnBar, Shell, ThemeMenu } from "./Shell";
import { Icon } from "./Icon";
import { WorkerHome } from "./screens/Enroll";
import { ConnectionsSettings } from "./screens/Connections";
import { NotificationsSettings } from "./screens/Notifications";
import { getSetup } from "./setupApi";
import { useData } from "./useData";

let firstRunDone = false;
export function App() {
  const raw = usePath();
  const p0 = raw.length > 1 ? raw.replace(/\/+$/, "") : raw; // "/settings/" is "/settings"
  const search = useSearch();
  const moved = settingsTarget(p0); // an old Settings address (or a page that moved): where it lives now
  useEffect(() => { if (moved && moved !== p0) navigate(redirectTarget(moved, search), true); }, [moved, p0, search]);
  const path = moved ? pathOf(moved) : p0;
  const settings = ["/settings/general", "/settings/processing", "/settings/connections", "/settings/system"].includes(path);
  // Other pages' deeper paths (/workflows/42/graph) are their future routes; Settings' sub-pages are known, so any
  // other /settings/... is not found.
  const item = path.startsWith("/settings/") && !settings ? undefined : itemFor(path);
  const start = path === "/start", signin = path === "/signin"; // not in the menu (IA.md)
  // Sample data pill: built screens on the mock only; a page not built yet says so in its empty state (design, 00:02Z).
  // Screens on the real server (no Sample data pill): Dashboard, Libraries, first run, Workflows, the step list, Jobs.
  const real = !MOCK && (start || signin || path === "/libraries" || path.startsWith("/libraries/") || path === "/workflows" || /^\/workflows\/[^/]+(\/graph)?$/.test(path) || path === "/jobs" || path === "/" || path === "/workers" || /^\/workers\/[^/]+$/.test(path));
  const title = settings ? "Settings" : start ? "Set up" : signin ? "Sign in" : path === "/libraries/shares" ? "Network shares" : item ? item.label : "Page not found";
  // A computer that joined another server shows only its worker page, at every address but Settings (Change role).
  const su = useData(getSetup, []), joined = su.data?.role === "worker" && (!su.data.join || su.data.join.state === "allowed");
  useEffect(() => { document.title = title + " · Shrink Your Shit"; }, [title]);
  // Until the first run is done, every page goes to it (IA.md; parity G8), except sign-in and the About/Health pages. Upgrades from v1 have it done.
  // M5f fix 3: the Change links on the wizard's done cards open a library or a workflow page; those two pages are let through too.
  useEffect(() => {
    if (MOCK || firstRunDone || openBeforeFirstRun(path)) return;
    Promise.all([getSettings(), getSetup()]).then(([r, s]) => { // a joined worker stays on its own page
      if (!failed(r) && r.firstRunDone) firstRunDone = true; // stop asking on every page once it is done
      if (!failed(r) && !r.firstRunDone && !(!failed(s) && s.role === "worker" && (!s.join || s.join.state === "allowed")) && !openBeforeFirstRun(location.pathname)) navigate("/start"); })
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
        {settings ? (path === "/settings/general" ? <SettingsPage at="general"><GeneralSettings /><SignInSettings /><AppearanceSettings /></SettingsPage> : path === "/settings/processing" ? <SettingsPage at="processing"><ProcessingSettings /><SafetySettings /></SettingsPage> : path === "/settings/connections" ? <SettingsPage at="connections"><ConnectionsSettings /><NotificationsSettings /></SettingsPage> : <SettingsPage at="system"><HealthSettings /><BackupSettings /><AboutSettings /></SettingsPage>) : path === "/" ? <Dashboard /> : start ? <Start /> : path === "/jobs" ? <Jobs /> : path === "/workers" ? <Workers /> : /^\/workers\/[^/]+$/.test(path) ? <WorkerPage key={path} id={decodeURIComponent(path.split("/")[2])} /> : signin ? <SignIn /> : path === "/libraries" ? <Libraries /> : path === "/libraries/shares" ? <SharesSettings /> : /^\/libraries\/[^/]+$/.test(path) ? <LibraryPage key={path} id={path.split("/")[2]} />: path === "/workflows" ? <Workflows /> : /^\/workflows\/[^/]+\/graph$/.test(path) ? <WorkflowGraph key={path} id={path.split("/")[2]} /> : path === "/workflows/guided" ? <Guided /> : /^\/workflows\/[^/]+\/basic$/.test(path) ? <Guided key={path} id={path.split("/")[2]} /> : path === "/workflows/new" ? <Workflows /> /* not a route (firstRun.ts): the list, and no stack, so no 404s for a workflow named "new" */ :/^\/workflows\/[^/]+$/.test(path) ? <WorkflowStack key={path} id={path.split("/")[2]} /> : path === "/plugins" ? <PluginsPlanned /> : item ? <Coming item={item} /> : <NotFound />}
      </Shell>
    </>
  );
}
