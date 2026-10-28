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

/** Tables per row below the Grand Master before the row wraps. */
const PER_ROW = 3;
const GAP = { x: 40, y: 100 };

/**
 * Where the tables stand at first: in rows below the Grand Master, centered under it and
 * wrapping after PER_ROW, in the order of their ids (so a table appearing does not move the
 * others around much). Positions are the top-left corner of the node, in canvas coordinates;
 * the Grand Master stands at the origin.
 */
export function rows(ids: string[]): Map<string, { x: number; y: number }> {
  const out = new Map<string, { x: number; y: number }>();
  const sorted = [...ids].sort();
  sorted.forEach((id, i) => {
    const row = Math.floor(i / PER_ROW);
    const inRow = Math.min(PER_ROW, sorted.length - row * PER_ROW);
    const width = inRow * BLOCK_SIZE.w + (inRow - 1) * GAP.x;
    const col = i % PER_ROW;
    out.set(id, {
      x: Math.round(GM_SIZE.w / 2 - width / 2 + col * (BLOCK_SIZE.w + GAP.x)),
      y: GM_SIZE.h + GAP.y + row * (BLOCK_SIZE.h + GAP.y),
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
  const around = rows(view.tables.map((t) => t.id));
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
