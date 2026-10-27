import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";
import { useTranslation } from "react-i18next";
import { useMe } from "../app/session";
import { useLive, useLiveEvents } from "../lib/live";
import { useCurrentWorkspace } from "../workspace/current";
import { Deliveries } from "./home/Deliveries";
import { FirstSteps, useIsGuildmaster } from "./home/FirstSteps";
import { Health } from "./home/Health";
import { Now } from "./home/Now";
import { useHomeTables } from "./home/reads";
import { TalkButton } from "./home/TalkButton";
import { Waiting } from "./home/Waiting";
import { WORKSPACES_TOPIC, workspacesQuery } from "./workspaces/queries";

/** Início: what is happening now (docs/painel/primeira-jornada.md#início-no-dia-a-dia). Only composes. */
export function Overview() {
  const { t } = useTranslation("home");
  const me = useMe();
  const live = useLive();
  const qc = useQueryClient();
  const guildmaster = useIsGuildmaster();
  const { isError } = useQuery(workspacesQuery);
  const { list, ready } = useCurrentWorkspace();
  const tables = useHomeTables(list);

  // a table born or closed is a change of this topic: the list and the views are read again
  useEffect(() => live.subscribe([WORKSPACES_TOPIC]), [live]);
  useLiveEvents(
    "workspaces:",
    () => void qc.invalidateQueries({ queryKey: workspacesQuery.queryKey }),
  );

  // with no campaign there is nothing to say about tables: the button above says what to do
  const none = ready && list.length === 0;
  return (
    <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_20rem] lg:items-start">
      <section className="max-w-3xl">
        <h1 className="text-3xl">{t("title")}</h1>
        <p className="mt-2 text-muted">{t("hello", { name: me.user.name })}</p>
        {guildmaster ? <FirstSteps /> : null}
        <TalkButton />
        {none ? null : <Now tables={tables.open} failed={isError ? [] : tables.failed} />}
        <Waiting tables={tables.open} />
        <Deliveries tables={tables.delivered} />
        <p className="mt-10 text-sm text-muted">{t("times", { zone: me.timezone })}</p>
      </section>
      <Health />
    </div>
  );
}
