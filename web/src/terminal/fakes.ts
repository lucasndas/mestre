import type { TerminalInfo } from "../gen/panel";
import type { LiveTerm, SnapshotTerm, TermFactory } from "./factory";
import type { FeedHandlers, TerminalFeed } from "./feed";

/** A terminal as the server describes it, for the tests of the terminal. */
export const info = (over: Partial<TerminalInfo> = {}): TerminalInfo => ({
  terminal_id: "T1",
  frame_id: "F1",
  character: "c1",
  name: "Revisor",
  machine: "torre-a",
  workspace: "w",
  state: "idle",
  busy: false,
  cols: 200,
  rows: 50,
  signal: true,
  can_type: true,
  ...over,
});

/** A feed that remembers who follows what, and lets a test push output and refusals. */
export function fakeFeed() {
  const followers = new Map<string, FeedHandlers>();
  const typed: string[] = [];
  const ended: string[] = [];
  const feed: TerminalFeed = {
    follow(id, h) {
      followers.set(id, h);
      return () => followers.delete(id);
    },
    type: (_id, d) => typed.push(d),
    endTyping: (id) => ended.push(id),
  };
  return { feed, followers, typed, ended };
}

export type FakeLive = LiveTerm & {
  typingOn: boolean;
  send(d: string): void;
  disposed: boolean;
  fontSize: number;
  opened: { cols: number; rows: number; fontSize?: number; readable?: boolean };
  moveCursor(row: number): void;
};

/** Terminals that draw nothing: the live ones count, the snapshot ones keep the text. */
export function fakeFactory() {
  const lives: FakeLive[] = [];
  const factory: TermFactory = {
    live(_host, size, _colors, opts) {
      let cb: (d: string) => void = () => {};
      let cursor: (row: number) => void = () => {};
      const t: FakeLive = {
        typingOn: false,
        disposed: false,
        webgl: true,
        fontSize: opts?.fontSize ?? 0,
        opened: { ...size, fontSize: opts?.fontSize, readable: opts?.readable },
        moveCursor: (row) => cursor(row),
        write() {},
        reset() {},
        onData: (f) => {
          cb = f;
        },
        setTyping(on) {
          t.typingOn = on;
        },
        setFontSize(px) {
          t.fontSize = px;
        },
        text: () => "tela copiada",
        toEnd() {},
        onScrolled() {},
        onCursor(f) {
          cursor = f;
        },
        send: (d) => cb(d),
        dispose() {
          t.disposed = true;
        },
      };
      lives.push(t);
      return t;
    },
    snapshot() {
      let text = "";
      const s: SnapshotTerm = {
        write: (d) => {
          text += typeof d === "string" ? d : new TextDecoder().decode(d);
        },
        reset: () => {
          text = "";
        },
        settle: async () => {},
        rows: () => text.split("\r\n"),
        dispose() {},
      };
      return s;
    },
  };
  return { factory, lives };
}
