import { useInfiniteQuery } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useFormatDateTime, useMe } from "../app/session";
import { useDynamicT } from "../components/ApprovalCard";
import { SelectField } from "../components/SelectField";
import { Alert, Button, Field } from "../components/ui";
import type { ApprovalHistory } from "../gen/panel";
import { api } from "../lib/api";
import { type HistoryFilters, historyPath } from "../lib/approvalQueries";
import { errorText } from "../lib/errors";
import { ApprovalTabs } from "./Approvals";

const STATES = ["approved", "denied", "expired", "invalidated", "cancelled"];
const LEVELS = ["agent", "mesa", "grand_master", "people"];
const RISKS = ["baixo", "medio", "alto", "fixo"];

/**
 * The history: the requests that ended, read from the audit of the flow, with their
 * filters (docs/painel/conversa-e-aprovacoes.md#central-de-aprovações). It has what the
 * agents decided alone.
 */
export function ApprovalHistoryScreen() {
  const t = useDynamicT("approvals");
  const { t: te } = useTranslation("errors");
  const fmt = useFormatDateTime();
  const me = useMe();
  const [f, setF] = useState<HistoryFilters>({
    state: "",
    by: "",
    level: "",
    risk: "",
    workspace: "",
    from: "",
    to: "",
  });
  const q = useInfiniteQuery({
    queryKey: ["approvals", "history", f],
    queryFn: ({ pageParam }) => api<ApprovalHistory>("GET", historyPath(f, pageParam || undefined)),
    initialPageParam: 0,
    getNextPageParam: (last) => (last.next > 0 ? last.next : undefined),
  });
  const items = q.data?.pages.flatMap((p) => p.items) ?? [];
  const people = [
    ...new Map(
      items.filter((i) => i.by_user && i.by_id).map((i) => [i.by_id as string, i.by]),
    ).entries(),
  ];
  const any = { value: "", label: t("history.filters.any") };
  return (
    <section>
      <h1 className="text-3xl">{t("history.title")}</h1>
      <p className="mt-1 mb-5 max-w-2xl text-muted">{t("history.intro")}</p>
      <ApprovalTabs current="history" />
      <form
        aria-label={t("filters.label")}
        className="mb-6 grid max-w-4xl gap-3 sm:grid-cols-3"
        onSubmit={(e) => e.preventDefault()}
      >
        <SelectField
          label={t("history.filters.state")}
          value={f.state}
          onChange={(state) => setF({ ...f, state })}
          options={[any, ...STATES.map((s) => ({ value: s, label: t(`history.states.${s}`) }))]}
        />
        <SelectField
          label={t("history.filters.by")}
          value={f.by}
          onChange={(by) => setF({ ...f, by })}
          options={[
            any,
            { value: "agent", label: t("history.filters.agents") },
            ...people.map(([id, name]) => ({ value: id, label: name })),
          ]}
        />
        <SelectField
          label={t("history.filters.level")}
          value={f.level}
          onChange={(level) => setF({ ...f, level })}
          options={[any, ...LEVELS.map((s) => ({ value: s, label: t(`history.levels.${s}`) }))]}
        />
        <SelectField
          label={t("history.filters.risk")}
          value={f.risk}
          onChange={(risk) => setF({ ...f, risk })}
          options={[any, ...RISKS.map((s) => ({ value: s, label: t(`risk.${s}`) }))]}
        />
        <Field
          label={t("history.filters.from")}
          type="date"
          value={f.from}
          onChange={(from) => setF({ ...f, from })}
        />
        <Field
          label={t("history.filters.to")}
          type="date"
          value={f.to}
          onChange={(to) => setF({ ...f, to })}
        />
      </form>
      <div className="flex max-w-4xl flex-col gap-3">
        {q.error ? <Alert>{errorText(te, q.error)}</Alert> : null}
        {!q.data && !q.error ? <p className="text-muted">{t("loading")}</p> : null}
        {q.data && items.length === 0 ? <p className="text-muted">{t("history.empty")}</p> : null}
        <ul className="flex flex-col gap-3">
          {items.map((i) => (
            <li key={i.id} className="rounded-field border border-line bg-surface p-4">
              <p className="font-semibold">
                {t("history.row", { id: i.id, type: t(`types.${i.type}`) })}
                <span className="ml-3 text-sm font-normal text-muted">
                  {i.workspace
                    ? `${i.workspace}${i.table ? ` · ${i.table}` : ""}`
                    : t("item.system")}{" "}
                  · {t(`risk.${i.risk}`)}
                </span>
              </p>
              <p>{i.about}</p>
              <p className="text-muted">
                {i.by
                  ? t("history.decidedBy", {
                      state: t(`history.states.${i.state}`),
                      name: i.by,
                      level: i.level,
                      time: fmt(i.closed_at),
                    })
                  : t("history.decidedByNobody", {
                      state: t(`history.states.${i.state}`),
                      time: fmt(i.closed_at),
                    })}
              </p>
              {i.reason ? (
                <p className="text-sm text-muted">{t("history.reason", { reason: i.reason })}</p>
              ) : null}
            </li>
          ))}
        </ul>
        {q.hasNextPage ? (
          <div>
            <Button
              variant="secondary"
              isDisabled={q.isFetchingNextPage}
              onPress={() => void q.fetchNextPage()}
            >
              {t("history.more")}
            </Button>
          </div>
        ) : null}
        <p className="mt-4 text-sm text-muted">{t("history.zone", { zone: me.timezone })}</p>
      </div>
    </section>
  );
}
