import type { CanvasState } from "../gen/panel";

/** A table with a Master, two characters, a scroll, a mission under way and a wait. */
export function tableState(over: Partial<CanvasState> = {}): CanvasState {
  const ch = (id: string, name: string, extra: object = {}) => ({
    id,
    name,
    kind: "character",
    role: "character",
    machine: "m1",
    machine_name: "windows-pc",
    state: "idle",
    pinned: false,
    has_terminal: false,
    ...extra,
  });
  return {
    workspace: "loja",
    table: "m1",
    table_state: "working",
    characters: [
      ch("gm", "Grão-Mestre", { role: "grand_master", pinned: true }),
      ch("mm", "Mestre", { role: "table_master" }),
      ch("dev", "Dev-1", { class: "Dev", state: "busy" }),
      ch("rev", "Revisor", { class: "Revisor", state: "waiting", reason: "Dev-1" }),
    ],
    scrolls: [
      { id: "loja/m1/p0001", name: "Plano", content: "# Plano\n", revision: 3, author: "Mestre" },
    ],
    tomes: [],
    connections: [
      {
        a: { kind: "character", id: "gm" },
        b: { kind: "character", id: "mm" },
        reasons: ["mestre"],
        manual: false,
      },
      {
        a: { kind: "character", id: "dev" },
        b: { kind: "character", id: "rev" },
        reasons: ["manual"],
        manual: true,
      },
    ],
    quests: [
      {
        id: "q1",
        name: "entrega",
        state: "running",
        round: 1,
        editable: false,
        steps: [
          {
            id: "dev",
            title: "Dev",
            state: "running",
            character: "dev",
            current: true,
            returns: 0,
          },
          {
            id: "rev",
            title: "Revisão",
            state: "pending",
            character: "rev",
            after: ["dev"],
            return_to: "dev",
            current: false,
            returns: 0,
          },
        ],
      },
    ],
    waits: [
      { id: "w1", waiter: "rev", target: "dev", kind: "step", reason: "Dev-1", deadlock: false },
    ],
    deadlocks: [],
    layout: [
      { key: "character:gm", x: -700, y: 0 },
      { key: "character:mm", x: 0, y: 0 },
      { key: "character:dev", x: 700, y: -100, w: 700, h: 500 },
      { key: "character:rev", x: 700, y: 600 },
      { key: "scroll:loja/m1/p0001", x: 0, y: 500 },
    ],
    can_edit: true,
    ...over,
  };
}
