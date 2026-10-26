import { useQuery } from "@tanstack/react-query";
import { useRouterState } from "@tanstack/react-router";
import { useMe } from "../app/session";
import type { WorkspaceRow } from "../gen/panel";
import { workspacesQuery } from "../screens/workspaces/queries";

/** The slug in the address of the screen (/workspaces/<slug>…), "" on the others. */
export function useRouteWorkspace(): string {
  const path = useRouterState({ select: (s) => s.location.pathname });
  const m = /^\/workspaces\/([^/]+)/.exec(path);
  return m ? decodeURIComponent(m[1] ?? "") : "";
}

export interface CurrentWorkspace {
  /** The workspaces the user may see (the server decides which). */
  list: WorkspaceRow[];
  /** The workspace the screens talk about: the one of the address, else the last opened, else the first; "" when none. */
  slug: string;
  canCreate: boolean;
  ready: boolean;
}

/**
 * The workspace of the moment (docs/painel/visao-do-workspace.md#seletor-de-workspaces): the one
 * in the address when the user may see it, else the last one opened (kept on the Citadel), else
 * the first of the list.
 */
export function useCurrentWorkspace(): CurrentWorkspace {
  const { data } = useQuery(workspacesQuery);
  const me = useMe();
  const routed = useRouteWorkspace();
  const list = data?.workspaces ?? [];
  const has = (s: string) => s !== "" && list.some((w) => w.slug === s);
  const slug = has(routed)
    ? routed
    : has(me.user.last_workspace)
      ? me.user.last_workspace
      : (list[0]?.slug ?? "");
  return { list, slug, canCreate: data?.can_create ?? false, ready: data !== undefined };
}
