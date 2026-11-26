import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// The new frontend (ui/). Builds to ui/dist (gitignored); nothing is embedded yet.
export default defineConfig({
  base: "/",
  plugins: [react()],
  build: { outDir: "dist", emptyOutDir: true },
});
