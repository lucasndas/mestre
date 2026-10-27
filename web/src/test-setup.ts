import { cleanup, configure } from "@testing-library/react";
import { afterEach, vi } from "vitest";

// findBy* and waitFor give up after 1 s by default. Under load a correct render takes longer, and
// the failures that followed looked like leaks between files (see afterEach below).
configure({ asyncUtilTimeout: 8000 });

afterEach(() => {
  // A test that left fake timers on would stall the next file in the worker.
  vi.useRealTimers();
  cleanup();
  localStorage.clear();
  // The files share one jsdom: whatever a test put in the page outside its RTL container (portals,
  // live-region announcers, toasts) must not be seen by the next test or file.
  document.body.replaceChildren();
  document.body.removeAttribute("style");
  document.body.removeAttribute("class");
  document.documentElement.removeAttribute("style");
});

// jsdom has no scrolling; react-aria asks for it when an overlay opens
window.scrollTo = () => {};

// jsdom has no canvas; xterm.js probes for one when it loads, and no test needs it
HTMLCanvasElement.prototype.getContext = (() => null) as never;

// jsdom has neither of these; React Flow measures its nodes with them
class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
globalThis.ResizeObserver ??= ResizeObserverStub as unknown as typeof ResizeObserver;
globalThis.DOMMatrixReadOnly ??= class {
  m22 = 1;
  constructor(t?: string) {
    const m = /scale\(([\d.]+)\)/.exec(t ?? "");
    if (m) this.m22 = Number(m[1]);
  }
} as unknown as typeof DOMMatrixReadOnly;
