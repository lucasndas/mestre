import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { lazy, Suspense, useEffect, useState } from "react";
import { Checkbox } from "react-aria-components";
import { useTranslation } from "react-i18next";
import { useChatControl } from "../app/chatControl";
import { useCan, useMe } from "../app/session";
import { Term } from "../components/Term";
import { Alert, Button } from "../components/ui";
import type { GuildView, Me, WorkspaceRow } from "../gen/panel";
import { ApiError, api } from "../lib/api";
import { approvalCountQuery } from "../lib/approvalQueries";
import { errorText } from "../lib/errors";
import { useLive, useLiveEvents } from "../lib/live";
import { meQuery } from "../lib/queries";
import { guildQuery } from "../screens/admin/Guild";
import { Badge } from "../screens/admin/kit";
import { WORKSPACES_TOPIC, workspacesQuery } from "../screens/workspaces/queries";
import { Stages } from "../screens/workspaces/Workspaces";
import { useCurrentWorkspace } from "./current";
import { isEnded } from "./model";
import { viewQuery } from "./queries";

// React Flow and xterm.js are most of the weight of the panel: the canvas loads when it is opened
const WorkspaceCanvas = lazy(() =>
  import("./WorkspaceCanvas").then((m) => ({ default: m.WorkspaceCanvas })),
);

type Say = (k: string, o?: object) => string;

/** The screen of a workspace the user may not see (an old link, say): the ones the user may. */
function NoAccess({ workspace, list }: { workspace: string; list: WorkspaceRow[] }) {
  const { t } = useTranslation("workspace");
  return (
    <section className="max-w-2xl">
      <h1 className="text-3xl">{t("noAccess.title")}</h1>
      {list.length === 0 ? (
        <p className="mt-2 text-muted">{t("noAccess.none")}</p>
      ) : (
        <>
          <p className="mt-2 text-muted">{t("noAccess.body", { workspace })}</p>
          <ul className="mt-4 flex flex-col gap-1">
            {list.map((w) => (
              <li key={w.slug}>
                <Link
                  to="/workspaces/$workspace"
                  params={{ workspace: w.slug }}
                  className="inline-flex min-h-11 items-center text-accent underline"
                >
                  {w.slug}
                </Link>
              </li>
            ))}
          </ul>
        </>
      )}
    </section>
  );
}

/** The shortcuts of the screen: the chat, the approvals that wait for the user and the health of the machines. */
function Shortcuts() {
  const { t } = useTranslation("workspace");
  const chat = useChatControl();
  const canDecide = useCan("decide_approvals");
  const canGuild = useCan("manage_machines");
  const { data: count } = useQuery({ ...approvalCountQuery, enabled: canDecide });
  const { data: guild } = useQuery({ ...guildQuery, enabled: canGuild });
  const pending = count?.pending ?? 0;
  const down =
    (guild as GuildView | undefined)?.machines.filter((m) => !m.excluded && !m.online).length ?? 0;
  return (
    <nav aria-label={t("shortcuts.label")} className="mb-4 flex flex-wrap items-center gap-2">
      <Button variant="secondary" aria-expanded={chat.open} onPress={chat.toggle}>
        {t("shortcuts.chat")}
      </Button>
      {canDecide ? (
        <Link
          to="/aprovacoes"
          search={{}}
          className="inline-flex min-h-11 items-center rounded-field border border-line px-3 hover:bg-accent-wash"
        >
          {pending > 0
            ? t("shortcuts.approvals", { count: pending })
            : t("shortcuts.approvalsNone")}
        </Link>
      ) : null}
      {canGuild && guild ? (
        <Link
          to="/guilda"
          className="inline-flex min-h-11 items-center rounded-field border border-line px-3 hover:bg-accent-wash"
        >
          <Badge tone={down > 0 ? "warn" : "ok"}>
            {down > 0 ? t("shortcuts.healthDown", { count: down }) : t("shortcuts.healthOk")}
          </Badge>
        </Link>
      ) : null}
    </nav>
  );
}

