import { useInfiniteQuery } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useFormatDateTime, useMe } from "../../app/session";
import { Alert, Button, Field } from "../../components/ui";
import { errorText } from "../../lib/errors";
import { type AuditFilters, auditFetch, NativeSelect, noAuditFilters, PageHeader } from "./kit";

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
];

export function Audit() {
  const { t } = useTranslation("audit");
  const { t: te } = useTranslation("errors");
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
        {q.error ? <Alert>{errorText(te, q.error)}</Alert> : null}
        {q.isPending ? <p className="text-muted">{t("loading")}</p> : null}
        {q.data && entries.length === 0 ? <p className="text-muted">{t("empty")}</p> : null}
        <ul className="flex flex-col gap-2">
          {entries.map((e) => {
            const label = actionLabel(say, e.action);
            return (
              <li key={e.id} className="rounded-field border border-line bg-surface p-3">
                <div className="flex flex-wrap items-baseline gap-x-4 gap-y-1">
                  <time dateTime={e.at} className="text-sm text-muted">
                    {fmt(e.at)}
                  </time>
                  <span className="font-semibold">{label}</span>
                  <span className="text-sm">{e.user_name || t("system")}</span>
                  {e.workspace ? (
                    <span className="rounded-full bg-accent-wash px-2 text-sm">{e.workspace}</span>
                  ) : null}
                </div>
                {Object.keys(e.detail).length > 0 ? (
                  <details className="mt-1 text-sm">
                    <summary className="cursor-pointer text-muted">
                      <span className="sr-only">{t("detailsNamed", { action: label })}</span>
                      <span aria-hidden="true">{t("details")}</span>
                    </summary>
                    <pre className="mt-1 overflow-x-auto rounded-field bg-paper p-2 text-sm">
                      {JSON.stringify(e.detail, null, 2)}
                    </pre>
                  </details>
                ) : null}
              </li>
            );
          })}
        </ul>
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

/** The name of an action in the language of the panel; the ones it does not know show the code itself. */
function actionLabel(say: Say, action: string): string {
  const k = `actions.${action.replaceAll(".", "_")}`;
  const text = say(k);
  return text === k ? action : text;
}
