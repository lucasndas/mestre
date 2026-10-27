import type { TableBlock, ViewWait } from "../gen/panel";
import { quietParts } from "./model";

/**
 * The situation of a table: one word of the vocabulary of states (docs/painel/design.md#vocabulário-de-estados)
 * derived from the block of the view, the same for the campaign and for the Home. Pure: nothing is kept.
 */
export type SituationKey =
  | "delivered"
  | "closed"
  | "cancelled"
  | "stuck"
  | "approval"
  | "paused"
  | "waiting"
  | "stalled"
  | "review"
  | "preparing"
  | "working";

/** The label of the vocabulary a situation shows (the catalog key under `situation.label`). */
export type SituationLabel =
  | "preparing"
  | "working"
  | "review"
  | "decision"
  | "waiting"
  | "stuck"
  | "delivered"
  | "cancelled"
  | "closed";

export type Tone = "ok" | "plain" | "warn" | "accent" | "danger" | "done";
export type Icon = "open" | "work" | "wait" | "hand" | "review" | "check" | "warn" | "x";

export interface Situation {
  key: SituationKey;
  label: SituationLabel;
  tone: Tone;
  icon: Icon;
  /** Needs the user: a decision the user can make, a failure that came back or a table that delivers nothing. */
  urgent: boolean;
  /** The parameters of the sentence of the card (`situation.line.<key>`). */
  params: Record<string, string | number>;
  /** The step of the cycle: the position in Aberta, Em trabalho, Em revisão, Encerrando, Encerrada; -1 when the table was cancelled. */
  stage: number;
}

const wait = (b: TableBlock, kind: ViewWait["kind"]) => b.waits.find((w) => w.kind === kind);

export const CYCLE = ["open", "working", "in_review", "closing", "closed"] as const;

/** The position of a table in the normal cycle; a cancelled one has none (-1). */
export function stageOf(b: TableBlock): number {
  if (b.state === "cancelled" || b.state === "cancelling") return -1;
  return CYCLE.indexOf(b.state as (typeof CYCLE)[number]);
}

/** What the table is doing, in the first of these that holds. `canDecide`: the user may answer approvals. */
export function situationOf(b: TableBlock, canDecide: boolean): Situation {
  const stage = stageOf(b);
  const pr = b.pr;
  const base = { urgent: false, params: {}, stage };
  if (b.state === "closed") {
    return pr > 0
      ? {
          ...base,
          key: "delivered",
          label: "delivered",
          tone: "done",
          icon: "check",
          params: { pr },
        }
      : { ...base, key: "closed", label: "closed", tone: "plain", icon: "check" };
  }
  if (b.state === "closing") {
    return {
      ...base,
      key: "delivered",
      label: "delivered",
      tone: "done",
      icon: "check",
      params: { pr, closing: 1 },
    };
  }
  if (b.state === "cancelled" || b.state === "cancelling") {
    return {
      ...base,
      key: "cancelled",
      label: "cancelled",
      tone: b.state === "cancelled" ? "plain" : "warn",
      icon: "x",
      params: { cancelling: b.state === "cancelling" ? 1 : 0 },
    };
  }
  const failure = wait(b, "same_failure");
  if (failure) {
    return { ...base, key: "stuck", label: "stuck", tone: "danger", icon: "warn", urgent: true };
  }
  const approval = wait(b, "approval");
  if (approval) {
    return {
      ...base,
      key: "approval",
      label: "decision",
      tone: "accent",
      icon: "hand",
      urgent: canDecide,
      params: { reason: approval.reason },
    };
  }
  if (b.paused) {
    return {
      ...base,
      key: "paused",
      label: "waiting",
      tone: "warn",
      icon: "wait",
      params: { reason: pausedReason(b) },
    };
  }
  const other = b.waits.find((w) => w.kind === "quota" || w.kind === "table" || w.kind === "other");
  if (other) {
    return {
      ...base,
      key: "waiting",
      label: "waiting",
      tone: "warn",
      icon: "wait",
      params: { kind: other.kind, reason: other.reason },
    };
  }
  if (b.stalled) {
    const q = quietParts(b.quiet_seconds);
    return {
      ...base,
      key: "stalled",
      label: "stuck",
      tone: "warn",
      icon: "warn",
      urgent: true,
      params: { unit: q.unit, count: q.count },
    };
  }
  if (b.state === "in_review") {
    return {
      ...base,
      key: "review",
      label: "review",
      tone: "plain",
      icon: "review",
      params: { pr },
    };
  }
  if (b.state === "open") {
    return { ...base, key: "preparing", label: "preparing", tone: "plain", icon: "open" };
  }
  return { ...base, key: "working", label: "working", tone: "ok", icon: "work" };
}

/** Who paused it and why, from the wait the engine gives ("" when it says nothing). */
function pausedReason(b: TableBlock): string {
  return b.waits.find((w) => w.kind === "paused")?.reason ?? "";
}

/** The order of the list: what needs the user first, what is over last. */
export const RANK: Record<SituationKey, number> = {
  stuck: 0,
  approval: 1,
  stalled: 2,
  paused: 3,
  waiting: 4,
  review: 5,
  working: 6,
  preparing: 7,
  delivered: 8,
  closed: 9,
  cancelled: 10,
};

/** The tables ordered by urgency; the ones with the same rank keep their order. */
export function byUrgency(blocks: TableBlock[], canDecide: boolean): TableBlock[] {
  return blocks
    .map((b, i) => ({ b, i, r: RANK[situationOf(b, canDecide).key] }))
    .sort((x, y) => x.r - y.r || x.i - y.i)
    .map((x) => x.b);
}

/** The filters of the campaign: which situations each one lets through. */
export type FilterKey = "all" | "need" | "work" | "wait" | "review";
export const FILTERS: FilterKey[] = ["all", "need", "work", "wait", "review"];

export function matchesFilter(s: Situation, f: FilterKey): boolean {
  switch (f) {
    case "need":
      return s.urgent;
    case "work":
      return s.key === "working" || s.key === "preparing";
    case "wait":
      return s.key === "waiting" || s.key === "paused";
    case "review":
      return s.key === "review";
    default:
      return true;
  }
}
