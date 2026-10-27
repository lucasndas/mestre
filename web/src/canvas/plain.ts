import type { CanvasState } from "../gen/panel";

/** One line of "Em palavras simples": a catalog key under `plain.` and its params. */
export interface PlainLine {
  id: string;
  key: string;
  params: Record<string, string>;
}

const KNOWN = new Set(["starting", "idle", "busy", "stuck", "stopping", "stopped", "interrupted"]);

/** What each character does and waits for, from the state the board draws (the same data, in words). */
export function plainLines(st: CanvasState): PlainLine[] {
  const byId = new Map(st.characters.map((c) => [c.id, c]));
  return st.characters.map((c) => {
    const wait = st.waits.find((w) => w.waiter === c.id);
    const target = wait?.target ? byId.get(wait.target) : undefined;
    const reason = (c.reason ?? "").trim();
    const params = { name: c.name, reason: target?.name ?? reason };
    let key = KNOWN.has(c.state) ? c.state : "other";
    if (c.state === "blocked") {
      key = reason === "permission" ? "permission" : reason === "quota" ? "quota" : "input";
    } else if (c.state === "waiting") {
      key = reason.startsWith("approval")
        ? "approval"
        : target || reason
          ? "waitingFor"
          : "waiting";
    }
    return { id: c.id, key, params: { ...params, state: c.state } };
  });
}
