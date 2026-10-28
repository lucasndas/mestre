import { queryOptions } from "@tanstack/react-query";
import type {
  CanvasQuest,
  CanvasRef,
  CanvasScroll,
  CanvasState,
  GrimoireInfo,
  GrimoirePlan,
  GrimoireRestoreRequest,
  GrimoireSaveRequest,
  LayoutRequest,
  QuestRequest,
  RitualEditRequest,
  RitualInfo,
} from "../gen/panel";
import { api } from "../lib/api";

const base = (ws: string, table: string) =>
  `/api/workspaces/${encodeURIComponent(ws)}/tables/${encodeURIComponent(table)}/canvas`;

export const canvasTopic = (ws: string, table: string) => `canvas:${ws}/${table}`;

export const canvasQuery = (ws: string, table: string) =>
  queryOptions({
    queryKey: ["canvas", ws, table],
    queryFn: () => api<CanvasState>("GET", base(ws, table)),
    retry: false,
    staleTime: 0,
  });

export const ritualsQuery = (ws: string, table: string) =>
  queryOptions({
    queryKey: ["canvas", ws, table, "rituals"],
    queryFn: () => api<{ rituals: RitualInfo[] }>("GET", `${base(ws, table)}/rituals`),
    retry: false,
  });

export const grimoiresQuery = queryOptions({
  queryKey: ["grimoires"],
  queryFn: () => api<{ grimoires: GrimoireInfo[] }>("GET", "/api/grimoires"),
  retry: false,
});

/** The actions of the canvas: each one asks the engine, which may refuse (an ApiError with the code of the refusal, "canvas.name_taken"). */
export const actions = (ws: string, table: string) => ({
  saveLayout: (req: LayoutRequest) => api<void>("PUT", `${base(ws, table)}/layout`, req),
  connect: (a: CanvasRef, b: CanvasRef) =>
    api<void>("POST", `${base(ws, table)}/connections`, { a, b }),
  disconnect: (a: CanvasRef, b: CanvasRef) =>
    api<void>("DELETE", `${base(ws, table)}/connections`, { a, b }),
  pin: (character: string, pinned: boolean) =>
    api<void>("PUT", `${base(ws, table)}/characters/${encodeURIComponent(character)}/pin`, {
      pinned,
    }),
  writeScroll: (scroll: string, content: string, rev: number) =>
    api<CanvasScroll>("PUT", `${base(ws, table)}/scrolls/${encodeURIComponent(scroll)}`, {
      content,
      base: rev,
    }),
  createQuest: (req: QuestRequest) => api<CanvasQuest>("POST", `${base(ws, table)}/quests`, req),
  editQuest: (id: string, req: QuestRequest) =>
    api<CanvasQuest>("PUT", `${base(ws, table)}/quests/${encodeURIComponent(id)}`, req),
  saveGrimoire: (req: GrimoireSaveRequest) =>
    api<GrimoireInfo>("POST", `${base(ws, table)}/grimoires`, req),
  restoreGrimoire: (req: GrimoireRestoreRequest) =>
    api<GrimoirePlan>("POST", `${base(ws, table)}/grimoires/restore`, req),
  editRitual: (id: string, req: RitualEditRequest) =>
    api<RitualInfo>("PUT", `${base(ws, table)}/rituals/${encodeURIComponent(id)}`, req),
});
