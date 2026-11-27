import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import "./styles/draft-b.css";
import "./styles/app.css";
import "./styles/panels.css";
import { App } from "./app.tsx";
import { navigate } from "./router.tsx";
import { setCodeNavigator } from "./api/http.ts";
import { applyAppearance, readAccent, readTheme } from "./shell/theme.ts";

// The real client sends sign-in and first-run codes to the router.
setCodeNavigator(navigate);

// Apply the cached theme and accent before the first paint.
applyAppearance({ theme: readTheme(), accent: readAccent() });

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
