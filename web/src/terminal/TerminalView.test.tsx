import { act, fireEvent, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import type { APIError, TerminalInfo } from "../gen/panel";
import { renderWithI18n } from "../test-utils";
import { WebglBudget } from "./budget";
import type { LiveTerm, SnapshotTerm, TermFactory } from "./factory";
import type { FeedHandlers, TerminalFeed } from "./feed";
import { TerminalView } from "./TerminalView";

const info = (over: Partial<TerminalInfo> = {}): TerminalInfo => ({
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
function fakeFeed() {
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

/** Terminals that draw nothing: the live ones count, the snapshot ones keep the text. */
function fakeFactory() {
  const lives: Array<LiveTerm & { typingOn: boolean; send(d: string): void; disposed: boolean }> =
    [];
  const factory: TermFactory = {
    live() {
      let cb: (d: string) => void = () => {};
      const t = {
        typingOn: false,
        disposed: false,
        webgl: true,
        write() {},
        reset() {},
        onData: (f: (d: string) => void) => {
          cb = f;
        },
        setTyping(on: boolean) {
          t.typingOn = on;
        },
        send: (d: string) => cb(d),
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

beforeEach(() => vi.useFakeTimers());
afterEach(() => vi.useRealTimers());

// CA-CANVAS-07
test("CA-CANVAS-07: with 12 terminals visible, exactly 8 draw with WebGL and the 4 others show a snapshot that is drawn again every 2 s", () => {
  const budget = new WebglBudget();
  const { factory, lives } = fakeFactory();
  const feeds = Array.from({ length: 12 }, () => fakeFeed());
  renderWithI18n(
    <div>
      {feeds.map((f, i) => (
        <TerminalView
          // biome-ignore lint/suspicious/noArrayIndexKey: a fixed list in a test
          key={i}
          terminalId={`T${i}`}
          info={info({ terminal_id: `T${i}`, name: `P${i}` })}
          feed={f.feed}
          canType={false}
          timeZone="America/Sao_Paulo"
          budget={budget}
          factory={factory}
        />
      ))}
    </div>,
  );
  const renderers = (kind: string) => document.querySelectorAll(`[data-renderer="${kind}"]`);
  expect(renderers("webgl")).toHaveLength(8);
  expect(renderers("snapshot")).toHaveLength(4);
  expect(lives).toHaveLength(8);
  expect(budget.used).toBe(8);

  // output reaches the snapshot terminals too; the picture is static until the 2 s pass
  const snapshotView = screen.getByLabelText(/Retrato do terminal de P8,/);
  act(() => {
    feeds[8]?.followers.get("T8")?.frame(0, new TextEncoder().encode("olá do agente"));
  });
  act(() => {
    vi.advanceTimersByTime(1999);
  });
  expect(snapshotView.textContent).not.toContain("olá do agente");
  act(() => {
    vi.advanceTimersByTime(1);
  });
  expect(snapshotView.textContent).toContain("olá do agente");
  act(() => {
    feeds[8]?.followers.get("T8")?.frame(14, new TextEncoder().encode(" e mais"));
    vi.advanceTimersByTime(1999);
  });
  expect(snapshotView.textContent).not.toContain("e mais");
  act(() => {
    vi.advanceTimersByTime(1);
  });
  expect(snapshotView.textContent).toContain("olá do agente e mais");
});

test("a terminal that is off screen or far gives its place to one that waits", () => {
  const budget = new WebglBudget(1);
  const { factory } = fakeFactory();
  const f = [fakeFeed(), fakeFeed()];
  const view = (a: boolean) => (
    <div>
      <TerminalView
        terminalId="A"
        info={info({ terminal_id: "A" })}
        feed={f[0]?.feed as TerminalFeed}
        canType={false}
        timeZone="UTC"
        budget={budget}
        factory={factory}
        visible={a}
      />
      <TerminalView
        terminalId="B"
        info={info({ terminal_id: "B" })}
        feed={f[1]?.feed as TerminalFeed}
        canType={false}
        timeZone="UTC"
        budget={budget}
        factory={factory}
      />
    </div>
  );
  const { rerender } = renderWithI18n(view(true));
  const modes = () =>
    [...document.querySelectorAll("[data-renderer]")].map((e) => e.getAttribute("data-renderer"));
  expect(modes()).toEqual(["webgl", "snapshot"]);
  rerender(view(false));
  expect(modes()).toEqual(["snapshot", "webgl"]);
});

test("a terminal is read-only for a level that cannot type: no control, nothing typed reaches the server", () => {
  const { feed, typed } = fakeFeed();
  const { factory, lives } = fakeFactory();
  renderWithI18n(
    <TerminalView
      terminalId="T1"
      info={info()}
      feed={feed}
      canType={false}
      timeZone="UTC"
      budget={new WebglBudget()}
      factory={factory}
    />,
  );
  expect(screen.queryByRole("button", { name: "Digitar" })).toBeNull();
  expect(screen.getByText(/Somente leitura/)).toBeTruthy();
  expect(lives[0]?.typingOn).toBe(false);
  lives[0]?.send("rm -rf /\r");
  expect(typed).toEqual([]);
  // the three classes the canvas needs so the mouse and the wheel go to the terminal
  const el = document.querySelector("[data-renderer]");
  expect(el?.classList.contains("nodrag")).toBe(true);
  expect(el?.classList.contains("nowheel")).toBe(true);
  expect(el?.classList.contains("nopan")).toBe(true);
});

test("typing: the busy agent asks for a confirmation first, the keys go to the server, and releasing the keyboard ends the session", async () => {
  vi.useRealTimers();
  const user = userEvent.setup();
  const { feed, typed, ended } = fakeFeed();
  const { factory, lives } = fakeFactory();
  const refresh = vi.fn(async () => info({ busy: true, state: "busy" }));
  renderWithI18n(
    <TerminalView
      terminalId="T1"
      info={info()}
      feed={feed}
      canType
      refreshInfo={refresh}
      timeZone="UTC"
      budget={new WebglBudget()}
      factory={factory}
    />,
  );
  await user.click(screen.getByRole("button", { name: "Digitar" }));
  // the state is read again at the click, and a busy agent asks first
  expect(refresh).toHaveBeenCalledOnce();
  expect(await screen.findByText("O agente está trabalhando")).toBeTruthy();
  expect(lives[0]?.typingOn).toBe(false);
  await user.click(screen.getByRole("button", { name: "Cancelar" }));
  expect(lives[0]?.typingOn).toBe(false);

  await user.click(screen.getByRole("button", { name: "Digitar" }));
  await user.click(await screen.findByRole("button", { name: "Digitar mesmo assim" }));
  expect(lives[0]?.typingOn).toBe(true);
  lives[0]?.send("ls\r");
  expect(typed).toEqual(["ls\r"]);

  await user.click(screen.getByRole("button", { name: "Parar de digitar" }));
  expect(lives[0]?.typingOn).toBe(false);
  expect(ended).toEqual(["T1"]);
  lives[0]?.send("x");
  expect(typed).toEqual(["ls\r"]);
});

test("leaving the page while typing ends the typing session", async () => {
  vi.useRealTimers();
  const user = userEvent.setup();
  const { feed, ended } = fakeFeed();
  const { factory } = fakeFactory();
  const { unmount } = renderWithI18n(
    <TerminalView
      terminalId="T1"
      info={info()}
      feed={feed}
      canType
      timeZone="UTC"
      budget={new WebglBudget()}
      factory={factory}
    />,
  );
  await user.click(screen.getByRole("button", { name: "Digitar" }));
  expect(ended).toEqual([]);
  unmount();
  expect(ended).toEqual(["T1"]);
});

test("a refusal of the server is shown translated and a refused level releases the keyboard", async () => {
  vi.useRealTimers();
  const user = userEvent.setup();
  const { feed, followers } = fakeFeed();
  const { factory, lives } = fakeFactory();
  renderWithI18n(
    <TerminalView
      terminalId="T1"
      info={info()}
      feed={feed}
      canType
      timeZone="UTC"
      budget={new WebglBudget()}
      factory={factory}
    />,
  );
  await user.click(screen.getByRole("button", { name: "Digitar" }));
  expect(lives[0]?.typingOn).toBe(true);
  const refusal: APIError = { code: "forbidden_level", message: "x" };
  act(() => followers.get("T1")?.refused?.(refusal));
  expect(screen.getByRole("alert").textContent).toContain("Seu nível não permite esta ação.");
  expect(lives[0]?.typingOn).toBe(false);
});

test("a machine without signal shows the terminal grey with the time in the Guild zone and keeps the screen", () => {
  const { feed, followers } = fakeFeed();
  const { factory, lives } = fakeFactory();
  renderWithI18n(
    <TerminalView
      terminalId="T1"
      info={info()}
      feed={feed}
      canType={false}
      timeZone="America/Sao_Paulo"
      budget={new WebglBudget()}
      factory={factory}
    />,
  );
  const host = () => document.querySelector("[data-renderer]");
  expect(host()?.getAttribute("data-signal")).toBe("ok");
  act(() => followers.get("T1")?.signal?.({ signal: false, since: "2026-10-26T17:42:00Z" }));
  expect(host()?.getAttribute("data-signal")).toBe("lost");
  expect(screen.getByRole("status").textContent).toContain("Máquina sem sinal desde 14:42.");
  expect(lives[0]?.disposed).toBe(false); // the screen stays
  act(() => followers.get("T1")?.signal?.({ signal: true }));
  expect(host()?.getAttribute("data-signal")).toBe("ok");
  expect(screen.queryByRole("status")).toBeNull();
  fireEvent.click(document.body);
});
