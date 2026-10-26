import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useCan, useFormatDateTime } from "../../app/session";
import { SensitiveButton } from "../../components/PinField";
import { Term } from "../../components/Term";
import { Alert } from "../../components/ui";
import type { ClassRow, ProposalRow } from "../../gen/panel";
import { api } from "../../lib/api";
import { errorText } from "../../lib/errors";
import { Badge, PageHeader } from "./kit";
import { classesQuery, labelOr, type Say } from "./manage";

/** The tone of the label of an App state ("em criação", "pronto", "com erro"). */
export function appTone(state: string): "plain" | "ok" | "warn" | "danger" {
  if (state === "pronto") return "ok";
  if (state === "com erro") return "danger";
  return "warn";
}

/**
 * The classes screen: the warning band of the Apps count (CA-ADM-02), the proposals that wait
 * for an Administrator, and the classes in force with the App of each.
 */
export function Classes() {
  const { t } = useTranslation("classes");
  const { t: te } = useTranslation("errors");
  const { data, error } = useQuery(classesQuery);
  const [notice, setNotice] = useState<string | null>(null);

  return (
    <section>
      <PageHeader title={t("title")} intro={t("intro")} />
      <div className="flex max-w-5xl flex-col gap-6">
        {data?.apps.near ? <AppsBand total={data.apps.total} limit={data.apps.limit} /> : null}
        {notice ? <Alert tone="ok">{notice}</Alert> : null}
        {error ? <Alert>{errorText(te, error)}</Alert> : null}
        {!data && !error ? <p className="text-muted">{t("loading")}</p> : null}
        {data ? (
          <>
            <Proposals proposals={data.proposals} onApproved={setNotice} />
            <ClassList classes={data.classes} />
          </>
        ) : null}
      </div>
    </section>
  );
}

function AppsBand({ total, limit }: { total: number; limit: number }) {
  const { t } = useTranslation("classes");
  const canApps = useCan("approve_workspace_cls");
  return (
    <div
      role="status"
      className="flex flex-wrap items-center justify-between gap-2 rounded-field bg-warn px-4 py-2 font-semibold text-warn-ink"
    >
      <span>{t("band", { total, limit })}</span>
      {canApps ? (
        <Link to="/apps" className="underline underline-offset-2">
          {t("bandLink")}
        </Link>
      ) : null}
    </div>
  );
}

function Proposals({
  proposals,
  onApproved,
}: {
  proposals: ProposalRow[];
  onApproved: (text: string) => void;
}) {
  const { t } = useTranslation("classes");
  return (
    <section aria-labelledby="proposals-title" className="flex flex-col gap-3">
      <h2 id="proposals-title" className="text-2xl">
        {t("proposals.title")}
      </h2>
      {proposals.length === 0 ? <p className="text-muted">{t("proposals.empty")}</p> : null}
      <ul className="flex flex-col gap-3">
        {proposals.map((p) => (
          <ProposalItem key={p.id} p={p} onApproved={onApproved} />
        ))}
      </ul>
    </section>
  );
}

function ProposalItem({ p, onApproved }: { p: ProposalRow; onApproved: (text: string) => void }) {
  const { t } = useTranslation("classes");
  const say = t as unknown as Say;
  const fmt = useFormatDateTime();
  const qc = useQueryClient();
  const blocked = p.problems.length > 0;
  return (
    <li className="rounded-field border border-line bg-surface p-4">
      <p className="flex flex-wrap items-center gap-2 font-semibold">
        {p.name}
        <Badge>{labelOr(say, `scopes.${p.scope}`, p.scope)}</Badge>
        <Badge>{labelOr(say, `proposals.origin.${p.origin}`, p.origin)}</Badge>
        {p.tested ? null : <Badge tone="warn">{t("proposals.untested")}</Badge>}
      </p>
      <p className="mt-1 text-sm text-muted">
        {p.workspace ? p.workspace : t("proposals.global")}
        {p.table ? ` / ${p.table}` : ""} · {t("proposals.by", { name: p.by })} · {fmt(p.created_at)}
      </p>
      {blocked ? (
        <div className="mt-3 text-sm">
          <p className="font-semibold">{t("proposals.problems")}</p>
          <ul className="list-disc pl-6">
            {p.problems.map((x) => (
              <li key={x}>{x}</li>
            ))}
          </ul>
        </div>
      ) : null}
      <div className="mt-3">
        <SensitiveButton
          isDisabled={blocked}
          onConfirm={async (pin) => {
            try {
              await api(
                "POST",
                `/api/classes/proposals/${p.id}/approve`,
                { workspace: p.workspace, hash: p.hash },
                { pin },
              );
            } catch (err) {
              // a proposal that changed or went away: show what is there now
              await qc.invalidateQueries({ queryKey: ["admin", "classes"] });
              throw err;
            }
            onApproved(t("proposals.approved", { name: p.name }));
            await qc.invalidateQueries({ queryKey: ["admin"] });
          }}
        >
          {t("proposals.approve", { name: p.name })}
        </SensitiveButton>
      </div>
    </li>
  );
}

function ClassList({ classes }: { classes: ClassRow[] }) {
  const { t } = useTranslation("classes");
  const say = t as unknown as Say;
  return (
    <section aria-labelledby="classes-title" className="flex flex-col gap-3">
      <h2 id="classes-title" className="text-2xl">
        {t("list.title")}
      </h2>
      <p className="text-sm text-muted">
        <Term name="Classe" />
      </p>
      {classes.length === 0 ? <p className="text-muted">{t("list.empty")}</p> : null}
      {classes.length > 0 ? (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[40rem] border-collapse text-left">
            <caption className="sr-only">{t("list.caption")}</caption>
            <thead>
              <tr className="border-b border-line text-sm text-muted">
                <th scope="col" className="py-2 pr-4">
                  {t("list.name")}
                </th>
                <th scope="col" className="py-2 pr-4">
                  {t("list.scope")}
                </th>
                <th scope="col" className="py-2 pr-4">
                  {t("list.where")}
                </th>
                <th scope="col" className="py-2 pr-4">
                  {t("list.state")}
                </th>
                <th scope="col" className="py-2 pr-4">
                  {t("list.app")}
                </th>
                <th scope="col" className="py-2">
                  {t("list.version")}
                </th>
              </tr>
            </thead>
            <tbody>
              {classes.map((c) => (
                <tr
                  key={`${c.scope}/${c.workspace}/${c.table}/${c.name}`}
                  className="border-b border-line align-top"
                >
                  <th scope="row" className="py-2 pr-4 font-semibold">
                    {c.name}
                    {c.problems.length > 0 ? (
                      <span className="block text-sm font-normal text-danger">
                        {c.problems.join("; ")}
                      </span>
                    ) : null}
                  </th>
                  <td className="py-2 pr-4">{labelOr(say, `scopes.${c.scope}`, c.scope)}</td>
                  <td className="py-2 pr-4">
                    {c.workspace
                      ? c.table
                        ? `${c.workspace} / ${c.table}`
                        : c.workspace
                      : t("list.everywhere")}
                  </td>
                  <td className="py-2 pr-4">{labelOr(say, `states.${c.state}`, c.state)}</td>
                  <td className="py-2 pr-4">
                    {c.app ? (
                      <span className="flex flex-wrap items-center gap-2">
                        {c.app}
                        {c.app_state ? (
                          <Badge tone={appTone(c.app_state)}>{c.app_state}</Badge>
                        ) : null}
                      </span>
                    ) : (
                      <span className="text-muted">{t("list.noApp")}</span>
                    )}
                  </td>
                  <td className="py-2">{c.version}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : null}
    </section>
  );
}
