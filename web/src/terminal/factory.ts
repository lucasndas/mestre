import { WebglAddon } from "@xterm/addon-webgl";
import { Terminal as HeadlessTerminal } from "@xterm/headless";
import { Terminal } from "@xterm/xterm";
import "@xterm/xterm/css/xterm.css";

/** The size of the PTY, fixed by the machine; the browser never changes it. */
export interface TermSize {
  cols: number;
  rows: number;
}

/** A terminal that draws on the page. */
export interface LiveTerm {
  write(data: Uint8Array | string): void;
  reset(): void;
  /** Called with each thing the user types (only while typing is on). */
  onData(cb: (data: string) => void): void;
  /** Typing on: the keyboard goes to the terminal; off: read-only, hollow cursor, no focus. */
  setTyping(on: boolean): void;
  /** The size of the letters in px; the PTY keeps its size, only the font changes. */
  setFontSize(px: number): void;
  /** The text of the screen as the terminal holds it (for "Copiar tela"). */
  text(): string;
  /** Scrolls to the end of the output and follows it again. */
  toEnd(): void;
  /** Called when the view leaves the end of the output (true) or comes back to it (false). */
  onScrolled(cb: (away: boolean, behind: number) => void): void;
  /** Called with the row of the cursor (0 is the top of the screen) each time it moves. */
  onCursor(cb: (row: number) => void): void;
  /** True when it draws with WebGL (false: the fallback of the page, when WebGL is not there). */
  readonly webgl: boolean;
  dispose(): void;
}

/** A terminal that draws nothing: it keeps the screen, and the page shows rows of text. */
export interface SnapshotTerm {
  write(data: Uint8Array | string): void;
  reset(): void;
  /** Resolves when everything written so far is on the screen (xterm.js writes in the background). */
  settle(): Promise<void>;
  /** The rows of the screen as text, trailing blanks and trailing empty rows trimmed. */
  rows(): string[];
  dispose(): void;
}

/** Choices of a live terminal. */
export interface LiveOptions {
  /** Keep the screen as text in the page too (xterm's screen reader mode), for who cannot see the canvas. */
  readable?: boolean;
  /** The size of the letters in px at the start (15 on the screen, 13 in the card, 12 on the phone). */
  fontSize?: number;
}

/** How the terminals are made; the tests give a fake one (jsdom has no WebGL). */
export interface TermFactory {
  live(host: HTMLElement, size: TermSize, colors: TermColors, opts?: LiveOptions): LiveTerm;
  snapshot(size: TermSize): SnapshotTerm;
}

/** The colors of the screen of the terminal; they come from the tokens of the panel. */
export interface TermColors {
  background: string;
  foreground: string;
  cursor: string;
  selection: string;
  font: string;
  /** The 16 ANSI colors, black to bright white. */
  ansi: string[];
}

const ANSI: Array<[string, string]> = [
  ["black", "#3a3025"],
  ["red", "#e0735a"],
  ["green", "#8fbf7a"],
  ["yellow", "#e8c06a"],
  ["blue", "#7fa6d6"],
  ["magenta", "#c78fc4"],
  ["cyan", "#6fc2b8"],
  ["white", "#d9cfba"],
  ["bright-black", "#9a8d78"],
  ["bright-red", "#f08a70"],
  ["bright-green", "#a8d894"],
  ["bright-yellow", "#f5d58a"],
  ["bright-blue", "#9bbde8"],
  ["bright-magenta", "#dca8d8"],
  ["bright-cyan", "#8ad6cc"],
  ["bright-white", "#f7efdc"],
];

/** Reads the colors from the tokens of index.css. */
export function termColors(): TermColors {
  const css = getComputedStyle(document.documentElement);
  const get = (name: string, fallback: string) => css.getPropertyValue(name).trim() || fallback;
  return {
    background: get("--color-term-bg", "#1f1a14"),
    foreground: get("--color-term-fg", "#efe6d2"),
    cursor: get("--color-term-cursor", "#f1b04c"),
    selection: get("--color-term-selection", "#5a4a36"),
    font: get("--font-mono", "ui-monospace, monospace"),
    ansi: ANSI.map(([name, fallback]) => get(`--color-term-${name}`, fallback)),
  };
}

const SCROLLBACK = 2000;

