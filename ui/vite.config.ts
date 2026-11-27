import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// The new frontend (ui/). Builds straight into the Go package that embeds it (internal/ui/dist).
// Only that folder's .gitkeep is committed: the build's own clean step (scripts/clean-dist.mjs in
// the build script) empties everything but it, so emptyOutDir is off here and a build leaves
// `git status` clean.
export default defineConfig({
  base: "/",
  plugins: [react()],
  build: { outDir: "../internal/ui/dist", emptyOutDir: false },
  // The real client: /api goes to the syp server (SYP_URL, default the local dev port).
  server: {
    proxy: {
      "/api": {
        target: (globalThis as { process?: { env?: Record<string, string | undefined> } }).process?.env?.SYP_URL || "http://127.0.0.1:8484",
      },
    },
  },
});
