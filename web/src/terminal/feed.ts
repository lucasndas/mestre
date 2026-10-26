import type {
  APIError,
  TerminalInfo,
  TerminalReplayed,
  TerminalScreenEvent,
  TerminalSignal,
} from "../gen/panel";
import {
  EventReplayed,
  EventScreen,
  EventSignal,
  EventSnapshot,
  TypeTerminalInput,
  TypeTerminalTypingEnd,
} from "../gen/panel";
import { type DataFrame, FRAME_TERMINAL, type UiSocket } from "../lib/ws";

/** What a followed terminal tells the view. */
export interface FeedHandlers {
  info?(i: TerminalInfo): void;
  frame(offset: number, data: Uint8Array): void;
  replayed?(r: TerminalReplayed): void;
  signal?(s: TerminalSignal): void;
  screen?(s: TerminalScreenEvent): void;
  /** The stream starts again from offset 0 (the socket was lost and came back). */
  reset(): void;
  /** The Citadel refused a message sent with type(). */
  refused?(e: APIError): void;
}

/**
 * How a terminal view reaches the Citadel. The canvas and the tests give their own;
 * socketFeed is the one over /ws/ui.
 */
export interface TerminalFeed {
  /** Follows the terminal (subscribes, and the stream starts from 0); the return stops it. */
  follow(terminalId: string, h: FeedHandlers): () => void;
  /** Types into the terminal (the server checks the level at each call). */
  type(terminalId: string, data: string): void;
  /** Releases the keyboard: the typing session ends. */
  endTyping(terminalId: string): void;
}

export const terminalTopic = (id: string) => `terminal:${id}`;

/** The feed over the socket of the panel. */
export function socketFeed(sock: UiSocket): TerminalFeed {
  // the ids of the messages typed, to tell a refusal to the terminal it is about
  const sent = new Map<string, string>();
  const handlers = new Map<string, Set<FeedHandlers>>();
  // the id the binary frames of a terminal carry (the snapshot says it) -> its terminal id
  const frames = new Map<string, string>();
  const dispatch = (id: string, f: (h: FeedHandlers) => void) => {
    for (const h of [...(handlers.get(id) ?? [])]) f(h);
  };
  let listening = false;
  const ensure = () => {
    if (listening) return;
    listening = true;
    sock.listen({
      onEvent(topic, event, data) {
        const id = topic.startsWith("terminal:") ? topic.slice(9) : "";
        if (!id) return;
        switch (event) {
          case EventSnapshot: {
            const info = data as TerminalInfo;
            if (info.frame_id) frames.set(info.frame_id, id);
            dispatch(id, (h) => h.info?.(info));
            break;
          }
          case EventReplayed:
            dispatch(id, (h) => h.replayed?.(data as TerminalReplayed));
            break;
          case EventSignal:
            dispatch(id, (h) => h.signal?.(data as TerminalSignal));
            break;
          case EventScreen:
            dispatch(id, (h) => h.screen?.(data as TerminalScreenEvent));
            break;
        }
      },
      onFrame(f: DataFrame) {
        if (f.type !== FRAME_TERMINAL) return;
        const id = frames.get(f.id);
        if (id !== undefined) dispatch(id, (h) => h.frame(f.offset, f.data));
      },
      onError(replyTo, err) {
        const id = sent.get(replyTo);
        if (id === undefined) return;
        sent.delete(replyTo);
        dispatch(id, (h) => h.refused?.(err));
      },
      onOpen(again) {
        if (!again) return;
        for (const hs of handlers.values()) for (const h of hs) h.reset();
      },
    });
  };
  return {
    follow(id, h) {
      ensure();
      let set = handlers.get(id);
      if (!set) {
        set = new Set();
        handlers.set(id, set);
      }
      const others = set.size > 0;
      set.add(h);
      // a second view of a terminal that is followed already needs its stream from 0
      // too: leaving and entering the topic makes the Citadel send it again
      if (others) sock.unsubscribe([terminalTopic(id)]);
      sock.subscribe([terminalTopic(id)]);
      return () => {
        set.delete(h);
        if (set.size === 0) {
          handlers.delete(id);
          for (const [frame, term] of frames) if (term === id) frames.delete(frame);
          sock.unsubscribe([terminalTopic(id)]);
        }
      };
    },
    type(id, data) {
      const msgId = sock.send(TypeTerminalInput, { terminal_id: id, data });
      if (msgId) {
        sent.set(msgId, id);
        if (sent.size > 256) sent.delete(sent.keys().next().value as string);
      }
    },
    endTyping(id) {
      sock.send(TypeTerminalTypingEnd, { terminal_id: id });
    },
  };
}
