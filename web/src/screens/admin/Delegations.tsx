import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useFormatDateTime } from "../../app/session";
import { Alert, Button } from "../../components/ui";
import type {
  DecreeProposal,
  DelegationRow,
  RevokeAllResult,
  RuleRow,
  SubRow,
} from "../../gen/panel";
import { api } from "../../lib/api";
import { errorText } from "../../lib/errors";
import {
  Badge,
  EmptyState,
  LevelGate,
  LoadError,
  linkButton,
  PageHeader,
  StatusStrip,
  VocabLine,
} from "./kit";
import { ConfirmButton, delegationsQuery, ProposalCard, useWords } from "./rulesKit";

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <>
      <dt className="text-muted">{label}</dt>
      <dd>{children}</dd>
    </>
  );
}

export function Delegations() {
  const { t } = useTranslation("rules");
  const qc = useQueryClient();
  const fmt = useFormatDateTime();
  const { data, error, refetch } = useQuery(delegationsQuery);
  const [notice, setNotice] = useState<string | null>(null);
  const [renewing, setRenewing] = useState<DecreeProposal | null>(null);
  const refresh = () => qc.invalidateQueries({ queryKey: ["admin"] });
  const total = (data?.workspaces ?? []).reduce(
    (n, w) => n + w.delegations.length + w.subdelegations.length + w.rules.length,
    0,
  );

  const ends = (data?.workspaces ?? []).flatMap((w) => [
    ...w.delegations.map((d) => d.until),
    ...w.subdelegations.map((x) => x.until),
    ...w.rules.map((x) => x.until),
  ]);
  const soonest = ends.filter(Boolean).sort()[0];

  return (
    <section>
      <PageHeader title={t("delegations.title")} intro={t("delegations.intro")} />
      <VocabLine terms={["Delegação", "Regra temporária de mesa", "Grão-Mestre"]} />
      {data ? (
        <StatusStrip
          tone={total > 0 ? "ok" : "neutral"}
          title={t("delegations.strip", { count: total })}
          hint={
            soonest
              ? t("delegations.stripSoonest", { when: fmt(soonest) })
              : total === 0
                ? t("delegations.stripNone")
                : undefined
          }
        >
          <Link to="/decretos" className={linkButton}>
            {t("delegations.toDecrees")}
          </Link>
        </StatusStrip>
      ) : null}
      <div className="flex max-w-4xl flex-col gap-5">
        {notice ? <Alert tone="ok">{notice}</Alert> : null}
        {error ? (
          <LoadError
            what={t("loadWhat.delegations")}
            error={error}
            onRetry={() => void refetch()}
          />
        ) : null}
        {renewing ? (
          <ProposalCard
            proposal={renewing}
            onCancel={() => setRenewing(null)}
            onConfirmed={async () => {
              setRenewing(null);
              setNotice(t("delegations.renewed"));
              await refresh();
            }}
          />
        ) : null}
        {!data && !error ? <p className="text-muted">{t("loading")}</p> : null}
        {data && (data.workspaces.length === 0 || total === 0) ? (
          <EmptyState title={t("delegations.emptyTitle")} why={t("delegations.empty")} />
        ) : null}
        {data?.workspaces
          .filter((g) => g.delegations.length + g.subdelegations.length + g.rules.length > 0)
          .map((g) => (
            <section
              key={g.workspace}
              aria-labelledby={`dl-${g.workspace}`}
              className="flex flex-col gap-4"
            >
              <h2 id={`dl-${g.workspace}`} className="text-2xl">
                {g.workspace}
              </h2>
              <Group title={t("delegations.delegations")} items={g.delegations.length}>
                {g.delegations.map((d) => (
                  <DelegationCard
                    key={d.number}
                    d={d}
                    canRenew={data.can_revoke_all}
                    onRenew={async () => {
                      setNotice(null);
                      setRenewing(
                        await api<DecreeProposal>("POST", `/api/decrees/${d.number}/renew`),
                      );
                    }}
                    onRevoked={async () => {
                      setNotice(t("delegations.revoked"));
                      await refresh();
                    }}
                  />
                ))}
              </Group>
              <Group title={t("delegations.subdelegations")} items={g.subdelegations.length}>
                {g.subdelegations.map((s) => (
                  <SubCard
                    key={s.id}
                    s={s}
                    canRevoke={data.can_revoke}
                    onRevoked={async () => {
                      setNotice(t("delegations.revoked"));
                      await refresh();
                    }}
                  />
                ))}
              </Group>
              <Group title={t("delegations.rules")} items={g.rules.length}>
                {g.rules.map((r) => (
                  <RuleCard
                    key={r.id}
                    r={r}
                    canRevoke={data.can_revoke}
                    onRevoked={async () => {
                      setNotice(t("delegations.revoked"));
                      await refresh();
                    }}
                  />
                ))}
              </Group>
            </section>
          ))}
        {data?.can_revoke_all ? (
          <section
            aria-labelledby="dl-emergency"
            className="max-w-4xl rounded-field border border-line bg-surface p-4"
          >
            <h2 id="dl-emergency" className="mb-1 text-xl">
              {t("delegations.emergency")}
            </h2>
            <p className="mb-3 text-sm">{t("delegations.revokeAllHint")}</p>
            <ConfirmButton
              warning={t("delegations.revokeAllWarning", { count: total })}
              confirmLabel={t("delegations.revokeAllConfirm")}
              onConfirm={async () => {
                const r = await api<RevokeAllResult>("POST", "/api/delegations/revoke-all");
                setNotice(
                  t("delegations.revokedAll", {
                    delegations: r.delegations,
                    subdelegations: r.subdelegations,
                    rules: r.rules,
                  }),
                );
                await refresh();
              }}
            >
              {t("delegations.revokeAll")}
            </ConfirmButton>
          </section>
        ) : null}
      </div>
    </section>
  );
}

