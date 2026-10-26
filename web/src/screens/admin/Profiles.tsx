import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { SensitiveButton } from "../../components/PinField";
import { Term } from "../../components/Term";
import { Alert, Button, Field } from "../../components/ui";
import type { ProfileRow, ProfileSetupStatus } from "../../gen/panel";
import { api } from "../../lib/api";
import { errorText } from "../../lib/errors";
import { Badge, NativeSelect, PageHeader } from "./kit";
import { profilesQuery, type Say } from "./manage";

const STEPS = ["install", "login", "hooks", "conformance", "done"] as const;

/** Account profiles of this machine, with the guided setup: a checklist and one button that goes on. */
export function Profiles() {
  const { t } = useTranslation("profiles");
  const { t: te } = useTranslation("errors");
  const qc = useQueryClient();
  const { data, error } = useQuery(profilesQuery);
  const [notice, setNotice] = useState<string | null>(null);
  const refresh = () => qc.invalidateQueries({ queryKey: ["admin"] });

  return (
    <section>
      <PageHeader title={t("title")} intro={t("intro")} />
      <p className="mb-6 max-w-2xl text-sm text-muted">
        <Term name="Perfil de conta" />
        {data ? ` · ${t("machine", { machine: data.machine })}` : null}
      </p>
      <div className="flex max-w-4xl flex-col gap-6">
        {notice ? <Alert tone="ok">{notice}</Alert> : null}
        {error ? <Alert>{errorText(te, error)}</Alert> : null}
        {!data && !error ? <p className="text-muted">{t("loading")}</p> : null}
        {data && !data.setup ? <p className="text-sm text-muted">{t("noSetup")}</p> : null}
        {data && data.profiles.length === 0 ? <p className="text-muted">{t("empty")}</p> : null}
        <ul className="flex flex-col gap-4">
          {data?.profiles.map((p) => (
            <ProfileItem
              key={p.name}
              p={p}
              setup={data.setup}
              onChanged={async (text) => {
                setNotice(text);
                await refresh();
              }}
            />
          ))}
        </ul>
        {data ? (
          <AddProfile
            providers={data.providers}
            onAdded={async (name) => {
              setNotice(t("add.done", { name }));
              await refresh();
            }}
          />
        ) : null}
      </div>
    </section>
  );
}

function ProfileItem({
  p,
  setup,
  onChanged,
}: {
  p: ProfileRow;
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

  return (
    <li className="rounded-field border border-line bg-surface p-4">
      <p className="flex flex-wrap items-center gap-2 text-lg font-semibold">
        {p.name}
        <Badge>{p.provider}</Badge>
        {p.default ? <Badge tone="ok">{t("default")}</Badge> : null}
      </p>
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
            className="font-semibold text-accent underline underline-offset-2"
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
        <SensitiveButton
          variant="danger"
          onConfirm={async (pin) => {
            await api("DELETE", `/api/profiles/${encodeURIComponent(p.name)}`, undefined, { pin });
            await onChanged(t("removed", { name: p.name }));
          }}
        >
          {t("remove", { name: p.name })}
        </SensitiveButton>
      </div>
    </li>
  );
}

function AddProfile({
  providers,
  onAdded,
}: {
  providers: string[];
  onAdded: (name: string) => Promise<void>;
}) {
  const { t } = useTranslation("profiles");
  const [name, setName] = useState("");
  const [provider, setProvider] = useState(providers[0] ?? "");
  return (
    <section
      aria-labelledby="add-profile"
      className="flex max-w-xl flex-col gap-3 border-t border-line pt-6"
    >
      <h2 id="add-profile" className="text-2xl">
        {t("add.title")}
      </h2>
      <Field label={t("add.name")} value={name} onChange={setName} autoComplete="off" />
      {providers.length > 0 ? (
        <NativeSelect
          label={t("add.provider")}
          value={provider}
          onChange={setProvider}
          options={providers.map((x) => ({ value: x, label: x }))}
        />
      ) : null}
      <div>
        <SensitiveButton
          isDisabled={name.trim() === "" || provider === ""}
          onConfirm={async (pin) => {
            await api("POST", "/api/profiles", { name: name.trim(), provider }, { pin });
            const added = name.trim();
            setName("");
            await onAdded(added);
          }}
        >
          {t("add.submit")}
        </SensitiveButton>
      </div>
    </section>
  );
}
