import type { CanvasQuest, CanvasState, QuestRequest } from "../gen/panel";
import { type CanvasEdge, keyOf, stepCharacter } from "./model";

// The mission being drawn on the canvas (docs/painel/canvas.md#ações-no-canvas): an arrow
// dragged from one character to the next makes a step after the other; the number of a
// step is its place in the list, which the person reorders; an arrow dragged back to an
// earlier step is the return. The same engine validates it as when the Master of the
// table builds it by the CLI. One step per character is drawn.

export interface DraftStep {
  id: string;
  /** The name of the character that does it. */
  character: string;
  after: string[];
  returnTo?: string;
}

export interface Draft {
  /** The id of the draft mission being adjusted; absent while a new one is drawn. */
  questId?: string;
  name: string;
  steps: DraftStep[];
}

export const emptyDraft = (): Draft => ({ name: "", steps: [] });

const slug = (s: string) =>
  s
    .toLowerCase()
    .normalize("NFD")
    .replace(/[̀-ͯ]/g, "")
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-|-$/g, "") || "etapa";

function ensure(d: Draft, character: string): { draft: Draft; step: DraftStep } {
  const found = d.steps.find((s) => s.character === character);
  if (found) return { draft: d, step: found };
  const taken = new Set(d.steps.map((s) => s.id));
  let id = slug(character);
  for (let n = 2; taken.has(id); n++) id = `${slug(character)}-${n}`;
  const step: DraftStep = { id, character, after: [] };
  return { draft: { ...d, steps: [...d.steps, step] }, step };
}

/** An arrow dragged from one character to another. Going to an earlier step is a return. */
export function addArrow(d: Draft, from: string, to: string): Draft {
  if (from === to) return d;
  const a = ensure(d, from);
  const b = ensure(a.draft, to);
  const order = b.draft.steps.map((s) => s.id);
  const forward = order.indexOf(a.step.id) < order.indexOf(b.step.id);
  return {
    ...b.draft,
    steps: b.draft.steps.map((s) => {
      if (forward && s.id === b.step.id && !s.after.includes(a.step.id))
        return { ...s, after: [...s.after, a.step.id] };
      if (!forward && s.id === a.step.id) return { ...s, returnTo: b.step.id };
      return s;
    }),
  };
}

/** Moves a step in the numbering; the arrows that would then point forward become returns, and the reverse. */
export function move(d: Draft, id: string, delta: -1 | 1): Draft {
  const i = d.steps.findIndex((s) => s.id === id);
  const j = i + delta;
  if (i < 0 || j < 0 || j >= d.steps.length) return d;
  const steps = [...d.steps];
  [steps[i], steps[j]] = [steps[j] as DraftStep, steps[i] as DraftStep];
  return fixOrder({ ...d, steps });
}

/** Keeps `after` pointing only to earlier steps and `returnTo` only to earlier ones: what points the wrong way is swapped. */
function fixOrder(d: Draft): Draft {
  const pos = new Map(d.steps.map((s, i) => [s.id, i]));
  const steps = d.steps.map((s) => ({ ...s, after: [...s.after] }));
  for (const s of steps) {
    const me = pos.get(s.id) ?? 0;
    for (const a of [...s.after]) {
      if ((pos.get(a) ?? 0) > me) {
        s.after = s.after.filter((x) => x !== a);
        const other = steps.find((x) => x.id === a);
        if (other) other.returnTo = s.id;
      }
    }
  }
  for (const s of steps) {
    if (s.returnTo && (pos.get(s.returnTo) ?? 0) > (pos.get(s.id) ?? 0)) {
      const target = steps.find((x) => x.id === s.returnTo);
      if (target && !target.after.includes(s.id)) target.after.push(s.id);
      s.returnTo = undefined;
    }
  }
  return { ...d, steps };
}

export function remove(d: Draft, id: string): Draft {
  return {
    ...d,
    steps: d.steps
      .filter((s) => s.id !== id)
      .map((s) => ({
        ...s,
        after: s.after.filter((a) => a !== id),
        returnTo: s.returnTo === id ? undefined : s.returnTo,
      })),
  };
}

/** What the engine takes to make or redraw the mission. */
export function toRequest(d: Draft, run: boolean): QuestRequest {
  return {
    name: d.name.trim(),
    run,
    steps: d.steps.map((s) => ({
      id: s.id,
      character: s.character,
      after: s.after.length ? s.after : undefined,
      can_return_to: s.returnTo,
    })),
  } as QuestRequest;
}

/** A draft mission of the table, to adjust. */
export function fromQuest(q: CanvasQuest, st: CanvasState): Draft {
  const name = (stepId: string) =>
    st.characters.find((c) => c.id === stepCharacter(st, q, stepId))?.name ?? "";
  return {
    questId: q.id,
    name: q.name,
    steps: q.steps.map((s) => ({
      id: s.id,
      character: name(s.id),
      after: s.after ?? [],
      returnTo: s.return_to || undefined,
    })),
  };
}

/** The arrows of the draft, drawn on the canvas as it takes shape (dashed until saved). */
export function draftEdges(d: Draft, st: CanvasState): CanvasEdge[] {
  const idOf = (name: string) => st.characters.find((c) => c.name === name)?.id;
  const out: CanvasEdge[] = [];
  d.steps.forEach((s, i) => {
    const to = idOf(s.character);
    if (!to) return;
    for (const a of s.after) {
      const from = idOf(d.steps.find((x) => x.id === a)?.character ?? "");
      if (!from) continue;
      out.push({
        id: `draft:${a}>${s.id}`,
        source: keyOf("character", from),
        target: keyOf("character", to),
        type: "canvas",
        deletable: false,
        data: { kind: "step", label: String(i + 1), lane: 1, pending: true },
      });
    }
    if (s.returnTo) {
      const back = idOf(d.steps.find((x) => x.id === s.returnTo)?.character ?? "");
      if (back) {
        out.push({
          id: `draft-ret:${s.id}>${s.returnTo}`,
          source: keyOf("character", to),
          target: keyOf("character", back),
          type: "canvas",
          deletable: false,
          data: { kind: "return", label: String(i + 1), lane: 2, pending: true },
        });
      }
    }
  });
  return out;
}
