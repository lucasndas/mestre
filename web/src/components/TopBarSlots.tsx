import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Button as AriaButton, Dialog, DialogTrigger, Popover } from "react-aria-components";
import { useTranslation } from "react-i18next";
import { useCan, useFormatDateTime, useMe } from "../app/session";
import type { ApprovalsSnapshot, NoticeView } from "../gen/panel";
import { api } from "../lib/api";
import { approvalCountQuery, noticesQuery } from "../lib/approvalQueries";
import { formatTime } from "../lib/format";
import { noticePath, useGoto } from "../lib/links";
import { useLiveEvents } from "../lib/live";
import { messageText } from "../lib/messages";
import { useTableNames } from "../workspace/names";
import { Button } from "./ui";

function Badge({ count }: { count: number }) {
  if (count <= 0) return null;
  return (
    <span
      aria-hidden="true"
      className="absolute -top-1 -right-1 grid min-w-5 place-items-center rounded-full bg-accent px-1 text-xs font-semibold text-accent-ink"
    >
      {count > 99 ? "99+" : count}
    </span>
  );
}

/** The number of approvals that wait for the user, kept by the events of the channel; 0 for who does not decide. */
export function useApprovalsCount(): number {
  const can = useCan("decide_approvals");
  const qc = useQueryClient();
  const { data } = useQuery({ ...approvalCountQuery, enabled: can });
  useLiveEvents("approvals:", (_topic, event, payload) => {
    if (event === "count" || event === "snapshot")
      qc.setQueryData(approvalCountQuery.queryKey, {
        pending: (payload as ApprovalsSnapshot).pending,
      });
  });
  return can ? (data?.pending ?? 0) : 0;
}

type Kind = "request" | "notify" | "blocked" | "alert" | "system" | "stranger" | "github";

/** What a notice is, for the label: an approval (the request link) or the kind the engine gave. */
/** The notice in the language of the screen: from its code and params when the engine sent them, else as written. */
function noticeText(n: NoticeView, language: string): string {
  const msg = n.msg as { code: string; params?: Record<string, unknown> } | undefined;
  return (msg && messageText(language, msg.code, msg.params, undefined)) || n.text;
}

function kindOf(n: NoticeView): Kind {
  if (n.link.kind === "request") return "request";
  const known: Kind[] = ["notify", "blocked", "alert", "system", "stranger", "github"];
  return known.find((k) => k === n.kind) ?? "notify";
}

const dayKey = (iso: string, tz: string) =>
  new Intl.DateTimeFormat("en-CA", { timeZone: tz }).format(new Date(iso));

/** "Hoje", "Ontem" or the date, in the Guild time zone. */
function dayLabel(
  iso: string,
  now: Date,
  tz: string,
  language: string,
  today: string,
  yesterday: string,
): string {
  const key = dayKey(iso, tz);
  if (key === dayKey(now.toISOString(), tz)) return today;
  if (key === dayKey(new Date(now.getTime() - 24 * 3600 * 1000).toISOString(), tz))
    return yesterday;
  return new Intl.DateTimeFormat(language, { dateStyle: "medium", timeZone: tz }).format(
    new Date(iso),
  );
}

function NoticeRow({
  n,
  name,
  onOpen,
}: {
  n: NoticeView;
  name: string;
  onOpen: (n: NoticeView) => void;
}) {
  const { t } = useTranslation("approvals");
  const say = t as unknown as (k: string, o?: object) => string;
  const { i18n } = useTranslation();
  const { timezone } = useMe();
  const path = noticePath(n.workspace, n.link, n.table);
  const kind = kindOf(n);
  // the human name of the table, never its identifier (the identifier only when the name is not known)
  const place = n.workspace
    ? n.table
      ? t("bell.placeNamed", { workspace: n.workspace, table: name || n.table })
      : t("bell.placeNoTable", { workspace: n.workspace })
    : t("bell.system");
  const time = formatTime(n.created_at, i18n.language, timezone);
  const open =
    n.link.kind === "request"
      ? "bell.openRequest"
      : n.link.kind === "table"
        ? "bell.openTable"
        : "bell.openNotice";
  return (
    <li
      className={`flex flex-col gap-1 border-b border-line p-3 last:border-b-0 ${n.read ? "" : "bg-accent-wash/50"}`}
    >
      <p className="m-0 text-sm text-muted">
        <strong className="text-ink">{say(`bell.kind.${kind}`)}</strong> · {place} · {time}
        {n.read ? null : <span className="ml-2 font-semibold text-ink">{t("bell.unread")}</span>}
      </p>
      <p className="break-words whitespace-pre-line">{noticeText(n, i18n.language)}</p>
      {path ? (
        <div>
          <Button variant="quiet" onPress={() => onOpen(n)}>
            {say(open)}
          </Button>
        </div>
      ) : null}
    </li>
  );
}

