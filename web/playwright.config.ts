import { defineConfig } from "@playwright/test";

// The smoke test runs against the real engine: tools/e2eserver founds a
// throwaway Guild, starts the Citadel with the panel embedded in the binary
// (internal/panel/webdist, built by `npm run build`) and prints
// `E2E_READY url=... setup_code=...` once the panel answers. Playwright waits
// for that line and exports the named groups as E2E_URL and E2E_SETUP_CODE
// (see e2e/support.ts). Telegram and GitHub are never reached
// (docs/qualidade/testes.md#smoke-do-painel).
export default defineConfig({
  testDir: "e2e",
  retries: 0,
  reporter: [["list"], ["./e2e/budget-reporter.ts"]],
  // The Guild CA is not in any trust store of the machine.
  use: { browserName: "chromium", ignoreHTTPSErrors: true, locale: "pt-BR" },
  // One Guild serves the whole run and only its first test can found it (the setup code is used
  // once): the journey walks the Guild the smoke left, so it runs after the smoke.
  projects: [
    { name: "smoke", testMatch: "smoke.spec.ts" },
    { name: "journey", testMatch: "journey.spec.ts", dependencies: ["smoke"] },
  ],
  webServer: {
    command: "go run ./tools/e2eserver",
    cwd: "..",
    wait: { stdout: /E2E_READY url=(?<E2E_URL>\S+) setup_code=(?<E2E_SETUP_CODE>\S+)/ },
    // the first `go run` compiles the engine
    timeout: 300_000,
    reuseExistingServer: false,
    stdout: "pipe",
    stderr: "pipe",
  },
});
