import {
  type APIError,
  TypeUIError,
  TypeUIEvent,
  TypeUISubscribe,
  TypeUIUnsubscribe,
  type UIEnvelope,
} from "../gen/panel";

const CROCKFORD = "0123456789ABCDEFGHJKMNPQRSTVWXYZ";
/** The type byte of the binary frame of the output of a terminal (docs/conectividade/protocolo.md). */
export const FRAME_TERMINAL = 0x01;
const FRAME_HEADER = 1 + 16 + 8;

/** The 26-character ULID form of 16 bytes (Crockford base32), as the Go side prints ids. */
export function ulidOf(b: Uint8Array): string {
  let n = 0n;
  for (const x of b) n = (n << 8n) | BigInt(x);
  let out = "";
  for (let i = 0; i < 26; i++) {
    out = CROCKFORD[Number(n & 31n)] + out;
    n >>= 5n;
  }
  return out;
}

/** A binary frame of the browser channel: [type][16 bytes id][8 bytes offset, big-endian][data]. */
export interface DataFrame {
  type: number;
  /** The id as the ULID string (for the output of a terminal, the id of the terminal). */
  id: string;
  offset: number;
  data: Uint8Array;
}

/** Reads a binary frame; null when it is shorter than its header. */
export function decodeFrame(buf: ArrayBuffer): DataFrame | null {
  if (buf.byteLength < FRAME_HEADER) return null;
  const u = new Uint8Array(buf);
  const view = new DataView(buf);
  return {
    type: u[0] ?? 0,
    id: ulidOf(u.subarray(1, 17)),
    offset: Number(view.getBigUint64(17, false)),
    data: u.subarray(FRAME_HEADER),
  };
}

/** What a part of the panel listens to on the channel; every callback is optional. */
export interface SocketListener {
  onEvent?: (topic: string, event: string, data: unknown) => void;
  onFrame?: (f: DataFrame) => void;
  /** An error message of the Citadel: replyTo is the id of the message that was refused. */
  onError?: (replyTo: string, error: APIError) => void;
  /** The socket opened; again is true after a loss (the topics are subscribed again, their streams start from 0). */
  onOpen?: (again: boolean) => void;
}

export type SocketState = "connecting" | "open" | "reconnecting" | "closed";

export interface SocketLike {
  binaryType?: string;
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
  onFrame?: (f: DataFrame) => void;
  onError?: (replyTo: string, error: APIError) => void;
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
  private listeners = new Set<SocketListener>();

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

  /** Leaves topics (and stops subscribing to them again after a reconnection). */
  unsubscribe(topics: string[]) {
    for (const t of topics) this.topics.delete(t);
    if (topics.length > 0) this.sendEnvelope(TypeUIUnsubscribe, { topics });
  }

  /** Sends a message of any type; returns its id (the replyTo of a refusal). "" when there is no connection. */
  send(type: string, payload: unknown): string {
    return this.sendEnvelope(type, payload);
  }

  /** Adds a listener; the return undoes it. */
  listen(l: SocketListener): () => void {
    this.listeners.add(l);
    return () => this.listeners.delete(l);
  }

  private each(f: (l: SocketListener) => void) {
    for (const l of [...this.listeners]) f(l);
  }

  private sendEnvelope(type: string, payload: unknown): string {
    if (!this.socket) return "";
    const id = crypto.randomUUID();
    const env: Pick<UIEnvelope, "type"> & { id: string; ts: string; payload: unknown } = {
      id,
      type,
      ts: new Date().toISOString(),
      payload,
    };
    this.socket.send(JSON.stringify(env));
    return id;
  }

  /** How long the next attempt waits, in ms. */
  delay(): number {
    const base = this.o.baseDelay ?? 1000;
    const max = this.o.maxDelay ?? 30000;
    return Math.min(max, base * 2 ** Math.max(0, this.attempts - 1));
  }

  private sendSubscribe(topics: string[]) {
    if (topics.length > 0) this.sendEnvelope(TypeUISubscribe, { topics });
  }

  private connect() {
    this.o.onState?.(this.everOpened ? "reconnecting" : "connecting");
    const s = (this.o.open ?? ((u) => new WebSocket(u) as unknown as SocketLike))(this.o.url);
    this.socket = s;
    s.binaryType = "arraybuffer";
    s.onopen = () => {
      const again = this.everOpened;
      this.everOpened = true;
      this.attempts = 0;
      this.o.onState?.("open");
      this.each((l) => l.onOpen?.(again));
      this.sendSubscribe([...this.topics]);
      if (again) this.o.onReconnected?.();
    };
    s.onmessage = (ev) => {
      if (typeof ev.data !== "string") {
        if (ev.data instanceof ArrayBuffer) {
          const f = decodeFrame(ev.data);
          if (f) {
            this.o.onFrame?.(f);
            this.each((l) => l.onFrame?.(f));
          }
        }
        return;
      }
      try {
        const env = JSON.parse(ev.data) as {
          type: string;
          reply_to?: string;
          payload: { topic: string; event: string; data: unknown } & APIError;
        };
        if (env.type === TypeUIEvent) {
          const { topic, event, data } = env.payload;
          this.o.onEvent?.(topic, event, data);
          this.each((l) => l.onEvent?.(topic, event, data));
        } else if (env.type === TypeUIError) {
          this.o.onError?.(env.reply_to ?? "", env.payload);
          this.each((l) => l.onError?.(env.reply_to ?? "", env.payload));
        }
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
