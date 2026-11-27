import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// The new frontend (ui/). Builds straight into the Go package that embeds it
// (internal/ui/dist), whose committed index.html is the "not built" placeholder the
// build replaces. A build wipes that folder, so commit named files, never `git add -A`.
export default defineConfig({
  base: "/",
  plugins: [react()],
  build: { outDir: "../internal/ui/dist", emptyOutDir: true },
  // The real client: /api goes to the syp server (SYP_URL, default the local dev port).
  server: {
    proxy: {
      "/api": {
        target: (globalThis as { process?: { env?: Record<string, string | undefined> } }).process?.env?.SYP_URL || "http://127.0.0.1:8484",
      },
    },
  },
});
