import { queryOptions } from "@tanstack/react-query";
import type {
  ApprovalCount,
  ApprovalDetails,
  ApprovalHistory,
  ApprovalList,
  NoticeList,
} from "../gen/panel";
import { api } from "./api";

export interface ApprovalFilters {
  workspace: string;
  table: string;
  type: string;
}

function qs(params: Record<string, string>): string {
  const q = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) if (v) q.set(k, v);
  const s = q.toString();
  return s ? `?${s}` : "";
}

export const approvalsQuery = (f: ApprovalFilters) =>
  queryOptions({
    queryKey: ["approvals", "list", f],
    queryFn: () => api<ApprovalList>("GET", `/api/approvals${qs({ ...f })}`),
  });

export const approvalCountQuery = queryOptions({
  queryKey: ["approvals", "count"],
  queryFn: () => api<ApprovalCount>("GET", "/api/approvals/count"),
});

export const approvalDetailsQuery = (id: number) =>
  queryOptions({
    queryKey: ["approvals", "details", id],
    queryFn: () => api<ApprovalDetails>("GET", `/api/approvals/${id}/details`),
  });

export interface HistoryFilters {
  state: string;
  by: string;
  level: string;
  risk: string;
  workspace: string;
  from: string;
  to: string;
}

export const historyPath = (f: HistoryFilters, before?: number) =>
  `/api/approvals/history${qs({ ...f, before: before ? String(before) : "" })}`;

export const noticesQuery = queryOptions({
  queryKey: ["notices"],
  queryFn: () => api<NoticeList>("GET", "/api/notices"),
});

export type { ApprovalHistory };
