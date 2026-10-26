import { createContext, useContext } from "react";
import type { UiSocket } from "../lib/ws";

/** The socket of /ws/ui the shell keeps open; null until it exists. */
export const SocketContext = createContext<UiSocket | null>(null);

export function useUiSocket(): UiSocket | null {
  return useContext(SocketContext);
}
