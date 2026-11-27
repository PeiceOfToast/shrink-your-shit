import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// The new frontend (ui/). Builds to ui/dist (gitignored); nothing is embedded yet.
export default defineConfig({
  base: "/",
  plugins: [react()],
  build: { outDir: "dist", emptyOutDir: true },
  // The real client: /api goes to the syp server (SYP_URL, default the local dev port).
  server: {
    proxy: {
      "/api": {
        target: (globalThis as { process?: { env?: Record<string, string | undefined> } }).process?.env?.SYP_URL || "http://127.0.0.1:8484",
        changeOrigin: true,
      },
    },
  },
});
