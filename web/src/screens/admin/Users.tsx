import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useFormatDateTime, useMe } from "../../app/session";
import { SensitiveButton } from "../../components/PinField";
import { Alert, Button, Field } from "../../components/ui";
import type {
  AccessCodeResponse,
  EditUserRequest,
  PairingCodeResponse,
  UserRow,
} from "../../gen/panel";
import { api } from "../../lib/api";
import { errorText } from "../../lib/errors";
import { InviteDelivery, InviteForm } from "./Invite";
import {
  Badge,
  ChoiceGroup,
  EmptyState,
  LoadError,
  MultiChoice,
  PageHeader,
  ShownOnce,
  StatusStrip,
  usersQuery,
  VocabLine,
} from "./kit";

type Say = (k: string, o?: object) => string;

interface Delivery {
  name: string;
  code: string;
  expires: string;
}

interface Shown {
  title: string;
  secret: string;
  detail: string;
}

const ASSIGNABLE = ["administrator", "approver", "observer"] as const;

export function Users() {
  const { t } = useTranslation("users");
  const say = t as unknown as Say;
  const me = useMe();
  const fmt = useFormatDateTime();
  const qc = useQueryClient();
  const { data, error, refetch } = useQuery(usersQuery);
  const [creating, setCreating] = useState(false);
  const [managing, setManaging] = useState<string | null>(null);
  const [shown, setShown] = useState<Shown | null>(null);
  const [invite, setInvite] = useState<Delivery | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const refresh = () => qc.invalidateQueries({ queryKey: ["admin"] });
  const showCode = (title: string, c: { code: string; expires_at: string }) =>
    setShown({ title, secret: c.code, detail: t("codes.expires", { when: fmt(c.expires_at) }) });
  const people = data?.users ?? [];
  const alone = people.length <= 1;
  const waiting = people.filter((u) => u.level !== "owner" && !u.login).length;
  const openButton =
    creating || invite ? null : (
      <Button onPress={() => setCreating(true)}>{t("create.open")}</Button>
    );

  return (
    <section>
      <PageHeader title={t("title")} intro={t("intro")} />
      <VocabLine terms={["Nível", "PIN"]} />
      {data ? (
        <StatusStrip
          tone={waiting > 0 ? "warn" : "neutral"}
          title={alone ? t("stripAlone") : t("strip", { count: people.length })}
          hint={
            alone
              ? t("stripAloneHint")
              : waiting > 0
                ? t("stripWaiting", { count: waiting })
                : t("stripHint")
          }
        >
          {openButton}
        </StatusStrip>
      ) : null}
      {invite ? <InviteDelivery {...invite} onDone={() => setInvite(null)} /> : null}
      {shown ? <ShownOnce {...shown} onDone={() => setShown(null)} /> : null}
      <div className="flex max-w-3xl flex-col gap-4">
        {notice ? <Alert tone="ok">{notice}</Alert> : null}
        {error ? (
          <LoadError what={t("loadWhat")} error={error} onRetry={() => void refetch()} />
        ) : null}
        {!data && !error ? <p className="text-muted">{t("loading")}</p> : null}
        {data?.transfer && !data.transfer.to_me ? (
          <div className="flex flex-col gap-3 rounded-field border border-line bg-surface p-4">
            <p>
              {t("transfer.waiting", {
                name: data.transfer.to_name,
                when: fmt(data.transfer.expires_at),
              })}
            </p>
            <div>
              <SensitiveButton
                variant="secondary"
                onConfirm={async (pin) => {
                  await api("DELETE", "/api/transfer", undefined, { pin });
                  setNotice(t("transfer.cancelled"));
                  await refresh();
                }}
              >
                {t("transfer.cancel")}
              </SensitiveButton>
            </div>
          </div>
        ) : null}
        {data && creating ? (
          <InviteForm
            workspaces={data.workspaces}
            onClose={() => setCreating(false)}
            onCreated={async (r) => {
              setCreating(false);
              setNotice(null);
              setInvite({ name: r.user.name, code: r.code.code, expires: fmt(r.code.expires_at) });
              await refresh();
            }}
          />
        ) : null}
        {data && alone && !creating && !invite ? (
          <EmptyState
            title={t("emptyTitle")}
            why={t("empty")}
            steps={[t("emptyStep1"), t("emptyStep2"), t("emptyStep3")]}
          >
            {openButton}
          </EmptyState>
        ) : null}
        <ul className="flex flex-col gap-3">
          {people.map((u) => (
            <li key={u.id} className="rounded-field border border-line bg-surface p-4">
              <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
                <div className="min-w-0 flex-1">
                  <p className="flex flex-wrap items-center gap-2 font-semibold">
                    {u.name}
                    <Badge>{say(`levels.${u.level}`)}</Badge>
                    {u.state === "suspended" ? (
                      <Badge tone="danger">{t("states.suspended")}</Badge>
                    ) : null}
                    {u.level !== "owner" && !u.login ? (
                      <Badge tone="warn">{t("notEntered")}</Badge>
                    ) : null}
                    {u.id === me.user.id ? <Badge tone="ok">{t("you")}</Badge> : null}
                  </p>
                  <p className="text-sm text-muted">
                    {u.login ? t("loginIs", { login: u.login }) : t("loginNone")} ·{" "}
                    {u.level === "owner"
                      ? ""
                      : `${u.workspaces.join(", ") || t("noWorkspaces")} · `}
                    {u.telegram ? t("telegramPaired") : t("telegramNot")}
                  </p>
                  <p className="text-sm text-muted">{githubText(say, u, fmt)}</p>
                </div>
                <Button
                  variant="secondary"
                  aria-expanded={managing === u.id}
                  aria-label={t("manageNamed", { name: u.name })}
                  onPress={() => setManaging(managing === u.id ? null : u.id)}
                >
                  {t("manage")}
                </Button>
              </div>
              {managing === u.id ? (
                <ManageUser
                  key={u.id}
                  u={u}
                  self={u.id === me.user.id}
                  workspaces={data?.workspaces ?? []}
                  hasTransfer={Boolean(data?.transfer)}
                  onNotice={(s) => {
                    setShown(null);
                    setNotice(s);
                  }}
                  onCode={showCode}
                  onChanged={refresh}
                />
              ) : null}
            </li>
          ))}
        </ul>
      </div>
    </section>
  );
}

