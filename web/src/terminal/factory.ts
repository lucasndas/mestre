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
  /** Typing on: the keyboard goes to the terminal; off: read-only, cursor hidden, no focus. */
  setTyping(on: boolean): void;
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
}

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
  };
}

const SCROLLBACK = 2000;
const SNAPSHOT_SCROLLBACK = 0;

function liveTerm(host: HTMLElement, size: TermSize, c: TermColors, opts?: LiveOptions): LiveTerm {
  const theme = (typing: boolean) => ({
    background: c.background,
    foreground: c.foreground,
    selectionBackground: c.selection,
    // read-only: no cursor at all
    cursor: typing ? c.cursor : "transparent",
    cursorAccent: typing ? c.background : "transparent",
  });
  const term = new Terminal({
    cols: size.cols,
    rows: size.rows,
    scrollback: SCROLLBACK,
    disableStdin: true,
    screenReaderMode: opts?.readable ?? false,
    cursorBlink: false,
    cursorInactiveStyle: "none",
    fontFamily: c.font,
    fontSize: 14,
    theme: theme(false),
  });
  term.open(host);
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
  return {
    write: (d) => term.write(d),
    reset: () => term.reset(),
    onData: (cb) => {
      term.onData(cb);
    },
    setTyping(on) {
      term.options.disableStdin = !on;
      term.options.cursorBlink = on;
      term.options.theme = theme(on);
      if (term.textarea) term.textarea.tabIndex = on ? 0 : -1;
      if (on) term.focus();
      else term.blur();
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
