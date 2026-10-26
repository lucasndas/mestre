import { createContext, useContext, useEffect, useRef } from "react";

export type LiveListener = (topic: string, event: string, data: unknown) => void;

/** The channel of the browser (/ws/ui) as the screens use it: follow topics, hear events. */
export interface Live {
  /** Follows topics (again after a reconnection, which the socket does itself). */
  subscribe(topics: string[]): void;
  /** Hears every event of every topic; returns what stops hearing. */
  listen(fn: LiveListener): () => void;
}

export const noLive: Live = { subscribe: () => {}, listen: () => () => {} };

export const LiveContext = createContext<Live>(noLive);

export function useLive(): Live {
  return useContext(LiveContext);
}

/** Calls `fn` for the events of the topics that start with `prefix`, while mounted. */
export function useLiveEvents(prefix: string, fn: LiveListener) {
  const live = useLive();
  const ref = useRef(fn);
  ref.current = fn;
  useEffect(
    () =>
      live.listen((topic, event, data) => {
        if (topic.startsWith(prefix)) ref.current(topic, event, data);
      }),
    [live, prefix],
  );
}
