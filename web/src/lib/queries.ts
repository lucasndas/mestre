import { queryOptions } from "@tanstack/react-query";
import type { Me, SessionList, Status } from "../gen/panel";
import { api } from "./api";

export const statusQuery = queryOptions({
  queryKey: ["status"],
  queryFn: () => api<Status>("GET", "/api/status"),
  staleTime: 0,
});

export const meQuery = queryOptions({
  queryKey: ["me"],
  queryFn: () => api<Me>("GET", "/api/me", undefined, { expect401: true }),
  retry: false,
  staleTime: Number.POSITIVE_INFINITY,
});

export const sessionsQuery = (all: boolean) =>
  queryOptions({
    queryKey: ["sessions", all ? "all" : "mine"],
    queryFn: () => api<SessionList>("GET", all ? "/api/admin/sessions" : "/api/sessions"),
  });
