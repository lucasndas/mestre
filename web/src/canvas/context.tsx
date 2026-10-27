import { useStore } from "@xyflow/react";
import { createContext, useContext } from "react";
import type { TerminalFeed } from "../terminal/feed";
import type { actions } from "./api";

/** What the nodes of the canvas need from the screen that holds them. */
export interface CanvasEnv {
  act: ReturnType<typeof actions>;
  /** The terminals' stream; null until the socket exists. */
  feed: TerminalFeed | null;
  timeZone: string;
  canType: boolean;
  /** An action was refused or failed: the screen shows the reason. */
  fail: (err: unknown) => void;
  /** An action went through: the screen reads the state again. */
  changed: () => void;
  /** Mission being drawn: its arrows are drawn by dragging between characters. */
  drawing: boolean;
  /** Undoes a group (its cards stay where they are). */
  ungroup: (id: string) => void;
  /** Gives a group its name. */
  renameGroup: (id: string, label: string) => void;
}

export const CanvasEnvContext = createContext<CanvasEnv | null>(null);

export function useCanvasEnv(): CanvasEnv {
  const env = useContext(CanvasEnvContext);
  if (!env) throw new Error("canvas: no environment");
  return env;
}

/** Below this zoom a terminal is too small to read: it shows a snapshot (docs/painel/canvas.md#terminais-no-canvas). */
export const NEAR_ZOOM = 0.3;

/**
 * Whether the node is on screen and near enough (in the zoom) to be read live. A node off
 * the viewport, or far away, gets a snapshot, which saves the WebGL contexts of the browser.
 */
export function useNodeView(id: string): { visible: boolean; near: boolean } {
  const code = useStore((s) => {
    const n = s.nodeLookup.get(id);
    if (!n) return "00";
    const [tx, ty, zoom] = s.transform;
    const { x, y } = n.internals.positionAbsolute;
    const w = n.measured.width ?? 0;
    const h = n.measured.height ?? 0;
    const visible =
      x * zoom + tx < s.width &&
      (x + w) * zoom + tx > 0 &&
      y * zoom + ty < s.height &&
      (y + h) * zoom + ty > 0;
    return `${visible ? 1 : 0}${zoom >= NEAR_ZOOM ? 1 : 0}`;
  });
  return { visible: code[0] === "1", near: code[1] === "1" };
}
