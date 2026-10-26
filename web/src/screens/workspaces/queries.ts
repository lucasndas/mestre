import { queryOptions } from "@tanstack/react-query";
import type { VerificationView, WorkspaceList } from "../../gen/panel";
import { api } from "../../lib/api";

// What the screens of the workspaces ask the server; the live events of the topic
// invalidate these keys (the topic only says that something changed).
export const workspacesQuery = queryOptions({
  queryKey: ["workspaces"],
  queryFn: () => api<WorkspaceList>("GET", "/api/workspaces"),
});

export const verificationQuery = (slug: string) =>
  queryOptions({
    queryKey: ["workspaces", slug, "verification"],
    queryFn: () =>
      api<VerificationView>("GET", `/api/workspaces/${encodeURIComponent(slug)}/verification`),
  });

export const WORKSPACES_TOPIC = "workspaces:all";
