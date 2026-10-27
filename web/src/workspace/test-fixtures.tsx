import { screen } from "@testing-library/react";
import type { TableBlock, WorkspaceRow, WorkspaceView } from "../gen/panel";
import { ME } from "../test-utils";

// The fixtures of the tests of the campaign: a row, a table, a view and the fake server around them.
export const row = (o: Partial<WorkspaceRow> = {}): WorkspaceRow => ({
  slug: "loja",
  repo: "acme/loja",
  default_branch: "main",
  state: "active",
  stages: [],
  versioning: "local",
  versioning_asked: true,
  grand_master: "idle",
  created_at: "2026-10-20T12:00:00Z",
  can_act: true,
  ...o,
});

export const table = (o: Partial<TableBlock> = {}): TableBlock => ({
  id: "42-login",
  epic: 42,
  epic_title: "Login com Google",
  branch: "epico/42-login",
  state: "working",
  paused: false,
  party: [],
  party_size: 0,
  waits: [],
  last_delivery: "2026-10-26T14:00:00Z",
  stalled: false,
  quiet_seconds: 0,
  pr: 0,
  ...o,
});

export const view = (tables: TableBlock[], o: Partial<WorkspaceView> = {}): WorkspaceView => ({
  workspace: row(),
  tables,
  layout: [],
  can_pause: true,
  can_type: true,
  ...o,
});

export const as = (level: string, caps: string[], workspaces: string[] = []) => ({
  "GET /api/me": () => ({
    body: { ...ME, user: { ...ME.user, level, workspaces }, capabilities: caps },
  }),
});

export const base = (
  v: WorkspaceView | (() => WorkspaceView),
  extra: Record<string, unknown> = {},
) => ({
  "GET /api/status": () => ({ body: { setup_open: false } }),
  "GET /api/transfer": () => ({ body: {} }),
  "GET /api/notices": () => ({ body: { notices: [], unread: 0 } }),
  "GET /api/chat": () => ({ body: { workspaces: [], unread: 0, limit: 1, max_files: 1 } }),
  "GET /api/approvals/count": () => ({ body: { pending: 3 } }),
  "GET /api/workspaces": () => ({
    body: { workspaces: [row()], sheets: [], can_create: false },
  }),
  "GET /api/workspaces/loja/view": () => ({ body: typeof v === "function" ? v() : v }),
  // the answer is the same user, so what he may do does not change when the campaign is remembered
  "PUT /api/me/workspace": () =>
    as("approver", ["status", "pause_buttons", "decide_approvals"], ["loja"])["GET /api/me"](),
  ...as("approver", ["status", "pause_buttons", "decide_approvals"], ["loja"]),
  ...extra,
});

// the canvas loads on demand (React Flow and xterm.js): the first block waits for that import
export const block = (name: string) =>
  screen.findByRole("article", { name: `Mesa ${name}` }, { timeout: 8000 });
