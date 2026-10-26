import { createContext, useContext } from "react";

/** The side panel of the chat, as the screens ask for it (the workspace view has a shortcut to open it). */
export interface ChatControl {
  open: boolean;
  toggle: () => void;
}

export const ChatControlContext = createContext<ChatControl>({ open: false, toggle: () => {} });

export function useChatControl(): ChatControl {
  return useContext(ChatControlContext);
}
