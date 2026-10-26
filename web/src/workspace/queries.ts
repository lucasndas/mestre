import { queryOptions } from "@tanstack/react-query";
import type { WorkspaceView } from "../gen/panel";
import { api } from "../lib/api";

export const enc = encodeURIComponent;

// The key starts with "workspaces", so the live events of the list (which
// invalidate that prefix) read the view again too.
export const viewQuery = (slug: string) =>
  queryOptions({
    queryKey: ["workspaces", slug, "view"],
    queryFn: () => api<WorkspaceView>("GET", `/api/workspaces/${enc(slug)}/view`),
    retry: false,
    staleTime: 0,
  });
