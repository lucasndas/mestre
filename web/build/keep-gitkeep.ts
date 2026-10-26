import { readdir, rm } from "node:fs/promises";
import { join } from "node:path";
import type { Plugin } from "vite";

// The build writes into internal/panel/webdist, whose .gitkeep keeps the
// folder (and the go:embed pattern) valid on a fresh checkout. Vite's
// emptyOutDir would delete the marker, so the folder is emptied here, except
// for it.
export const MARKER = ".gitkeep";

export async function emptyExceptMarker(dir: string): Promise<void> {
  let names: string[];
  try {
    names = await readdir(dir);
  } catch {
    return; // not created yet: Vite creates it
  }
  await Promise.all(
    names
      .filter((n) => n !== MARKER)
      .map((n) => rm(join(dir, n), { recursive: true, force: true })),
  );
}

export function keepGitkeep(dir: string): Plugin {
  return {
    name: "mestre-keep-gitkeep",
    apply: "build",
    buildStart: () => emptyExceptMarker(dir),
  };
}
