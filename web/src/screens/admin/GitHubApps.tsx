import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useCan, useFormatDateTime } from "../../app/session";
import { SensitiveButton } from "../../components/PinField";
import { Alert, Button, Field } from "../../components/ui";
import type { AppCount, AppLinkResponse, AppRow } from "../../gen/panel";
import { api } from "../../lib/api";
import { errorText } from "../../lib/errors";
import { appTone } from "./Classes";
import { Badge, PageHeader, ShownOnce } from "./kit";
import { appsQuery, labelOr, type Say } from "./manage";

/** The Apps screen: "N de 100 Apps na conta", the Apps with their state, and (the Owner) the creation links. */
export function GitHubApps() {
  const { t } = useTranslation("apps");
  const { t: te } = useTranslation("errors");
  const fmt = useFormatDateTime();
  const owner = useCan("github_apps");
  const qc = useQueryClient();
  const { data, error } = useQuery(appsQuery);
  const [link, setLink] = useState<AppLinkResponse | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const refresh = () => qc.invalidateQueries({ queryKey: ["admin"] });

  return (
    <section>
      <PageHeader title={t("title")} intro={t("intro")} />
      <div className="flex max-w-5xl flex-col gap-6">
        {notice ? <Alert tone="ok">{notice}</Alert> : null}
        {error ? <Alert>{errorText(te, error)}</Alert> : null}
        {!data && !error ? <p className="text-muted">{t("loading")}</p> : null}
        {data ? (
          <>
            <Count count={data.count} />
            {owner ? (
              <Others
                value={data.count.others}
                onSaved={async () => {
                  setNotice(t("others.saved"));
                  await refresh();
                }}
              />
            ) : (
              <p className="text-sm text-muted">{t("ownerOnly")}</p>
            )}
            {owner && !data.creation ? (
              <p className="text-sm text-muted">{t("unavailable")}</p>
            ) : null}
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
            <AppList
              apps={data.apps}
              owner={owner}
              onLink={async (name, pin) => {
                const r = await api<AppLinkResponse>(
                  "POST",
                  `/api/github/apps/${encodeURIComponent(name)}/link`,
                  undefined,
                  { pin },
                );
                setLink(r);
                setNotice(null);
                await refresh();
              }}
            />
            {owner ? <Requests requests={data.requests} /> : null}
          </>
        ) : null}
      </div>
    </section>
  );
}

function Count({ count }: { count: AppCount }) {
  const { t } = useTranslation("apps");
  return (
    <div>
      <p className="text-2xl font-semibold">
        {t("count", { total: count.total, limit: count.limit })}
        {count.near ? (
          <>
            {" "}
            <Badge tone="warn">{t("near", { limit: count.limit })}</Badge>
          </>
        ) : null}
      </p>
      <p className="text-sm text-muted">
        {t("countDetail", { mine: count.mine, others: count.others })}
      </p>
      <progress
        className="mt-2 h-2 w-full max-w-md accent-accent"
        max={count.limit}
        value={Math.min(count.total, count.limit)}
        aria-label={t("count", { total: count.total, limit: count.limit })}
      />
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
    <form
      className="flex max-w-xl flex-col gap-2"
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
  );
}

function AppList({
  apps,
  owner,
  onLink,
}: {
  apps: AppRow[];
  owner: boolean;
  onLink: (name: string, pin: string) => Promise<void>;
}) {
  const { t } = useTranslation("apps");
  const say = t as unknown as Say;
  return (
    <section aria-labelledby="apps-title" className="flex flex-col gap-3">
      <h2 id="apps-title" className="text-2xl">
        {t("list.title")}
      </h2>
      {apps.length === 0 ? <p className="text-muted">{t("list.empty")}</p> : null}
      {apps.length > 0 ? (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[40rem] border-collapse text-left">
            <caption className="sr-only">{t("list.caption")}</caption>
            <thead>
              <tr className="border-b border-line text-sm text-muted">
                <th scope="col" className="py-2 pr-4">
                  {t("list.name")}
                </th>
                <th scope="col" className="py-2 pr-4">
                  {t("list.class")}
                </th>
                <th scope="col" className="py-2 pr-4">
                  {t("list.state")}
                </th>
                <th scope="col" className="py-2 pr-4">
                  {t("list.repos")}
                </th>
                {owner ? (
                  <th scope="col" className="py-2">
                    {t("list.actions")}
                  </th>
                ) : null}
              </tr>
            </thead>
            <tbody>
              {apps.map((a) => (
                <tr key={a.name} className="border-b border-line align-top">
                  <th scope="row" className="py-2 pr-4 font-semibold">
                    {a.name}
                    <span className="block text-sm font-normal text-muted">{a.role}</span>
                  </th>
                  <td className="py-2 pr-4">
                    {a.class || <span className="text-muted">{t("list.fixed")}</span>}
                  </td>
                  <td className="py-2 pr-4">
                    <Badge tone={appTone(a.state_label)}>{a.state_label}</Badge>
                    {a.request ? (
                      <span className="mt-1 block text-sm text-muted">
                        {labelOr(say, `requests.status.${a.request}`, a.request)}
                      </span>
                    ) : null}
                  </td>
                  <td className="py-2 pr-4">
                    {a.repos.length > 0 ? (
                      a.repos.join(", ")
                    ) : (
                      <span className="text-muted">{t("list.noRepos")}</span>
                    )}
                  </td>
                  {owner ? (
                    <td className="py-2">
                      {a.can_link ? (
                        <SensitiveButton
                          variant="secondary"
                          onConfirm={(pin) => onLink(a.name, pin)}
                        >
                          {t(a.request ? "link.new" : "link.make", { name: a.name })}
                        </SensitiveButton>
                      ) : null}
                    </td>
                  ) : null}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : null}
    </section>
  );
}

function Requests({
  requests,
}: {
  requests: { app: string; status: string; expires_at: string; expired: boolean }[];
}) {
  const { t } = useTranslation("apps");
  const say = t as unknown as Say;
  const fmt = useFormatDateTime();
  return (
    <section aria-labelledby="requests-title" className="flex flex-col gap-2">
      <h2 id="requests-title" className="text-2xl">
        {t("requests.title")}
      </h2>
      {requests.length === 0 ? <p className="text-muted">{t("requests.empty")}</p> : null}
      <ul className="flex flex-col gap-2">
        {requests.map((r) => (
          <li key={r.app} className="flex flex-wrap items-center gap-2">
            <span className="font-semibold">{r.app}</span>
            <Badge tone={r.expired ? "danger" : "plain"}>
              {labelOr(say, `requests.status.${r.status}`, r.status)}
            </Badge>
            <span className="text-sm text-muted">
              {r.expired
                ? t("requests.expired")
                : t("requests.expires", { when: fmt(r.expires_at) })}
            </span>
          </li>
        ))}
      </ul>
    </section>
  );
}
