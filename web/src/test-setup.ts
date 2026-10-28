import { configure } from "@testing-library/react";

// findBy* and waitFor give up after 1 s by default. Under load a correct render takes longer, and
// the failures that followed looked like leaks between files.
// The cleanup after each test is in test-runner.ts, not in an afterEach here: see there.
configure({ asyncUtilTimeout: 8000 });

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
