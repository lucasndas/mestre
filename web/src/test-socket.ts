import { vi } from "vitest";

/** A WebSocket the tests drive: it opens when told and says what the Citadel would say. */
export class FakeWebSocket {
  static all: FakeWebSocket[] = [];
  onopen: ((ev: unknown) => void) | null = null;
  onmessage: ((ev: { data: unknown }) => void) | null = null;
  onclose: ((ev: { code: number }) => void) | null = null;
  sent: string[] = [];

  constructor(readonly url: string) {
    FakeWebSocket.all.push(this);
  }
  send(data: string) {
    this.sent.push(data);
  }
  close() {}

  /** The topics the page asked to follow, in order. */
  subscribed(): string[] {
    return this.sent.flatMap((m) => {
      const env = JSON.parse(m) as { type: string; payload: { topics: string[] } };
      return env.type === "ui.subscribe" ? env.payload.topics : [];
    });
  }
}

/** Puts the fake in place of WebSocket; call `unstub` (vi.unstubAllGlobals) after the test. */
export function installFakeSocket() {
  FakeWebSocket.all = [];
  vi.stubGlobal("WebSocket", FakeWebSocket);
}

/** The socket of the page, opened (what the browser does when the Citadel answers). */
export function openSocket(): FakeWebSocket {
  const s = FakeWebSocket.all.at(-1);
  if (!s) throw new Error("the page opened no socket");
  s.onopen?.({});
  return s;
}

/** An event of a topic, as /ws/ui sends it. */
export function emit(s: FakeWebSocket, topic: string, event: string, data: unknown) {
  s.onmessage?.({
    data: JSON.stringify({
      id: "e1",
      type: "ui.event",
      ts: "2026-10-26T17:00:00.000Z",
      payload: { topic, event, data },
    }),
  });
}