function Group({
  title,
  items,
  children,
}: {
  title: string;
  items: number;
  children: React.ReactNode;
}) {
  if (items === 0) return null;
  return (
    <div>
      <h3 className="mb-2 text-lg">{title}</h3>
      <ul className="flex flex-col gap-3">{children}</ul>
    </div>
  );
}

function DelegationCard({
  d,
  canRenew,
  onRenew,
  onRevoked,
}: {
  d: DelegationRow;
  canRenew: boolean;
  onRenew: () => Promise<void>;
  onRevoked: () => Promise<void>;
}) {
  const { t } = useTranslation("rules");
  const { t: te } = useTranslation("errors");
  const fmt = useFormatDateTime();
  const words = useWords();
  const [error, setError] = useState<unknown>(null);
  return (
    <li className="rounded-field border border-line bg-surface p-4">
      <p className="flex flex-wrap items-center gap-2 font-semibold">
        {t("decrees.number", { number: d.number })}
        <Badge>{words("types", d.type)}</Badge>
        {d.state === "suspended" ? (
          <Badge tone="warn">{t("decrees.states.suspended")}</Badge>
        ) : null}
      </p>
      <p className="mt-1">{d.summary}</p>
      <dl className="mt-3 grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-sm">
        <Row label={t("delegations.scope")}>
          {[d.mesa, d.scope].filter(Boolean).join(" · ") || t("decrees.wholeWorkspace")}
        </Row>
        <Row label={t("decrees.validity")}>
          {d.until ? t("decrees.until", { when: fmt(d.until) }) : t("decrees.permanent")}
        </Row>
        <Row label={t("decrees.author")}>{d.author_name}</Row>
        <Row label={t("delegations.decisions")}>{d.decisions}</Row>
      </dl>
      {error ? <Alert>{errorText(te, error)}</Alert> : null}
      <div className="mt-3 flex flex-wrap items-start gap-3">
        <LevelGate need="owner" label={t("delegations.renew")}>
          {canRenew ? (
            <Button
              variant="secondary"
              onPress={async () => {
                setError(null);
                try {
                  await onRenew();
                } catch (err) {
                  setError(err);
                }
              }}
            >
              {t("delegations.renew")}
            </Button>
          ) : null}
        </LevelGate>
        {d.can_revoke ? (
          <ConfirmButton
            warning={t("delegations.revokeOneWarning")}
            confirmLabel={t("decrees.revokeConfirm")}
            onConfirm={async () => {
              await api("POST", `/api/decrees/${d.number}/revoke`);
              await onRevoked();
            }}
          >
            {t("decrees.revoke")}
          </ConfirmButton>
        ) : null}
      </div>
    </li>
  );
}

