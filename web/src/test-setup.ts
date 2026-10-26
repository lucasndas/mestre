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
