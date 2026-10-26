import { test as base, expect } from "@playwright/test";

// What the engine of the smoke test (tools/e2eserver) tells the specs, through
// the environment that playwright.config.ts fills from its ready line.
function fromEnv(name: "E2E_URL" | "E2E_SETUP_CODE"): string {
  const v = process.env[name];
  if (!v) {
    throw new Error(`${name} is not set: the e2e server did not start (see playwright.config.ts)`);
  }
  return v;
}

/** The single-use code of the setup wizard of the throwaway Guild. */
export function setupCode(): string {
  return fromEnv("E2E_SETUP_CODE");
}

/** `test` of the smoke: `baseURL` is the HTTPS address of the real Citadel. */
export const test = base.extend({
  // biome-ignore lint/correctness/noEmptyPattern: Playwright requires the destructured first argument
  baseURL: async ({}, use) => {
    await use(fromEnv("E2E_URL"));
  },
});

export { expect };