function githubText(say: Say, u: UserRow, fmt: (iso: string) => string): string {
  const g = u.github;
  if (g.state === "connected")
    return say("githubState.connected", {
      login: g.login,
      until: g.expires_at ? fmt(g.expires_at) : "",
    });
  if (g.state === "expired") return say("githubState.expired", { login: g.login });
  if (u.github_login) return say("githubState.none", { login: u.github_login });
  return say("githubState.noLogin");
}

function ManageUser({
  u,
  self,
  workspaces,
  hasTransfer,
  onNotice,
  onCode,
  onChanged,
}: {
  u: UserRow;
  self: boolean;
  workspaces: string[];
  hasTransfer: boolean;
  onNotice: (s: string) => void;
  onCode: (title: string, c: { code: string; expires_at: string }) => void;
  onChanged: () => Promise<unknown>;
}) {
  const { t } = useTranslation("users");
  const say = t as unknown as Say;
  const [name, setName] = useState(u.name);
  const [level, setLevel] = useState(u.level);
  const [ws, setWs] = useState(u.workspaces);
  const [gh, setGh] = useState(u.github_login);
  const owner = u.level === "owner";
  const post = async (path: string, pin: string, body?: unknown) =>
    api("POST", `/api/users/${u.id}/${path}`, body, { pin });
  const save = async (pin: string) => {
    const body: EditUserRequest = {};
    if (name.trim() !== u.name) body.name = name.trim();
    if (level !== u.level) body.level = level;
    if ([...ws].sort().join() !== [...u.workspaces].sort().join()) body.workspaces = ws;
    if (gh.trim() !== u.github_login) body.github_login = gh.trim();
    await api("PATCH", `/api/users/${u.id}`, body, { pin });
    onNotice(t("edit.saved"));
    await onChanged();
  };
  const code = async (kind: "first" | "reset", pin: string) => {
    const c = await post("access-code", pin, { kind });
    onCode(
      t(kind === "first" ? "create.codeTitle" : "codes.resetCodeTitle", { name: u.name }),
      c as AccessCodeResponse,
    );
    await onChanged();
  };
  return (
    <div className="mt-4 flex flex-col gap-6 border-t border-line pt-4">
      <div className="flex max-w-xl flex-col gap-4">
        <h3 className="text-lg">{t("edit.title")}</h3>
        <Field label={t("create.name")} value={name} onChange={setName} autoComplete="off" />
        {owner ? null : (
          <>
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
            {level !== u.level ? (
              <p className="text-sm text-muted">{t("edit.levelChange", { name: u.name })}</p>
            ) : null}
            <MultiChoice
              label={t("create.workspaces")}
              value={ws}
              onChange={setWs}
              options={workspaces}
              empty={t("workspaceNone")}
            />
          </>
        )}
        <Field label={t("create.githubLogin")} value={gh} onChange={setGh} autoComplete="off" />
        <div>
          <SensitiveButton isDisabled={name.trim() === ""} onConfirm={save}>
            {t("edit.save")}
          </SensitiveButton>
        </div>
      </div>

      <div className="flex max-w-xl flex-col gap-3">
        <h3 className="text-lg">{t("codes.title")}</h3>
        {u.login ? (
          <Action hint={t("codes.resetHint")}>
            <SensitiveButton variant="secondary" onConfirm={(pin) => code("reset", pin)}>
              {t("codes.reset")}
            </SensitiveButton>
          </Action>
        ) : (
          <Action hint={t("codes.firstHint")}>
            <SensitiveButton variant="secondary" onConfirm={(pin) => code("first", pin)}>
              {t("codes.first")}
            </SensitiveButton>
          </Action>
        )}
        <Action hint={t("codes.telegramHint")}>
          <SensitiveButton
            variant="secondary"
            onConfirm={async (pin) => {
              const c = (await post("pairing-code", pin)) as PairingCodeResponse;
              onCode(t("codes.telegramCodeTitle", { name: u.name }), c);
            }}
          >
            {t("codes.telegram")}
          </SensitiveButton>
        </Action>
      </div>

      {self ? null : (
        <div className="flex max-w-xl flex-col gap-3">
          <h3 className="text-lg">{t("state.title")}</h3>
          {u.state === "active" ? (
            <Action hint={t("state.suspendHint")}>
              <SensitiveButton
                variant="secondary"
                onConfirm={async (pin) => {
                  await post("suspend", pin);
                  onNotice(t("state.suspended"));
                  await onChanged();
                }}
              >
                {t("state.suspend")}
              </SensitiveButton>
            </Action>
          ) : (
            <Action>
              <SensitiveButton
                variant="secondary"
                onConfirm={async (pin) => {
                  await post("reactivate", pin);
                  onNotice(t("state.reactivated"));
                  await onChanged();
                }}
              >
                {t("state.reactivate")}
              </SensitiveButton>
            </Action>
          )}
          <Action hint={t("state.removeHint")}>
            <SensitiveButton
              variant="danger"
              onConfirm={async (pin) => {
                await post("remove", pin);
                onNotice(t("state.removed"));
                await onChanged();
              }}
            >
              {t("state.remove")}
            </SensitiveButton>
          </Action>
          {u.state === "active" && !hasTransfer ? (
            <>
              <h3 className="mt-2 text-lg">{t("transfer.title")}</h3>
              <Action
                hint={
                  u.has_pin ? t("transfer.startHint") : t("transfer.needsPin", { name: u.name })
                }
              >
                <SensitiveButton
                  variant="danger"
                  isDisabled={!u.has_pin}
                  onConfirm={async (pin) => {
                    await api("POST", "/api/transfer", { to: u.id }, { pin });
                    onNotice(t("transfer.started", { name: u.name }));
                    await onChanged();
                  }}
                >
                  {t("transfer.start")}
                </SensitiveButton>
              </Action>
            </>
          ) : null}
        </div>
      )}
    </div>
  );
}

function Action({ hint, children }: { hint?: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-1">
      {children}
      {hint ? <p className="text-sm text-muted">{hint}</p> : null}
    </div>
  );
}
