import { beforeEach, expect, test, vi } from "vitest";
import { type TermColors, xtermFactory } from "./factory";

// jsdom has no canvas: the xterm.js of the page is replaced by one that remembers what it was asked
const made = vi.hoisted(() => ({
  options: [] as Array<Record<string, unknown>>,
  terms: [] as unknown[],
}));
vi.mock("@xterm/xterm", () => ({
  Terminal: class {
    options: Record<string, unknown>;
    textarea = document.createElement("textarea");
    constructor(o: Record<string, unknown>) {
      this.options = { ...o };
      made.options.push(this.options);
      made.terms.push(this);
    }
    open() {}
    loadAddon() {}
    onData() {}
    onScroll() {}
    onCursorMove() {}
    focus() {}
    blur() {}
    write() {}
    reset() {}
    dispose() {}
    scrollToBottom() {}
    buffer = {
      active: { baseY: 0, viewportY: 0, cursorY: 0, length: 0, getLine: () => undefined },
    };
  },
}));
vi.mock("@xterm/addon-webgl", () => ({
  WebglAddon: class {
    onContextLoss() {}
    dispose() {}
  },
}));
vi.mock("@xterm/headless", () => ({ Terminal: class {} }));

const colors: TermColors = {
  background: "#1f1a14",
  foreground: "#efe6d2",
  cursor: "#f1b04c",
  selection: "#5a4a36",
  font: '"JetBrains Mono Variable", monospace',
  ansi: Array.from({ length: 16 }, (_, i) => `#${(i + 1).toString(16).padStart(2, "0").repeat(3)}`),
};

beforeEach(() => {
  made.options.length = 0;
  made.terms.length = 0;
});

// CA-TUI-01, CA-TUI-04
test("CA-TUI-04: a read-only terminal draws a hollow block cursor, at the line height 1.4, in the packaged font and the 16 ANSI colors", () => {
  xtermFactory.live(document.createElement("div"), { cols: 200, rows: 50 }, colors, {
    fontSize: 15,
  });
  const o = made.options[0];
  expect(o).toMatchObject({
    cols: 200,
    rows: 50,
    scrollback: 2000,
    disableStdin: true,
    cursorStyle: "block",
    cursorInactiveStyle: "outline",
    cursorBlink: false,
    lineHeight: 1.4,
    fontSize: 15,
    fontFamily: colors.font,
  });
  const theme = o?.theme as Record<string, string>;
  expect(theme.red).toBe(colors.ansi[1]);
  expect(theme.brightWhite).toBe(colors.ansi[15]);
  expect(theme.cursor).toBe(colors.cursor);
});

// CA-TUI-14
test("CA-TUI-14: typing blinks the cursor, unless the user prefers reduced motion", () => {
  const blink = (reduced: boolean) => {
    vi.stubGlobal("matchMedia", () => ({ matches: reduced }));
    const live = xtermFactory.live(document.createElement("div"), { cols: 200, rows: 50 }, colors);
    live.setTyping(true);
    return (made.options[made.options.length - 1] as { cursorBlink: boolean }).cursorBlink;
  };
  expect(blink(false)).toBe(true);
  expect(blink(true)).toBe(false);
  vi.unstubAllGlobals();
});

// CA-TUI-13
test("CA-TUI-13: changing the size of the letter never changes the columns and rows", () => {
  const live = xtermFactory.live(document.createElement("div"), { cols: 200, rows: 50 }, colors);
  live.setFontSize(18);
  const o = made.options[0] as { fontSize: number; cols: number; rows: number };
  expect([o.fontSize, o.cols, o.rows]).toEqual([18, 200, 50]);
});
