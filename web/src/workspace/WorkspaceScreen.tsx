import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { lazy, Suspense, useEffect, useMemo, useState, useSyncExternalStore } from "react";
import { useTranslation } from "react-i18next";
import { useChatControl } from "../app/chatControl";
import { useCan, useFormatDateTime, useMe } from "../app/session";
import { Term } from "../components/Term";
import { Alert, Button } from "../components/ui";
import type { GuildView, Me, WorkspaceRow, WorkspaceView } from "../gen/panel";
import { ApiError, api } from "../lib/api";
import { approvalCountQuery, noticesQuery } from "../lib/approvalQueries";
import { errorText } from "../lib/errors";
import { useLive, useLiveEvents } from "../lib/live";
import { meQuery } from "../lib/queries";
import { guildQuery } from "../screens/admin/Guild";
import { Badge } from "../screens/admin/kit";
import { WORKSPACES_TOPIC, workspacesQuery } from "../screens/workspaces/queries";
import { Stages } from "../screens/workspaces/Workspaces";
import { useCurrentWorkspace } from "./current";
import { type BuildOptions, isEnded } from "./model";
import { NeedsYouBand, type Pending } from "./NeedsYouBand";
import { viewQuery } from "./queries";
import { FILTERS, type FilterKey, matchesFilter, RANK, situationOf } from "./situation";
import { TableBlockCard } from "./TableBlockCard";
import { TableFilters, type ViewMode } from "./TableFilters";
import { TableToasts, useTableToasts } from "./TableToasts";

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

/** True on a screen as wide as the canvas needs (the md of the panel); a test with no matchMedia is a wide screen. */
function useWide(): boolean {
  const query = "(min-width: 768px)";
  return useSyncExternalStore(
    (notify) => {
      const m = window.matchMedia?.(query);
      m?.addEventListener("change", notify);
      return () => m?.removeEventListener("change", notify);
    },
    () => window.matchMedia?.(query).matches ?? true,
    () => true,
  );
}

