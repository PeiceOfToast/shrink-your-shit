import { useEffect } from "react";
import sprite from "./icons.svg?raw";
import { itemFor } from "./nav";
import { AppearanceSettings, Coming, NotFound } from "./pages";
import { usePath } from "./router";
import { Shell } from "./Shell";

export function App() {
  const path = usePath();
  const item = itemFor(path);
  const settings = path === "/settings" || path === "/settings/appearance"; // Appearance is the only Settings tab so far
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
