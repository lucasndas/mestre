import { createContext, useContext } from "react";
import type { SocketState, UiSocket } from "../lib/ws";

/** The socket of /ws/ui the shell keeps open; null until it exists. */
export const SocketContext = createContext<UiSocket | null>(null);

export function useUiSocket(): UiSocket | null {
  return useContext(SocketContext);
}

/** Whether the channel with the Citadel is up: what a screen that must not act "blind" asks. */
export const ConnectionContext = createContext<SocketState>("connecting");

export function useConnectionState(): SocketState {
  return useContext(ConnectionContext);
}
