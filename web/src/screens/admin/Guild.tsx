import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { type ReactNode, useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useFormatDateTime } from "../../app/session";
import { SensitiveButton } from "../../components/PinField";
import { Term } from "../../components/Term";
import { Alert, Button, Field } from "../../components/ui";
import type {
  GuildAgent,
  GuildMachineRow,
  GuildPendingJoin,
  GuildRemoval,
  GuildView,
} from "../../gen/panel";
import { ApiError, api } from "../../lib/api";
import { errorText } from "../../lib/errors";
import { formatNumber } from "../../lib/format";
import { useLive, useLiveEvents } from "../../lib/live";
import { Badge, NativeSelect, PageHeader } from "./kit";

type Say = (k: string, o?: object) => string;

const GUILD_TOPIC = "guild:main";
export const guildQuery = {
  queryKey: ["admin", "guild"],
  queryFn: () => api<GuildView>("GET", "/api/guild"),
};

/** The text of an error of this screen: a refusal of the Citadel says its reason. */
function useGuildError() {
  const { t } = useTranslation("guild");
  const { t: te } = useTranslation("errors");
  const say = t as unknown as Say;
  return (err: unknown): string => {
    if (err instanceof ApiError && err.code === "refused") {
      const text = say(`refusal.${String(err.params.reason ?? "")}`, { defaultValue: "" });
      return text || err.message || errorText(te, err);
    }
    return errorText(te, err);
  };
}

