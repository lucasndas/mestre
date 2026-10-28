import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useMe } from "../../app/session";
import { Button } from "../../components/ui";
import { usersQuery } from "../admin/kit";

type Say = (k: string, o?: object) => string;

const key = (id: string) => `mestre.home.welcome.${id}`;

function seen(id: string): boolean {
  try {
    return localStorage.getItem(key(id)) === "1";
  } catch {
    return false;
  }
}

/**
 * The card of the person who just joined: what the level allows, what stays with the Guildmaster,
 * the campaigns and the Telegram. Only the choice to close it is kept, in this browser
 * (docs/painel/crescer-a-guilda.md#1-convidar-uma-pessoa).
 */
export function Welcome() {
  const { t } = useTranslation("users");
  const say = t as unknown as Say;
  const { user } = useMe();
  const [closed, setClosed] = useState(() => seen(user.id));
  if (user.level === "owner" || closed) return null;
  const close = () => {
    try {
      localStorage.setItem(key(user.id), "1");
    } catch {
      // the choice is only a convenience of this browser
    }
    setClosed(true);
  };
  return (
    <section
      aria-label={t("welcome.title", { level: say(`levels.${user.level}`) })}
      className="mt-6 flex max-w-3xl flex-col gap-3 rounded-field border border-accent bg-accent-wash p-5"
    >
      <h2 className="text-xl">{t("welcome.title", { level: say(`levels.${user.level}`) })}</h2>
      <div>
        <h3 className="text-lg">{t("welcome.can")}</h3>
        <ul className="mt-1 flex flex-col gap-1">
          <li className="flex gap-2">
            <span aria-hidden="true">✓</span>
            <span>{say(`welcome.can_${user.level}`)}</span>
          </li>
        </ul>
      </div>
      <div>
        <h3 className="text-lg">{t("welcome.cannot")}</h3>
        <ul className="mt-1 flex flex-col gap-1">
          <li className="flex gap-2">
            <span aria-hidden="true">–</span>
            <span>{say(`welcome.cannot_${user.level}`)}</span>
          </li>
        </ul>
      </div>
      <p>
        {user.workspaces.length > 0
          ? t("welcome.workspaces", { list: user.workspaces.join(", ") })
          : t("welcome.noWorkspaces")}
      </p>
      <div className="flex flex-wrap items-center justify-between gap-3 rounded-field border border-line bg-surface p-3">
        <p>{t("welcome.telegram")}</p>
        <Link
          to="/configuracoes"
          className="inline-flex min-h-11 items-center rounded-field border border-line px-4 font-semibold hover:bg-accent-wash"
        >
          {t("welcome.telegramLink")}
        </Link>
      </div>
      <div>
        <Button onPress={close}>{t("welcome.done")}</Button>
      </div>
    </section>
  );
}

/** For the Guildmaster alone in the Guild: a line that leads to inviting someone. */
export function InviteRow() {
  const { t } = useTranslation("users");
  const users = useQuery(usersQuery);
  if (!users.data || users.data.users.length > 1) return null;
  return (
    <section
      aria-label={t("welcome.invite")}
      className="mt-6 flex max-w-3xl flex-wrap items-center justify-between gap-3 rounded-field border border-line bg-surface p-4"
    >
      <p className="min-w-0 flex-1">{t("welcome.inviteHint")}</p>
      <Link
        to="/usuarios"
        className="inline-flex min-h-11 items-center justify-center rounded-field border border-line bg-surface px-4 font-semibold hover:bg-accent-wash"
      >
        {t("welcome.invite")}
      </Link>
    </section>
  );
}
