import { fileURLToPath } from "node:url";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vitest/config";
import { keepGitkeep } from "./build/keep-gitkeep.ts";

// The bundle is embedded in the Go binary from internal/panel/webdist.
const outDir = fileURLToPath(new URL("../internal/panel/webdist", import.meta.url));

export default defineConfig({
  plugins: [react(), keepGitkeep(outDir)],
  build: { outDir, emptyOutDir: false },
  test: {
    environment: "jsdom",
    include: ["src/**/*.test.{ts,tsx}", "build/**/*.test.ts"],
    // A failed test is never run again (docs/qualidade/testes.md#regras-e-erros).
    retry: 0,
    // Tests above this are flagged by the runner (testrun) as slow.
    slowTestThreshold: 100,
  },
});
