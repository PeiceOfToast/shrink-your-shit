import { useEffect } from "react";
import sprite from "./icons.svg?raw";
import { itemFor } from "./nav";
import { AppearanceSettings, Coming, NotFound } from "./pages";
import { usePath } from "./router";
import { Shell } from "./Shell";

export function App() {
  const raw = usePath();
  const path = raw.length > 1 ? raw.replace(/\/+$/, "") : raw; // "/settings/" is "/settings"
  const settings = path === "/settings" || path === "/settings/appearance"; // Appearance is the only Settings tab so far
  // Other pages' deeper paths (/workflows/42/graph) are their future routes; Settings' sub-pages are known, so any
  // other /settings/... is not found.
  const item = path.startsWith("/settings/") && !settings ? undefined : itemFor(path);
  const title = settings ? "Settings" : item ? item.label : "Page not found";
  useEffect(() => { document.title = title + " · Shrink Your Shit (new interface)"; }, [title]);
  return (
    <>
      <div dangerouslySetInnerHTML={{ __html: sprite }} />
      <Shell title={title} proto={!settings && !!item}>
        {settings ? <AppearanceSettings /> : item ? <Coming item={item} /> : <NotFound />}
      </Shell>
    </>
  );
}
