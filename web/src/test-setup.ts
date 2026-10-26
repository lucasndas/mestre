import { cleanup } from "@testing-library/react";
import { afterEach } from "vitest";

afterEach(() => {
  cleanup();
  localStorage.clear();
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