/** The Grand Master in the list of a narrow screen (the terminal lives in the canvas). */
function GmListCard({ view }: { view: WorkspaceView }) {
  const { t } = useTranslation("workspace");
  const chat = useChatControl();
  const gm = view.grand_master;
  return (
    <article
      aria-label={gm ? t("gm.node", { name: gm.name }) : t("gm.none")}
      className="flex flex-wrap items-center gap-x-3 gap-y-2 rounded-field border-2 border-ink bg-surface p-3 shadow-card"
    >
      <h3 className="m-0 text-lg">{gm ? gm.name : <Term name="Grão-Mestre" />}</h3>
      {gm ? (
        <span className="rounded-full bg-accent-wash px-2 text-sm">
          {t(`gm.states.${gm.state}`, { defaultValue: gm.state })}
        </span>
      ) : null}
      {gm ? (
        <span className="text-sm text-muted">{t("gm.machine", { machine: gm.machine_name })}</span>
      ) : null}
      <Button variant="secondary" className="ml-auto" onPress={chat.show}>
        {t("shortcuts.chat")}
      </Button>
    </article>
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
    <nav aria-label={t("shortcuts.label")} className="flex flex-wrap items-center gap-2">
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

/** What the summary under the title says: how many tables are going, how many need the user and how many are in review. */
function Summary({ live, need, review }: { live: number; need: number; review: number }) {
  const { t } = useTranslation("workspace");
  if (live === 0) return <p className="m-0 text-muted">{t("life.summary.none")}</p>;
  const parts: string[] = [t("life.summary.live", { count: live })];
  if (need > 0) parts.push(t("life.summary.need", { count: need }));
  if (review > 0) parts.push(t("life.summary.review", { count: review }));
  return (
    <p className="m-0 text-muted" aria-live="polite">
      {parts.join(" · ")}
    </p>
  );
}

/** The workspace (docs/painel/visao-do-workspace.md, docs/painel/vida-da-mesa.md): the Grand Master and a card for each table. */
export function WorkspaceScreen({ workspace }: { workspace: string }) {
  const { t } = useTranslation("workspace");
  const { t: te } = useTranslation("errors");
  const say = t as unknown as Say;
  const live = useLive();
  const qc = useQueryClient();
  const wide = useWide();
  const { list, ready } = useCurrentWorkspace();
  const allowed = list.some((w) => w.slug === workspace);
  const {
    data: view,
    error,
    dataUpdatedAt,
    refetch,
    isFetching,
  } = useQuery({ ...viewQuery(workspace), enabled: !ready || allowed });
  const canConfigure = useCan("configure_workspace") && !!view?.workspace.can_act;
  const canDecide = useCan("decide_approvals");
  const fmt = useFormatDateTime();
  const { data: notices } = useQuery(noticesQuery);
  const [showEnded, setShowEnded] = useState(false);
  const [filter, setFilter] = useState<FilterKey>("all");
  const [mode, setMode] = useState<ViewMode>("board");
  useRememberWorkspace(workspace, !!view);
  const { toasts, dismiss } = useTableToasts(view, canDecide);

  // the engine says something changed: the list and every view are read again
  useEffect(() => live.subscribe([WORKSPACES_TOPIC]), [live]);
  useLiveEvents(
    "workspaces:",
    () => void qc.invalidateQueries({ queryKey: workspacesQuery.queryKey }),
  );

  const newNotices = useMemo(() => {
    const out = new Map<string, number>();
    for (const n of notices?.notices ?? []) {
      if (n.read || n.workspace !== workspace || !n.table) continue;
      out.set(n.table, (out.get(n.table) ?? 0) + 1);
    }
    return out;
  }, [notices, workspace]);
  const build = useMemo<BuildOptions>(
    () => ({
      canDecide,
      newNotices,
      visible: (b) => matchesFilter(situationOf(b, canDecide), filter),
    }),
    [canDecide, newNotices, filter],
  );

  const notFound = error instanceof ApiError && error.status === 404;
  if (ready && (!allowed || notFound)) return <NoAccess workspace={workspace} list={list} />;
  if (error && !view) return <Alert>{errorText(te, error)}</Alert>;
  if (!view)
    return (
      <p role="status" className="text-muted">
        {t("loading")}
      </p>
    );

  const w = view.workspace;
  const ended = view.tables.filter(isEnded).length;
  const preparing = w.state === "preparing";
  const rows = view.tables.map((block) => ({ block, situation: situationOf(block, canDecide) }));
  const going = rows.filter((r) => !isEnded(r.block));
  const pending: Pending[] = going
    .filter((r) => r.situation.urgent)
    .sort((a, b) => RANK[a.situation.key] - RANK[b.situation.key]);
  const counts = Object.fromEntries(
    FILTERS.map((f) => [f, going.filter((r) => matchesFilter(r.situation, f)).length]),
  ) as Record<FilterKey, number>;
  const shown = (showEnded ? rows : going)
    .filter((r) => matchesFilter(r.situation, filter))
    .sort((a, b) => RANK[a.situation.key] - RANK[b.situation.key]);
  const board = wide && mode === "board";
  return (
    <section>
      <div className="mb-3 flex flex-wrap items-start justify-between gap-x-6 gap-y-3">
        <div>
          <div className="mb-2 flex flex-wrap items-baseline gap-x-3 gap-y-1">
            <h1 className="text-2xl">{t("title", { workspace: w.slug })}</h1>
            <Badge tone={w.state === "active" ? "ok" : w.state === "paused" ? "warn" : "plain"}>
              {say(`state.${w.state}`)}
            </Badge>
            <span className="text-sm text-muted">
              <Term name="Campanha" />: {w.repo}
            </span>
          </div>
          {preparing ? null : (
            <Summary
              live={going.length}
              need={pending.length}
              review={going.filter((r) => r.situation.key === "review").length}
            />
          )}
        </div>
        <Shortcuts />
      </div>
      {error ? (
        <div
          role="alert"
          className="mb-4 flex flex-wrap items-center gap-3 rounded-field border border-danger bg-danger-wash p-3"
        >
          <p className="m-0 flex-1">
            {t("life.read.failed", { time: fmt(new Date(dataUpdatedAt).toISOString()) })}
          </p>
          <Button isDisabled={isFetching} onPress={() => void refetch()}>
            {t("life.read.retry")}
          </Button>
        </div>
      ) : null}
      {preparing ? (
        <div className="max-w-2xl rounded-field border border-line bg-surface p-5">
          <h2 className="text-xl">{t("preparing.title")}</h2>
          <p className="mt-1 text-muted">{t("preparing.body")}</p>
          <Stages w={w} canRetry={canConfigure} />
        </div>
      ) : (
        <>
          <div className="mb-4">
            <NeedsYouBand items={pending} workspace={w.slug} />
          </div>
          {view.tables.length === 0 ? (
            <div className="mb-4 max-w-2xl rounded-field border border-dashed border-line p-4">
              <p className="font-semibold">{t("empty.title")}</p>
              <p className="text-muted">{t("empty.body")}</p>
            </div>
          ) : (
            <div className="mb-3 flex flex-col gap-2">
              <TableFilters
                counts={counts}
                filter={filter}
                onFilter={setFilter}
                view={mode}
                onView={setMode}
                wide={wide}
                ended={ended}
                showEnded={showEnded}
                onShowEnded={setShowEnded}
              />
            </div>
          )}
          {board ? (
            <Suspense
              fallback={
                <p role="status" className="text-muted">
                  {t("loading")}
                </p>
              }
            >
              <WorkspaceCanvas view={view} showEnded={showEnded} build={build} />
            </Suspense>
          ) : (
            <div className="flex flex-col gap-3">
              <GmListCard view={view} />
              {shown.length === 0 && view.tables.length > 0 ? (
                <div className="rounded-field border border-dashed border-line p-4">
                  <p className="font-semibold">{t("life.filters.empty")}</p>
                  <p className="text-muted">{t("life.filters.emptyHint")}</p>
                </div>
              ) : null}
              <div className="grid gap-3 md:grid-cols-[repeat(auto-fill,minmax(20rem,1fr))]">
                {shown.map((r) => (
                  <TableBlockCard
                    key={r.block.id}
                    block={r.block}
                    workspace={w.slug}
                    repo={w.repo}
                    canPause={view.can_pause}
                    situation={r.situation}
                    newNotices={newNotices.get(r.block.id) ?? 0}
                    clickable
                  />
                ))}
              </div>
            </div>
          )}
        </>
      )}
      <TableToasts workspace={w.slug} toasts={toasts} dismiss={dismiss} />
    </section>
  );
}
