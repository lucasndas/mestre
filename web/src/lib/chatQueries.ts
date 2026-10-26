import { queryOptions } from "@tanstack/react-query";
import type { ChatConversation, ChatOverview } from "../gen/panel";
import { api } from "./api";

export const chatOverviewQuery = queryOptions({
  queryKey: ["chat", "overview"],
  queryFn: () => api<ChatOverview>("GET", "/api/chat"),
});

export const chatQuery = (workspace: string) =>
  queryOptions({
    queryKey: ["chat", "conversation", workspace],
    queryFn: () => api<ChatConversation>("GET", `/api/chat/${encodeURIComponent(workspace)}`),
  });

/** A made-up id for a message: sending it twice puts it once in the inbox of the Grão-Mestre. */
export function newMessageId(): string {
  if (typeof crypto !== "undefined" && "randomUUID" in crypto) return crypto.randomUUID();
  return `m${Date.now().toString(36)}${Math.random().toString(36).slice(2, 12)}`;
}

const KEY = "mestre.chat.workspace";

/** The workspace the side panel showed last, in this browser (never needed: the first one is the default). */
export function storedChatWorkspace(): string {
  try {
    return localStorage.getItem(KEY) ?? "";
  } catch {
    return "";
  }
}

export function storeChatWorkspace(slug: string) {
  try {
    localStorage.setItem(KEY, slug);
  } catch {
    // no storage: the choice lasts until the page is closed
  }
}

/** A size as the user reads it ("1,2 MB"). */
export function humanSize(bytes: number, language: string): string {
  const fmt = (n: number, unit: string) =>
    `${new Intl.NumberFormat(language, { maximumFractionDigits: 1 }).format(n)} ${unit}`;
  if (bytes >= 1 << 20) return fmt(bytes / (1 << 20), "MB");
  if (bytes >= 1 << 10) return fmt(bytes / (1 << 10), "KB");
  return fmt(bytes, "B");
}