/** Remembers the workspace on the Citadel, so it follows the user to any device. */
function useRememberWorkspace(slug: string, ok: boolean) {
  const qc = useQueryClient();
  const me = useMe();
  useEffect(() => {
    if (!ok || !slug || me.user.last_workspace === slug) return;
    void api<Me>("PUT", "/api/me/workspace", { workspace: slug })
      .then((next) => qc.setQueryData(meQuery.queryKey, next))
      .catch(() => {
        // only a convenience: the next screen asks again
      });
  }, [ok, slug, me.user.last_workspace, qc]);
}

/** The workspace (docs/painel/visao-do-workspace.md): the Grand Master at the center and a block for each table. */
export function WorkspaceScreen({ workspace }: { workspace: string }) {
  const { t } = useTranslation("workspace");
  const { t: te } = useTranslation("errors");
  const say = t as unknown as Say;
  const live = useLive();
  const qc = useQueryClient();
  const { list, ready } = useCurrentWorkspace();
  const allowed = list.some((w) => w.slug === workspace);
  const { data: view, error } = useQuery({ ...viewQuery(workspace), enabled: !ready || allowed });
  const canConfigure = useCan("configure_workspace") && !!view?.workspace.can_act;
  const [showEnded, setShowEnded] = useState(false);
  useRememberWorkspace(workspace, !!view);

  // the engine says something changed: the list and every view are read again
  useEffect(() => live.subscribe([WORKSPACES_TOPIC]), [live]);
  useLiveEvents(
    "workspaces:",
    () => void qc.invalidateQueries({ queryKey: workspacesQuery.queryKey }),
  );

  const notFound = error instanceof ApiError && error.status === 404;
  if (ready && (!allowed || notFound)) return <NoAccess workspace={workspace} list={list} />;
  if (error) return <Alert>{errorText(te, error)}</Alert>;
  if (!view)
    return (
      <p role="status" className="text-muted">
        {t("loading")}
      </p>
    );

  const w = view.workspace;
  const ended = view.tables.filter(isEnded).length;
  const preparing = w.state === "preparing";
  return (
    <section>
      <div className="mb-2 flex flex-wrap items-baseline gap-x-3 gap-y-1">
        <h1 className="text-2xl">{t("title", { workspace: w.slug })}</h1>
        <Badge tone={w.state === "active" ? "ok" : w.state === "paused" ? "warn" : "plain"}>
          {say(`state.${w.state}`)}
        </Badge>
        <span className="text-sm text-muted">
          <Term name="Campanha" />: {w.repo}
        </span>
      </div>
      <Shortcuts />
      {preparing ? (
        <div className="max-w-2xl rounded-field border border-line bg-surface p-5">
          <h2 className="text-xl">{t("preparing.title")}</h2>
          <p className="mt-1 text-muted">{t("preparing.body")}</p>
          <Stages w={w} canRetry={canConfigure} />
        </div>
      ) : (
        <>
          {view.tables.length === 0 ? (
            <div className="mb-4 max-w-2xl rounded-field border border-dashed border-line p-4">
              <p className="font-semibold">{t("empty.title")}</p>
              <p className="text-muted">{t("empty.body")}</p>
            </div>
          ) : null}
          {ended > 0 ? (
            <div className="mb-3 flex flex-wrap items-center gap-3">
              <Checkbox
                isSelected={showEnded}
                onChange={setShowEnded}
                className="group flex min-h-11 items-center gap-2"
              >
                <span
                  aria-hidden="true"
                  className="grid size-5 place-items-center rounded border border-ink bg-surface group-data-[selected]:bg-accent group-data-[selected]:text-accent-ink"
                >
                  {showEnded ? "✓" : ""}
                </span>
                {t("ended.show")}
              </Checkbox>
              {!showEnded ? (
                <span className="text-sm text-muted">{t("ended.hidden", { count: ended })}</span>
              ) : null}
            </div>
          ) : null}
          <Suspense
            fallback={
              <p role="status" className="text-muted">
                {t("loading")}
              </p>
            }
          >
            <WorkspaceCanvas view={view} showEnded={showEnded} />
          </Suspense>
        </>
      )}
    </section>
  );
}
