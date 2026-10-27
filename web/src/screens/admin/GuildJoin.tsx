import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { Checkbox } from "react-aria-components";
import { useTranslation } from "react-i18next";
import { useFormatDateTime } from "../../app/session";
import { SensitiveButton } from "../../components/PinField";
import { Term } from "../../components/Term";
import { Alert, Field } from "../../components/ui";
import type { GuildMachineRow, GuildPendingJoin } from "../../gen/panel";
import { api } from "../../lib/api";
import { Badge } from "./kit";

const DIGITS = /\D/g;

/** The card of the journey "add a machine": the four steps, two of them still to come. */
export function AddMachineCard({ single }: { single: boolean }) {
  const { t } = useTranslation("guild");
  const steps: { key: string; soon: boolean }[] = [
    { key: "install", soon: true },
    { key: "accept", soon: false },
    { key: "conclude", soon: true },
    { key: "check", soon: false },
  ];
  return (
    <section
      aria-label={t(single ? "add.titleFirst" : "add.title")}
      className="flex max-w-3xl flex-col gap-3 rounded-field border border-dashed border-muted p-5"
    >
      <h2 className="text-xl">{t(single ? "add.titleFirst" : "add.title")}</h2>
      <p>{t("add.why")}</p>
      <ol className="flex flex-col gap-2">
        {steps.map((s, i) => (
          <li key={s.key} className="flex flex-wrap items-baseline gap-x-2">
            <span className="font-semibold">{i + 1}.</span>
            <span>{t(`add.steps.${s.key}` as "add.steps.install")}</span>
            {s.soon ? <Badge tone="warn">{t("add.soon")}</Badge> : null}
          </li>
        ))}
      </ol>
      <p className="text-sm text-muted">{t("add.where")}</p>
    </section>
  );
}

/**
 * The requests to enter. The panel never shows the code of the request: the Guildmaster types
 * the six digits the new machine shows, and only a match turns on "Aceitar" (the code is computed
 * on both sides; the PIN says it is them who decides).
 */
export function PendingJoins({
  joins,
  say,
  describeError,
  onDone,
}: {
  joins: GuildPendingJoin[];
  say: (s: string) => void;
  describeError: (err: unknown) => string;
  onDone: () => Promise<unknown>;
}) {
  const { t } = useTranslation("guild");
  return (
    <section aria-label={t("pending.title")} className="flex flex-col gap-3">
      <h2 className="text-xl">{t("pending.title")}</h2>
      {joins.length > 1 ? <Alert>{t("pending.many", { count: joins.length })}</Alert> : null}
      <p className="text-sm text-muted">
        <Term name="Código de entrada" />: {t("pending.source")}
      </p>
      <ul className="flex flex-col gap-3">
        {joins.map((j) => (
          <PendingJoin key={j.id} j={j} say={say} describeError={describeError} onDone={onDone} />
        ))}
      </ul>
    </section>
  );
}

function PendingJoin({
  j,
  say,
  describeError,
  onDone,
}: {
  j: GuildPendingJoin;
  say: (s: string) => void;
  describeError: (err: unknown) => string;
  onDone: () => Promise<unknown>;
}) {
  const { t } = useTranslation("guild");
  const fmt = useFormatDateTime();
  const [typed, setTyped] = useState("");
  const [excluded, setExcluded] = useState(false);
  const digits = typed.replace(DIGITS, "");
  const wanted = (j.code ?? "").replace(DIGITS, "");
  const match = wanted.length === 6 && digits === wanted;
  const wrong = Boolean(j.code) && digits.length >= 6 && !match;
  const decide = async (pin: string, accept: boolean) => {
    if (accept && !match) return; // the check is only here: never accept without it
    await api(
      "POST",
      `/api/guild/joins/${encodeURIComponent(j.id)}/decide`,
      { accept, excluded: accept && excluded },
      { pin },
    );
    say(t(accept ? "pending.accepted" : "pending.refused", { name: j.name }));
    await onDone();
  };
  return (
    <li className="flex flex-col gap-3 rounded-field border border-accent bg-accent-wash p-4">
      <p className="font-semibold">
        {j.name} <span className="text-sm font-normal">· {fmt(j.created_at)}</span>
      </p>
      {j.code ? (
        <>
          <Field
            label={t("pending.typeLabel", { name: j.name })}
            description={t("pending.typeHint")}
            inputMode="numeric"
            autoComplete="off"
            value={typed}
            onChange={setTyped}
          />
          {match ? <Alert tone="ok">{t("pending.match")}</Alert> : null}
          {wrong ? <Alert>{t("pending.noMatch")}</Alert> : null}
          <Checkbox
            isSelected={excluded}
            onChange={setExcluded}
            className="group flex min-h-11 cursor-pointer items-start gap-3"
          >
            <span
              aria-hidden="true"
              className="mt-0.5 grid size-5 shrink-0 place-items-center rounded-sm border border-muted text-xs text-accent-ink group-data-selected:border-accent group-data-selected:bg-accent"
            >
              <span className="hidden group-data-selected:block">✓</span>
            </span>
            <span>
              <Term name="Máquina excluída" /> · {t("pending.excludedLabel")}
              <span className="block text-sm">{t("pending.excludedWarn")}</span>
            </span>
          </Checkbox>
        </>
      ) : (
        <p>{t("pending.noCode")}</p>
      )}
      <div className="flex flex-wrap items-start gap-3">
        {j.code ? (
          <SensitiveButton
            describeError={describeError}
            isDisabled={!match}
            onConfirm={(pin) => decide(pin, true)}
          >
            {t(excluded ? "pending.acceptExcluded" : "pending.accept")}
          </SensitiveButton>
        ) : null}
        <SensitiveButton
          describeError={describeError}
          variant="danger"
          onConfirm={(pin) => decide(pin, false)}
        >
          {t("pending.refuse")}
        </SensitiveButton>
      </div>
    </li>
  );
}

/**
 * What to check on a Tower that just came in. The Citadel cannot read the AI accounts of another
 * machine, so that step is "não sei" with a link, never "feito".
 */
export function TowerCheck({ m }: { m: GuildMachineRow }) {
  const { t } = useTranslation("guild");
  return (
    <section
      aria-label={t("check.title", { name: m.name })}
      className="flex flex-col gap-2 rounded-field border border-line bg-paper p-3"
    >
      <h3 className="text-lg">{t("check.title", { name: m.name })}</h3>
      <ul className="flex flex-col gap-1 text-sm">
        <li>
          <span aria-hidden="true">{m.online ? "✓" : "▲"} </span>
          {m.online ? t("check.connected") : t("check.notConnected")}
        </li>
        <li>
          <span aria-hidden="true">? </span>
          {t("check.accounts")}{" "}
          <Link to="/perfis" className="underline">
            {t("check.accountsLink")}
          </Link>
        </li>
      </ul>
    </section>
  );
}
