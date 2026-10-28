import { cleanup } from "@testing-library/react";
import { type RunnerTask, TestRunner, vi } from "vitest";

/**
 * Puts the page back after every test, in every file.
 *
 * It lives in the runner and not in an afterEach of test-setup.ts because the files share a worker
 * (isolate: false) and Vitest does not always run the setup file again for the next file (it does
 * not under `vitest related`, which the pre-commit hook uses): the afterEach was then registered
 * for the first file only, and the DOM of each test leaked into the next ones.
 */
export default class CleanRunner extends TestRunner {
  override onAfterRunTask(test: RunnerTask) {
    super.onAfterRunTask(test);
    // A test that left fake timers on would stall the next file in the worker.
    vi.useRealTimers();
    cleanup();
    localStorage.clear();
    // Whatever a test put in the page outside its RTL container (portals, live-region announcers,
    // toasts) must not be seen by the next test or file.
    document.body.replaceChildren();
    document.body.removeAttribute("style");
    document.body.removeAttribute("class");
    document.documentElement.removeAttribute("style");
  }
}
