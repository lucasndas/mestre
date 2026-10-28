import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useCan, useFormatDateTime } from "../../app/session";
import { SensitiveButton } from "../../components/PinField";
import { Alert, Button, Field } from "../../components/ui";
import type { AppCount, AppLinkResponse, AppRequestRow, AppRow, AppsPage } from "../../gen/panel";
import { api } from "../../lib/api";
import { errorText } from "../../lib/errors";
import { appTone } from "./Classes";
import {
  Badge,
  EmptyState,
  LevelGate,
  LoadError,
  PageHeader,
  ShownOnce,
  StatusStrip,
  VocabLine,
} from "./kit";
import { appsQuery, labelOr, type Say } from "./manage";

/** The Apps screen: where the Apps stand, the next step of each and (the Guildmaster) the creation links. */
export function GitHubApps() {
  const { t } = useTranslation("apps");
  const fmt = useFormatDateTime();
  const owner = useCan("github_apps");
  const qc = useQueryClient();
  const { data, error, refetch } = useQuery(appsQuery);
  const [link, setLink] = useState<AppLinkResponse | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const refresh = () => qc.invalidateQueries({ queryKey: ["admin"] });

  const onLink = async (name: string, pin: string) => {
    const r = await api<AppLinkResponse>(
      "POST",
      `/api/github/apps/${encodeURIComponent(name)}/link`,
      undefined,
      { pin },
    );
    setLink(r);
    setNotice(null);
    await refresh();
  };

  return (
    <section>
      <PageHeader title={t("title")} intro={t("intro")} />
      <VocabLine terms={["App do GitHub", "Classe"]} />
      {error ? <LoadError what={t("what")} error={error} onRetry={() => void refetch()} /> : null}
      {!data && !error ? <p className="text-muted">{t("loading")}</p> : null}
      {data && !error ? (
        <div className="flex max-w-4xl flex-col gap-6">
          <Strip page={data} owner={owner} onLink={onLink} />
          {notice ? <Alert tone="ok">{notice}</Alert> : null}
          {link ? (
            <ShownOnce
              title={t("link.title", { name: link.app })}
              secret={link.url}
              detail={t("link.detail", { when: fmt(link.expires_at) })}
              note={t("link.once")}
              doneLabel={t("link.done")}
              onDone={() => setLink(null)}
            />
          ) : null}
          {!owner ? <p className="text-sm text-muted">{t("readOnly")}</p> : null}
          {owner && !data.creation ? (
            <p className="text-sm text-muted">{t("unavailable")}</p>
          ) : null}
          {data.apps.length === 0 ? (
            <EmptyState
              title={t("empty.title")}
              why={t("empty.why")}
              steps={[t("empty.step1"), t("empty.step2"), t("empty.step3")]}
            />
          ) : null}
          <Count count={data.count} />
          {data.apps.length > 0 ? (
            <AppList apps={data.apps} requests={data.requests} owner={owner} onLink={onLink} />
          ) : null}
          {owner ? (
            <Others
              value={data.count.others}
              onSaved={async () => {
                setNotice(t("others.saved"));
                await refresh();
              }}
            />
          ) : null}
        </div>
      ) : null}
    </section>
  );
}

function Strip({
  page,
  owner,
  onLink,
}: {
  page: AppsPage;
  owner: boolean;
  onLink: (name: string, pin: string) => Promise<void>;
}) {
  const { t } = useTranslation("apps");
  if (page.apps.length === 0)
    return (
      <StatusStrip tone="warn" title={t("strip.none")} hint={t("strip.noneHint")}>
        {owner && page.next_fixed ? (
          <SensitiveButton onConfirm={(pin) => onLink(page.next_fixed ?? "", pin)}>
            {t("strip.action", { name: page.next_fixed })}
          </SensitiveButton>
        ) : null}
      </StatusStrip>
    );
  const pending = page.apps.filter((a) => a.state_label !== "pronto").length;
  const target =
    page.apps.find((a) => a.can_link) ??
    (owner ? undefined : page.apps.find((a) => a.state_label !== "pronto"));
  const action = target ? (
    <LevelGate need="owner" label={t("strip.action", { name: target.name })}>
      <SensitiveButton onConfirm={(pin) => onLink(target.name, pin)}>
        {t("strip.action", { name: target.name })}
      </SensitiveButton>
    </LevelGate>
  ) : null;
  if (pending === 0)
    return (
      <StatusStrip tone="ok" title={t("strip.allReady")} hint={t("strip.allReadyHint")}>
        {action}
      </StatusStrip>
    );
  return (
    <StatusStrip
      tone="warn"
      title={pending === 1 ? t("strip.waitingOne") : t("strip.waiting", { count: pending })}
      hint={t("strip.waitingHint", {
        mine: page.count.mine,
        total: page.count.total,
        limit: page.count.limit,
      })}
    >
      {action}
    </StatusStrip>
  );
}