/** The bell: what the Telegram would say, with where it is and a link, read per user. */
export function NoticesBell() {
  const { t } = useTranslation("approvals");
  const me = useMe();
  const qc = useQueryClient();
  const goto = useGoto();
  const { i18n } = useTranslation();
  const [open, setOpen] = useState(false);
  const [tab, setTab] = useState<"unread" | "all">("unread");
  const { data, isPending } = useQuery(noticesQuery);
  useLiveEvents("notices:", (_topic, event) => {
    if (event === "unread" || event === "snapshot")
      void qc.invalidateQueries({ queryKey: noticesQuery.queryKey });
  });
  const read = useMutation({
    mutationFn: (ids: number[]) => api("POST", "/api/notices/read", { ids }),
    onSuccess: () => qc.invalidateQueries({ queryKey: noticesQuery.queryKey }),
  });
  const unread = data?.unread ?? 0;
  const all = data?.notices ?? [];
  const list = tab === "unread" ? all.filter((n) => !n.read) : all;
  // the name of a table is read only while the bell is open
  const nameOf = useTableNames(
    all.filter((n) => n.table).map((n) => n.workspace),
    open,
  );
  const say = t as unknown as (k: string, o?: object) => string;
  const now = new Date();
  const groups: { label: string; items: NoticeView[] }[] = [];
  for (const n of list) {
    const label = dayLabel(
      n.created_at,
      now,
      me.timezone,
      i18n.language,
      say("bell.today"),
      say("bell.yesterday"),
    );
    const last = groups[groups.length - 1];
    if (last?.label === label) last.items.push(n);
    else groups.push({ label, items: [n] });
  }
  return (
    <DialogTrigger isOpen={open} onOpenChange={setOpen}>
      <AriaButton
        aria-label={unread > 0 ? t("bell.openUnread", { count: unread }) : t("bell.open")}
        data-user={me.user.id}
        className="relative grid size-11 place-items-center rounded-full text-sm font-semibold hover:bg-accent-wash"
      >
        <svg
          viewBox="0 0 24 24"
          width="22"
          height="22"
          fill="none"
          stroke="currentColor"
          strokeWidth="1.75"
          strokeLinecap="round"
          strokeLinejoin="round"
          aria-hidden="true"
        >
          <path d="M6 16v-5a6 6 0 0 1 12 0v5l2 2H4z" />
          <path d="M10 21h4" />
        </svg>
        <Badge count={unread} />
      </AriaButton>
      <Popover
        placement="bottom end"
        className="w-[min(28rem,calc(100vw-2rem))] rounded-field border border-line bg-surface shadow-pop"
      >
        <Dialog aria-label={t("bell.title")} className="max-h-[70vh] overflow-y-auto outline-none">
          <div className="flex items-center justify-between gap-2 border-b border-line p-3">
            <h2 className="text-lg">{t("bell.title")}</h2>
            {unread > 0 ? (
              <Button variant="quiet" isDisabled={read.isPending} onPress={() => read.mutate([])}>
                {t("bell.markAll")}
              </Button>
            ) : null}
          </div>
          {isPending ? <p className="p-3 text-muted">{t("bell.loading")}</p> : null}
          <fieldset className="m-0 flex gap-2 border-0 border-b border-line px-3 py-2">
            <legend className="sr-only">{t("bell.tabs.label")}</legend>
            {(["unread", "all"] as const).map((k) => (
              <button
                key={k}
                type="button"
                aria-pressed={tab === k}
                onClick={() => setTab(k)}
                className={`inline-flex min-h-11 items-center gap-1.5 rounded-full border px-3 text-sm ${
                  tab === k
                    ? "border-ink bg-ink text-paper"
                    : "border-line bg-surface hover:bg-accent-wash"
                }`}
              >
                {t(`bell.tabs.${k}`)}{" "}
                <span className="font-mono text-xs opacity-75">
                  {k === "unread" ? unread : all.length}
                </span>
              </button>
            ))}
          </fieldset>
          {data && list.length === 0 ? (
            <p className="p-3 text-muted">
              {tab === "unread" ? t("bell.emptyUnread") : t("bell.empty")}
            </p>
          ) : null}
          {groups.map((g) => (
            <section key={g.label} aria-label={g.label}>
              <h3 className="px-3 pt-2 font-body text-xs tracking-wider text-muted uppercase">
                {g.label}
              </h3>
              <ul>
                {g.items.map((n) => (
                  <NoticeRow
                    key={n.id}
                    n={n}
                    name={nameOf(n.workspace, n.table)}
                    onOpen={(x) => {
                      if (!x.read) read.mutate([x.id]);
                      setOpen(false);
                      goto(noticePath(x.workspace, x.link, x.table));
                    }}
                  />
                ))}
              </ul>
            </section>
          ))}
        </Dialog>
      </Popover>
    </DialogTrigger>
  );
}
