import type { Edge, Node } from "@xyflow/react";
import type { CanvasItem, TableBlock, ViewGrandMaster, WorkspaceView } from "../gen/panel";
import { type Situation, situationOf } from "./situation";

export const GM_KEY = "gm";
export const tableKey = (id: string) => `table:${id}`;

/** The size the Grand Master and a block take on the canvas, for the first placement. */
export const GM_SIZE = { w: 560, h: 380 };
export const BLOCK_SIZE = { w: 340, h: 280 };

export interface GmData extends Record<string, unknown> {
  gm: ViewGrandMaster | null;
  canType: boolean;
}
export interface BlockData extends Record<string, unknown> {
  block: TableBlock;
  workspace: string;
  repo: string;
  canPause: boolean;
  situation: Situation;
  /** Unread notices of the bell about this table. */
  newNotices: number;
}
export type ViewNode = Node<GmData, "gm"> | Node<BlockData, "table">;
export type ViewEdge = Edge;

/** closed and cancelled tables are hidden unless the filter shows them. */
export const isEnded = (b: TableBlock) => b.state === "closed" || b.state === "cancelled";

/**
 * Where the tables stand at first: in a circle around the Grand Master, in the order of their
 * ids (so a table appearing does not move the others around much). Positions are the top-left
 * corner of the node, in canvas coordinates; the Grand Master stands at the origin.
 */
export function circle(ids: string[]): Map<string, { x: number; y: number }> {
  const out = new Map<string, { x: number; y: number }>();
  const sorted = [...ids].sort();
  const n = sorted.length;
  // an ellipse wide enough for each block's arc, and far enough from the Grand Master to clear it
  const arc = Math.ceil(((BLOCK_SIZE.w + 60) * n) / (2 * Math.PI));
  const rx = Math.max(GM_SIZE.w / 2 + BLOCK_SIZE.w / 2 + 60, arc);
  const ry = Math.max(GM_SIZE.h / 2 + BLOCK_SIZE.h / 2 + 60, Math.round(arc * 0.8));
  const cx = GM_SIZE.w / 2;
  const cy = GM_SIZE.h / 2;
  sorted.forEach((id, i) => {
    const a = -Math.PI / 2 + (2 * Math.PI * i) / Math.max(n, 1);
    out.set(id, {
      x: Math.round(cx + rx * Math.cos(a) - BLOCK_SIZE.w / 2),
      y: Math.round(cy + ry * Math.sin(a) - BLOCK_SIZE.h / 2),
    });
  });
  return out;
}

/** What the canvas needs besides the view: who decides, which tables the filters let through and the notices not read. */
export interface BuildOptions {
  canDecide?: boolean;
  visible?: (b: TableBlock) => boolean;
  newNotices?: ReadonlyMap<string, number>;
}

/** The nodes of the canvas: the Grand Master and the tables the filter lets through, each where it was saved or in the circle. */
export function buildNodes(
  view: WorkspaceView,
  showEnded: boolean,
  opts: BuildOptions = {},
): ViewNode[] {
  const saved = new Map(view.layout.map((it) => [it.key, it]));
  const around = circle(view.tables.map((t) => t.id));
  const nodes: ViewNode[] = [];
  const gmAt = saved.get(GM_KEY);
  nodes.push({
    id: GM_KEY,
    type: "gm",
    position: { x: gmAt?.x ?? 0, y: gmAt?.y ?? 0 },
    width: GM_SIZE.w,
    height: GM_SIZE.h,
    draggable: true,
    deletable: false,
    data: { gm: view.grand_master ?? null, canType: view.can_type },
  });
  for (const b of view.tables) {
    if (isEnded(b) && !showEnded) continue;
    if (opts.visible && !opts.visible(b)) continue;
    const at = saved.get(tableKey(b.id));
    const home = around.get(b.id) ?? { x: 0, y: 0 };
    nodes.push({
      id: tableKey(b.id),
      type: "table",
      position: { x: at?.x ?? home.x, y: at?.y ?? home.y },
      width: BLOCK_SIZE.w,
      // measured by the browser; the initial size lets the node draw before that
      initialHeight: BLOCK_SIZE.h,
      deletable: false,
      data: {
        block: b,
        workspace: view.workspace.slug,
        repo: view.workspace.repo,
        canPause: view.can_pause,
        situation: situationOf(b, opts.canDecide ?? false),
        newNotices: opts.newNotices?.get(b.id) ?? 0,
      },
    });
  }
  return nodes;
}

/** Each table is tied to the Grand Master by a line. */
export function buildEdges(nodes: ViewNode[]): ViewEdge[] {
  return nodes
    .filter((n) => n.type === "table")
    .map((n) => ({
      id: `gm-${n.id}`,
      source: GM_KEY,
      target: n.id,
      type: "straight",
      selectable: false,
      focusable: false,
      deletable: false,
      className: "workspace-edge",
    }));
}

/** The places to save: only the Grand Master and the tables are on this canvas. */
export function layoutOf(nodes: ViewNode[]): CanvasItem[] {
  return nodes.map((n) => ({
    key: n.id,
    x: Math.round(n.position.x),
    y: Math.round(n.position.y),
  }));
}

/** "35 min", "4 h", "2 d": the quiet of a table, rounded down to one unit. */
export function quietParts(seconds: number): { unit: "minutes" | "hours" | "days"; count: number } {
  const min = Math.max(1, Math.floor(seconds / 60));
  if (min < 60) return { unit: "minutes", count: min };
  const h = Math.floor(min / 60);
  if (h < 48) return { unit: "hours", count: h };
  return { unit: "days", count: Math.floor(h / 24) };
}
