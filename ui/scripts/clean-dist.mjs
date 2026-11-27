// Empty the embed's build output but keep its committed .gitkeep, the file that keeps go:embed's
// pattern matching on a fresh clone. Vite's emptyOutDir would delete it with everything else.
import { mkdirSync, readdirSync, rmSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

const dir = fileURLToPath(new URL("../../internal/ui/dist/", import.meta.url));
mkdirSync(dir, { recursive: true });
for (const name of readdirSync(dir)) {
  if (name !== ".gitkeep") rmSync(join(dir, name), { recursive: true, force: true });
}
