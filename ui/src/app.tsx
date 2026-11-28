import { useEffect, type ReactNode } from "react";
import { Shell } from "./shell/Shell.tsx";
import { DevStates, showDevStates } from "./devStates.tsx";
import { Dashboard } from "./pages/Dashboard.tsx";
import { Jobs } from "./pages/Jobs.tsx";
import { JobDetail } from "./pages/JobDetail.tsx";
import { Libraries } from "./pages/Libraries.tsx";
import { Placeholder } from "./pages/Placeholder.tsx";
import { SettingsPage } from "./pages/settings/SettingsPage.tsx";
import { SystemLog } from "./pages/SystemLog.tsx";
import { FirstRun, Starting } from "./pages/Start.tsx";
import { SignIn } from "./pages/SignIn.tsx";
import { Workers } from "./pages/Workers.tsx";
import { WorkerPage } from "./pages/WorkerPage.tsx";
import { Workflows } from "./pages/Workflows.tsx";
import { NewWorkflow } from "./pages/NewWorkflow.tsx";
import { Guided } from "./pages/Guided.tsx";
import { FlowEditor } from "./pages/FlowEditor.tsx";
import { api, sampleOnly } from "./api/index.ts";
import { useApi } from "./api/useApi.ts";
import { navigate, resolve, useLocation } from "./router.tsx";

export function App() {
  const { pathname } = useLocation();
  const route = resolve(pathname);
  // Setup gate: a fresh install goes to the first-run page until its account exists. Checked again on every
  // navigation, so finishing setup (which navigates to the dashboard) is seen at once.
  // The answer is tied to the path it was asked on: right after a navigation the old answer is still on screen,
  // and it must not send the person back to the first-run page.
  const setup = useApi(() => api.getSetup().then((s) => ({ ...s, pathname })), [pathname]);
  const needsSetup = setup.data?.pathname === pathname && Boolean(setup.data?.needsAccount) && route.name !== "start";
  // Redirect only on a fresh answer: while a reload runs, the old answer is still on screen.
  useEffect(() => {
    if (!setup.loading && needsSetup) navigate("/start");
  }, [setup.loading, needsSetup]);

  // Until the first answer arrives, draw nothing. If the check itself fails, the app shows its own
  // "Can't reach the server" line instead of a blank page.
  if (!setup.data && !setup.error) return null;
  if (setup.data?.starting) {
    return (
      <>
        <Starting />
        {showDevStates() && <DevStates />}
      </>
    );
  }

  // The first-run and sign-in pages stand alone, without the top bar or status line.
  if (route.name === "start") {
    return (
      <>
        <FirstRun />
        {showDevStates() && <DevStates />}
      </>
    );
  }
  if (route.name === "signin") {
    return (
      <>
        <SignIn />
        {showDevStates() && <DevStates />}
      </>
    );
  }

  let page: ReactNode;
  switch (route.name) {
    case "dashboard":
      page = <Dashboard />;
      break;
    case "jobs":
      page = <Jobs />;
      break;
    // The job panel sits over the list, which stays behind it (dimmed).
    case "job":
      page = (
        <>
          <Jobs />
          <JobDetail id={route.id} initialTab="summary" />
        </>
      );
      break;
    case "jobLog":
      page = (
        <>
          <Jobs />
          <JobDetail id={route.id} initialTab="log" />
        </>
      );
      break;
    case "workflows":
      page = <Workflows />;
      break;
    // The dialog opens over the Workflows list, which stays behind it (dimmed).
    case "workflowPicker":
      page = (
        <>
          <Workflows />
          <NewWorkflow />
        </>
      );
      break;
    case "workflowGuided":
      page = <Guided id={route.id} />;
      break;
    case "workflowEditor":
      page = <FlowEditor id={route.id} />;
      break;
    case "libraries":
      page = <Libraries />;
      break;
    case "library":
      page = <Libraries selected={route.id} />;
      break;
    // Workers and the system log have no server routes yet. Their sample rows would read as real, so the real build
    // shows "Not built yet" instead (?api=mock still shows the mock screens).
    case "workers":
      page = sampleOnly() ? <Placeholder title="Workers" note="Not built yet. This screen isn't connected to the server yet; it comes in a later batch." /> : <Workers />;
      break;
    case "worker":
      page = sampleOnly() ? <Placeholder title="Workers" note="Not built yet. This screen isn't connected to the server yet; it comes in a later batch." /> : <WorkerPage id={route.id} />;
      break;
    case "systemLog":
      page = sampleOnly() ? <Placeholder title="System log" note="Not built yet. This screen isn't connected to the server yet; it comes in a later batch." back={{ to: "/settings", label: "Back to Settings" }} /> : <SystemLog />;
      break;
    case "settings":
      page = <SettingsPage section={route.section} />;
      break;
    case "placeholder":
      page = <Placeholder title={route.title} />;
      break;
  }
  return (
    <>
      <Shell>{page}</Shell>
      {showDevStates() && <DevStates />}
    </>
  );
}
