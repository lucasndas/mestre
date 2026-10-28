import type { Edge, Node } from "@xyflow/react";
import type {
  CanvasCharacter,
  CanvasConnection,
  CanvasItem,
  CanvasQuest,
  CanvasRef,
  CanvasScroll,
  CanvasState,
  CanvasTome,
  CanvasWait,
} from "../gen/panel";

// The canvas of a table as a graph (docs/painel/canvas.md#elementos): the server
// state turned into the nodes and edges of React Flow, and the keys that name
// each element ("<kind>:<id>", the same the server keeps the layout by).

export type Kind = "character" | "scroll" | "tome" | "group";

export const keyOf = (kind: Kind, id: string) => `${kind}:${id}`;

export function splitKey(key: string): { kind: Kind; id: string } {
  const i = key.indexOf(":");
  return { kind: key.slice(0, i) as Kind, id: key.slice(i + 1) };
}

/** The size of a node of each kind when the layout gives none; the same the engine places by. */
export const DEFAULT_SIZE: Record<Kind, { w: number; h: number }> = {
  character: { w: 640, h: 420 },
  scroll: { w: 320, h: 240 },
  tome: { w: 260, h: 140 },
  group: { w: 600, h: 400 },
};

export interface CharacterData extends Record<string, unknown> {
  kind: "character";
  workspace: string;
  table: string;
  character: CanvasCharacter;
  /** What the card needs of the table around it: who waits for whom and the mission it is in. */
  state: Pick<CanvasState, "characters" | "waits" | "quests">;
  canEdit: boolean;
}
export interface ScrollData extends Record<string, unknown> {
  kind: "scroll";
  workspace: string;
  table: string;
  scroll: CanvasScroll;
  canEdit: boolean;
}
export interface TomeData extends Record<string, unknown> {
  kind: "tome";
  tome: CanvasTome;
}
export interface GroupData extends Record<string, unknown> {
  kind: "group";
  label: string;
  canEdit: boolean;
}
export type NodeData = CharacterData | ScrollData | TomeData | GroupData;
export type CanvasNode = Node<NodeData>;

export type EdgeKind = "connection" | "step" | "return" | "wait";
export interface EdgeData extends Record<string, unknown> {
  kind: EdgeKind;
  label?: string;
  /** The reasons that keep a connection (a user may remove it only if "manual" is one). */
  reasons?: string[];
  manual?: boolean;
  current?: boolean;
  deadlock?: boolean;
  /** Several edges between the same two nodes are drawn apart: 0, 1, 2… */
  lane?: number;
  pending?: boolean;
}
export type CanvasEdge = Edge<EdgeData>;

const refKey = (r: CanvasRef) => keyOf(r.kind as Kind, r.id);

/** The nodes of the table, groups first (React Flow wants a parent before its children). */
export function buildNodes(st: CanvasState): CanvasNode[] {
  const place = new Map<string, CanvasItem>(st.layout.map((i) => [i.key, i]));
  const at = (key: string, kind: Kind) => {
    const it = place.get(key);
    const d = DEFAULT_SIZE[kind];
    return {
      position: { x: it?.x ?? 0, y: it?.y ?? 0 },
      width: it?.w ? it.w : d.w,
      height: it?.h ? it.h : d.h,
      parent: it?.parent && place.has(it.parent) ? it.parent : undefined,
    };
  };
  const nodes: CanvasNode[] = [];
  for (const it of st.layout) {
    if (!it.key.startsWith("group:")) continue;
    const p = at(it.key, "group");
    nodes.push({
      id: it.key,
      type: "group",
      position: p.position,
      width: p.width,
      height: p.height,
      parentId: p.parent,
      data: { kind: "group", label: it.label ?? "", canEdit: st.can_edit },
      deletable: st.can_edit,
      draggable: st.can_edit,
    });
  }
  const add = (key: string, kind: Kind, type: string, data: NodeData) => {
    const p = at(key, kind);
    nodes.push({
      id: key,
      type,
      position: p.position,
      width: p.width,
      height: p.height,
      parentId: p.parent,
      data,
      deletable: false,
    });
  };
  const base = { workspace: st.workspace, table: st.table, canEdit: st.can_edit };
  for (const c of st.characters) {
    add(keyOf("character", c.id), "character", "character", {
      kind: "character",
      character: c,
      state: { characters: st.characters, waits: st.waits, quests: st.quests },
      ...base,
    });
  }
  for (const s of st.scrolls) {
    add(keyOf("scroll", s.id), "scroll", "scroll", { kind: "scroll", scroll: s, ...base });
  }
  for (const t of st.tomes) {
    add(keyOf("tome", t.id), "tome", "tome", { kind: "tome", tome: t });
  }
  return nodes;
}

function connectionEdge(c: CanvasConnection, lane: number): CanvasEdge {
  const source = refKey(c.a);
  const target = refKey(c.b);
  return {
    id: `conn:${source}|${target}`,
    source,
    target,
    type: "canvas",
    // the engine decides what may be removed: an automatic connection is refused with its reason
    deletable: true,
    selectable: true,
    data: { kind: "connection", reasons: c.reasons, manual: c.manual, lane },
  };
}

/** The character that does a step: the one it was given, else the one the definition names. */
export function stepCharacter(st: CanvasState, quest: CanvasQuest, stepId: string): string {
  const s = quest.steps.find((x) => x.id === stepId);
  if (!s) return "";
  if (s.character) return s.character;
  const named = st.characters.find(
    (c) => c.name.toLowerCase() === (s.assignee ?? "").toLowerCase(),
  );
  return named?.id ?? "";
}

