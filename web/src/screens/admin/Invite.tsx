import { useQuery } from "@tanstack/react-query";
import { Link, Navigate, useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { Checkbox } from "react-aria-components";
import { useTranslation } from "react-i18next";
import { useFormatDateTime } from "../../app/session";
import { SensitiveButton } from "../../components/PinField";
import { Button, Field } from "../../components/ui";
import type { CreateUserRequest, CreateUserResponse } from "../../gen/panel";
import { api } from "../../lib/api";
import { CA_PATH, caCheckCode, groupHex } from "../../lib/caFingerprint";
import { setDelivery, useDelivery } from "./inviteStore";
import {
  ChoiceGroup,
  LoadError,
  linkButton,
  MultiChoice,
  PageHeader,
  ShownOnce,
  usersQuery,
  VocabLine,
} from "./kit";

type Say = (k: string, o?: object) => string;

const ASSIGNABLE = ["administrator", "approver", "observer"] as const;

const CARD = "flex max-w-4xl flex-col gap-3 rounded-field border border-line bg-surface p-5";

/**
 * Step 1 of the invite, a page of its own: name, level (with what the level allows and does not),
 * campaigns. The PIN field appears only after "Criar convite" and goes in that request
 * (docs/painel/crescer-a-guilda.md#1-convidar-uma-pessoa).
 */
export function Invite() {
  const { t } = useTranslation("users");
  const say = t as unknown as Say;
  const navigate = useNavigate();
  const { data, error, refetch } = useQuery(usersQuery);
  const [name, setName] = useState("");
  const [level, setLevel] = useState<string>("administrator");
  const [ws, setWs] = useState<string[]>([]);
  const [gh, setGh] = useState("");
  const list = (k: string) => say(k, { returnObjects: true }) as unknown as string[];
  return (
    <section>
      <PageHeader title={t("create.pageTitle")} intro={t("create.pageIntro")} />
      <VocabLine terms={["Nível"]} />
      {error ? (
        <LoadError what={t("loadWhat")} error={error} onRetry={() => void refetch()} />
      ) : null}
      {!data && !error ? <p className="text-muted">{t("loading")}</p> : null}
      {data ? (
        <div className="grid max-w-4xl items-start gap-4 md:grid-cols-[minmax(0,1fr)_18rem]">
          <div className="flex min-w-0 flex-col gap-4">
            <div className={CARD}>
              <Field label={t("create.name")} value={name} onChange={setName} autoComplete="off" />
              <ChoiceGroup
                cards
                label={t("create.level")}
                value={level}
                onChange={setLevel}
                options={ASSIGNABLE.map((l) => ({
                  value: l,
                  label: say(`levels.${l}`),
                  hint: say(`levelHints.${l}`),
                }))}
              />
              <MultiChoice
                cards
                label={t("create.workspaces")}
                value={ws}
                onChange={setWs}
                options={data.workspaces}
                empty={t("workspaceNone")}
              />
              {data.workspaces.length > 0 ? (
                <p className="text-sm text-muted">{t("create.workspacesHint")}</p>
              ) : null}
              <details>
                <summary className="min-h-11 cursor-pointer py-2 font-semibold">
                  {t("create.more")}
                </summary>
                <Field
                  label={t("create.githubLogin")}
                  description={t("create.githubLoginHint")}
                  value={gh}
                  onChange={setGh}
                  autoComplete="off"
                />
              </details>
            </div>
            <div className={`${CARD} sm:flex-row sm:items-start`}>
              <SensitiveButton
                isDisabled={name.trim() === ""}
                onConfirm={async (pin) => {
                  const body: CreateUserRequest = {
                    name: name.trim(),
                    level,
                    workspaces: ws,
                    github_login: gh.trim(),
                  };
                  const r = await api<CreateUserResponse>("POST", "/api/users", body, { pin });
                  setDelivery({
                    name: r.user.name,
                    code: r.code.code,
                    expires: r.code.expires_at,
                  });
                  await navigate({ to: "/usuarios/convite" });
                }}
              >
                {t("create.submit")}
              </SensitiveButton>
              <Link to="/usuarios" className={linkButton}>
                {t("create.close")}
              </Link>
            </div>
          </div>
          <aside
            aria-live="polite"
            aria-label={t("create.previewLabel")}
            className="flex flex-col gap-3 rounded-field border border-line bg-surface p-5"
          >
            <h2 className="text-lg">
              {t("create.preview", {
                name: name.trim() || t("create.previewAnyone"),
                level: say(`levels.${level}`),
              })}
            </h2>
            <div>
              <p className="text-sm font-semibold">{t("create.can")}</p>
              <ul className="mt-1 list-disc pl-5">
                {list(`levelLists.${level}.can`).map((x) => (
                  <li key={x}>{x}</li>
                ))}
              </ul>
            </div>
            <div>
              <p className="text-sm font-semibold">{t("create.cannot")}</p>
              <ul className="mt-1 list-disc pl-5">
                {list(`levelLists.${level}.not`).map((x) => (
                  <li key={x}>{x}</li>
                ))}
              </ul>
            </div>
            <p className="text-sm text-muted">{t("create.pinNote")}</p>
          </aside>
        </div>
      ) : null}
    </section>
  );
}

/** A block of text with its own copy button. */
function CopyBlock({
  text,
  label,
  technical,
  copyLabel,
}: {
  text: string;
  label: string;
  /** What only the curious need (the folded "Detalhes técnicos"); it is copied too. */
  technical?: string;
  /** The visible text of the copy button (the accessible name is `label`). */
  copyLabel: string;
}) {
  const { t } = useTranslation("users");
  const [copied, setCopied] = useState(false);
  return (
    <div className="flex flex-col gap-2">
      <pre className="max-w-full overflow-x-auto rounded-field border border-line bg-paper p-3 font-mono text-sm whitespace-pre-wrap break-words">
        {text}
      </pre>
      {technical ? (
        <details>
          <summary className="min-h-11 cursor-pointer py-2 text-sm">
            {t("invite.technicalTitle")}
          </summary>
          <p className="text-sm">{technical}</p>
        </details>
      ) : null}
      <div className="flex items-center gap-3">
        <Button
          variant="secondary"
          aria-label={label}
          onPress={() => {
            void navigator.clipboard
              ?.writeText(text)
              .then(() => setCopied(true))
              .catch(() => undefined);
          }}
        >
          {copyLabel}
        </Button>
        {copied ? (
          <span role="status" className="text-sm">
            {t("invite.copied")}
          </span>
        ) : null}
      </div>
    </div>
  );
}

/**
 * Step 2 of the invite, a page of its own: three cards. The check code comes from the certificate
 * that `/ca.crt` serves, computed here, never typed; the first-access code lives only in the memory
 * of this tab and shows after the Guildmaster says the person did the certificate step. Without it
 * (a reload) there is nothing to deliver and the screen goes back to the list.
 */
export function InviteDelivery() {
  const { t } = useTranslation("users");
  const fmt = useFormatDateTime();
  const d = useDelivery();
  const check = useQuery({
    queryKey: ["ca-check-code"],
    queryFn: caCheckCode,
    staleTime: 0,
    gcTime: 0,
    retry: false,
    enabled: d !== null,
  });
  const [confirmed, setConfirmed] = useState(false);
  if (!d) return <Navigate to="/usuarios" replace />;
  const address = window.location.origin;
  const plain = t("invite.instructions", { name: d.name, address, path: CA_PATH });
  return (
    <section aria-label={t("invite.title", { name: d.name })}>
      <PageHeader title={t("invite.title", { name: d.name })} intro={t("invite.pageIntro")} />
      <div className="flex max-w-4xl flex-col gap-4">
        {check.isPending ? <p role="status">{t("invite.checking")}</p> : null}
        {check.isError ? (
          <div role="alert" className="flex flex-col gap-3 text-danger">
            <p>{t("invite.checkFailed")}</p>
            <div>
              <Button variant="secondary" onPress={() => void check.refetch()}>
                {t("invite.retry")}
              </Button>
            </div>
          </div>
        ) : null}
        {check.data ? (
          <>
            <section aria-label={t("invite.part1")} className={CARD}>
              <h2 className="text-xl">{t("invite.part1")}</h2>
              <p className="text-sm">{t("invite.part1Hint")}</p>
              <CopyBlock
                text={plain}
                technical={t("invite.technical")}
                copyLabel={t("invite.copyInstructions")}
                label={`${t("invite.copy")}: ${t("invite.part1")}`}
              />
            </section>
            <section aria-label={t("invite.part2")} className={CARD}>
              <h2 className="text-xl">{t("invite.part2")}</h2>
              <p className="text-sm">{t("invite.part2Hint", { path: CA_PATH })}</p>
              <CopyBlock
                text={groupHex(check.data)}
                copyLabel={t("invite.copyCheck")}
                label={`${t("invite.copy")}: ${t("invite.part2")}`}
              />
            </section>
            <section
              aria-label={t("invite.part3")}
              className="flex flex-col gap-3 rounded-field border border-accent bg-accent-wash p-5"
            >
              <h2 className="text-xl">{t("invite.part3")}</h2>
              <p className="text-sm">{t("invite.part3Hint")}</p>
              <Checkbox
                isSelected={confirmed}
                onChange={setConfirmed}
                className="group flex min-h-11 cursor-pointer items-center gap-3"
              >
                <span
                  aria-hidden="true"
                  className="grid size-5 shrink-0 place-items-center rounded-sm border border-muted text-xs text-accent-ink group-data-selected:border-accent group-data-selected:bg-accent"
                >
                  <span className="hidden group-data-selected:block">✓</span>
                </span>
                {t("invite.part3Gate")}
              </Checkbox>
              {confirmed ? (
                <ShownOnce
                  bare
                  title={t("create.codeTitle", { name: d.name })}
                  secret={d.code}
                  detail={t("codes.expires", { when: fmt(d.expires) })}
                  doneLabel={t("invite.codeDone")}
                  onDone={() => setDelivery(null)}
                />
              ) : (
                <p className="text-sm text-muted">{t("invite.part3Locked")}</p>
              )}
            </section>
            <p className="rounded-field border border-line bg-surface p-4 text-sm">
              {t("invite.closing")}
            </p>
          </>
        ) : null}
      </div>
    </section>
  );
}
