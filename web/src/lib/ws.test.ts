import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { CLOSE_SESSION_ENDED, type SocketLike, type SocketState, UiSocket } from "./ws";

class FakeSocket implements SocketLike {
  binaryType?: string;
  onopen: ((ev: unknown) => void) | null = null;
  onmessage: ((ev: { data: unknown }) => void) | null = null;
  onclose: ((ev: { code: number }) => void) | null = null;
  sent: string[] = [];
  send(d: string) {
    this.sent.push(d);
  }
  close() {}
}

let sockets: FakeSocket[] = [];
const open = () => {
  const s = new FakeSocket();
  sockets.push(s);
  return s;
};

beforeEach(() => {
  vi.useFakeTimers();
  sockets = [];
});
afterEach(() => vi.useRealTimers());

function make(extra: Partial<ConstructorParameters<typeof UiSocket>[0]> = {}) {
  const states: SocketState[] = [];
  const reloads = vi.fn();
  const events: unknown[] = [];
  const s = new UiSocket({
    url: "wss://x/ws/ui",
    open,
    onState: (st) => states.push(st),
    onReconnected: reloads,
    onEvent: (...a) => events.push(a),
    ...extra,
  });
  return { s, states, reloads, events };
}

test("a lost connection is retried with a growing wait, up to the limit", () => {
  const { s, states } = make({ baseDelay: 1000, maxDelay: 4000 });
  s.start();
  sockets[0]?.onopen?.({});
  sockets[0]?.onclose?.({ code: 1006 });
  expect(states.at(-1)).toBe("reconnecting");
  expect(sockets).toHaveLength(1);
  vi.advanceTimersByTime(999);
  expect(sockets).toHaveLength(1);
  vi.advanceTimersByTime(1);
  expect(sockets).toHaveLength(2);
  sockets[1]?.onclose?.({ code: 1006 }); // second failure: 2 s
  vi.advanceTimersByTime(1999);
  expect(sockets).toHaveLength(2);
  vi.advanceTimersByTime(1);
  expect(sockets).toHaveLength(3);
  sockets[2]?.onclose?.({ code: 1006 }); // 4 s
  vi.advanceTimersByTime(4000);
  sockets[3]?.onclose?.({ code: 1006 }); // capped at 4 s
  vi.advanceTimersByTime(3999);
  expect(sockets).toHaveLength(4);
  vi.advanceTimersByTime(1);
  expect(sockets).toHaveLength(5);
});

test("after a reconnection the state is reloaded and the topics subscribed again", () => {
  const { s, reloads, states } = make();
  s.start();
  s.subscribe(["workspace:a"]);
  sockets[0]?.onopen?.({});
  expect(reloads).not.toHaveBeenCalled(); // the first opening is no reconnection
  expect(JSON.parse(sockets[0]?.sent[0] ?? "{}").payload.topics).toEqual(["workspace:a"]);
  sockets[0]?.onclose?.({ code: 1006 });
  vi.advanceTimersByTime(1000);
  sockets[1]?.onopen?.({});
  expect(states.at(-1)).toBe("open");
  expect(reloads).toHaveBeenCalledOnce();
  expect(JSON.parse(sockets[1]?.sent[0] ?? "{}").type).toBe("ui.subscribe");
});

test("the end of the session is not retried", () => {
  const ended = vi.fn();
  const { s } = make({ onSessionEnded: ended });
  s.start();
  sockets[0]?.onopen?.({});
  sockets[0]?.onclose?.({ code: CLOSE_SESSION_ENDED });
  vi.advanceTimersByTime(60000);
  expect(ended).toHaveBeenCalledOnce();
  expect(sockets).toHaveLength(1);
});

test("events reach the listener and frames that are not ours are ignored", () => {
  const { s, events } = make();
  s.start();
  sockets[0]?.onmessage?.({
    data: JSON.stringify({
      type: "ui.event",
      payload: { topic: "t", event: "snapshot", data: { a: 1 } },
    }),
  });
  sockets[0]?.onmessage?.({ data: "not json" });
  sockets[0]?.onmessage?.({ data: new ArrayBuffer(2) });
  expect(events).toEqual([["t", "snapshot", { a: 1 }]]);
});

test("binary frames, refusals and typed messages reach the listeners; unsubscribing is not undone by a reconnection", () => {
  const frames: unknown[] = [];
  const errors: unknown[] = [];
  const opens: boolean[] = [];
  const { s } = make();
  s.listen({
    onFrame: (f) => frames.push([f.type, f.offset, [...f.data]]),
    onError: (replyTo, e) => errors.push([replyTo, e.code]),
    onOpen: (again) => opens.push(again),
  });
  s.start();
  const sock = sockets[0];
  expect(sock?.binaryType).toBe("arraybuffer");
  sock?.onopen?.({});
  s.subscribe(["terminal:T1", "terminal:T2"]);
  s.unsubscribe(["terminal:T2"]);
  const id = s.send("terminal.input", { terminal_id: "T1", data: "ls" });
  const sent = JSON.parse(sock?.sent.at(-1) ?? "{}");
  expect(sent).toMatchObject({
    id,
    type: "terminal.input",
    payload: { terminal_id: "T1", data: "ls" },
  });

  const buf = new ArrayBuffer(25 + 2);
  const u = new Uint8Array(buf);
  u[0] = 1;
  new DataView(buf).setBigUint64(17, 4096n, false);
  u.set([7, 8], 25);
  sock?.onmessage?.({ data: buf });
  expect(frames).toEqual([[1, 4096, [7, 8]]]);
  sock?.onmessage?.({
    data: JSON.stringify({
      type: "error",
      reply_to: id,
      payload: { code: "forbidden_level", message: "x" },
    }),
  });
  expect(errors).toEqual([[id, "forbidden_level"]]);

  sock?.onclose?.({ code: 1006 });
  vi.advanceTimersByTime(1000);
  sockets[1]?.onopen?.({});
  const again = JSON.parse(sockets[1]?.sent[0] ?? "{}");
  expect(again.payload.topics).toEqual(["terminal:T1"]);
  expect(opens).toEqual([false, true]);
});
