import { fileURLToPath } from "node:url";
import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vitest/config";
import { keepGitkeep } from "./build/keep-gitkeep.ts";

// The bundle is embedded in the Go binary from internal/panel/webdist.
const outDir = fileURLToPath(new URL("../internal/panel/webdist", import.meta.url));

// Files that use vi.mock: vi.mock does not reach a module another file already loaded in the
// same worker, so these keep a worker of their own.
const isolated = ["src/terminal/factory.test.ts", "src/screens/TableScreen.test.tsx"];

export default defineConfig({
  plugins: [tailwindcss(), react(), keepGitkeep(outDir)],
  build: { outDir, emptyOutDir: false },
  test: {
    setupFiles: ["src/test-setup.ts"],
    runner: "src/test-runner.ts",
    css: false,
    // A failed test is never run again (docs/qualidade/testes.md#regras-e-erros).
    retry: 0,
    // A test that hits this limit is abandoned, not stopped: its body keeps running and renders into
    // the next test (or the next file, in a shared worker). Heavy screens take 2 to 4 s under load,
    // so the limit only catches a real hang. The suite budget in testrun is unchanged.
    testTimeout: 30_000,
    hookTimeout: 30_000,
    // Tests above this are flagged by the runner (testrun) as slow.
    slowTestThreshold: 100,
    // A test that stubs a global gives it back, so files can share a worker.
    unstubGlobals: true,
    unstubEnvs: true,
    projects: [
      {
        // Most files share the worker's modules and jsdom: building a fresh environment and
        // re-importing React, react-aria and i18next for each file was a third of the time.
        extends: true,
        test: {
          name: "shared",
          environment: "jsdom",
          isolate: false,
          include: ["src/**/*.test.{ts,tsx}", "build/**/*.test.ts"],
          exclude: isolated,
        },
      },
      {
        extends: true,
        test: { name: "isolated", environment: "jsdom", include: isolated },
      },
    ],
  },
});
