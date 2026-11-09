import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Served by the Go binary at /next/; built straight into the folder the binary embeds (internal/web/next/dist, workshop's internal/web/next).
export default defineConfig({
  base: "/next/",
  plugins: [react()],
  // license: the bundled packages' license notices ship with the app (React is MIT), at /next/licenses.md.
  build: { outDir: "../internal/web/next/dist", emptyOutDir: true, license: { fileName: "licenses.md" } },
});
