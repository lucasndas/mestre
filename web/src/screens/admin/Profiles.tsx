import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { SensitiveButton } from "../../components/PinField";
import { Alert, Button, Field } from "../../components/ui";
import type { ProfileRow, ProfileSetupStatus, ProfilesPage } from "../../gen/panel";
import { api } from "../../lib/api";
import { errorText } from "../../lib/errors";
import {
  Badge,
  EmptyState,
  LevelGate,
  LoadError,
  NativeSelect,
  PageHeader,
  StatusStrip,
  useReaches,
  VocabLine,
} from "./kit";
import { labelOr, profilesQuery, type Say } from "./manage";

const STEPS = ["install", "login", "hooks", "conformance", "done"] as const;

/** A profile is ready when the guided setup finished it (or the server recorded the end of it). */
const isReady = (p: ProfileRow) => p.step === "done" || Boolean(p.setup_done_at);

/** `claude-code` in `claude-2`: the short name of the provider and the next free number. */
function suggestName(provider: string, profiles: ProfileRow[]): string {
  const short = provider.split("-")[0] || provider;
  const used = new Set(profiles.map((p) => p.name));
  let n = 1;
  while (used.has(`${short}-${n}`)) n++;
  return `${short}-${n}`;
}

/** Account profiles of this machine: the state, a card each with its next step, and the form to add one. */
export function Profiles() {
  const { t } = useTranslation("profiles");
  const say = t as unknown as Say;
  const qc = useQueryClient();
  const { data, error, refetch } = useQuery(profilesQuery);
  const [notice, setNotice] = useState<string | null>(null);
  // the provider whose form is open ("" when closed)
  const [adding, setAdding] = useState("");
  const reaches = useReaches("owner");
  const refresh = () => qc.invalidateQueries({ queryKey: ["admin"] });

  return (
    <section>
      <PageHeader title={t("title")} intro={t("intro")} />
      <VocabLine terms={["Perfil de conta"]} />
      {error ? <LoadError what={t("what")} error={error} onRetry={() => void refetch()} /> : null}
      {!data && !error ? <p className="text-muted">{t("loading")}</p> : null}
      {data && !error ? (
        <div className="flex max-w-4xl flex-col gap-6">
          <Strip data={data} onAdd={() => setAdding(data.providers[0] ?? "")} />
          <p className="max-w-2xl text-sm text-muted">{t("machineNote")}</p>
          {notice ? <Alert tone="ok">{notice}</Alert> : null}
          {data.profiles.length === 0 ? (
            <EmptyState title={t("empty.title")} why={t("empty.why")}>
              <div className="grid w-full gap-3 sm:grid-cols-3">
                {[...data.providers]
                  .sort((a, b) => providerRank(a) - providerRank(b))
                  .map((p) => (
                    <div
                      key={p}
                      className="flex flex-col gap-2 rounded-field border border-line bg-surface p-3"
                    >
                      <p className="font-semibold">{labelOr(say, `providers.${p}`, p)}</p>
                      <p className="text-sm text-muted">{labelOr(say, `providerHelp.${p}`, "")}</p>
                      <Button
                        variant="secondary"
                        isDisabled={!reaches}
                        onPress={() => setAdding(p)}
                      >
                        {t("empty.pick", { provider: labelOr(say, `providers.${p}`, p) })}
                      </Button>
                    </div>
                  ))}
              </div>
            </EmptyState>
          ) : null}
          <ul className="flex flex-col gap-4">
            {data.profiles.map((p) => (
              <ProfileItem
                key={p.name}
                p={p}
                machine={data.machine}
                setup={data.setup}
                onChanged={async (text) => {
                  setNotice(text);
                  await refresh();
                }}
              />
            ))}
          </ul>
          {adding ? (
            <AddProfile
              key={adding}
              providers={data.providers}
              initialProvider={adding}
              profiles={data.profiles}
              onClose={() => setAdding("")}
              onAdded={async (name) => {
                setAdding("");
                setNotice(t("add.done", { name }));
                await refresh();
              }}
            />
          ) : null}
        </div>
      ) : null}
    </section>
  );
}

