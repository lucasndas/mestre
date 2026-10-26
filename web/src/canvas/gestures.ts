import type { Connection } from "@xyflow/react";
import { useCallback, useState } from "react";
import type { CanvasState } from "../gen/panel";
import type { actions } from "./api";
import { addArrow, type Draft } from "./mission";
import { type CanvasEdge, keyOf, splitKey } from "./model";

const refOf = (key: string) => {
  const { kind, id } = splitKey(key);
  return { kind, id };
};

export interface GestureDeps {
  state: CanvasState | undefined;
  act: ReturnType<typeof actions>;
  /** The engine answered: read the state again. */
  changed: () => void;
  /** The engine refused (or failed): show why. */
  fail: (err: unknown) => void;
  draft: Draft | null;
  setDraft: (d: Draft) => void;
  clear: () => void;
  onlyCharacters: () => void;
}

/**
 * The gestures that end in the engine. Nothing is added or removed on the screen before the
 * engine answers: a line dragged shows as pending until then and is gone if the engine
 * refuses (CA-CANVAS-05); a removal changes the state only when the state comes back changed
 * (CA-CANVAS-10, CA-CONX-05). While a mission is drawn, a drag makes a step instead.
 */
export function useGestures(d: GestureDeps) {
  const { state, act, changed, fail, draft, setDraft, clear, onlyCharacters } = d;
  const [pending, setPending] = useState<CanvasEdge[]>([]);

  const connect = useCallback(
    async (c: Connection) => {
      if (!state || c.source === c.target) return;
      if (draft) {
        const name = (key: string) =>
          state.characters.find((x) => keyOf("character", x.id) === key)?.name;
        const from = name(c.source);
        const to = name(c.target);
        if (!from || !to) return onlyCharacters();
        return setDraft(addArrow(draft, from, to));
      }
      const id = `pending:${c.source}|${c.target}`;
      setPending((p) => [
        ...p,
        {
          id,
          source: c.source,
          target: c.target,
          type: "canvas",
          deletable: false,
          data: { kind: "connection", pending: true },
        },
      ]);
      clear();
      try {
        await act.connect(refOf(c.source), refOf(c.target));
        changed();
      } catch (err) {
        fail(err);
      } finally {
        setPending((p) => p.filter((e) => e.id !== id));
      }
    },
    [state, draft, act, changed, fail, setDraft, clear, onlyCharacters],
  );

  /** Removes the connections among the edges; each one is asked of the engine on its own. */
  const removeEdges = useCallback(
    async (edges: CanvasEdge[]) => {
      clear();
      for (const e of edges) {
        if (e.data?.kind !== "connection") continue;
        try {
          await act.disconnect(refOf(e.source), refOf(e.target));
        } catch (err) {
          fail(err);
        }
      }
      changed();
    },
    [act, changed, fail, clear],
  );

  return { pending, connect, removeEdges };
}
