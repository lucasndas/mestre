import { useSyncExternalStore } from "react";

/** The first-access code of an invite just made: only in the memory of this tab, never stored. */
export interface Delivery {
  name: string;
  code: string;
  expires: string;
}

let current: Delivery | null = null;
const listeners = new Set<() => void>();

export function setDelivery(d: Delivery | null) {
  current = d;
  for (const l of listeners) l();
}

export function useDelivery(): Delivery | null {
  return useSyncExternalStore(
    (l) => {
      listeners.add(l);
      return () => listeners.delete(l);
    },
    () => current,
  );
}
