import { useInfiniteQuery } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useFormatDateTime, useMe } from "../../app/session";
import { Button, Field } from "../../components/ui";
import {
  type AuditFilters,
  auditFetch,
  EmptyState,
  LoadError,
  NativeSelect,
  noAuditFilters,
  PageHeader,
  StatusStrip,
  VocabLine,
} from "./kit";

type Say = (k: string, o?: object) => string;

// what the person picks is the beginning of the name of the action
const CATEGORIES: [key: string, prefix: string][] = [
  ["users", "panel.user."],
  ["secrets", "panel.secret."],
  ["recovery", "panel.recovery_key."],
  ["github", "panel.github."],
  ["transfer", "panel.transfer."],
  ["login", "panel.login"],
  ["pin", "panel.pin."],
  ["decrees", "panel.decree."],
  ["delegations", "panel.subdelegation."],
  ["chain", "panel.chain."],
  ["workspaceRules", "panel.ceiling."],
  ["workspaces", "panel.workspace."],
  ["guild", "panel.guild."],
];

export function Audit() {
  const { t } = useTranslation("audit");
  const say = t as unknown as Say;
  const fmt = useFormatDateTime();
  const { timezone } = useMe();
  const [draft, setDraft] = useState<AuditFilters>(noAuditFilters);
  const [applied, setApplied] = useState<AuditFilters>(noAuditFilters);
  const set = (k: keyof AuditFilters) => (v: string) => setDraft((d) => ({ ...d, [k]: v }));
  const q = useInfiniteQuery({
    queryKey: ["admin", "audit", applied],
    queryFn: ({ pageParam }) => auditFetch(applied, pageParam),
    initialPageParam: "",
    getNextPageParam: (last) => last.next || undefined,
  });
  const first = q.data?.pages[0];
  const entries = q.data?.pages.flatMap((p) => p.entries) ?? [];

  return (
    <section>
      <PageHeader title={t("title")} intro={t("intro")} />
      <VocabLine terms={["Campanha"]} />
      {q.data ? (
        <StatusStrip
          title={t("strip", { count: entries.length })}
          hint={q.hasNextPage ? t("stripMore") : t("stripAll")}
        />
      ) : null}
      <form
        aria-label={t("filters.title")}
        className="mb-6 grid max-w-4xl gap-4 sm:grid-cols-2 lg:grid-cols-3"
        onSubmit={(e) => {
          e.preventDefault();
          setApplied(draft);
        }}
      >
        <NativeSelect
          label={t("filters.who")}
          value={draft.user}
          onChange={set("user")}
          options={[
            { value: "", label: t("filters.anyone") },
            ...(first?.people ?? []).map((p) => ({ value: p.id, label: p.name })),
          ]}
        />
        <NativeSelect
          label={t("filters.what")}
          value={draft.action}
          onChange={set("action")}
          options={[
            { value: "", label: t("filters.anything") },
            ...CATEGORIES.map(([k, prefix]) => ({ value: prefix, label: say(`categories.${k}`) })),
          ]}
        />
        <NativeSelect
          label={t("filters.workspace")}
          value={draft.workspace}
          onChange={set("workspace")}
          options={[
            { value: "", label: t("filters.anyWorkspace") },
            ...(first?.workspaces ?? []).map((w) => ({ value: w, label: w })),
          ]}
        />
        <Field label={t("filters.from")} type="date" value={draft.from} onChange={set("from")} />
        <Field label={t("filters.to")} type="date" value={draft.to} onChange={set("to")} />
        <div className="flex items-end gap-2">
          <Button type="submit">{t("filters.apply")}</Button>
          <Button
            variant="quiet"
            onPress={() => {
              setDraft(noAuditFilters);
              setApplied(noAuditFilters);
            }}
          >
            {t("filters.clear")}
          </Button>
        </div>
      </form>
      <div className="flex max-w-4xl flex-col gap-3">
        {q.error ? (
          <LoadError what={t("loadWhat")} error={q.error} onRetry={() => void q.refetch()} />
        ) : null}
        {q.isPending ? <p className="text-muted">{t("loading")}</p> : null}
        {q.data && entries.length === 0 ? (
          <EmptyState title={t("emptyTitle")} why={t("empty")}>
            <Button
              variant="secondary"
              onPress={() => {
                setDraft(noAuditFilters);
                setApplied(noAuditFilters);
              }}
            >
              {t("filters.clear")}
            </Button>
          </EmptyState>
        ) : null}
        {entries.length > 0 ? (
          <div className="overflow-x-auto rounded-field border border-line bg-surface">
            <table className="w-full text-left">
              <thead className="text-sm text-muted">
                <tr className="border-b border-line">
                  <th scope="col" className="px-3 py-2 font-semibold">
                    {t("columns.when")}
                  </th>
                  <th scope="col" className="px-3 py-2 font-semibold">
                    {t("columns.who")}
                  </th>
                  <th scope="col" className="px-3 py-2 font-semibold">
                    {t("columns.changed")}
                  </th>
                </tr>
              </thead>
              <tbody>
                {entries.map((e) => {
                  const label = actionLabel(say, e.action);
                  return (
                    <tr key={e.id} className="border-b border-line align-top last:border-0">
                      <td className="px-3 py-2 text-sm whitespace-nowrap text-muted">
                        <time dateTime={e.at}>{fmt(e.at)}</time>
                      </td>
                      <td className="px-3 py-2">{e.user_name || t("system")}</td>
                      <td className="px-3 py-2">
                        <span className="font-semibold">{label.text}</span>
                        {label.code ? (
                          <span className="ml-2 font-mono text-sm text-muted">{label.code}</span>
                        ) : null}
                        {e.workspace ? (
                          <span className="ml-2 rounded-full bg-accent-wash px-2 text-sm">
                            {e.workspace}
                          </span>
                        ) : null}
                        {Object.keys(e.detail).length > 0 ? (
                          <details className="mt-1 text-sm">
                            <summary className="min-h-11 cursor-pointer py-2 text-muted">
                              <span className="sr-only">
                                {t("detailsNamed", { action: label.text })}
                              </span>
                              <span aria-hidden="true">{t("details")}</span>
                            </summary>
                            <pre className="mt-1 overflow-x-auto rounded-field bg-paper p-2 text-sm">
                              {JSON.stringify(e.detail, null, 2)}
                            </pre>
                          </details>
                        ) : null}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        ) : null}
        {q.hasNextPage ? (
          <div>
            <Button
              variant="secondary"
              isDisabled={q.isFetchingNextPage}
              onPress={() => void q.fetchNextPage()}
            >
              {t("more")}
            </Button>
          </div>
        ) : null}
        <p className="text-sm text-muted">{t("zone", { zone: timezone })}</p>
      </div>
    </section>
  );
}

/**
 * The name of an action in the language of the panel. One the panel does not know yet is
 * named in general terms and keeps its code beside it, so nothing is hidden.
 */
function actionLabel(say: Say, action: string): { text: string; code?: string } {
  const k = `actions.${action.replaceAll(".", "_")}`;
  const text = say(k);
  return text === k ? { text: say("unknownAction"), code: action } : { text };
}
