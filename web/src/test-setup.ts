import { cleanup } from "@testing-library/react";
import { afterEach } from "vitest";

afterEach(() => {
  cleanup();
  localStorage.clear();
});

// jsdom has no scrolling; react-aria asks for it when an overlay opens
window.scrollTo = () => {};
