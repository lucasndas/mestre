import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { Checkbox } from "react-aria-components";
import { useTranslation } from "react-i18next";
import { useFormatDateTime } from "../../app/session";
import { SensitiveButton } from "../../components/PinField";
import { Alert, Button, Field } from "../../components/ui";
import type {
  RecoveryKeyResponse,
  ReplaceSecretRequest,
  SecretRow,
  SiteCredentialInput,
} from "../../gen/panel";
import { api } from "../../lib/api";
import {
  Badge,
  ChoiceGroup,
  LevelGate,
  LoadError,
  NativeSelect,
  PageHeader,
  ShownOnce,
  StatusStrip,
  secretsQuery,
  splitList,
  usersQuery,
  VocabLine,
} from "./kit";

type Say = (k: string, o?: object) => string;

const KINDS = ["senha_de_app", "token", "chave_api", "senha_principal"] as const;

export function Secrets() {
  const { t } = useTranslation("secrets");
  const say = t as unknown as Say;
  const fmt = useFormatDateTime();
  const qc = useQueryClient();
  const { data, error, refetch } = useQuery(secretsQuery);
  const people = useQuery(usersQuery).data?.users ?? [];
  const [creating, setCreating] = useState(false);
  const [replacing, setReplacing] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const refresh = () => qc.invalidateQueries({ queryKey: ["admin"] });
  const nameOf = (id: string) => people.find((p) => p.id === id)?.name ?? id;
  const sites = (data?.secrets ?? []).filter((s) => !s.managed);
  const managed = (data?.secrets ?? []).filter((s) => s.managed);
  const expiring = sites.filter((s) => s.notice).length;
  const openButton = (
    <LevelGate need="owner" label={t("site.open")}>
      {creating ? null : <Button onPress={() => setCreating(true)}>{t("site.open")}</Button>}
    </LevelGate>
  );

  return (
    <section>
      <PageHeader title={t("title")} intro={t("intro")} />
      <VocabLine terms={["Cofre", "Impressão"]} />
      {data ? (
        <StatusStrip
          tone={expiring > 0 ? "warn" : sites.length > 0 ? "ok" : "neutral"}
          title={sites.length > 0 ? t("strip", { count: sites.length, expiring }) : t("stripNone")}
          hint={sites.length > 0 ? t("stripHint") : t("stripNoneHint", { count: managed.length })}
        >
          {openButton}
        </StatusStrip>
      ) : null}
      <div className="flex max-w-4xl flex-col gap-4">
        {notice ? <Alert tone="ok">{notice}</Alert> : null}
        {error ? (
          <LoadError what={t("loadWhat")} error={error} onRetry={() => void refetch()} />
        ) : null}
        {!data && !error ? <p className="text-muted">…</p> : null}
        {data && creating ? (
          <SiteForm
            people={people}
            submitLabel={t("site.submit")}
            title={t("site.title")}
            onClose={() => setCreating(false)}
            onSubmit={async (pin, input) => {
              await api("POST", "/api/secrets/site", input, { pin });
              setCreating(false);
              setNotice(t("site.created"));
              await refresh();
            }}
            withName
          />
        ) : null}
        {data ? <WhereKeysGo /> : null}
        {sites.length > 0 ? <h2 className="text-2xl">{t("sitesTitle")}</h2> : null}
        <ul className="flex flex-col gap-3">
          {sites.map((s) => (
            <li key={s.name} className="rounded-field border border-line bg-surface p-4">
              <div className="flex flex-wrap items-start justify-between gap-3">
                <p className="flex min-w-0 flex-wrap items-center gap-2 text-lg font-semibold">
                  {s.name}
                  <Badge>{s.site ? say(`kinds.${s.site.kind}`) : typeLabel(say, s.type)}</Badge>
                  {s.notice ? (
                    <Badge tone={s.notice.kind === "expiring" ? "warn" : "danger"}>
                      {say(`noticeChip.${s.notice.kind}`, { count: s.notice.days })}
                    </Badge>
                  ) : null}
                </p>
                <LevelGate need="owner" label={t("replace.open")}>
                  <div className="flex flex-wrap items-start gap-3">
                    <Button
                      variant="secondary"
                      aria-expanded={replacing === s.name}
                      aria-label={t("replace.openNamed", { name: s.name })}
                      onPress={() => setReplacing(replacing === s.name ? null : s.name)}
                    >
                      {t("replace.open")}
                    </Button>
                    <DeleteSecret
                      s={s}
                      onDone={async () => {
                        setNotice(t("delete.done"));
                        await refresh();
                      }}
                    />
                  </div>
                </LevelGate>
              </div>
              <dl className="mt-3 grid grid-cols-2 gap-x-4 gap-y-3 text-sm sm:grid-cols-3 lg:grid-cols-5">
                <Cell label={t("classes")}>{s.site?.classes.join(", ") || "—"}</Cell>
                <Cell label={t("domains")}>{s.site?.domains.join(", ") || "—"}</Cell>
                <Cell label={t("owner")}>{s.site ? nameOf(s.site.owner_user) : "—"}</Cell>
                <Cell label={t("lastUsed")}>
                  {s.last_used_at ? fmt(s.last_used_at) : t("neverUsed")}
                </Cell>
                <Cell label={t("fingerprint")}>
                  <code>{s.fingerprint}</code>
                </Cell>
                {s.site?.expires_at ? (
                  <Cell label={t("expiresAt")}>{fmt(s.site.expires_at)}</Cell>
                ) : null}
              </dl>
              <p className="mt-3 text-sm text-muted">{t("delete.hint")}</p>
              {s.notice ? (
                <p
                  className={`mt-3 text-sm font-semibold ${s.notice.kind === "expiring" ? "text-warn-ink" : "text-danger"}`}
                >
                  {say(`notice.${s.notice.kind}`, { count: s.notice.days })}
                </p>
              ) : null}
              <div className="mt-3 flex flex-col gap-3">
                {replacing === s.name ? (
                  <Replace
                    s={s}
                    people={people}
                    onClose={() => setReplacing(null)}
                    onDone={async () => {
                      setReplacing(null);
                      setNotice(t("replace.done"));
                      await refresh();
                    }}
                  />
                ) : null}
              </div>
            </li>
          ))}
        </ul>
        {managed.length > 0 ? (
          <section aria-labelledby="sec-managed" className="flex flex-col gap-2">
            <h2 id="sec-managed" className="text-2xl">
              {t("managedTitle")}
            </h2>
            <p className="text-sm text-muted">{t("managed")}</p>
            <details open className="rounded-field border border-line bg-surface">
              <summary className="min-h-11 cursor-pointer px-3 py-2 text-sm font-semibold">
                {t("managedCount", { count: managed.length })}
              </summary>
              <div className="overflow-x-auto">
                <table className="w-full text-left">
                  <thead className="text-sm text-muted">
                    <tr className="border-y border-line">
                      <th scope="col" className="px-3 py-2 font-semibold">
                        {t("managedHeader.what")}
                      </th>
                      <th scope="col" className="px-3 py-2 font-semibold">
                        {t("managedHeader.fingerprint")}
                      </th>
                      <th scope="col" className="px-3 py-2 font-semibold">
                        {t("managedHeader.technical")}
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {managed.map((s) => (
                      <tr key={s.name} className="border-b border-line last:border-0">
                        <th scope="row" className="px-3 py-2 font-semibold">
                          {typeLabel(say, s.type)}
                        </th>
                        <td className="px-3 py-2">
                          <code>{s.fingerprint}</code>
                        </td>
                        <td className="px-3 py-2 break-all">
                          <code>{s.name}</code>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </details>
          </section>
        ) : null}
        {data ? <RecoveryKey /> : null}
      </div>
    </section>
  );
}

/** Where each kind of key goes, so nobody looks for a button that does not exist. */
function WhereKeysGo() {
  const { t } = useTranslation("secrets");
  const link = "font-semibold text-accent underline underline-offset-2";
  return (
    <section aria-labelledby="sec-where" className="flex flex-col gap-2">
      <h2 id="sec-where" className="text-2xl">
        {t("where.title")}
      </h2>
      <div className="overflow-x-auto rounded-field border border-line bg-surface">
        <table className="w-full text-left">
          <thead className="text-sm text-muted">
            <tr className="border-b border-line">
              <th scope="col" className="px-3 py-2 font-semibold">
                {t("where.wantHeader")}
              </th>
              <th scope="col" className="px-3 py-2 font-semibold">
                {t("where.doHeader")}
              </th>
            </tr>
          </thead>
          <tbody>
            <tr className="border-b border-line align-top">
              <th scope="row" className="px-3 py-2 font-semibold">
                {t("where.siteWant")}
              </th>
              <td className="px-3 py-2">{t("where.site")}</td>
            </tr>
            <tr className="border-b border-line align-top">
              <th scope="row" className="px-3 py-2 font-semibold">
                {t("where.githubWant")}
              </th>
              <td className="px-3 py-2">
                {t("where.github")}{" "}
                <Link to="/apps" className={link}>
                  {t("where.githubLink")}
                </Link>
              </td>
            </tr>
            <tr className="border-b border-line align-top">
              <th scope="row" className="px-3 py-2 font-semibold">
                {t("where.aiWant")}
              </th>
              <td className="px-3 py-2">
                {t("where.ai")}{" "}
                <Link to="/perfis" className={link}>
                  {t("where.aiLink")}
                </Link>
              </td>
            </tr>
            <tr className="border-b border-line align-top">
              <th scope="row" className="px-3 py-2 font-semibold">
                {t("where.telegramWant")}
              </th>
              <td className="px-3 py-2">{t("where.telegram")}</td>
            </tr>
            <tr className="align-top">
              <th scope="row" className="px-3 py-2 font-semibold">
                {t("where.providerWant")}
              </th>
              <td className="px-3 py-2">{t("where.provider")}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>
  );
}

function typeLabel(say: Say, type: string): string {
  const k = `types.${type}`;
  const text = say(k);
  return text === k ? type : text;
}

function Cell({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="min-w-0">
      <dt className="text-muted">{label}</dt>
      <dd className="break-words">{children}</dd>
    </div>
  );
}

function DeleteSecret({ s, onDone }: { s: SecretRow; onDone: () => Promise<void> }) {
  const { t } = useTranslation("secrets");
  return (
    <SensitiveButton
      variant="danger"
      onConfirm={async (pin) => {
        await api("DELETE", `/api/secrets/${encodeURIComponent(s.name)}`, undefined, { pin });
        await onDone();
      }}
    >
      {t("delete.open")}
    </SensitiveButton>
  );
}

function Replace({
  s,
  people,
  onClose,
  onDone,
}: {
  s: SecretRow;
  people: { id: string; name: string; level: string }[];
  onClose: () => void;
  onDone: () => Promise<void>;
}) {
  const { t } = useTranslation("secrets");
  const [value, setValue] = useState("");
  const put = async (pin: string, body: ReplaceSecretRequest) => {
    await api("PUT", `/api/secrets/${encodeURIComponent(s.name)}`, body, { pin });
    await onDone();
  };
  if (s.site) {
    return (
      <SiteForm
        initial={s}
        people={people}
        title={t("replace.openNamed", { name: s.name })}
        submitLabel={t("replace.submit")}
        onClose={onClose}
        onSubmit={(pin, input) => put(pin, { site: input })}
      />
    );
  }
  return (
    <div className="flex max-w-xl flex-col gap-3 rounded-field border border-line p-4">
      <p className="text-sm text-muted">{t("replace.hint")}</p>
      <Field
        label={t("replace.value")}
        type="password"
        autoComplete="off"
        value={value}
        onChange={setValue}
      />
      <div className="flex gap-3">
        <SensitiveButton isDisabled={value === ""} onConfirm={(pin) => put(pin, { value })}>
          {t("replace.submit")}
        </SensitiveButton>
        <Button variant="quiet" onPress={onClose}>
          {t("replace.close")}
        </Button>
      </div>
    </div>
  );
}

/** The fields of a site credential, to register it or to replace it. */
function SiteForm({
  initial,
  people,
  title,
  submitLabel,
  withName = false,
  onClose,
  onSubmit,
}: {
  initial?: SecretRow;
  people: { id: string; name: string; level: string }[];
  title: string;
  submitLabel: string;
  withName?: boolean;
  onClose: () => void;
  onSubmit: (pin: string, input: SiteCredentialInput) => Promise<void>;
}) {
  const { t } = useTranslation("secrets");
  const say = t as unknown as Say;
  const site = initial?.site;
  const [name, setName] = useState(initial?.name ?? "");
  const [url, setUrl] = useState("");
  const [user, setUser] = useState("");
  const [password, setPassword] = useState("");
  const [kind, setKind] = useState<string>(site?.kind ?? "senha_de_app");
  const [owner, setOwner] = useState(
    site?.owner_user ?? people.find((p) => p.level === "owner")?.id ?? "",
  );
  const [classes, setClasses] = useState(site?.classes.join(", ") ?? "");
  const [domains, setDomains] = useState(site?.domains.join(", ") ?? "");
  const [expires, setExpires] = useState(site?.expires_at?.slice(0, 10) ?? "");
  const [selectors, setSelectors] = useState("");
  const [success, setSuccess] = useState("");
  const ready =
    (!withName || name.trim() !== "") &&
    url.trim() !== "" &&
    password !== "" &&
    splitList(classes).length > 0 &&
    splitList(domains).length > 0;
  const input = (): SiteCredentialInput => ({
    name: name.trim(),
    url: url.trim(),
    user,
    password,
    selectors: parseSelectors(selectors),
    success: success.trim(),
    owner_user: owner,
    classes: splitList(classes),
    domains: splitList(domains),
    kind,
    // noon UTC keeps the same calendar day in every time zone the Guild may be in
    expires_at: expires ? `${expires}T12:00:00Z` : undefined,
  });
  return (
    <div className="flex max-w-xl flex-col gap-4 rounded-field border border-line bg-surface p-5">
      <h2 className="text-xl">{title}</h2>
      <p className="text-sm text-muted">{say("site.hint")}</p>
      {withName ? (
        <Field label={t("site.name")} value={name} onChange={setName} autoComplete="off" />
      ) : null}
      <Field label={t("site.url")} type="url" value={url} onChange={setUrl} autoComplete="off" />
      <Field label={t("site.user")} value={user} onChange={setUser} autoComplete="off" />
      <Field
        label={t("site.password")}
        type="password"
        value={password}
        onChange={setPassword}
        autoComplete="new-password"
      />
      <ChoiceGroup
        label={t("site.kind")}
        value={kind}
        onChange={setKind}
        options={KINDS.map((k) => ({ value: k, label: say(`kinds.${k}`) }))}
      />
      <p className="text-sm text-muted">{t("kindHint")}</p>
      <Field
        label={t("site.classes")}
        description={t("site.classesHint")}
        value={classes}
        onChange={setClasses}
        autoComplete="off"
      />
      <Field
        label={t("site.domains")}
        description={t("site.domainsHint")}
        value={domains}
        onChange={setDomains}
        autoComplete="off"
      />
      <details className="flex flex-col gap-4">
        <summary className="min-h-11 cursor-pointer py-2 font-semibold">{t("site.more")}</summary>
        <div className="flex flex-col gap-4">
          {people.length > 0 ? (
            <NativeSelect
              label={t("site.owner")}
              value={owner}
              onChange={setOwner}
              options={people.map((p) => ({ value: p.id, label: p.name }))}
            />
          ) : null}
          <Field label={t("site.expires")} type="date" value={expires} onChange={setExpires} />
          <label className="flex flex-col gap-1">
            <span className="text-[0.95em] font-semibold tracking-wide">{t("site.selectors")}</span>
            <textarea
              rows={3}
              value={selectors}
              onChange={(e) => setSelectors(e.target.value)}
              className="rounded-field border border-line bg-paper px-3 py-2 font-mono text-sm outline-none focus:border-accent focus:ring-3 focus:ring-accent/25"
            />
            <span className="text-sm text-muted">{t("site.selectorsHint")}</span>
          </label>
          <Field
            label={t("site.success")}
            value={success}
            onChange={setSuccess}
            autoComplete="off"
          />
        </div>
      </details>
      <div className="flex flex-wrap items-start gap-3">
        <SensitiveButton isDisabled={!ready} onConfirm={(pin) => onSubmit(pin, input())}>
          {submitLabel}
        </SensitiveButton>
        <Button variant="quiet" onPress={onClose}>
          {t("site.close")}
        </Button>
      </div>
    </div>
  );
}

function parseSelectors(text: string): Record<string, string> | undefined {
  const out: Record<string, string> = {};
  for (const line of text.split("\n")) {
    const i = line.indexOf("=");
    if (i > 0 && line.slice(i + 1).trim()) out[line.slice(0, i).trim()] = line.slice(i + 1).trim();
  }
  return Object.keys(out).length ? out : undefined;
}

/** A new recovery key: the warning about the old backups comes before the PIN. */
function RecoveryKey() {
  const { t } = useTranslation("secrets");
  const [open, setOpen] = useState(false);
  const [ack, setAck] = useState(false);
  const [key, setKey] = useState<string | null>(null);
  return (
    <section className="mt-4 flex flex-col gap-3 rounded-field border border-line bg-surface p-5">
      <h2 className="text-2xl">{t("recovery.title")}</h2>
      <p className="max-w-2xl text-muted">{t("recovery.intro")}</p>
      {key ? (
        <ShownOnce
          title={t("recovery.shown")}
          secret={key}
          note={`${t("recovery.once")} ${t("recovery.failsafe")}`}
          doneLabel={t("recovery.done")}
          onDone={() => setKey(null)}
        />
      ) : open ? (
        <div className="flex max-w-2xl flex-col gap-4">
          <Alert>{t("recovery.warning")}</Alert>
          <p className="text-sm text-muted">{t("recovery.failsafe")}</p>
          <Checkbox
            isSelected={ack}
            onChange={setAck}
            className="group flex min-h-11 cursor-pointer items-center gap-3"
          >
            <span
              aria-hidden="true"
              className="grid size-5 shrink-0 place-items-center rounded-sm border border-muted text-xs text-accent-ink group-data-selected:border-accent group-data-selected:bg-accent group-data-focus-visible:ring-3 group-data-focus-visible:ring-accent/25"
            >
              <span className="hidden group-data-selected:block">✓</span>
            </span>
            {t("recovery.ack")}
          </Checkbox>
          <div className="flex flex-wrap items-start gap-3">
            <SensitiveButton
              isDisabled={!ack}
              onConfirm={async (pin) => {
                const r = await api<RecoveryKeyResponse>(
                  "POST",
                  "/api/recovery-key",
                  { acknowledged: ack },
                  { pin },
                );
                setKey(r.key);
                setOpen(false);
                setAck(false);
              }}
            >
              {t("recovery.submit")}
            </SensitiveButton>
            <Button
              variant="quiet"
              onPress={() => {
                setOpen(false);
                setAck(false);
              }}
            >
              {t("recovery.cancel")}
            </Button>
          </div>
        </div>
      ) : (
        <div>
          <Button variant="secondary" onPress={() => setOpen(true)}>
            {t("recovery.open")}
          </Button>
          <p className="mt-2 text-sm text-muted">{t("recovery.oldBackups")}</p>
        </div>
      )}
    </section>
  );
}