function bytes(n: number, language: string): string {
  if (n <= 0) return "—";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${formatNumber(Math.round(v * 10) / 10, language)} ${units[i]}`;
}

/** The Guild and the health of the machines (docs/painel/administracao.md): live, one row per machine. */
export function Guild() {
  const { t } = useTranslation("guild");
  const { t: te } = useTranslation("errors");
  const qc = useQueryClient();
  const live = useLive();
  const { data, error } = useQuery(guildQuery);
  const [notice, setNotice] = useState<string | null>(null);

  // the snapshot opens the view and each "view" event replaces it
  useEffect(() => live.subscribe([GUILD_TOPIC]), [live]);
  useLiveEvents("guild:", (_topic, event, payload) => {
    if (event === "snapshot" || event === "view") qc.setQueryData(guildQuery.queryKey, payload);
  });
  const refresh = () => qc.invalidateQueries({ queryKey: guildQuery.queryKey });

  return (
    <section>
      <PageHeader title={t("title")} intro={t("intro")} />
      <p className="mb-6 flex flex-wrap items-center gap-3 text-sm text-muted">
        <Term name="Guilda" /> · <Term name="Cidadela" /> · <Term name="Torre" /> ·{" "}
        <Term name="Máquina excluída" />
        {data ? (
          <span className="inline-flex items-center gap-1">
            <Term name="Época" />
            <Badge>{t("epoch", { epoch: data.epoch })}</Badge>
          </span>
        ) : null}
      </p>
      <div className="flex max-w-5xl flex-col gap-6">
        {notice ? <Alert tone="ok">{notice}</Alert> : null}
        {error ? <Alert>{errorText(te, error)}</Alert> : null}
        {!data && !error ? <p className="text-muted">{t("loading")}</p> : null}
        {data?.single ? <SingleBand /> : null}
        {data && data.pending.length > 0 ? (
          <Pending joins={data.pending} say={setNotice} onDone={refresh} />
        ) : null}
        {data ? (
          <ul aria-label={t("machines")} className="flex flex-col gap-4">
            {data.machines.map((m) => (
              <MachineRow key={m.id} m={m} say={setNotice} onDone={refresh} />
            ))}
          </ul>
        ) : null}
        {data ? <p className="text-sm text-muted">{t("later")}</p> : null}
        {data ? <Timezone zone={data.timezone} say={setNotice} onDone={refresh} /> : null}
      </div>
    </section>
  );
}

function SingleBand() {
  const { t } = useTranslation("guild");
  return (
    <aside
      aria-label={t("single.title")}
      className="rounded-field border border-warn-ink/30 bg-warn px-4 py-3 text-warn-ink"
    >
      <p className="font-semibold">
        <Term name="Modo máquina única" />
      </p>
      <p>{t("single.body")}</p>
    </aside>
  );
}

function Pending({
  joins,
  say,
  onDone,
}: {
  joins: GuildPendingJoin[];
  say: (s: string) => void;
  onDone: () => Promise<unknown>;
}) {
  const { t } = useTranslation("guild");
  const errorOf = useGuildError();
  const fmt = useFormatDateTime();
  const decide = async (j: GuildPendingJoin, pin: string, accept: boolean, excluded: boolean) => {
    await api(
      "POST",
      `/api/guild/joins/${encodeURIComponent(j.id)}/decide`,
      { accept, excluded },
      { pin },
    );
    say(t(accept ? "pending.accepted" : "pending.refused", { name: j.name }));
    await onDone();
  };
  return (
    <section aria-label={t("pending.title")} className="flex flex-col gap-3">
      <h2 className="text-xl">{t("pending.title")}</h2>
      <ul className="flex flex-col gap-3">
        {joins.map((j) => (
          <li key={j.id} className="rounded-field border border-accent bg-accent-wash p-4">
            <p className="font-semibold">{j.name}</p>
            <p className="text-sm">
              {t("pending.code")}:{" "}
              {j.code ? (
                <code className="text-lg font-semibold tracking-wider">{j.code}</code>
              ) : (
                t("pending.noCode")
              )}{" "}
              · {fmt(j.created_at)}
            </p>
            <div className="mt-3 flex flex-wrap items-start gap-3">
              <SensitiveButton
                describeError={errorOf}
                onConfirm={(pin) => decide(j, pin, true, false)}
              >
                {t("pending.accept")}
              </SensitiveButton>
              <SensitiveButton
                describeError={errorOf}
                variant="secondary"
                onConfirm={(pin) => decide(j, pin, true, true)}
              >
                {t("pending.acceptExcluded")}
              </SensitiveButton>
              <SensitiveButton
                describeError={errorOf}
                variant="danger"
                onConfirm={(pin) => decide(j, pin, false, false)}
              >
                {t("pending.refuse")}
              </SensitiveButton>
            </div>
          </li>
        ))}
      </ul>
    </section>
  );
}

function Fact({
  label,
  children,
  wide = false,
}: {
  label: string;
  children: ReactNode;
  wide?: boolean;
}) {
  return (
    <div className={wide ? "lg:col-span-2" : ""}>
      <dt className="text-sm font-semibold text-muted">{label}</dt>
      <dd>{children}</dd>
    </div>
  );
}

function MachineRow({
  m,
  say,
  onDone,
}: {
  m: GuildMachineRow;
  say: (s: string) => void;
  onDone: () => Promise<unknown>;
}) {
  const { t, i18n } = useTranslation("guild");
  const sayT = t as unknown as Say;
  const errorOf = useGuildError();
  const fmt = useFormatDateTime();
  const [removing, setRemoving] = useState(false);
  const [priority, setPriority] = useState(String(m.priority));
  useEffect(() => setPriority(String(m.priority)), [m.priority]);

  const act = async (
    method: string,
    path: string,
    body: unknown,
    pin: string,
    done: string,
  ): Promise<void> => {
    await api(method, `/api/guild/machines/${encodeURIComponent(m.id)}/${path}`, body, { pin });
    say(sayT(done, { name: m.name }));
    await onDone();
  };

  const passHint = m.excluded
    ? t("passHint.excluded")
    : m.outside_network
      ? t("passHint.outside")
      : !m.online
        ? t("passHint.offline")
        : "";
  const noSignal = !m.online
    ? m.last_seen
      ? t("states.noSignalSince", { when: fmt(m.last_seen) })
      : t("states.noSignalYet")
    : t("states.connected");
  const sheet = m.sheet;

  return (
    <li className="rounded-field border border-line bg-surface p-4" aria-label={m.name}>
      <header className="flex flex-wrap items-center gap-2">
        <h2 className="text-xl">{m.name}</h2>
        <Badge tone={m.citadel ? "ok" : "plain"}>
          {t(m.citadel ? "roles.citadel" : "roles.tower")}
        </Badge>
        {m.self ? <Badge>{t("badges.self")}</Badge> : null}
        {m.suspended ? <Badge tone="danger">{t("badges.suspended")}</Badge> : null}
        {m.github_down ? <Badge tone="warn">{t("badges.githubDown")}</Badge> : null}
        {m.telegram_stopped ? <Badge tone="danger">{t("badges.telegramStopped")}</Badge> : null}
      </header>
      <dl className="mt-3 grid gap-x-6 gap-y-3 items-start sm:grid-cols-2 lg:grid-cols-4">
        <Fact label={t("state")}>
          <Badge tone={m.online ? "ok" : "warn"}>{noSignal}</Badge>
          {m.outside_network && !m.citadel ? (
            <Badge tone="warn">{t("states.outside")}</Badge>
          ) : null}
        </Fact>
        <Fact label={t("engine")}>
          {m.engine_version ? <p>{t("engineVersion", { version: m.engine_version })}</p> : null}
          {m.protocol ? <p>{t("protocol", { n: m.protocol })}</p> : null}
          <p className="text-sm text-muted">{t("guardian")}</p>
        </Fact>
        <Fact label={t("sheet")} wide>
          <p>{[sheet.os, sheet.gui ? t("gui") : t("noGui")].filter(Boolean).join(" · ")}</p>
          <p className="text-sm">
            {t("providers")}:{" "}
            {sheet.providers.length ? sheet.providers.join("; ") : t("noProviders")}
          </p>
          {sheet.browser ? (
            <p className="text-sm">
              {t("browser")}: {sheet.browser}
            </p>
          ) : null}
          <p className="text-sm">
            {t("operator")}: {t(`operators.${sheet.operator || "off"}` as "operators.off")}
            {sheet.operator_reason
              ? ` (${sayT(`operatorReasons.${sheet.operator_reason}`, { defaultValue: sheet.operator_reason })})`
              : ""}
          </p>
          <p className="text-sm">
            {t("cpu")}: {t("cores", { count: sheet.cpu_cores })} · {t("memory")}:{" "}
            {bytes(sheet.mem_bytes, i18n.language)} · {t("gpu")}:{" "}
            {sheet.gpus.length ? sheet.gpus.join(", ") : t("noGpu")}
          </p>
          <p className="text-sm">
            {t("transcription")}: {sheet.transcribes ? t("transcribes") : t("noTranscribes")}
          </p>
        </Fact>
        <Fact label={t("excluded")}>
          <p>{m.excluded ? t("excludedYes") : t("excludedNo")}</p>
          <p className="text-sm">
            {t("replica.label")}:{" "}
            {m.replica
              ? `${sayT(`replica.${m.replica.state}`, { defaultValue: m.replica.state })}${
                  m.replica.behind > 0
                    ? ` (${t("replica.behind", { count: m.replica.behind })})`
                    : ""
                }`
              : m.citadel
                ? "—"
                : t("replica.none")}
          </p>
        </Fact>
        <Fact label={t("load")}>
          <p>{t("running", { count: m.load.running })}</p>
          <p className="text-sm">
            {t("disk")}: {bytes(m.load.disk_free_bytes, i18n.language)}
          </p>
        </Fact>
        <Fact label={t("agents")} wide>
          <AgentList agents={m.agents} />
        </Fact>
        <Fact label={t("priority")}>
          <p>{m.priority}</p>
        </Fact>
      </dl>
      <p className="mt-2 text-sm text-muted">{t("transcriptionLater")}</p>

      {m.citadel ? null : (
        <div className="mt-4 flex flex-col gap-4 border-t border-line pt-4">
          <div className="flex flex-wrap items-start gap-3">
            <SensitiveButton
              describeError={errorOf}
              variant="secondary"
              onConfirm={(pin) =>
                act(
                  "POST",
                  "exclusion",
                  { excluded: !m.excluded },
                  pin,
                  m.excluded ? "done.included" : "done.excluded",
                )
              }
            >
              {sayT(m.excluded ? "actions.includeNamed" : "actions.excludeNamed", { name: m.name })}
            </SensitiveButton>
            <div className="flex flex-col gap-1">
              <SensitiveButton
                describeError={errorOf}
                isDisabled={Boolean(passHint)}
                onConfirm={(pin) => act("POST", "pass-citadel", undefined, pin, "done.passed")}
              >
                {t("actions.passNamed", { name: m.name })}
              </SensitiveButton>
              {passHint ? <p className="text-sm text-muted">{passHint}</p> : null}
            </div>
            <Button
              variant="danger"
              aria-expanded={removing}
              onPress={() => setRemoving(!removing)}
            >
              {t("actions.removeNamed", { name: m.name })}
            </Button>
          </div>
          <div className="flex max-w-md flex-col gap-2">
            <Field
              label={t("priorityLabel", { name: m.name })}
              description={t("priorityHint")}
              inputMode="numeric"
              value={priority}
              onChange={setPriority}
            />
            <div>
              <SensitiveButton
                describeError={errorOf}
                variant="secondary"
                isDisabled={!/^\d{1,4}$/.test(priority) || Number(priority) === m.priority}
                onConfirm={(pin) =>
                  act("PUT", "priority", { priority: Number(priority) }, pin, "prioritySaved")
                }
              >
                {t("savePriority")}
              </SensitiveButton>
            </div>
          </div>
          {removing ? (
            <Removal
              m={m}
              onClose={() => setRemoving(false)}
              onRemoved={async (offline) => {
                setRemoving(false);
                say(sayT(offline ? "done.removedOffline" : "done.removed", { name: m.name }));
                await onDone();
              }}
            />
          ) : null}
        </div>
      )}
    </li>
  );
}

function AgentList({ agents }: { agents: GuildAgent[] }) {
  const { t } = useTranslation("guild");
  if (agents.length === 0) return <p className="text-muted">{t("noAgents")}</p>;
  return (
    <ul className="text-sm">
      {agents.map((a) => (
        <li key={a.id}>
          {a.table ? (
            <Link
              to="/workspaces/$workspace/mesas/$table"
              params={{ workspace: a.workspace, table: a.table }}
              aria-label={`${a.name}: ${t("agentLink", { table: a.table })}`}
              className="underline"
            >
              {a.name}
            </Link>
          ) : (
            a.name
          )}{" "}
          <span className="text-muted">
            ({a.workspace}
            {a.table ? ` / ${a.table}` : ""} · {a.state})
          </span>
        </li>
      ))}
    </ul>
  );
}

/** The warning of a removal: how many and which agents run there, read before the PIN is asked. */
function Removal({
  m,
  onClose,
  onRemoved,
}: {
  m: GuildMachineRow;
  onClose: () => void;
  onRemoved: (offline: boolean) => Promise<void>;
}) {
  const { t } = useTranslation("guild");
  const errorOf = useGuildError();
  const { data, error } = useQuery({
    queryKey: ["admin", "guild", "removal", m.id],
    queryFn: () =>
      api<GuildRemoval>("GET", `/api/guild/machines/${encodeURIComponent(m.id)}/removal`),
    staleTime: 0,
  });
  return (
    <fieldset className="flex flex-col gap-3 rounded-field border border-danger/40 bg-danger-wash p-4">
      <legend className="px-1 text-lg">{t("removal.title", { name: m.name })}</legend>
      {error ? <Alert>{errorOf(error)}</Alert> : null}
      {data ? (
        <>
          <p>
            {data.agents.length
              ? t("removal.agents", { count: data.agents.length })
              : t("removal.none")}
          </p>
          {data.agents.length ? <AgentList agents={data.agents} /> : null}
          <p className="text-sm">{data.online ? t("removal.online") : t("removal.offline")}</p>
          <div className="flex flex-wrap items-start gap-3">
            <SensitiveButton
              describeError={errorOf}
              variant="danger"
              onConfirm={async (pin) => {
                await api(
                  "POST",
                  `/api/guild/machines/${encodeURIComponent(m.id)}/remove`,
                  { confirm: true },
                  { pin },
                );
                await onRemoved(!data.online);
              }}
            >
              {t("actions.remove")}
            </SensitiveButton>
            <Button variant="quiet" onPress={onClose}>
              {t("removal.cancel")}
            </Button>
          </div>
        </>
      ) : null}
    </fieldset>
  );
}

function Timezone({
  zone,
  say,
  onDone,
}: {
  zone: string;
  say: (s: string) => void;
  onDone: () => Promise<unknown>;
}) {
  const { t } = useTranslation("guild");
  const errorOf = useGuildError();
  const [value, setValue] = useState(zone);
  useEffect(() => setValue(zone), [zone]);
  const zones = useMemo(() => {
    const all =
      typeof Intl.supportedValuesOf === "function" ? Intl.supportedValuesOf("timeZone") : [];
    const list = all.includes("UTC") ? all : ["UTC", ...all];
    return list.includes(zone) ? list : [zone, ...list];
  }, [zone]);
  return (
    <section aria-label={t("timezone.title")} className="flex max-w-xl flex-col gap-3">
      <h2 className="text-xl">{t("timezone.title")}</h2>
      <p className="text-sm text-muted">{t("timezone.intro")}</p>
      <NativeSelect
        label={t("timezone.label")}
        value={value}
        onChange={setValue}
        options={zones.map((z) => ({ value: z, label: z }))}
      />
      <div>
        <SensitiveButton
          describeError={errorOf}
          isDisabled={value === zone}
          onConfirm={async (pin) => {
            await api("PUT", "/api/guild/timezone", { timezone: value }, { pin });
            say(t("timezone.saved", { zone: value }));
            await onDone();
          }}
        >
          {t("timezone.save")}
        </SensitiveButton>
      </div>
    </section>
  );
}
