import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { ApprovalCard, useDynamicT } from "../components/ApprovalCard";
import { BlockingCard } from "../components/BlockingCard";
import { SelectField } from "../components/SelectField";
import { Alert } from "../components/ui";
import type { ApprovalClosed, ApprovalEvent, ApprovalItem } from "../gen/panel";
import { type ApprovalFilters, approvalsQuery } from "../lib/approvalQueries";
import { errorText } from "../lib/errors";
import { useLiveEvents } from "../lib/live";

/** The two sections of the area of approvals: what waits, and what ended. */
export function ApprovalTabs({ current }: { current: "pending" | "history" }) {
  const { t } = useTranslation("approvals");
  const cls =
    "min-h-11 rounded-field px-3 py-2.5 font-semibold text-muted hover:bg-accent-wash aria-[current=page]:bg-accent-wash aria-[current=page]:text-ink";
  return (
    <nav aria-label={t("tabs.label")} className="mb-5 flex gap-2">
      <Link
        to="/aprovacoes"
        search={{}}
        className={cls}
        aria-current={current === "pending" ? "page" : undefined}
      >
        {t("tabs.pending")}
      </Link>
      <Link
        to="/aprovacoes/historico"
        className={cls}
        aria-current={current === "history" ? "page" : undefined}
      >
        {t("tabs.history")}
      </Link>
    </nav>
  );
}

function useAccumulated(values: string[]): string[] {
  const [all, setAll] = useState<string[]>([]);
  const key = values.join("\u0000");
  // biome-ignore lint/correctness/useExhaustiveDependencies: `key` is what `values` says
  useEffect(() => {
    setAll((prev) => {
      const next = new Set(prev);
      for (const v of values) if (v) next.add(v);
      return next.size === prev.length ? prev : [...next].sort();
    });
  }, [key]);
  return all;
}

/**
 * The central of approvals: the requests that wait for the user across his workspaces,
 * newest first, with filters (docs/painel/conversa-e-aprovacoes.md#central-de-aprovações).
 * A request answered somewhere else is told by /ws/ui and stays on the screen, with
 * no buttons, saying who answered.
 */
export function Approvals({
  request,
  notice,
  table,
  workspace,
}: {
  request?: number;
  notice?: number;
  /** Opens the central already filtered by a table (the card of a table that waits for a decision). */
  table?: string;
  /** The campaign of that table: two campaigns may have a table with the same name. */
  workspace?: string;
}) {
  const { t } = useTranslation("approvals");
  const tx = useDynamicT("approvals");
  const { t: te } = useTranslation("errors");
  const qc = useQueryClient();
  const [filters, setFilters] = useState<ApprovalFilters>({
    workspace: workspace ?? "",
    table: table ?? "",
    type: "",
  });
  const { data, error } = useQuery(approvalsQuery(filters));

  // what the screen showed, so that a request that ended meanwhile does not vanish
  const seen = useRef(new Map<number, ApprovalItem>());
  const [closed, setClosed] = useState<Record<number, ApprovalClosed>>({});
  useLiveEvents("approvals:", (_topic, event, payload) => {
    if (event !== "changed") return;
    const ev = payload as ApprovalEvent;
    if (ev.closed) setClosed((c) => ({ ...c, [ev.request]: ev.closed as ApprovalClosed }));
    void qc.invalidateQueries({ queryKey: ["approvals"] });
  });

  const items = useMemo(() => {
    const fetched = data?.requests ?? [];
    for (const it of fetched) seen.current.set(it.id, it);
    const out: ApprovalItem[] = fetched.map((it) =>
      closed[it.id] ? { ...it, answers: [], closed: closed[it.id] } : it,
    );
    const have = new Set(out.map((i) => i.id));
    for (const [id, c] of Object.entries(closed)) {
      const old = seen.current.get(Number(id));
      if (old && !have.has(old.id) && matches(old, filters)) {
        out.push({ ...old, answers: [], closed: c });
      }
    }
    return out.sort((a, b) => b.id - a.id);
  }, [data, closed, filters]);

  const blocking = data?.blocking ?? [];
  const workspaces = useAccumulated([
    ...items.map((i) => i.workspace),
    ...blocking.map((b) => b.workspace),
  ]);
  const tables = useAccumulated([...items.map((i) => i.table), ...blocking.map((b) => b.table)]);
  const types = useAccumulated([
    ...items.map((i) => i.type),
    ...(blocking.length > 0 ? ["aviso"] : []),
  ]);

  const onlyNotices = filters.type === "aviso";
  const empty = data && items.length === 0 && blocking.length === 0;
  const filtered = filters.workspace || filters.table || filters.type;

  const target = request ? `request-${request}` : notice ? `blocking-${notice}` : "";
  useEffect(() => {
    if (target && data) document.getElementById(target)?.scrollIntoView?.({ block: "center" });
  }, [target, data]);

  return (
    <section>
      <h1 className="text-3xl">{t("title")}</h1>
      <p className="mt-1 mb-5 max-w-2xl text-muted">{t("intro")}</p>
      <ApprovalTabs current="pending" />
      <form
        aria-label={t("filters.label")}
        className="mb-6 grid max-w-3xl gap-3 sm:grid-cols-3"
        onSubmit={(e) => e.preventDefault()}
      >
        <SelectField
          label={t("filters.workspace")}
          value={filters.workspace}
          onChange={(workspace) => setFilters({ ...filters, workspace })}
          options={[
            { value: "", label: t("filters.all") },
            ...workspaces.map((w) => ({ value: w, label: w })),
          ]}
        />
        <SelectField
          label={t("filters.table")}
          value={filters.table}
          onChange={(table) => setFilters({ ...filters, table })}
          options={[
            { value: "", label: t("filters.allTables") },
            ...tables.map((w) => ({ value: w, label: w })),
          ]}
        />
        <SelectField
          label={t("filters.type")}
          value={filters.type}
          onChange={(type) => setFilters({ ...filters, type })}
          options={[
            { value: "", label: t("filters.all") },
            ...types.map((ty) => ({ value: ty, label: tx(`types.${ty}`) })),
          ]}
        />
      </form>
      <div className="flex max-w-4xl flex-col gap-4">
        {error ? <Alert>{errorText(te, error)}</Alert> : null}
        {!data && !error ? <p className="text-muted">{t("loading")}</p> : null}
        {empty ? (
          <div className="rounded-field border border-dashed border-line p-6">
            <p className="font-semibold">{filtered ? t("empty.filtered") : t("empty.title")}</p>
            <p className="text-muted">{filtered ? t("empty.filteredHint") : t("empty.body")}</p>
          </div>
        ) : null}
        {onlyNotices
          ? null
          : items.map((it) => (
              <ApprovalCard
                key={it.id}
                item={it}
                highlighted={it.id === request}
                onClosed={(id, c) => setClosed((all) => ({ ...all, [id]: c }))}
              />
            ))}
        {blocking.map((b) => (
          <BlockingCard key={`b${b.id}`} item={b} highlighted={b.id === notice} />
        ))}
      </div>
    </section>
  );
}

function matches(it: ApprovalItem, f: ApprovalFilters): boolean {
  return (
    (!f.workspace || it.workspace === f.workspace) &&
    (!f.table || it.table === f.table) &&
    (!f.type || it.type === f.type)
  );
}
