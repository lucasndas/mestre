import { TypeUIEvent, TypeUISubscribe, type UIEnvelope } from "../gen/panel";

export type SocketState = "connecting" | "open" | "reconnecting" | "closed";

export interface SocketLike {
  onopen: ((ev: unknown) => void) | null;
  onmessage: ((ev: { data: unknown }) => void) | null;
  onclose: ((ev: { code: number }) => void) | null;
  send(data: string): void;
  close(): void;
}

export interface UiSocketOptions {
  url: string;
  open?: (url: string) => SocketLike;
  /** Called with every ui.event: the topic, the event name and the data. */
  onEvent?: (topic: string, event: string, data: unknown) => void;
  onState?: (state: SocketState) => void;
  /** Called when the socket opens again after a loss: reload the state. */
  onReconnected?: () => void;
  /** The Citadel ended the session (close code 4001): go to the login. */
  onSessionEnded?: () => void;
  /** The first wait and the largest wait, in ms; each failure doubles the wait. */
  baseDelay?: number;
  maxDelay?: number;
}

/** The close code the Citadel uses when the session ends (internal/panel/ws.go). */
export const CLOSE_SESSION_ENDED = 4001;

export class UiSocket {
  private socket: SocketLike | null = null;
  private topics = new Set<string>();
  private attempts = 0;
  private everOpened = false;
  private timer: ReturnType<typeof setTimeout> | null = null;
  private stopped = false;

  constructor(private readonly o: UiSocketOptions) {}

  start() {
    this.stopped = false;
    this.connect();
  }

  stop() {
    this.stopped = true;
    if (this.timer) clearTimeout(this.timer);
    this.timer = null;
    this.socket?.close();
    this.socket = null;
    this.o.onState?.("closed");
  }

  subscribe(topics: string[]) {
    for (const t of topics) this.topics.add(t);
    this.sendSubscribe(topics);
  }

  /** How long the next attempt waits, in ms. */
  delay(): number {
    const base = this.o.baseDelay ?? 1000;
    const max = this.o.maxDelay ?? 30000;
    return Math.min(max, base * 2 ** Math.max(0, this.attempts - 1));
  }

  private sendSubscribe(topics: string[]) {
    if (!this.socket || topics.length === 0) return;
    const env: Pick<UIEnvelope, "type"> & { id: string; ts: string; payload: unknown } = {
      id: crypto.randomUUID(),
      type: TypeUISubscribe,
      ts: new Date().toISOString(),
      payload: { topics },
    };
    this.socket.send(JSON.stringify(env));
  }

  private connect() {
    this.o.onState?.(this.everOpened ? "reconnecting" : "connecting");
    const s = (this.o.open ?? ((u) => new WebSocket(u) as unknown as SocketLike))(this.o.url);
    this.socket = s;
    s.onopen = () => {
      const again = this.everOpened;
      this.everOpened = true;
      this.attempts = 0;
      this.o.onState?.("open");
      this.sendSubscribe([...this.topics]);
      if (again) this.o.onReconnected?.();
    };
    s.onmessage = (ev) => {
      if (typeof ev.data !== "string") return;
      try {
        const env = JSON.parse(ev.data) as {
          type: string;
          payload: { topic: string; event: string; data: unknown };
        };
        if (env.type === TypeUIEvent)
          this.o.onEvent?.(env.payload.topic, env.payload.event, env.payload.data);
      } catch {
        // a frame that is not ours is ignored
      }
    };
    s.onclose = (ev) => {
      this.socket = null;
      if (this.stopped) return;
      if (ev.code === CLOSE_SESSION_ENDED) {
        this.o.onSessionEnded?.();
        this.stop();
        return;
      }
      this.attempts += 1;
      this.o.onState?.("reconnecting");
      this.timer = setTimeout(() => this.connect(), this.delay());
    };
  }
}
