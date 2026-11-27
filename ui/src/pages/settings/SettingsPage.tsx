import { useEffect, useState } from "react";
import { api, type SettingsSection } from "../../api/index.ts";
import { useApi } from "../../api/useApi.ts";
import { Link, navigate } from "../../router.tsx";
import { hasChanges, setAsker } from "../../settings/leaveGuard.ts";
import { Access } from "./Access.tsx";
import { Backup } from "./Backup.tsx";
import { Connections } from "./Connections.tsx";
import { General } from "./General.tsx";
import { Health } from "./Health.tsx";
import { Notifications } from "./Notifications.tsx";
import { Processing } from "./Processing.tsx";
import { Variables } from "./Variables.tsx";

const SECTIONS: { key: SettingsSection; label: string }[] = [
  { key: "general", label: "General" },
  { key: "processing", label: "Processing" },
  { key: "connections", label: "Connections" },
  { key: "notifications", label: "Notifications" },
  { key: "variables", label: "Variables" },
  { key: "backup", label: "Backup" },
  { key: "health", label: "Health check" },
  { key: "access", label: "Access" },
];

function Body({ section }: { section: SettingsSection }) {
  switch (section) {
    case "general": return <General />;
    case "processing": return <Processing />;
    case "connections": return <Connections />;
    case "notifications": return <Notifications />;
    case "variables": return <Variables />;
    case "backup": return <Backup />;
    case "health": return <Health />;
    case "access": return <Access />;
  }
}

// Settings (page 11): a section list on the left, the section on the right. On a phone the list is the first screen.
export function SettingsPage({ section }: { section: SettingsSection | null }) {
  const doctor = useApi(() => api.getDoctor(false), []);
  const healthBad = doctor.data?.checks.some((c) => c.status === "bad") ?? false;
  const [leave, setLeave] = useState<(() => void) | null>(null);

  useEffect(() => {
    setAsker((go) => setLeave(() => go));
    return () => setAsker(null);
  }, []);
  useEffect(() => {
    const warn = (e: BeforeUnloadEvent) => {
      if (hasChanges()) e.preventDefault();
    };
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, []);

  const current = SECTIONS.find((s) => s.key === section);
  return (
    <>
      <div className={`pagehead${section ? " pagehead-desk" : ""}`}>
        <h1>Settings</h1>
      </div>
      <div className={`set${section ? " open" : ""}`}>
        <nav className="set-nav" aria-label="Settings sections">
          {/* At 390 the top bar has no Account menu, so the account actions sit at the top of this list. */}
          <section className="set-account" aria-label="Account">
            <p className="set-account-who">Signed in on this browser</p>
            <div className="set-account-acts">
              <Link to="/settings/access">Your access tokens</Link>
              <button type="button" className="btn sm" onClick={() => api.signOut().finally(() => navigate("/signin"))}>Sign out</button>
            </div>
          </section>
          {SECTIONS.map((s) => (
            <Link key={s.key} to={`/settings/${s.key}`} aria-current={s.key === section ? "page" : undefined}>
              {s.label}
              {s.key === "health" && healthBad && <span className="dot" aria-label="Problem found" />}
            </Link>
          ))}
          <Link to="/settings/log" className="set-log">System log</Link>
        </nav>
        <div className="set-main">
          {section && current ? (
            <>
              <Link to="/settings" className="set-back">← Settings</Link>
              <Body section={section} />
            </>
          ) : (
            <p className="muted">Choose a section.</p>
          )}
        </div>
      </div>
      {leave && (
        <div className="scrim">
          <div className="dlg" role="dialog" aria-modal="true" aria-labelledby="leave-q">
            <h2 id="leave-q">Leave without saving?</h2>
            <p className="muted sm">Your changes on this page will be lost.</p>
            <div className="row dlg-actions">
              <button type="button" className="btn ghost sm" onClick={() => setLeave(null)}>Stay</button>
              <button type="button" className="btn primary sm" onClick={() => { const go = leave; setLeave(null); go(); }}>Leave</button>
            </div>
          </div>
        </div>
      )}
    </>
  );
}
