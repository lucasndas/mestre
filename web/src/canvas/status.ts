import type { CanvasCharacter, CanvasState } from "../gen/panel";
import type { Icon, Tone } from "../workspace/situation";

/** The words of the vocabulary of states for a character (docs/painel/design.md#vocabulário-de-estados). */
export type StatusKey =
  | "working"
  | "idle"
  | "decision"
  | "approval"
  | "waiting"
  | "waitingFor"
  | "quota"
  | "stuck"
  | "noSignal"
  | "ended";

export interface CharacterStatus {
  key: StatusKey;
  tone: Tone;
  /** The shape of the sign next to the word: the color is never the only sign. */
  icon: Icon;
  /** Who or what it waits for, for "Esperando <quem>". */
  reason: string;
  /** A person who may decide can answer the approval it waits for. */
  needsYou: boolean;
}

const ENDED = new Set(["stopped", "stopping", "interrupted"]);

/**
 * One word for what a character is doing, from the data the board already has. `noSignal`: its machine lost the
 * signal (the terminal says so); `canDecide`: the user may answer approvals.
 */
export function characterStatus(
  c: CanvasCharacter,
  state: Pick<CanvasState, "characters" | "waits">,
  opts: { noSignal: boolean; canDecide: boolean },
): CharacterStatus {
  const reason = (c.reason ?? "").trim();
  const wait = state.waits.find((w) => w.waiter === c.id);
  const target = wait?.target ? state.characters.find((x) => x.id === wait.target) : undefined;
  const base = { reason: target?.name ?? reason, needsYou: false };
  if (ENDED.has(c.state)) return { ...base, key: "ended", tone: "plain", icon: "check" };
  if (opts.noSignal) return { ...base, key: "noSignal", tone: "danger", icon: "x" };
  if (c.state === "stuck") return { ...base, key: "stuck", tone: "danger", icon: "warn" };
  const approval =
    (c.state === "waiting" && reason.startsWith("approval")) ||
    (c.state === "blocked" && reason === "permission");
  if (approval)
    return opts.canDecide
      ? { ...base, key: "decision", tone: "accent", icon: "hand", needsYou: true }
      : { ...base, key: "approval", tone: "warn", icon: "wait" };
  if (c.state === "blocked") {
    return reason === "quota"
      ? { ...base, key: "quota", tone: "warn", icon: "wait" }
      : { ...base, key: "waiting", tone: "warn", icon: "wait" };
  }
  if (c.state === "waiting") {
    return {
      ...base,
      key: target || reason ? "waitingFor" : "waiting",
      tone: "warn",
      icon: "wait",
    };
  }
  if (c.state === "busy") return { ...base, key: "working", tone: "ok", icon: "work" };
  return { ...base, key: "idle", tone: "plain", icon: "open" };
}

export type Bar = "done" | "current" | "todo";

/** The mission of a character and its step: "Missão entrega · Etapa 1 de 2". Null when no mission runs through it. */
export function missionLine(
  c: CanvasCharacter,
  state: Pick<CanvasState, "quests">,
): { quest: string; step: number; total: number; bars: Bar[] } | null {
  for (const q of state.quests) {
    if (q.state === "cancelled" || q.state === "completed") continue;
    const at = q.steps.findIndex((s) => s.character === c.id);
    if (at < 0) continue;
    const now = q.steps.findIndex((s) => s.current);
    const bars = q.steps.map(
      (_, i): Bar => (now < 0 || i > now ? "todo" : i === now ? "current" : "done"),
    );
    return { quest: q.name, step: at + 1, total: q.steps.length, bars };
  }
  return null;
}
