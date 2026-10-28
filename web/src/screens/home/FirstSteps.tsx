import { type UseQueryResult, useQueries, useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { type ReactNode, useState } from "react";
import { useTranslation } from "react-i18next";
import { useChatControl } from "../../app/chatControl";
import { useCan, useMe } from "../../app/session";
import { Button } from "../../components/ui";
import { chatQuery } from "../../lib/chatQueries";
import { usersQuery } from "../admin/kit";
import { appsQuery, profilesQuery } from "../admin/manage";
import { workspacesQuery } from "../workspaces/queries";

/** done and pending are read facts; unknown is a read that failed (never shown as either). */
type Status = "done" | "pending" | "unknown";

const DISMISS_KEY = "mestre.home.telegram-later";

function dismissedBefore(): boolean {
  try {
    return localStorage.getItem(DISMISS_KEY) === "1";
  } catch {
    return false;
  }
}

function remember() {
  try {
    localStorage.setItem(DISMISS_KEY, "1");
  } catch {
    // the choice is only a convenience of this browser
  }
}

const statusOf = (q: UseQueryResult, done: boolean): Status =>
  done ? "done" : q.isError ? "unknown" : "pending";

/** Whether the Home shows the first steps: only for who can do all of them (the Guildmaster). */
export function useIsGuildmaster(): boolean {
  const apps = useCan("github_apps");
  const profiles = useCan("account_profiles");
  const users = useCan("manage_users");
  return apps && profiles && users;
}

interface Step {
  key: "github" | "profile" | "workspace" | "request";
  status: Status;
  retry: () => void;
}

/**
 * The four steps, derived from what the server already has; nothing is kept. `settled` is false
 * until every read answered, so the card does not flash for who has finished.
 */
function useSteps(): { steps: Step[]; settled: boolean; telegram: Status } {
  const me = useMe();
  const apps = useQuery(appsQuery);
  const profiles = useQuery(profilesQuery);
  const workspaces = useQuery(workspacesQuery);
  const users = useQuery(usersQuery);
  const slugs = (workspaces.data?.workspaces ?? []).map((w) => w.slug);
  const chats = useQueries({ queries: slugs.map((s) => chatQuery(s)) });

  const fixed = (apps.data?.apps ?? []).filter((a) => a.role !== "class");
  const github = statusOf(apps, fixed.length >= 2 && fixed.every((a) => a.state === "pronto"));
  const profile = statusOf(
    profiles,
    (profiles.data?.profiles ?? []).some((p) => !!p.setup_done_at),
  );
  const workspace = statusOf(workspaces, slugs.length > 0);
  const spoke = chats.some((c) => c.data?.entries.some((e) => e.from === "user"));
  const failedChat = chats.some((c) => c.isError);
  const request: Status = spoke ? "done" : failedChat ? "unknown" : "pending";
  const mine = users.data?.users.find((u) => u.id === me.user.id);
  const telegram = statusOf(users, !!mine?.telegram);

  const settled =
    ![apps, profiles, workspaces, users].some((q) => q.isPending) &&
    !chats.some((c) => c.isPending);
  const retryChats = () => chats.forEach((c) => void c.refetch());
  return {
    settled,
    telegram,
    steps: [
      { key: "github", status: github, retry: () => void apps.refetch() },
      { key: "profile", status: profile, retry: () => void profiles.refetch() },
      { key: "workspace", status: workspace, retry: () => void workspaces.refetch() },
      { key: "request", status: request, retry: retryChats },
    ],
  };
}

const linkClass =
  "inline-flex min-h-11 items-center justify-center rounded-field px-4 font-semibold transition-colors duration-150";
const primary = `${linkClass} bg-accent text-accent-ink hover:bg-accent-strong`;
const secondary = `${linkClass} border border-line bg-surface text-ink hover:bg-accent-wash`;

function Item({
  n,
  title,
  body,
  status,
  hint,
  action,
  retry,
}: {
  n: number;
  title: string;
  body: string;
  status: Status;
  hint: string;
  action: ReactNode;
  retry: () => void;
}) {
  const { t } = useTranslation("home");
  return (
    <li
      data-status={status}
      className="grid grid-cols-[2.2rem_minmax(0,1fr)] items-start gap-x-3 gap-y-1 border-t border-line py-3 first:border-t-0 sm:grid-cols-[2.2rem_minmax(0,1fr)_auto]"
    >
      <span
        aria-hidden="true"
        className={`grid size-9 place-items-center rounded-full border-2 font-semibold ${status === "done" ? "border-signal bg-signal text-accent-ink" : "border-line"}`}
      >
        {status === "done" ? "✓" : status === "unknown" ? "?" : n}
      </span>
      <div>
        <h3 className={`text-lg ${status === "done" ? "text-muted line-through" : ""}`}>{title}</h3>
        <p className="text-muted">{body}</p>
        {status === "unknown" ? (
          <p className="text-sm text-danger">{t("steps.unknownHint")}</p>
        ) : null}
        {status === "pending" && hint ? <p className="text-sm text-muted">{hint}</p> : null}
      </div>
      <div className="col-start-2 sm:col-start-3">
        {status === "done" ? (
          <span className="rounded-full border border-signal px-2 py-0.5 text-sm font-semibold text-signal">
            {t("steps.done")}
          </span>
        ) : status === "unknown" ? (
          <>
            <span className="sr-only">{t("steps.unknown")}</span>
            <Button variant="secondary" onPress={retry}>
              {t("steps.retry")}
            </Button>
          </>
        ) : (
          action
        )}
      </div>
    </li>
  );
}

/** The card of the first steps (docs/painel/primeira-jornada.md#primeiros-passos). */
export function FirstSteps() {
  const { t } = useTranslation("home");
  const chat = useChatControl();
  const { steps, settled, telegram } = useSteps();
  const [later, setLater] = useState(dismissedBefore);
  if (!settled) return null;

  const done = steps.filter((s) => s.status === "done").length;
  if (done === steps.length) {
    if (telegram === "done" || later) return null;
    return (
      <div className="mt-6 flex flex-wrap items-center gap-3 rounded-field border border-line bg-surface p-3">
        <p className="grow text-muted">{t("steps.optionalLine")}</p>
        <Link to="/usuarios" className={secondary}>
          {t("steps.telegram.action")}
        </Link>
        <Button
          variant="quiet"
          onPress={() => {
            remember();
            setLater(true);
          }}
        >
          {t("steps.telegram.later")}
        </Button>
      </div>
    );
  }

  const by = Object.fromEntries(steps.map((s) => [s.key, s])) as Record<Step["key"], Step>;
  const current = steps.find((s) => s.status !== "done")?.key;
  const cls = (k: Step["key"]) => (current === k ? primary : secondary);
  const off = (k: Step["key"], label: string) => (
    <Button variant={current === k ? "primary" : "secondary"} isDisabled>
      {label}
    </Button>
  );
  const blockedBy = (...deps: Step["key"][]) => {
    const i = steps.findIndex((s) => deps.includes(s.key) && s.status !== "done");
    return i < 0 ? 0 : i + 1;
  };
  const workspaceWait = blockedBy("github");
  const requestWait = blockedBy("profile", "workspace");
  const hint = (n: number) => (n ? t("steps.before", { n }) : "");
  const label = (k: Step["key"]) => t(`steps.${k}.action`);

  return (
    <section
      aria-labelledby="home-steps"
      className="mt-6 rounded-field border border-line bg-surface p-5 shadow-card"
    >
      <h2 id="home-steps" className="text-xl">
        {t("steps.title")}
      </h2>
      <p className="text-muted">{t("steps.count", { done, total: steps.length })}</p>
      <div
        role="progressbar"
        aria-label={t("steps.progress")}
        aria-valuemin={0}
        aria-valuemax={steps.length}
        aria-valuenow={done}
        className="mt-2 h-1.5 w-full overflow-hidden rounded-full bg-accent-wash"
      >
        <div
          className="h-full rounded-full bg-signal"
          style={{ width: `${(done / steps.length) * 100}%` }}
        />
      </div>
      <ol aria-label={t("steps.list")} className="mt-3">
        <Item
          n={1}
          status={by.github.status}
          retry={by.github.retry}
          title={t("steps.github.title")}
          body={t("steps.github.body")}
          hint=""
          action={
            <Link to="/apps" className={cls("github")}>
              {label("github")}
            </Link>
          }
        />
        <Item
          n={2}
          status={by.profile.status}
          retry={by.profile.retry}
          title={t("steps.profile.title")}
          body={t("steps.profile.body")}
          hint=""
          action={
            <Link to="/perfis" className={cls("profile")}>
              {label("profile")}
            </Link>
          }
        />
        <Item
          n={3}
          status={by.workspace.status}
          retry={by.workspace.retry}
          title={t("steps.workspace.title")}
          body={t("steps.workspace.body")}
          hint={hint(workspaceWait)}
          action={
            workspaceWait ? (
              off("workspace", label("workspace"))
            ) : (
              <Link to="/workspaces" search={{ criar: true }} className={cls("workspace")}>
                {label("workspace")}
              </Link>
            )
          }
        />
        <Item
          n={4}
          status={by.request.status}
          retry={by.request.retry}
          title={t("steps.request.title")}
          body={t("steps.request.body")}
          hint={hint(requestWait)}
          action={
            requestWait ? (
              off("request", label("request"))
            ) : (
              <Button variant={current === "request" ? "primary" : "secondary"} onPress={chat.show}>
                {label("request")}
              </Button>
            )
          }
        />
      </ol>
    </section>
  );
}