function Count({ count }: { count: AppCount }) {
  const { t } = useTranslation("apps");
  return (
    <div>
      <p className="text-xl font-semibold">
        {t("count", { total: count.total, limit: count.limit })}
        {count.near ? (
          <>
            {" "}
            <Badge tone="danger">{t("near", { limit: count.limit })}</Badge>
          </>
        ) : null}
      </p>
      <p className="text-sm text-muted">
        {t("countDetail", { mine: count.mine, others: count.others, limit: count.limit })}
      </p>
      {/* biome-ignore lint/a11y/useSemanticElements: a native <meter> cannot take the colors of the panel */}
      <div
        role="meter"
        className="mt-2 h-3 w-full overflow-hidden rounded-full bg-accent-wash"
        aria-valuemin={0}
        aria-valuemax={count.limit}
        aria-valuenow={Math.min(count.total, count.limit)}
        aria-label={t("meterTitle")}
      >
        <div
          className={`h-full rounded-full ${count.near ? "bg-danger" : "bg-accent"}`}
          style={{ width: `${Math.min(100, (count.total / Math.max(1, count.limit)) * 100)}%` }}
        />
      </div>
    </div>
  );
}

function Others({ value, onSaved }: { value: number; onSaved: () => Promise<void> }) {
  const { t } = useTranslation("apps");
  const { t: te } = useTranslation("errors");
  const [text, setText] = useState(String(value));
  const [error, setError] = useState<unknown>(null);
  const [busy, setBusy] = useState(false);
  const n = Number(text);
  const valid = text.trim() !== "" && Number.isInteger(n) && n >= 0;
  return (
    <details className="max-w-xl text-sm">
      <summary className="min-h-11 cursor-pointer py-2 font-semibold">
        {t("others.summary")}
      </summary>
      <form
        className="flex flex-col gap-2"
        onSubmit={async (e) => {
          e.preventDefault();
          setBusy(true);
          setError(null);
          try {
            await api("PUT", "/api/github/apps/others", { count: n });
            await onSaved();
          } catch (err) {
            setError(err);
          } finally {
            setBusy(false);
          }
        }}
      >
        <div className="flex flex-wrap items-end gap-3">
          <Field
            label={t("others.label")}
            description={t("others.hint")}
            type="number"
            inputMode="numeric"
            value={text}
            onChange={setText}
            className="w-full"
          />
          <Button type="submit" variant="secondary" isDisabled={busy || !valid}>
            {t("others.save")}
          </Button>
        </div>
        {error ? <Alert>{errorText(te, error)}</Alert> : null}
      </form>
    </details>
  );
}

function AppList({
  apps,
  requests,
  owner,
  onLink,
}: {
  apps: AppRow[];
  requests: AppRequestRow[];
  owner: boolean;
  onLink: (name: string, pin: string) => Promise<void>;
}) {
  const { t } = useTranslation("apps");
  return (
    <section aria-labelledby="apps-title" className="flex flex-col gap-3">
      <h2 id="apps-title" className="text-2xl">
        {t("list.title")}
      </h2>
      <ul aria-label={t("list.caption")} className="flex flex-col gap-3">
        {apps.map((a) => (
          <AppItem
            key={a.name}
            a={a}
            request={requests.find((r) => r.app === a.name)}
            owner={owner}
            onLink={onLink}
          />
        ))}
      </ul>
    </section>
  );
}

/** What to do with this App now, from its state and its open request ([estados](docs/github/criacao-de-apps.md)). */
function AppItem({
  a,
  request,
  owner,
  onLink,
}: {
  a: AppRow;
  request: AppRequestRow | undefined;
  owner: boolean;
  onLink: (name: string, pin: string) => Promise<void>;
}) {
  const { t } = useTranslation("apps");
  const say = t as unknown as Say;
  const fmt = useFormatDateTime();
  const ready = a.state_label === "pronto";
  const status = a.request || request?.status || "";
  let next = "";
  if (ready) next = "";
  else if (request && !request.expired && (status === "link" || status === "opened"))
    next = t("next.link", { when: fmt(request.expires_at) });
  else if (status === "exchanging") next = t("next.exchanging");
  else if (status === "failed") next = t("next.failed");
  else if (request?.expired || status === "expired") next = t("next.expired");
  else if (a.can_link) next = t("next.noLink");
  else if (!owner) next = t("next.waitingOwner");
  const expired = a.state_label === "link vencido" || Boolean(request?.expired);
  return (
    <li className="grid gap-x-6 gap-y-2 rounded-field border border-line bg-surface p-4 sm:grid-cols-[minmax(0,1fr)_auto]">
      <div className="min-w-0">
        <p className="flex flex-wrap items-center gap-2 text-lg font-semibold">
          <span className="font-mono text-base break-all">{a.name}</span>
          <Badge>{a.class ? t("list.class", { class: a.class }) : t("list.fixed")}</Badge>
          <Badge tone={appTone(a.state_label)}>{a.state_label}</Badge>
          {status && !ready && !expired ? (
            <Badge>{labelOr(say, `requests.status.${status}`, status)}</Badge>
          ) : null}
        </p>
        <p className="text-sm text-muted">
          {a.class ? t("list.descClass", { class: a.class }) : say(`list.desc.${a.role}`)}
        </p>
        {next ? <p className="mt-2">{next}</p> : null}
        {owner && a.can_link ? (
          <div className="mt-3">
            <SensitiveButton
              variant={expired ? "primary" : "secondary"}
              onConfirm={(pin) => onLink(a.name, pin)}
            >
              {t(request ? "link.new" : "link.make")}
            </SensitiveButton>
          </div>
        ) : null}
      </div>
      <div className="text-sm sm:text-right">
        <p className="text-muted">{t("list.installedLabel")}</p>
        <p className="text-base">
          {a.repos.length > 0 ? a.repos.join(", ") : t("list.notInstalledShort")}
        </p>
      </div>
    </li>
  );
}
