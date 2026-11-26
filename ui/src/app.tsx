import { Shell } from "./shell/Shell.tsx";
import { DevStates, showDevStates } from "./devStates.tsx";
import { Dashboard } from "./pages/Dashboard.tsx";
import { Jobs } from "./pages/Jobs.tsx";
import { JobDetail } from "./pages/JobDetail.tsx";
import { Libraries } from "./pages/Libraries.tsx";
import { Placeholder } from "./pages/Placeholder.tsx";
import { SettingsPage } from "./pages/settings/SettingsPage.tsx";
import { SystemLog } from "./pages/SystemLog.tsx";
import { Workers } from "./pages/Workers.tsx";
import { WorkerPage } from "./pages/WorkerPage.tsx";
import { Workflows } from "./pages/Workflows.tsx";
import { resolve, useLocation } from "./router.tsx";

export function App() {
  const { pathname } = useLocation();
  const route = resolve(pathname);
  let page;
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
    // The editor (pages 05-07) is on hold: a row, Edit, New workflow and an import all land here.
    case "workflowEditor":
      page = <Placeholder title="Workflows" note="The workflow editor is being redesigned." back={{ to: "/workflows", label: "Back to Workflows" }} />;
      break;
    case "libraries":
      page = <Libraries />;
      break;
    case "library":
      page = <Libraries selected={route.id} />;
      break;
    case "workers":
      page = <Workers />;
      break;
    case "worker":
      page = <WorkerPage id={route.id} />;
      break;
    case "systemLog":
      page = <SystemLog />;
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
