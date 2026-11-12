import { useEffect } from "react";
import { MOCK, SignedOut, failed } from "./api";
import { getSettings } from "./libraryApi";
import { SignIn } from "./screens/SignIn";
import sprite from "./icons.svg?raw";
import { itemFor } from "./nav";
import { AppearanceSettings, Coming, NotFound } from "./pages";
import { Dashboard } from "./screens/Dashboard";
import { Libraries, LibraryPage } from "./screens/Libraries";
import { Start } from "./screens/Start";
import { Jobs } from "./screens/Jobs";
import { WorkerPage, Workers } from "./screens/Workers";
import { Workflows } from "./screens/Workflows";
import { WorkflowStack } from "./screens/WorkflowStack";
import { navigate, usePath } from "./router";
import { Shell } from "./Shell";

export function App() {
  const raw = usePath();
  const path = raw.length > 1 ? raw.replace(/\/+$/, "") : raw; // "/settings/" is "/settings"
  const settings = path === "/settings" || path === "/settings/appearance"; // Appearance is the only Settings tab so far
  // Other pages' deeper paths (/workflows/42/graph) are their future routes; Settings' sub-pages are known, so any
  // other /settings/... is not found.
  const item = path.startsWith("/settings/") && !settings ? undefined : itemFor(path);
  const start = path === "/start", signin = path === "/signin"; // not in the menu (IA.md)
  // Sample data pill: built screens on the mock only; a page not built yet says so in its empty state (design, 00:02Z).
  // Screens on the real server (no Sample data pill): Dashboard, Libraries, first run, Workflows, the step list, Jobs.
  const real = !MOCK && (start || signin || path === "/libraries" || path.startsWith("/libraries/") || path === "/workflows" || /^\/workflows\/[^/]+$/.test(path) || path === "/jobs" || path === "/" || path === "/workers" || /^\/workers\/[^/]+$/.test(path));
  const title = settings ? "Settings" : start ? "Set up" : signin ? "Sign in" : item ? item.label : "Page not found";
  useEffect(() => { document.title = title + " · Shrink Your Shit (new interface)"; }, [title]);
  // A server whose first run isn't done opens on it (IA.md: shown until done). Upgrades from v1 have it done.
  useEffect(() => {
    if (MOCK || path !== "/") return;
    getSettings().then((r) => { if (!failed(r) && !r.firstRunDone && location.pathname.replace(/\/+$/, "") === "/next") navigate("/start"); })
      .catch((e) => { if (!(e instanceof SignedOut)) throw e; });
  }, [path]);
  return (
    <>
      <div dangerouslySetInnerHTML={{ __html: sprite }} />
      <Shell title={title} proto={!real && (start || path === "/" || path === "/libraries" || path.startsWith("/libraries/") || path === "/workflows" || /^\/workflows\/[^/]+$/.test(path) || path === "/jobs" || path === "/workers" || /^\/workers\/[^/]+$/.test(path))}>
        {settings ? <AppearanceSettings /> : path === "/" ? <Dashboard /> : start ? <Start /> : path === "/jobs" ? <Jobs /> : path === "/workers" ? <Workers /> : /^\/workers\/[^/]+$/.test(path) ? <WorkerPage key={path} id={decodeURIComponent(path.split("/")[2])} /> : signin ? <SignIn /> : path === "/libraries" ? <Libraries /> : /^\/libraries\/[^/]+$/.test(path) ? <LibraryPage key={path} id={path.split("/")[2]} />: path === "/workflows" ? <Workflows /> : /^\/workflows\/[^/]+$/.test(path) ? <WorkflowStack key={path} id={path.split("/")[2]} /> : item ? <Coming item={item} /> : <NotFound />}
      </Shell>
    </>
  );
}
