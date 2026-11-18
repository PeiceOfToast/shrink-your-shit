import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Served by the Go binary at / (one UI, 2026-11-12); built straight into the folder the binary embeds (internal/web/next/dist, workshop's internal/web/next).
export default defineConfig({
  base: "/",
  plugins: [react()],
  // license: the bundled packages' license notices ship with the app (React is MIT), at /licenses.md.
  build: { outDir: "../internal/web/next/dist", emptyOutDir: true, license: { fileName: "licenses.md" } },
});
