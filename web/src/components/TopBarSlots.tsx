import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { Button as AriaButton, Dialog, DialogTrigger, Popover } from "react-aria-components";
import { useTranslation } from "react-i18next";
import { useCan, useFormatDateTime, useMe } from "../app/session";
import type { ApprovalsSnapshot, NoticeView } from "../gen/panel";
import { api } from "../lib/api";
import { approvalCountQuery, noticesQuery } from "../lib/approvalQueries";
import { noticePath, useGoto } from "../lib/links";
import { useLiveEvents } from "../lib/live";
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

/** The counter of the approvals that wait for the user, on every screen. */
export function ApprovalsCounter() {
  const { t } = useTranslation("approvals");
  const { t: ts } = useTranslation("shell");
  const can = useCan("decide_approvals");
  const qc = useQueryClient();
  const { data } = useQuery({ ...approvalCountQuery, enabled: can });
  useLiveEvents("approvals:", (_topic, event, payload) => {
    if (event === "count" || event === "snapshot")
      qc.setQueryData(approvalCountQuery.queryKey, {
        pending: (payload as ApprovalsSnapshot).pending,
      });
  });
  if (!can) return null;
  const n = data?.pending ?? 0;
  return (
    <Link
      to="/aprovacoes"
      search={{}}
      aria-label={n > 0 ? t("counter.label", { count: n }) : t("counter.none")}
      className="relative grid size-11 place-items-center rounded-full text-sm font-semibold hover:bg-accent-wash"
    >
      <span aria-hidden="true">{ts("top.approvals")}</span>
      <Badge count={n} />
    </Link>
  );
}

function NoticeRow({ n, onOpen }: { n: NoticeView; onOpen: (n: NoticeView) => void }) {
  const { t } = useTranslation("approvals");
  const fmt = useFormatDateTime();
  const path = noticePath(n.workspace, n.link);
  const place = n.workspace
    ? n.table
      ? t("bell.place", { workspace: n.workspace, table: n.table })
      : t("bell.placeNoTable", { workspace: n.workspace })
    : t("bell.system");
  return (
    <li
      className={`flex flex-col gap-1 border-b border-line p-3 last:border-b-0 ${n.read ? "" : "bg-accent-wash/50"}`}
    >
      <p className="text-sm text-muted">
        {place} · {fmt(n.created_at)}
        {n.read ? null : <span className="ml-2 font-semibold text-ink">{t("bell.unread")}</span>}
      </p>
      <p className="break-words whitespace-pre-line">{n.text}</p>
      {path ? (
        <div>
          <Button variant="quiet" onPress={() => onOpen(n)}>
            {t("bell.openLink")}
          </Button>
        </div>
      ) : null}
    </li>
  );
}

/** The bell: what the Telegram would say, with where it is and a link, read per user. */
export function NoticesBell() {
  const { t } = useTranslation("approvals");
  const { t: ts } = useTranslation("shell");
  const me = useMe();
  const qc = useQueryClient();
  const goto = useGoto();
  const [open, setOpen] = useState(false);
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
  const list = data?.notices ?? [];
  return (
    <DialogTrigger isOpen={open} onOpenChange={setOpen}>
      <AriaButton
        aria-label={unread > 0 ? t("bell.openUnread", { count: unread }) : t("bell.open")}
        data-user={me.user.id}
        className="relative grid size-11 place-items-center rounded-full text-sm font-semibold hover:bg-accent-wash"
      >
        <span aria-hidden="true">{ts("top.notices")}</span>
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
          {data && list.length === 0 ? <p className="p-3 text-muted">{t("bell.empty")}</p> : null}
          <ul>
            {list.map((n) => (
              <NoticeRow
                key={n.id}
                n={n}
                onOpen={(x) => {
                  if (!x.read) read.mutate([x.id]);
                  setOpen(false);
                  goto(noticePath(x.workspace, x.link));
                }}
              />
            ))}
          </ul>
        </Dialog>
      </Popover>
    </DialogTrigger>
  );
}