const PROVIDER_ORDER = ["claude-code", "codex", "antigravity"];
const providerRank = (p: string) => {
  const i = PROVIDER_ORDER.indexOf(p);
  return i < 0 ? PROVIDER_ORDER.length : i;
};

function Strip({ data, onAdd }: { data: ProfilesPage; onAdd: () => void }) {
  const { t } = useTranslation("profiles");
  const total = data.profiles.length;
  const ready = data.profiles.filter(isReady).length;
  const action = (
    <LevelGate need="owner" label={t("strip.action")}>
      <Button onPress={onAdd}>{t("strip.action")}</Button>
    </LevelGate>
  );
  if (total === 0)
    return (
      <StatusStrip
        tone="warn"
        title={t("strip.none")}
        hint={data.setup ? t("strip.noneHint") : `${t("strip.noneHint")} ${t("strip.manualHint")}`}
      >
        {action}
      </StatusStrip>
    );
  if (!data.setup)
    return (
      <StatusStrip title={t("strip.manual", { total })} hint={t("strip.manualHint")}>
        {action}
      </StatusStrip>
    );
  return (
    <StatusStrip
      tone={ready === total ? "ok" : "warn"}
      title={t("strip.ready", { ready, total })}
      hint={t("strip.readyHint")}
    >
      {action}
    </StatusStrip>
  );
}

function ProfileItem({
  p,
  machine,
  setup,
  onChanged,
}: {
  p: ProfileRow;
  machine: string;
  setup: boolean;
  onChanged: (text: string) => Promise<void>;
}) {
  const { t } = useTranslation("profiles");
  const { t: te } = useTranslation("errors");
  const say = t as unknown as Say;
  const [status, setStatus] = useState<ProfileSetupStatus | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [busy, setBusy] = useState(false);
  const step = status?.step ?? p.step;
  const at = STEPS.indexOf(step as (typeof STEPS)[number]);
  const waiting = status ? status.waiting : p.detail;
  const provider = labelOr(say, `providers.${p.provider}`, p.provider);

  return (
    <li className="rounded-field border border-line bg-surface p-4">
      <p className="flex flex-wrap items-center gap-2 text-lg font-semibold">
        {p.name}
        <Badge>{provider}</Badge>
        {p.default ? <Badge tone="ok">{t("default")}</Badge> : null}
        {setup ? (
          step === "done" ? (
            <Badge tone="ok">{t("badge.ready")}</Badge>
          ) : (
            <Badge tone="warn">{t("badge.missing")}</Badge>
          )
        ) : (
          <Badge tone="warn">{t("badge.unchecked")}</Badge>
        )}
      </p>
      {setup ? (
        <ol aria-label={t("steps.title")} className="mt-3 flex flex-col gap-1">
          {STEPS.map((s, i) => {
            const state =
              at < 0
                ? "pending"
                : i < at || step === "done"
                  ? "done"
                  : i === at
                    ? "current"
                    : "pending";
            return (
              <li
                key={s}
                aria-current={state === "current" ? "step" : undefined}
                className="flex flex-wrap items-center gap-2"
              >
                <span aria-hidden="true" className="w-5 text-center">
                  {state === "done" ? "✓" : state === "current" ? "▸" : "·"}
                </span>
                <span className={state === "pending" ? "text-muted" : "font-semibold"}>
                  {say(`steps.${s}`)}
                </span>
                <span className="text-sm text-muted">
                  (
                  {say(
                    state === "done"
                      ? "steps.stateDone"
                      : state === "current"
                        ? "steps.stateCurrent"
                        : "steps.statePending",
                  )}
                  )
                </span>
              </li>
            );
          })}
        </ol>
      ) : (
        <div className="mt-3 text-sm">
          <p className="font-semibold">{t("manual.title")}</p>
          <p>{t("manual.body", { machine, provider })}</p>
          {p.provider.includes("codex") ? <p>{t("manual.codex")}</p> : null}
        </div>
      )}
      {waiting && step !== "done" ? (
        <p className="mt-2 text-sm">{t("waiting", { what: waiting })}</p>
      ) : null}
      {status?.done ? (
        <div className="mt-2">
          <Alert tone="ok">{t("advanced", { name: p.name })}</Alert>
        </div>
      ) : null}
      {status && !status.conformant && status.reason ? (
        <p className="mt-2 text-sm text-danger">{t("notConformant", { reason: status.reason })}</p>
      ) : null}
      {status?.terminal ? (
        <p className="mt-2">
          <Link
            to="/terminal/$character"
            params={{ character: status.terminal }}
            className="inline-flex min-h-11 items-center font-semibold text-accent underline underline-offset-2"
          >
            {t("terminal")}
          </Link>
        </p>
      ) : null}
      {error ? (
        <div className="mt-2">
          <Alert>{errorText(te, error)}</Alert>
        </div>
      ) : null}
      <div className="mt-3 flex flex-wrap items-start gap-3">
        {setup && step !== "done" ? (
          <Button
            isDisabled={busy}
            onPress={async () => {
              setBusy(true);
              setError(null);
              try {
                const r = await api<ProfileSetupStatus>(
                  "POST",
                  `/api/profiles/${encodeURIComponent(p.name)}/advance`,
                );
                setStatus(r);
                if (r.done) await onChanged(t("advanced", { name: p.name }));
              } catch (err) {
                setError(err);
              } finally {
                setBusy(false);
              }
            }}
          >
            {t("advance", { name: p.name })}
          </Button>
        ) : null}
        {p.default ? null : (
          <SensitiveButton
            variant="secondary"
            onConfirm={async (pin) => {
              await api("POST", `/api/profiles/${encodeURIComponent(p.name)}/default`, undefined, {
                pin,
              });
              await onChanged(t("defaultDone", { name: p.name }));
            }}
          >
            {t("makeDefault", { name: p.name })}
          </SensitiveButton>
        )}
      </div>
      <div className="mt-4 flex flex-col gap-2 border-t border-line pt-3">
        <p className="text-sm text-muted">
          <span className="font-semibold">{t("removeTitle")}.</span> {t("removeNote")}
        </p>
        <div>
          <SensitiveButton
            variant="danger"
            onConfirm={async (pin) => {
              await api("DELETE", `/api/profiles/${encodeURIComponent(p.name)}`, undefined, {
                pin,
              });
              await onChanged(t("removed", { name: p.name }));
            }}
          >
            {t("remove", { name: p.name })}
          </SensitiveButton>
        </div>
      </div>
    </li>
  );
}

