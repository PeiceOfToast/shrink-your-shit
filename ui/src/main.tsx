import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import "./styles/draft-b.css";
import "./styles/app.css";
import "./styles/panels.css";
import { App } from "./app.tsx";
import { applyTheme, readTheme } from "./shell/theme.ts";

// Apply the saved theme before the first paint.
applyTheme(readTheme());

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
