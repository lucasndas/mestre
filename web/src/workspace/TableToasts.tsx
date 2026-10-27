import { Link } from "@tanstack/react-router";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { TableBlock, WorkspaceView } from "../gen/panel";
import { type SituationKey, situationOf } from "./situation";

export interface TableToast {
  id: number;
  kind: "created" | "decision" | "stuck" | "review" | "delivered" | "cancelled";
  table: string;
  title: string;
  pr: number;
}

/** The notices on the screen: what the new read of the view says that the last one did not. */
export function diffToasts(
  prev: ReadonlyMap<string, SituationKey> | null,
  blocks: TableBlock[],
  canDecide: boolean,
): Omit<TableToast, "id">[] {
  // the first read of a screen says nothing: there is nothing to compare with
  if (!prev) return [];
  const out: Omit<TableToast, "id">[] = [];
  for (const b of blocks) {
    const s = situationOf(b, canDecide);
    const was = prev.get(b.id);
    const base = { table: b.id, title: b.epic_title || b.id, pr: b.pr };
    if (was === undefined) {
      out.push({ ...base, kind: "created" });
      continue;
    }
    if (was === s.key) continue;
    if (s.key === "approval" && s.urgent) out.push({ ...base, kind: "decision" });
    else if (s.key === "stuck" || s.key === "stalled") out.push({ ...base, kind: "stuck" });
    else if (s.key === "review") out.push({ ...base, kind: "review" });
    else if (s.key === "delivered") out.push({ ...base, kind: "delivered" });
    else if (s.key === "cancelled") out.push({ ...base, kind: "cancelled" });
  }
  return out;
}

const SHOWN_MS = 9000;

/** Follows the view and keeps the notices to show; a campaign read for the first time shows none. */
export function useTableToasts(view: WorkspaceView | undefined, canDecide: boolean) {
  const [toasts, setToasts] = useState<TableToast[]>([]);
  const seen = useRef<{ slug: string; keys: Map<string, SituationKey> } | null>(null);
  const next = useRef(1);
  useEffect(() => {
    if (!view) return;
    const slug = view.workspace.slug;
    const before = seen.current?.slug === slug ? seen.current.keys : null;
    const fresh = diffToasts(before, view.tables, canDecide).map((t) => ({
      ...t,
      id: next.current++,
    }));
    seen.current = {
      slug,
      keys: new Map(view.tables.map((b) => [b.id, situationOf(b, canDecide).key])),
    };
    if (fresh.length === 0) return;
    setToasts((prev) => [...prev, ...fresh]);
    const ids = new Set(fresh.map((f) => f.id));
    // not cleared with the effect: a later read must not keep these on the screen for good
    setTimeout(() => setToasts((prev) => prev.filter((t) => !ids.has(t.id))), SHOWN_MS);
  }, [view, canDecide]);
  return { toasts, dismiss: (id: number) => setToasts((prev) => prev.filter((t) => t.id !== id)) };
}

/** The place of the notices: bottom right (above the tabs on a phone), read out by `role="status"`. */
export function TableToasts({
  workspace,
  toasts,
  dismiss,
}: {
  workspace: string;
  toasts: TableToast[];
  dismiss: (id: number) => void;
}) {
  const { t } = useTranslation("workspace");
  return (
    <div
      role="status"
      aria-live="polite"
      className="fixed right-3 bottom-20 z-30 flex w-[min(24rem,calc(100vw-1.5rem))] flex-col gap-2 md:bottom-4"
    >
      {toasts.map((x) => (
        <div
          key={x.id}
          className="flex items-center gap-2 rounded-field bg-ink py-1 pr-1 pl-3 text-paper shadow-pop"
        >
          <span className="flex-1 text-sm">
            {t(`life.toast.${x.kind}`, { table: x.title, pr: x.pr })}
          </span>
          <Link
            to="/workspaces/$workspace/mesas/$table"
            params={{ workspace, table: x.table }}
            className="inline-flex min-h-11 items-center px-2 text-sm text-warn underline"
          >
            {t("life.card.openTable")}
          </Link>
          <button
            type="button"
            aria-label={t("life.toast.close")}
            onClick={() => dismiss(x.id)}
            className="grid min-h-11 min-w-11 place-items-center rounded-field text-lg hover:bg-paper/10"
          >
            ×
          </button>
        </div>
      ))}
    </div>
  );
}
