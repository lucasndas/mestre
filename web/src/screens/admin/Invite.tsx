import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { Checkbox } from "react-aria-components";
import { useTranslation } from "react-i18next";
import { SensitiveButton } from "../../components/PinField";
import { Button, Field } from "../../components/ui";
import type { CreateUserRequest, CreateUserResponse } from "../../gen/panel";
import { api } from "../../lib/api";
import { CA_PATH, caCheckCode, groupHex } from "../../lib/caFingerprint";
import { ChoiceGroup, MultiChoice, ShownOnce } from "./kit";

type Say = (k: string, o?: object) => string;

const ASSIGNABLE = ["administrator", "approver", "observer"] as const;

/**
 * Invite a person: name, level (with what the level allows and does not), campaigns. The PIN
 * field appears only after "Criar convite" and goes in that request
 * (docs/painel/crescer-a-guilda.md#1-convidar-uma-pessoa).
 */
export function InviteForm({
  workspaces,
  onClose,
  onCreated,
}: {
  workspaces: string[];
  onClose: () => void;
  onCreated: (r: CreateUserResponse) => Promise<void>;
}) {
  const { t } = useTranslation("users");
  const say = t as unknown as Say;
  const [name, setName] = useState("");
  const [level, setLevel] = useState<string>("approver");
  const [ws, setWs] = useState<string[]>([]);
  const [gh, setGh] = useState("");
  return (
    <div className="grid max-w-4xl gap-4 rounded-field border border-line bg-surface p-5 md:grid-cols-[minmax(0,1fr)_16rem]">
      <div className="flex min-w-0 flex-col gap-4">
        <h2 className="text-xl">{t("create.title")}</h2>
        <Field label={t("create.name")} value={name} onChange={setName} autoComplete="off" />
        <ChoiceGroup
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
          label={t("create.workspaces")}
          value={ws}
          onChange={setWs}
          options={workspaces}
          empty={t("workspaceNone")}
        />
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
        <div className="flex flex-wrap items-start gap-3">
          <SensitiveButton
            isDisabled={name.trim() === ""}
            onConfirm={async (pin) => {
              const body: CreateUserRequest = {
                name: name.trim(),
                level,
                workspaces: ws,
                github_login: gh.trim(),
              };
              await onCreated(await api<CreateUserResponse>("POST", "/api/users", body, { pin }));
            }}
          >
            {t("create.submit")}
          </SensitiveButton>
          <Button variant="quiet" onPress={onClose}>
            {t("create.close")}
          </Button>
        </div>
      </div>
      <aside
        aria-live="polite"
        aria-label={t("create.previewLabel")}
        className="flex h-fit flex-col gap-2 rounded-field bg-accent-wash p-4 text-sm"
      >
        <p className="text-base font-semibold">
          {t("create.preview", {
            name: name.trim() || t("create.previewAnyone"),
            level: say(`levels.${level}`),
          })}
        </p>
        <p>{say(`levelCan.${level}`)}</p>
        <p>{say(`levelCannot.${level}`)}</p>
        <p className="border-t border-line pt-2">{t("create.pinNote")}</p>
      </aside>
    </div>
  );
}

/** A block of text with its own copy button. */
function CopyBlock({
  text,
  label,
  technical,
}: {
  text: string;
  label: string;
  /** What only the curious need (the folded "Detalhes técnicos"); it is copied too. */
  technical?: string;
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
          {t("invite.copy")}
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
 * The invite in three parts. The check code comes from the certificate that `/ca.crt` serves,
 * computed here, never typed; the first-access code is only in the state of this component and
 * shows after the Guildmaster says the person did the certificate step.
 */
export function InviteDelivery({
  name,
  code,
  expires,
  onDone,
}: {
  name: string;
  code: string;
  expires: string;
  onDone: () => void;
}) {
  const { t } = useTranslation("users");
  const check = useQuery({
    queryKey: ["ca-check-code"],
    queryFn: caCheckCode,
    staleTime: 0,
    gcTime: 0,
    retry: false,
  });
  const [confirmed, setConfirmed] = useState(false);
  const address = window.location.origin;
  const plain = t("invite.instructions", { name, address, path: CA_PATH });
  return (
    <section
      aria-label={t("invite.title", { name })}
      className="mb-6 flex max-w-3xl flex-col gap-5 rounded-field border border-accent bg-accent-wash p-5"
    >
      <div>
        <h2 className="text-xl">{t("invite.title", { name })}</h2>
        <p className="mt-1 text-sm">{t("invite.lead")}</p>
      </div>
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
          <section aria-label={t("invite.part1")} className="flex flex-col gap-2">
            <h3 className="text-lg">{t("invite.part1")}</h3>
            <p className="text-sm">{t("invite.part1Hint")}</p>
            <CopyBlock
              text={plain}
              technical={t("invite.technical")}
              label={`${t("invite.copy")}: ${t("invite.part1")}`}
            />
          </section>
          <section aria-label={t("invite.part2")} className="flex flex-col gap-2">
            <h3 className="text-lg">{t("invite.part2")}</h3>
            <p className="text-sm">{t("invite.part2Hint", { path: CA_PATH })}</p>
            <CopyBlock
              text={groupHex(check.data)}
              label={`${t("invite.copy")}: ${t("invite.part2")}`}
            />
          </section>
          <section aria-label={t("invite.part3")} className="flex flex-col gap-3">
            <h3 className="text-lg">{t("invite.part3")}</h3>
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
                title={t("create.codeTitle", { name })}
                secret={code}
                detail={t("codes.expires", { when: expires })}
                doneLabel={t("invite.codeDone")}
                onDone={onDone}
              />
            ) : (
              <p className="text-sm text-muted">{t("invite.part3Locked")}</p>
            )}
          </section>
        </>
      ) : null}
    </section>
  );
}
