import { defineConfig } from "@playwright/test";

// The smoke test serves the bundle that the `web` job built. TODO(1C): serve
// the embedded panel from the mestre binary instead of `vite preview`
// (docs/qualidade/testes.md#smoke-do-painel).
const port = 4173;

export default defineConfig({
  testDir: "e2e",
  retries: 0,
  reporter: [["list"]],
  use: { baseURL: `http://127.0.0.1:${port}`, browserName: "chromium" },
  webServer: {
    command: `npx --no-install vite preview --host 127.0.0.1 --port ${port} --strictPort --outDir ../internal/panel/webdist`,
    url: `http://127.0.0.1:${port}`,
    reuseExistingServer: false,
  },
});
