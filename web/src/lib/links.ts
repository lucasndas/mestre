import { useRouter } from "@tanstack/react-router";
import type { NoticeLink } from "../gen/panel";

/**
 * Goes to a path of the panel. The screens of the other units own their routes, so
 * a notice or a step that leads to one names it by its address, not by the typed
 * route of this unit.
 */
export function useGoto(): (path: string) => void {
  const router = useRouter();
  return (path) => router.history.push(path);
}

/** Where a notice of the bell leads; "" when it leads nowhere. */
export function noticePath(workspace: string, link: NoticeLink): string {
  switch (link.kind) {
    case "request":
      return `/aprovacoes?pedido=${encodeURIComponent(link.ref)}`;
    case "blocking":
      return `/aprovacoes?aviso=${encodeURIComponent(link.ref)}`;
    case "table":
      return `/workspaces/${encodeURIComponent(workspace)}/mesas/${encodeURIComponent(link.ref)}`;
    case "machine":
      return "/guilda";
    default:
      return "";
  }
}