function AddProfile({
  providers,
  initialProvider,
  profiles,
  onClose,
  onAdded,
}: {
  providers: string[];
  initialProvider: string;
  profiles: ProfileRow[];
  onClose: () => void;
  onAdded: (name: string) => Promise<void>;
}) {
  const { t } = useTranslation("profiles");
  const say = t as unknown as Say;
  const [provider, setProvider] = useState(initialProvider);
  const [name, setName] = useState(suggestName(initialProvider, profiles));
  return (
    <section
      aria-labelledby="add-profile"
      className="flex max-w-xl flex-col gap-3 rounded-field border border-accent p-4"
    >
      <h2 id="add-profile" className="text-2xl">
        {t("add.title")}
      </h2>
      <p className="text-sm text-muted">{t("add.hint")}</p>
      <Field label={t("add.name")} value={name} onChange={setName} autoComplete="off" />
      {providers.length > 0 ? (
        <NativeSelect
          label={t("add.provider")}
          value={provider}
          onChange={(v) => {
            // the suggestion follows the provider while the name is still the suggested one
            if (name === suggestName(provider, profiles)) setName(suggestName(v, profiles));
            setProvider(v);
          }}
          options={providers.map((x) => ({ value: x, label: labelOr(say, `providers.${x}`, x) }))}
        />
      ) : null}
      <div className="flex flex-wrap items-start gap-3">
        <SensitiveButton
          isDisabled={name.trim() === "" || provider === ""}
          onConfirm={async (pin) => {
            await api("POST", "/api/profiles", { name: name.trim(), provider }, { pin });
            await onAdded(name.trim());
          }}
        >
          {t("add.submit")}
        </SensitiveButton>
        <Button variant="quiet" onPress={onClose}>
          {t("add.close")}
        </Button>
      </div>
    </section>
  );
}