/** The arrows of the missions: numbered by the order of the steps, the current one highlighted, returns going back. */
export function questEdges(st: CanvasState): CanvasEdge[] {
  const out: CanvasEdge[] = [];
  for (const q of st.quests) {
    if (q.state === "cancelled") continue;
    q.steps.forEach((s, i) => {
      const to = stepCharacter(st, q, s.id);
      if (!to) return;
      for (const prev of s.after ?? []) {
        const from = stepCharacter(st, q, prev);
        if (!from || from === to) continue;
        out.push({
          id: `step:${q.id}:${prev}>${s.id}`,
          source: keyOf("character", from),
          target: keyOf("character", to),
          type: "canvas",
          deletable: false,
          data: {
            kind: "step",
            label: String(i + 1),
            // the arrows into and out of the step in progress are the highlighted ones
            current: s.current || Boolean(q.steps.find((x) => x.id === prev)?.current),
            lane: 1,
            reasons: [q.name],
          },
        });
      }
      if (s.return_to) {
        const back = stepCharacter(st, q, s.return_to);
        if (back && back !== to) {
          out.push({
            id: `ret:${q.id}:${s.id}>${s.return_to}`,
            source: keyOf("character", to),
            target: keyOf("character", back),
            type: "canvas",
            deletable: false,
            data: { kind: "return", label: String(i + 1), lane: 2, reasons: [q.name] },
          });
        }
      }
    });
  }
  return out;
}

function waitEdges(waits: CanvasWait[]): CanvasEdge[] {
  const out: CanvasEdge[] = [];
  for (const w of waits) {
    if (!w.target) continue; // it waits for a time or an approval: the reason is on the node
    out.push({
      id: `wait:${w.id}:${w.target}`,
      source: keyOf("character", w.waiter),
      target: keyOf("character", w.target),
      type: "canvas",
      deletable: false,
      data: { kind: "wait", label: w.reason, deadlock: w.deadlock, lane: 3 },
    });
  }
  return out;
}

/** The edges of the table: connections, mission steps and returns, and waits. */
export function buildEdges(st: CanvasState): CanvasEdge[] {
  const seen = new Map<string, number>();
  const conns = st.connections.map((c) => {
    const pair = [refKey(c.a), refKey(c.b)].sort().join("|");
    const lane = seen.get(pair) ?? 0;
    seen.set(pair, lane + 1);
    return connectionEdge(c, lane);
  });
  return [...conns, ...questEdges(st), ...waitEdges(st.waits)];
}

/** Nodes that moved, as the layout the server keeps (positions relative to the group, as React Flow has them). */
export function layoutItems(nodes: CanvasNode[]): CanvasItem[] {
  return nodes.map((n) => ({
    key: n.id,
    label: n.data.kind === "group" ? n.data.label : undefined,
    x: Math.round(n.position.x),
    y: Math.round(n.position.y),
    w: Math.round(n.width ?? n.measured?.width ?? 0),
    h: Math.round(n.height ?? n.measured?.height ?? 0),
    parent: n.parentId,
  }));
}

/** The group that holds the nodes: its box is theirs with a margin; the children move into it. */
export function groupOf(
  nodes: CanvasNode[],
  id: string,
  label: string,
  canEdit: boolean,
): CanvasNode[] {
  const pad = 40;
  const w = (n: CanvasNode) => n.width ?? n.measured?.width ?? DEFAULT_SIZE[n.data.kind].w;
  const h = (n: CanvasNode) => n.height ?? n.measured?.height ?? DEFAULT_SIZE[n.data.kind].h;
  const x0 = Math.min(...nodes.map((n) => n.position.x)) - pad;
  const y0 = Math.min(...nodes.map((n) => n.position.y)) - pad - 30;
  const x1 = Math.max(...nodes.map((n) => n.position.x + w(n))) + pad;
  const y1 = Math.max(...nodes.map((n) => n.position.y + h(n))) + pad;
  const group: CanvasNode = {
    id,
    type: "group",
    position: { x: x0, y: y0 },
    width: x1 - x0,
    height: y1 - y0,
    data: { kind: "group", label, canEdit },
    deletable: canEdit,
  };
  const children = nodes.map((n) => ({
    ...n,
    parentId: id,
    position: { x: n.position.x - x0, y: n.position.y - y0 },
  }));
  return [group, ...children];
}

/** The character the board opens on: who is working, else the master of the table, else the first. */
export function focusCharacter(st: CanvasState): string {
  const c =
    st.characters.find((x) => x.state === "busy") ??
    st.characters.find((x) => x.role === "table_master") ??
    st.characters[0];
  return c ? keyOf("character", c.id) : "";
}

/**
 * What the board opens on, at zoom 100% (docs/painel/canvas.md#canvas-infinito): the whole
 * table when it fits in the frame, else the focus character, whose node is centered.
 */
export function initialView(
  nodes: CanvasNode[],
  focus: string,
  frame: { width: number; height: number },
): { nodes?: { id: string }[] } {
  const own = nodes.filter((n) => !n.parentId);
  if (own.length === 0) return {};
  const w = (n: CanvasNode) => n.width ?? 0;
  const h = (n: CanvasNode) => n.height ?? 0;
  const minX = Math.min(...own.map((n) => n.position.x));
  const minY = Math.min(...own.map((n) => n.position.y));
  const maxX = Math.max(...own.map((n) => n.position.x + w(n)));
  const maxY = Math.max(...own.map((n) => n.position.y + h(n)));
  const zoom = Math.min(frame.width / (maxX - minX), frame.height / (maxY - minY));
  if (zoom >= 1 || !own.some((n) => n.id === focus)) return {};
  return { nodes: [{ id: focus }] };
}