function SubCard({
  s,
  canRevoke,
  onRevoked,
}: {
  s: SubRow;
  canRevoke: boolean;
  onRevoked: () => Promise<void>;
}) {
  const { t } = useTranslation("rules");
  const words = useWords();
  const fmt = useFormatDateTime();
  return (
    <li className="rounded-field border border-line bg-surface p-4">
      <p className="flex flex-wrap items-center gap-2 font-semibold">
        {t("delegations.subNumber", { id: s.id })}
        <Badge>{words("types", s.type)}</Badge>
      </p>
      <dl className="mt-3 grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-sm">
        <Row label={t("delegations.table")}>{s.mesa}</Row>
        <Row label={t("delegations.grantee")}>{s.grantee}</Row>
        <Row label={t("decrees.validity")}>{t("decrees.until", { when: fmt(s.until) })}</Row>
        <Row label={t("delegations.origin")}>
          {s.origin ? t("decrees.number", { number: s.origin }) : t("delegations.fromCeiling")}
        </Row>
        {s.reason ? <Row label={t("delegations.reason")}>{s.reason}</Row> : null}
        <Row label={t("delegations.decisions")}>{s.decisions}</Row>
      </dl>
      <div className="mt-3">
        <LevelGate need="administrator" label={t("decrees.revoke")}>
          {canRevoke ? (
            <ConfirmButton
              warning={t("delegations.revokeOneWarning")}
              confirmLabel={t("decrees.revokeConfirm")}
              onConfirm={async () => {
                await api("POST", `/api/subdelegations/${s.id}/revoke`);
                await onRevoked();
              }}
            >
              {t("decrees.revoke")}
            </ConfirmButton>
          ) : null}
        </LevelGate>
      </div>
    </li>
  );
}

function RuleCard({
  r,
  canRevoke,
  onRevoked,
}: {
  r: RuleRow;
  canRevoke: boolean;
  onRevoked: () => Promise<void>;
}) {
  const { t } = useTranslation("rules");
  const words = useWords();
  const fmt = useFormatDateTime();
  return (
    <li className="rounded-field border border-line bg-surface p-4">
      <p className="flex flex-wrap items-center gap-2 font-semibold">
        {t("delegations.ruleNumber", { id: r.id })}
        <Badge>{words("risks", r.risk)}</Badge>
        {r.state === "suspended" ? (
          <Badge tone="warn">{t("decrees.states.suspended")}</Badge>
        ) : null}
      </p>
      <p className="mt-1 font-mono text-sm">{r.covers}</p>
      <dl className="mt-3 grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-sm">
        <Row label={t("delegations.table")}>{r.table}</Row>
        <Row label={t("delegations.class")}>{r.class}</Row>
        <Row label={t("decrees.author")}>{r.author}</Row>
        <Row label={t("decrees.validity")}>
          {r.until
            ? t("decrees.until", { when: fmt(r.until) })
            : r.mission
              ? t("delegations.untilMission", { mission: r.mission })
              : t("decrees.permanent")}
        </Row>
        <Row label={t("delegations.decisions")}>{r.decisions}</Row>
      </dl>
      <div className="mt-3">
        <LevelGate need="administrator" label={t("decrees.revoke")}>
          {canRevoke ? (
            <ConfirmButton
              warning={t("delegations.revokeOneWarning")}
              confirmLabel={t("decrees.revokeConfirm")}
              onConfirm={async () => {
                await api("POST", `/api/table-rules/${r.id}/revoke`);
                await onRevoked();
              }}
            >
              {t("decrees.revoke")}
            </ConfirmButton>
          ) : null}
        </LevelGate>
      </div>
    </li>
  );
}
