import { Shell } from "./shell/Shell.tsx";
import { DevStates, showDevStates } from "./devStates.tsx";
import { Dashboard } from "./pages/Dashboard.tsx";
import { Jobs } from "./pages/Jobs.tsx";
import { Placeholder } from "./pages/Placeholder.tsx";
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
    case "job":
      page = <Placeholder title="Job details" note="Job details come in the next pull request (batch 1, PR 2)." />;
      break;
    case "jobLog":
      page = <Placeholder title="Job log" note="The job log comes in the next pull request (batch 1, PR 2)." />;
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
