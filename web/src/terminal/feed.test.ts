import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { type SocketLike, UiSocket, ulidOf } from "../lib/ws";
import type { FeedHandlers } from "./feed";
import { socketFeed } from "./feed";

class FakeSocket implements SocketLike {
  binaryType?: string;
  onopen: ((ev: unknown) => void) | null = null;
  onmessage: ((ev: { data: unknown }) => void) | null = null;
  onclose: ((ev: { code: number }) => void) | null = null;
  sent: Array<{ type: string; payload: Record<string, unknown>; id: string }> = [];
  send(d: string) {
    this.sent.push(JSON.parse(d));
  }
  close() {}
}

const ID = "01ARZ3NDEKTSV4RRFFQ69G5FAV";
let sockets: FakeSocket[] = [];
beforeEach(() => {
  vi.useFakeTimers();
  sockets = [];
});
afterEach(() => vi.useRealTimers());

const handlers = (log: string[]): FeedHandlers => ({
  frame: (o, d) => log.push(`frame ${o} ${d.length}`),
  reset: () => log.push("reset"),
  replayed: (r) => log.push(`replayed ${r.to}`),
  refused: (e) => log.push(`refused ${e.code}`),
});

test("the feed subscribes to the topic, routes events and refusals, and starts the stream again after a reconnection", () => {
  const sock = new UiSocket({
    url: "x",
    open: () => {
      const s = new FakeSocket();
      sockets.push(s);
      return s;
    },
  });
  sock.start();
  sockets[0]?.onopen?.({});
  const feed = socketFeed(sock);
  const log: string[] = [];
  const stop = feed.follow(ID, handlers(log));
  expect(sockets[0]?.sent.at(-1)).toMatchObject({
    type: "ui.subscribe",
    payload: { topics: [`terminal:${ID}`] },
  });
  sockets[0]?.onmessage?.({
    data: JSON.stringify({
      type: "ui.event",
      payload: {
        topic: `terminal:${ID}`,
        event: "replayed",
        data: { from: 0, to: 9, lost: false },
      },
    }),
  });
  expect(log).toEqual(["replayed 9"]);

  feed.type(ID, "ls");
  const typed = sockets[0]?.sent.at(-1);
  expect(typed?.type).toBe("terminal.input");
  sockets[0]?.onmessage?.({
    data: JSON.stringify({
      type: "error",
      reply_to: typed?.id,
      payload: { code: "forbidden_level", message: "x" },
    }),
  });
  expect(log.at(-1)).toBe("refused forbidden_level");

  feed.endTyping(ID);
  expect(sockets[0]?.sent.at(-1)?.type).toBe("terminal.typing_end");

  sockets[0]?.onclose?.({ code: 1006 });
  vi.advanceTimersByTime(1000);
  sockets[1]?.onopen?.({});
  expect(log.at(-1)).toBe("reset");
  expect(sockets[1]?.sent[0]?.type).toBe("ui.subscribe");

  stop();
  expect(sockets[1]?.sent.at(-1)?.type).toBe("ui.unsubscribe");
});

test("the frames of a terminal are routed by the frame id its snapshot gave, since the terminal ids are not ULIDs", () => {
  const sock = new UiSocket({
    url: "x",
    open: () => {
      const s = new FakeSocket();
      sockets.push(s);
      return s;
    },
  });
  sock.start();
  sockets[0]?.onopen?.({});
  const feed = socketFeed(sock);
  const log: string[] = [];
  const terminal = "01ARZ3NDEKTSV4RRFFQ69G5FAV-1";
  feed.follow(terminal, handlers(log));
  const idBytes = Uint8Array.from({ length: 16 }, (_, i) => i + 1);
  const frame = (pos: number, text: string) => {
    const data = new TextEncoder().encode(text);
    const b = new Uint8Array(25 + data.length);
    b[0] = 1;
    b.set(idBytes, 1);
    new DataView(b.buffer).setBigUint64(17, BigInt(pos));
    b.set(data, 25);
    return b.buffer;
  };
  // before the snapshot nobody knows the id: the frame is not anybody's
  sockets[0]?.onmessage?.({ data: frame(0, "x") });
  sockets[0]?.onmessage?.({
    data: JSON.stringify({
      type: "ui.event",
      payload: {
        topic: `terminal:${terminal}`,
        event: "snapshot",
        data: { frame_id: ulidOf(idBytes) },
      },
    }),
  });
  sockets[0]?.onmessage?.({ data: frame(0, "olá") });
  expect(log).toEqual(["frame 0 4"]);
});