/** prefers-reduced-motion: the cursor does not blink. */
const reducedMotion = () =>
  typeof matchMedia === "function" && matchMedia("(prefers-reduced-motion: reduce)").matches;
const SNAPSHOT_SCROLLBACK = 0;

function liveTerm(host: HTMLElement, size: TermSize, c: TermColors, opts?: LiveOptions): LiveTerm {
  const names = [
    "black",
    "red",
    "green",
    "yellow",
    "blue",
    "magenta",
    "cyan",
    "white",
    "brightBlack",
    "brightRed",
    "brightGreen",
    "brightYellow",
    "brightBlue",
    "brightMagenta",
    "brightCyan",
    "brightWhite",
  ] as const;
  const theme = {
    background: c.background,
    foreground: c.foreground,
    selectionBackground: c.selection,
    cursor: c.cursor,
    cursorAccent: c.background,
    ...Object.fromEntries(names.map((n, i) => [n, c.ansi[i]])),
  };
  const term = new Terminal({
    cols: size.cols,
    rows: size.rows,
    scrollback: SCROLLBACK,
    disableStdin: true,
    screenReaderMode: opts?.readable ?? false,
    cursorBlink: false,
    // read-only: the cursor is a hollow block, as in a terminal that has no focus
    cursorStyle: "block",
    cursorInactiveStyle: "outline",
    fontFamily: c.font,
    fontSize: opts?.fontSize ?? 15,
    lineHeight: 1.4,
    theme,
  });
  term.open(host);
  // the packaged font may still be loading: measure the cells again once it is there
  void document.fonts?.load(`${opts?.fontSize ?? 15}px "JetBrains Mono Variable"`).then(() => {
    term.options.fontFamily = `${c.font} `;
    term.options.fontFamily = c.font;
  });
  let webgl = false;
  try {
    const addon = new WebglAddon();
    addon.onContextLoss(() => addon.dispose());
    term.loadAddon(addon);
    webgl = true;
  } catch {
    // no WebGL here: the page keeps the renderer it has
  }
  if (term.textarea) term.textarea.tabIndex = -1;
  let scrolled: (away: boolean, behind: number) => void = () => {};
  term.onScroll(() => {
    const buf = term.buffer.active;
    const behind = buf.baseY - buf.viewportY;
    scrolled(behind > 0, behind);
  });
  return {
    write: (d) => term.write(d),
    reset: () => term.reset(),
    onData: (cb) => {
      term.onData(cb);
    },
    setTyping(on) {
      term.options.disableStdin = !on;
      term.options.cursorBlink = on && !reducedMotion();
      if (term.textarea) term.textarea.tabIndex = on ? 0 : -1;
      if (on) term.focus();
      else term.blur();
    },
    setFontSize(px) {
      term.options.fontSize = px;
    },
    text() {
      const buf = term.buffer.active;
      const out: string[] = [];
      for (let i = 0; i < buf.length; i++) out.push(buf.getLine(i)?.translateToString(true) ?? "");
      while (out.length > 0 && out[out.length - 1] === "") out.pop();
      return out.join("\n");
    },
    toEnd: () => term.scrollToBottom(),
    onCursor(cb) {
      term.onCursorMove(() => cb(term.buffer.active.cursorY));
    },
    onScrolled(cb) {
      scrolled = cb;
    },
    webgl,
    dispose: () => term.dispose(),
  };
}

function snapshotTerm(size: TermSize): SnapshotTerm {
  const term = new HeadlessTerminal({
    cols: size.cols,
    rows: size.rows,
    scrollback: SNAPSHOT_SCROLLBACK,
    allowProposedApi: true,
  });
  return {
    write: (d) => term.write(d),
    reset: () => term.reset(),
    settle: () => new Promise((resolve) => term.write("", resolve)),
    rows() {
      const buf = term.buffer.active;
      const out: string[] = [];
      for (let i = 0; i < term.rows; i++)
        out.push((buf.getLine(i)?.translateToString(true) ?? "").trimEnd());
      while (out.length > 0 && out[out.length - 1] === "") out.pop();
      return out;
    },
    dispose: () => term.dispose(),
  };
}

/** The factory the panel uses: xterm.js, with WebGL for the live ones and a headless screen for the snapshots. */
export const xtermFactory: TermFactory = {
  live: liveTerm,
  snapshot: snapshotTerm,
};
