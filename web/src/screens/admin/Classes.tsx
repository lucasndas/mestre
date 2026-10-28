import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useChatControl } from "../../app/chatControl";
import { useCan, useFormatDateTime } from "../../app/session";
import { SensitiveButton } from "../../components/PinField";
import { Alert, Button } from "../../components/ui";
import type { ClassRow, ProposalRow } from "../../gen/panel";
import { api } from "../../lib/api";
import {
  Badge,
  EmptyState,
  LoadError,
  linkButton,
  PageHeader,
  StatusStrip,
  VocabLine,
} from "./kit";
import { classesQuery, labelOr, type Say } from "./manage";

/** The tone of the label of an App state ("em criação", "pronto", "com erro"). */
export function appTone(state: string): "plain" | "ok" | "warn" | "danger" {
  if (state === "pronto") return "ok";
  if (state === "com erro" || state === "link vencido") return "danger";
  return "warn";
}

/** The class has an App that is not ready: without it the class cannot open PRs. */
const appWaits = (c: ClassRow) => c.app !== "" && c.app_state !== "" && c.app_state !== "pronto";

/**
 * The classes screen: where the classes stand, the proposals that wait for an Administrator, and
 * the classes in force by scope with the App of each.
 */
export function Classes() {
  const { t } = useTranslation("classes");
  const chat = useChatControl();
  const { data, error, refetch } = useQuery(classesQuery);
  const [notice, setNotice] = useState<string | null>(null);

  return (
    <section>
      <PageHeader title={t("title")} intro={t("intro")} />
      <VocabLine terms={["Classe", "App do GitHub"]} />
      {error ? <LoadError what={t("what")} error={error} onRetry={() => void refetch()} /> : null}
      {!data && !error ? <p className="text-muted">{t("loading")}</p> : null}
      {data && !error ? (
        <div className="flex max-w-4xl flex-col gap-6">
          {data.proposals.length > 0 ? (
            <StatusStrip
              tone="warn"
              title={
                data.proposals.length === 1
                  ? t("strip.proposalsOne")
                  : t("strip.proposals", { count: data.proposals.length })
              }
              hint={t("strip.proposalsHint")}
            />
          ) : data.classes.length > 0 ? (
            <StatusStrip
              tone={data.classes.some(appWaits) ? "warn" : "ok"}
              title={
                data.classes.length === 1
                  ? t("strip.inForceOne")
                  : t("strip.inForce", { count: data.classes.length })
              }
              hint={
                data.classes.some(appWaits)
                  ? t("strip.inForceApp", { count: data.classes.filter(appWaits).length })
                  : t("strip.inForceHint")
              }
            />
          ) : (
            <StatusStrip tone="warn" title={t("strip.none")} hint={t("strip.noneHint")}>
              <Button onPress={chat.show}>{t("strip.talk")}</Button>
            </StatusStrip>
          )}
          {data.apps.near ? <AppsBand total={data.apps.total} limit={data.apps.limit} /> : null}
          {notice ? <Alert tone="ok">{notice}</Alert> : null}
          {data.proposals.length > 0 ? (
            <Proposals proposals={data.proposals} onApproved={setNotice} />
          ) : null}
          {data.classes.length > 0 ? (
            <ClassList classes={data.classes} />
          ) : (
            <EmptyState
              title={t("empty.title")}
              why={t("empty.why")}
              steps={[t("empty.step1"), t("empty.step2"), t("empty.step3")]}
            />
          )}
        </div>
      ) : null}
    </section>
  );
}

function AppsBand({ total, limit }: { total: number; limit: number }) {
  const { t } = useTranslation("classes");
  const canApps = useCan("approve_workspace_cls");
  return (
    <div
      role="status"
      className="flex max-w-4xl flex-wrap items-center justify-between gap-2 rounded-field border border-warn bg-warn-wash px-4 py-2 font-semibold"
    >
      <span>{t("band", { total, limit })}</span>
      {canApps ? (
        <Link to="/apps" className={linkButton}>
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
          <p className="mt-1 text-muted">{t("proposals.howToFix")}</p>
        </div>
      ) : (
        <div className="mt-3">
          <SensitiveButton
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
      )}
    </li>
  );
}

/** The classes in cards, grouped by where they hold: the whole Guild, each campaign, the tables. */
function ClassList({ classes }: { classes: ClassRow[] }) {
  const { t } = useTranslation("classes");
  const groups: { key: string; title: string; rows: ClassRow[] }[] = [];
  const add = (key: string, title: string, c: ClassRow) => {
    const g = groups.find((x) => x.key === key);
    if (g) g.rows.push(c);
    else groups.push({ key, title, rows: [c] });
  };
  const order = { global: 0, workspace: 1, table: 2 } as Record<string, number>;
  for (const c of [...classes].sort((a, b) => (order[a.scope] ?? 3) - (order[b.scope] ?? 3))) {
    if (c.scope === "workspace")
      add(`ws/${c.workspace}`, t("groups.workspace", { where: c.workspace }), c);
    else if (c.scope === "table") add("table", t("groups.table"), c);
    else add("global", t("groups.global"), c);
  }
  return (
    <div className="flex flex-col gap-6">
      {groups.map((g) => (
        <section key={g.key} aria-label={g.title} className="flex flex-col gap-3">
          <h2 className="text-2xl">{g.title}</h2>
          <ul className="flex flex-col gap-3">
            {g.rows.map((c) => (
              <ClassItem key={`${c.scope}/${c.workspace}/${c.table}/${c.name}`} c={c} />
            ))}
          </ul>
        </section>
      ))}
    </div>
  );
}

function ClassItem({ c }: { c: ClassRow }) {
  const { t } = useTranslation("classes");
  const say = t as unknown as Say;
  const notReady = appWaits(c);
  return (
    <li className="rounded-field border border-line bg-surface p-4">
      <p className="flex flex-wrap items-center gap-2 text-lg font-semibold">
        <span>{c.name}</span>
        <Badge>{labelOr(say, `states.${c.state}`, c.state)}</Badge>
        {c.app ? (
          <Badge tone={appTone(c.app_state)}>
            {c.app_state === "pronto"
              ? t("list.appReady", { name: c.app })
              : labelOr(say, `list.appState.${c.app_state}`, t("list.appPending"))}
          </Badge>
        ) : (
          <Badge tone="warn">{t("list.noApp")}</Badge>
        )}
        <span className="text-sm font-normal text-muted">
          {t("list.version", { version: c.version })}
        </span>
      </p>
      <p className="mt-1 text-sm text-muted">
        {t("list.appliesTo", {
          where:
            c.scope === "global"
              ? t("proposals.global")
              : c.table
                ? `${c.workspace} / ${c.table}`
                : c.workspace,
        })}
      </p>
      {c.problems.length > 0 ? (
        <p className="mt-1 text-sm text-danger">{c.problems.join("; ")}</p>
      ) : null}
      {notReady ? (
        <p className="mt-2 text-sm">
          {t("list.appNotReady")}{" "}
          <Link to="/apps" className={linkButton}>
            {t("list.appLink")}
          </Link>
        </p>
      ) : null}
    </li>
  );
}
