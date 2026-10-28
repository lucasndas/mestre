import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { type DragEvent, Fragment, type KeyboardEvent, useEffect, useRef, useState } from "react";
import { Button as AriaButton } from "react-aria-components";
import { useTranslation } from "react-i18next";
import { useCan, useFormatDateTime, useMe } from "../app/session";
import { useConnectionState } from "../app/socket";
import type { ChatConversation, ChatEntry, ChatSent, ChatStatus } from "../gen/panel";
import { ApiError, api } from "../lib/api";
import { chatOverviewQuery, chatQuery, humanSize, newMessageId } from "../lib/chatQueries";
import { errorText } from "../lib/errors";
import { formatTime } from "../lib/format";
import { useCurrentWorkspace, useRouteWorkspace } from "../workspace/current";
import { isEnded } from "../workspace/model";
import { viewQuery } from "../workspace/queries";
import { SituationChip } from "../workspace/SituationChip";
import { situationOf } from "../workspace/situation";
import { ChatMarkdown } from "./Markdown";
import { Term } from "./Term";
import { Alert, Button } from "./ui";

function ChannelIcon({ channel }: { channel: string }) {
  const { t } = useTranslation("chat");
  const telegram = channel === "telegram";
  const label = telegram ? t("entry.viaTelegram") : t("entry.viaPanel");
  return (
    <span
      role="img"
      aria-label={label}
      title={label}
      className="inline-grid size-4 place-items-center text-muted"
    >
      <svg
        viewBox="0 0 16 16"
        width="14"
        height="14"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.5"
        aria-hidden="true"
      >
        {telegram ? (
          <path
            d="M14.5 2 1.5 7.2l4 1.6 1.3 4.2 2.2-2.6 3.4 2.6L14.5 2ZM5.5 8.8 14 3.6"
            strokeLinejoin="round"
          />
        ) : (
          <>
            <rect x="1.5" y="2.5" width="13" height="9" rx="1" />
            <path d="M5.5 14h5M8 11.5V14" />
          </>
        )}
      </svg>
    </span>
  );
}

function statusText(t: ReturnType<typeof useTranslation<"chat">>["t"], s: ChatStatus): string {
  switch (s.state) {
    case "idle":
      return t("status.idle");
    case "busy":
      return t("status.busy");
    case "unavailable":
      return s.reason
        ? t("status.unavailable", { reason: s.reason })
        : t("status.unavailableNoReason");
    default:
      return t("status.none");
  }
}

/** The state of the Grão-Mestre at the top: free, busy, the queue ahead, unavailable, and the lock. */
function StatusLine({ status }: { status: ChatStatus }) {
  const { t } = useTranslation("chat");
  const dot = status.locked
    ? "bg-danger"
    : status.state === "idle"
      ? "bg-signal"
      : status.state === "busy"
        ? "bg-warn"
        : "bg-muted";
  return (
    <div role="status" className="flex flex-col gap-1 border-b border-line px-4 py-2 text-sm">
      <p className="flex flex-wrap items-center justify-between gap-x-3">
        <span className="inline-flex items-center gap-2">
          <span aria-hidden="true" className={`size-2.5 rounded-full ${dot}`} />
          {status.locked ? t("status.lockedShort") : statusText(t, status)}
        </span>
        {status.locked ? (
          <span className="text-muted">{t("status.lockedHeld")}</span>
        ) : status.ahead > 0 ? (
          <span className="text-muted">{t("status.ahead", { count: status.ahead })}</span>
        ) : null}
      </p>
      {status.locked ? <p className="sr-only">{t("status.locked")}</p> : null}
    </div>
  );
}

/**
 * The message of progress: the one card of the campaign's progress, closed in one line by default and rewritten in
 * place by the engine. Open, it lists the tables with their situation (the vocabulary of the design) and what needs
 * the user. Only the Owner and the Administrators of the campaign receive it (the server decides).
 */
function ProgressCard({
  lines,
  updatedAt,
  workspace,
  onNavigate,
}: {
  lines: string[];
  updatedAt: string;
  workspace: string;
  onNavigate: () => void;
}) {
  const { t, i18n } = useTranslation("chat");
  const { timezone } = useMe();
  const canDecide = useCan("decide_approvals");
  const { data: view } = useQuery(viewQuery(workspace));
  const rows = (view?.tables ?? [])
    .filter((b) => !isEnded(b))
    .map((b) => ({ b, s: situationOf(b, canDecide) }));
  const need = rows.filter((r) => r.s.urgent).length;
  const [head, ...rest] = lines;
  const time = formatTime(updatedAt, i18n.language, timezone);
  return (
    <details
      aria-label={t("progress.title")}
      className="group mx-4 mt-3 rounded-field border border-line bg-paper shadow-card"
    >
      <summary className="flex min-h-12 cursor-pointer list-none items-center gap-3 px-3 py-2 [&::-webkit-details-marker]:hidden">
        <span className="min-w-0 flex-1">
          <h3 className="font-body text-base font-semibold">{t("progress.title")}</h3>
          <span className="block text-sm text-muted">
            {t("progress.tablesCount", { count: rows.length })} ·{" "}
            {need > 0 ? t("progress.needCount", { count: need }) : t("progress.noneNeed")} · {time}
          </span>
        </span>
        <span aria-hidden="true" className="text-muted transition-transform group-open:rotate-180">
          ⌄
        </span>
      </summary>
      <div className="border-t border-line px-3 pb-3">
        {head ? <p className="mt-2 font-semibold break-words">{head}</p> : null}
        {rest.length > 0 ? (
          <ul className="mt-1 flex list-disc flex-col gap-0.5 pl-5 text-sm break-words">
            {rest.map((l, i) => (
              // the lines of one message, in order, with no id of their own
              // biome-ignore lint/suspicious/noArrayIndexKey: the message is replaced as a whole
              <li key={i}>{l.replace(/^[-•]\s*/, "")}</li>
            ))}
          </ul>
        ) : null}
        {rows.length > 0 ? (
          <ul aria-label={t("progress.tables")} className="mt-2 flex flex-col">
            {rows.map(({ b, s }) => (
              <li key={b.id} className="flex flex-wrap items-center gap-x-3 gap-y-1">
                <Link
                  to="/workspaces/$workspace/mesas/$table"
                  params={{ workspace, table: b.id }}
                  onClick={onNavigate}
                  className="inline-flex min-h-11 items-center text-accent underline underline-offset-4"
                >
                  {b.epic_title || b.id}
                </Link>
                <SituationChip s={s} />
              </li>
            ))}
          </ul>
        ) : null}
        {need > 0 ? (
          <p className="mt-1 flex flex-wrap items-center gap-x-3 border-t border-line pt-2">
            <span className="font-semibold">{t("progress.needTitle")}</span>
            <Link
              to="/aprovacoes"
              onClick={onNavigate}
              className="inline-flex min-h-11 items-center text-accent underline underline-offset-4"
            >
              {t("progress.seeApprovals")}
            </Link>
          </p>
        ) : null}
        <p className="mt-1 text-sm text-muted">{t("progress.updated", { time })}</p>
      </div>
    </details>
  );
}

function Attachments({ entry }: { entry: ChatEntry }) {
  const { t } = useTranslation("chat");
  if (!entry.attachments?.length) return null;
  return (
    <ul className="mt-1 flex flex-col gap-1">
      {entry.attachments.map((a, i) => (
        // biome-ignore lint/suspicious/noArrayIndexKey: names can repeat; the order is fixed
        <li key={i} className="text-sm break-all">
          {a.download ? (
            <a
              href={a.download}
              download
              className="text-accent underline"
              aria-label={t("attachment.download", { name: a.name })}
            >
              {a.name}
            </a>
          ) : entry.from === "grand_master" ? (
            <span className="text-muted">{t("attachment.unavailable", { name: a.name })}</span>
          ) : (
            <span>{t("attachment.sent", { name: a.name })}</span>
          )}
        </li>
      ))}
    </ul>
  );
}

function Question({ entry, workspace }: { entry: ChatEntry; workspace: string }) {
  const { t } = useTranslation("chat");
  const { t: te } = useTranslation("errors");
  const qc = useQueryClient();
  const q = entry.question;
  const choose = useMutation({
    mutationFn: (option: number) => api("POST", `/api/chat-choices/${q?.id}`, { option }),
    onSettled: () => qc.invalidateQueries({ queryKey: chatQuery(workspace).queryKey }),
  });
  if (!q) return null;
  if (q.state === "answered")
    return <p className="mt-2 font-semibold">{t("question.chose", { answer: q.answer ?? "" })}</p>;
  // the buttons are only in the channel the question was asked in
  if (entry.channel !== "panel") return <p className="mt-2 text-muted">{t("question.telegram")}</p>;
  return (
    <div className="mt-2 flex flex-col gap-2">
      <fieldset className="flex flex-col gap-2">
        <legend className="sr-only">{t("question.options")}</legend>
        {q.options.map((o, i) => (
          <Button
            key={o}
            variant="secondary"
            isDisabled={choose.isPending}
            onPress={() => choose.mutate(i)}
          >
            {o}
          </Button>
        ))}
      </fieldset>
      {choose.isError ? <Alert>{errorText(te, choose.error)}</Alert> : null}
    </div>
  );
}

function EntryView({
  entry,
  workspace,
  locked,
  onReply,
}: {
  entry: ChatEntry;
  workspace: string;
  locked: boolean;
  onReply: (e: ChatEntry) => void;
}) {
  const { t } = useTranslation("chat");
  const fmt = useFormatDateTime();
  const mine = entry.from === "user";
  // the engine keeps a notice in place of what the filter of secrets held or cut (internal/panelchat/send.go)
  const held = mine && entry.text.startsWith("[mensagem retida:");
  const removed = mine && entry.text.includes("[segredo removido]");
  const who = mine
    ? t("entry.you")
    : entry.from === "question"
      ? t("entry.question")
      : t("entry.grandMaster");
  return (
    <li
      className={`flex max-w-[92%] flex-col gap-1 rounded-field border p-3 ${mine ? "ml-auto border-line bg-accent-wash" : "mr-auto border-line bg-paper"} ${entry.waiting && locked ? "border-warn bg-warn-wash" : ""}`}
      data-entry={entry.id}
    >
      <p className="flex flex-wrap items-center gap-x-2 text-sm text-muted">
        <span className="font-semibold text-ink">{who}</span>
        <ChannelIcon channel={entry.channel} />
        <span>{fmt(entry.at)}</span>
        {entry.table ? <span>{t("entry.table", { table: entry.table })}</span> : null}
      </p>
      {entry.quote ? (
        <blockquote className="border-l-2 border-accent pl-2 text-sm text-muted break-words">
          <span className="font-semibold">
            {t("entry.quote", { who: entry.quote.mine ? t("entry.you") : t("entry.grandMaster") })}
          </span>
          {": "}
          {entry.quote.excerpt}
        </blockquote>
      ) : null}
      {entry.from === "grand_master" ? (
        <ChatMarkdown>{entry.text}</ChatMarkdown>
      ) : held ? (
        <p className="break-words">{t("entry.heldText")}</p>
      ) : entry.text ? (
        <p className="break-words whitespace-pre-wrap">{entry.text}</p>
      ) : null}
      {held ? (
        <p className="m-0 flex gap-3 rounded-field bg-danger-wash p-2 text-sm text-danger">
          <strong className="shrink-0">{t("entry.notDelivered")}</strong>
          <span>
            {t("entry.notDeliveredWhy")}{" "}
            <Link to="/segredos" className="underline">
              {t("entry.secrets")}
            </Link>
            .
          </span>
        </p>
      ) : null}
      {removed ? (
        <p className="m-0 flex gap-3 rounded-field bg-warn-wash p-2 text-sm text-warn-ink">
          <strong className="shrink-0">{t("entry.sentWithout")}</strong>
          <span>{t("entry.sentWithoutWhy")}</span>
        </p>
      ) : null}
      <Attachments entry={entry} />
      <Question entry={entry} workspace={workspace} />
      <div className="flex flex-wrap items-center justify-between gap-2">
        {entry.waiting ? (
          <p className="text-sm text-muted">{locked ? t("entry.held") : t("entry.waiting")}</p>
        ) : (
          <span />
        )}
        <AriaButton
          aria-label={t("entry.replyTo", { who })}
          onPress={() => onReply(entry)}
          className="min-h-11 rounded-field px-2 text-sm font-semibold text-accent hover:bg-accent-wash"
        >
          {t("entry.reply")}
        </AriaButton>
      </div>
    </li>
  );
}

interface Quote {
  id: string;
  who: string;
  excerpt: string;
}

function excerptOf(text: string): string {
  const one = text.split(/\s+/).join(" ").trim();
  return one.length > 80 ? `${one.slice(0, 80)}…` : one;
}

function Composer({
  workspace,
  conv,
  quote,
  onClearQuote,
  text,
  setText,
}: {
  workspace: string;
  conv: ChatConversation;
  quote: Quote | null;
  onClearQuote: () => void;
  text: string;
  setText: (v: string) => void;
}) {
  const { t, i18n } = useTranslation("chat");
  const { t: te } = useTranslation("errors");
  const me = useMe();
  const qc = useQueryClient();
  const online = useConnectionState() === "open";
  const [files, setFiles] = useState<{ id: number; file: File }[]>([]);
  const nextFile = useRef(0);
  const [problem, setProblem] = useState("");
  const [notice, setNotice] = useState("");
  const [dragging, setDragging] = useState(false);
  // the id stays until the message went: a retry after a failure is the same message
  const id = useRef(newMessageId());
  const limit = humanSize(conv.limit, i18n.language);

  const send = useMutation({
    mutationFn: () => {
      const form = new FormData();
      form.append("text", text);
      form.append("client_id", id.current);
      if (quote) form.append("quote", quote.id);
      for (const { file } of files) form.append("files", file, file.name);
      return api<ChatSent>("POST", `/api/chat/${encodeURIComponent(workspace)}/messages`, form);
    },
    onSuccess: (sent) => {
      setText("");
      setFiles([]);
      // the filter of secrets acted: the user is told to change the secret
      setProblem(
        sent.secret === "held"
          ? t("composer.secretHeld")
          : sent.secret === "removed"
            ? t("composer.secretRemoved")
            : "",
      );
      setNotice(sent.held ? t("composer.sentHeld") : t("composer.sent"));
      onClearQuote();
      id.current = newMessageId();
      void qc.invalidateQueries({ queryKey: ["chat"] });
    },
    onError: (err) => {
      setNotice("");
      if (
        err instanceof ApiError &&
        err.code === "chat_file_too_large" &&
        typeof err.params.limit === "number"
      ) {
        setProblem(
          t("composer.tooLargeServer", { limit: humanSize(err.params.limit, i18n.language) }),
        );
        return;
      }
      setProblem(errorText(te, err)); // what was typed stays in the field
    },
  });

  const addFiles = (list: File[]) => {
    const next = [...files];
    let why = "";
    for (const f of list) {
      if (f.size > conv.limit) {
        why = t("composer.tooLarge", { name: f.name, limit });
      } else if (next.length >= conv.max_files) {
        why = t("composer.tooMany", { max: conv.max_files });
      } else {
        next.push({ id: nextFile.current++, file: f });
      }
    }
    setFiles(next);
    setProblem(why);
  };

  const noGM = conv.status.state === "none";
  const reason = !conv.can_talk
    ? t("composer.cannotTalk", { level: me.user.level_name })
    : noGM
      ? t("composer.noGrandMaster")
      : !online
        ? t("composer.offline")
        : "";
  const empty = text.trim() === "" && files.length === 0;
  const canSend = conv.can_talk && !noGM && online && !empty && !send.isPending;

  const submit = () => {
    if (canSend) send.mutate();
  };
  const onKey = (e: KeyboardEvent) => {
    if (e.key === "Enter" && (e.ctrlKey || e.metaKey)) {
      e.preventDefault();
      submit();
    }
  };
  const onDrop = (e: DragEvent) => {
    e.preventDefault();
    setDragging(false);
    if (conv.can_talk) addFiles(Array.from(e.dataTransfer?.files ?? []));
  };

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        submit();
      }}
      onDragOver={(e) => {
        e.preventDefault();
        if (conv.can_talk) setDragging(true);
      }}
      onDragLeave={() => setDragging(false)}
      onDrop={onDrop}
      className={`flex flex-col gap-2 border-t border-line p-3 pb-[max(0.75rem,env(safe-area-inset-bottom))] ${dragging ? "bg-accent-wash outline-2 -outline-offset-4 outline-accent outline-dashed" : "bg-surface"}`}
    >
      {dragging ? <p className="text-center font-semibold">{t("composer.drop")}</p> : null}
      {quote ? (
        <div className="flex items-start justify-between gap-2 rounded-field border border-line bg-paper px-2 py-1 text-sm">
          <p className="break-words">
            {t("composer.quoting", { who: quote.who, excerpt: quote.excerpt })}
          </p>
          <AriaButton
            aria-label={t("composer.cancelQuote")}
            onPress={onClearQuote}
            className="grid size-11 shrink-0 place-items-center rounded-full hover:bg-accent-wash"
          >
            <span aria-hidden="true">×</span>
          </AriaButton>
        </div>
      ) : null}
      {files.length > 0 ? (
        <ul aria-label={t("composer.files")} className="flex flex-wrap gap-2">
          {files.map(({ id: fid, file: f }) => (
            <li
              key={fid}
              className="flex items-center gap-1 rounded-field border border-line bg-paper pl-2 text-sm"
            >
              <span className="break-all">
                {f.name} · {humanSize(f.size, i18n.language)}
              </span>
              <AriaButton
                aria-label={t("composer.removeFile", { name: f.name })}
                onPress={() => setFiles(files.filter((x) => x.id !== fid))}
                className="min-h-11 px-3 font-semibold text-accent underline underline-offset-4 hover:bg-accent-wash"
              >
                {t("composer.remove")}
              </AriaButton>
            </li>
          ))}
        </ul>
      ) : null}
      <label className="sr-only" htmlFor="chat-text">
        {t("composer.label")}
      </label>
      <textarea
        id="chat-text"
        value={text}
        rows={3}
        disabled={!conv.can_talk}
        onChange={(e) => setText(e.target.value)}
        onKeyDown={onKey}
        placeholder={t("composer.placeholder")}
        className="min-h-20 rounded-field border border-line bg-paper px-3 py-2 text-ink outline-none placeholder:text-muted focus:border-accent focus:ring-3 focus:ring-accent/25 disabled:opacity-60"
      />
      {reason ? <p className="text-sm font-semibold">{reason}</p> : null}
      {problem ? <Alert>{problem}</Alert> : null}
      {notice && !problem ? <Alert tone="ok">{notice}</Alert> : null}
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <label className="focus-within:ring-3 focus-within:ring-accent/25 inline-flex min-h-11 cursor-pointer items-center gap-2 rounded-field border border-line bg-surface px-4 font-semibold hover:bg-accent-wash has-[:disabled]:opacity-60">
          <input
            type="file"
            multiple
            className="sr-only"
            disabled={!conv.can_talk}
            onChange={(e) => {
              addFiles(Array.from(e.target.files ?? []));
              e.target.value = "";
            }}
          />
          <ComposerIcon d="M21 11.5 12.5 20a5.5 5.5 0 0 1-8-8L13 3.5a3.7 3.7 0 0 1 5.3 5.3L9.8 17.3a1.8 1.8 0 0 1-2.6-2.6L15 7" />
          {t("composer.attach")}
        </label>
        {reason ? null : <p className="min-w-0 flex-1 text-sm text-muted">{t("composer.hint")}</p>}
        <div className="flex-1" />
        <Button type="submit" isDisabled={!canSend}>
          <ComposerIcon d="M22 2 11 13M22 2l-7 20-4-9-9-4Z" />
          {send.isPending ? t("composer.sending") : t("composer.send")}
        </Button>
      </div>
    </form>
  );
}

/** The icon beside Anexar and Enviar. */
function ComposerIcon({ d }: { d: string }) {
  return (
    <svg
      viewBox="0 0 24 24"
      width="18"
      height="18"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.75"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      className="shrink-0"
    >
      <path d={d} />
    </svg>
  );
}

/** The history is the last 200 of each channel (the server's window). */
const HISTORY_LIMIT = 200;

const dayOf = (iso: string) => iso.slice(0, 10);

function DayLabel({ at }: { at: string }) {
  const { i18n } = useTranslation("chat");
  const { timezone } = useMe();
  return (
    <>
      {new Intl.DateTimeFormat(i18n.language, {
        weekday: "long",
        day: "numeric",
        month: "long",
        timeZone: timezone,
      }).format(new Date(at))}
    </>
  );
}

/** The first message, or a reader who cannot write: what to ask, in words, with three requests that only fill the field. */
function EmptyConversation({
  canTalk,
  onPick,
}: {
  canTalk: boolean;
  onPick: (text: string) => void;
}) {
  const { t } = useTranslation("chat");
  if (!canTalk)
    return (
      <div className="px-4 pt-4">
        <h3 className="text-xl">{t("emptyReadOnly.title")}</h3>
        <p className="mt-1 text-muted">{t("emptyReadOnly.text")}</p>
      </div>
    );
  const suggestions = ["one", "two", "three"] as const;
  return (
    <div className="px-4 pt-4">
      <h3 className="text-xl">{t("empty.title")}</h3>
      <p className="mt-1 text-muted">{t("empty.text")}</p>
      <ul className="mt-3 flex flex-col gap-2">
        {suggestions.map((k) => (
          <li key={k}>
            <AriaButton
              onPress={() => onPick(t(`suggestion.${k}.text`))}
              className="min-h-12 w-full rounded-field border border-line bg-paper px-3 py-2 text-left hover:border-accent hover:bg-accent-wash"
            >
              <span className="block">{t(`suggestion.${k}.text`)}</span>
              <span className="block text-sm text-muted">{t(`suggestion.${k}.hint`)}</span>
            </AriaButton>
          </li>
        ))}
      </ul>
    </div>
  );
}

function Conversation({ workspace, onNavigate }: { workspace: string; onNavigate: () => void }) {
  const { t } = useTranslation("chat");
  const qc = useQueryClient();
  const { data, isPending, isError } = useQuery(chatQuery(workspace));
  const [quote, setQuote] = useState<Quote | null>(null);
  const [draft, setDraft] = useState("");
  const [atEnd, setAtEnd] = useState(true);
  const [fresh, setFresh] = useState<Set<string>>(new Set());
  const log = useRef<HTMLDivElement>(null);
  const read = useMutation({
    mutationFn: () => api("POST", `/api/chat/${encodeURIComponent(workspace)}/read`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["chat"] }),
  });
  const markRead = useRef(read.mutate);
  markRead.current = read.mutate;
  const seen = useRef(0);
  // the answers that came while he was away are marked "new" until the panel is closed, and read now
  useEffect(() => {
    const unread = data?.unread ?? 0;
    if (!data || unread === 0) {
      seen.current = 0;
      return;
    }
    if (seen.current === unread) return;
    seen.current = unread;
    setFresh((old) => new Set([...old, ...data.entries.filter((e) => e.unread).map((e) => e.id)]));
    markRead.current();
  }, [data]);
  const total = data?.entries.length ?? 0;
  // biome-ignore lint/correctness/useExhaustiveDependencies: the end of the list is shown when a message comes
  useEffect(() => {
    if (log.current) log.current.scrollTop = log.current.scrollHeight;
  }, [total]);
  const goEnd = () => {
    if (log.current) log.current.scrollTop = log.current.scrollHeight;
  };
  if (isPending) return <p className="p-4 text-muted">{t("loading")}</p>;
  if (isError || !data)
    return (
      <div className="p-4">
        <Alert>{t("loadError")}</Alert>
      </div>
    );
  const firstFresh = data.entries.findIndex((e) => fresh.has(e.id));
  const last = data.entries[data.entries.length - 1];
  const working = data.status.state === "busy" && last?.from === "user";
  const empty = data.entries.length === 0;
  return (
    <>
      <StatusLine status={data.status} />
      <div
        ref={log}
        onScroll={(e) => {
          const el = e.currentTarget;
          setAtEnd(el.scrollHeight - el.scrollTop - el.clientHeight < 40);
        }}
        className="relative flex min-h-0 flex-1 flex-col overflow-y-auto"
      >
        {data.progress ? (
          <ProgressCard
            lines={data.progress.lines}
            updatedAt={data.progress.updated_at}
            workspace={workspace}
            onNavigate={onNavigate}
          />
        ) : null}
        {empty ? (
          <EmptyConversation canTalk={data.can_talk} onPick={(text) => setDraft(text)} />
        ) : null}
        {data.entries.length >= HISTORY_LIMIT ? (
          <p className="px-4 pt-3 text-center text-sm text-muted">
            {t("last200", { count: HISTORY_LIMIT })}
          </p>
        ) : null}
        <ol aria-label={t("log")} className="flex flex-col gap-3 p-4">
          {data.entries.map((e, i) => (
            <Fragment key={e.id}>
              {i === 0 || dayOf(e.at) !== dayOf(data.entries[i - 1]?.at ?? "") ? (
                <li className="self-center rounded-full border border-line bg-paper px-3 text-sm text-muted">
                  <DayLabel at={e.at} />
                </li>
              ) : null}
              {i === firstFresh ? (
                <li
                  aria-hidden="false"
                  className="flex items-center gap-2 text-sm font-semibold text-accent"
                >
                  <span className="h-px flex-1 bg-accent" />
                  {t("entry.newDivider")}
                  <span className="h-px flex-1 bg-accent" />
                </li>
              ) : null}
              <EntryView
                entry={e}
                workspace={workspace}
                locked={data.status.locked}
                onReply={(x) =>
                  setQuote({
                    id: x.id,
                    who: x.from === "user" ? t("entry.you") : t("entry.grandMaster"),
                    excerpt: excerptOf(x.text || x.question?.options.join(" / ") || ""),
                  })
                }
              />
            </Fragment>
          ))}
          {working ? (
            <li role="status" className="flex items-center gap-2 text-sm text-muted">
              <span aria-hidden="true" className="size-2.5 animate-pulse rounded-full bg-accent" />
              {t("working")}
            </li>
          ) : null}
        </ol>
        {firstFresh >= 0 && !atEnd ? (
          <div className="sticky bottom-2 mt-auto flex justify-center">
            <Button className="shadow-card" onPress={goEnd}>
              {t("jump", { count: fresh.size })}
            </Button>
          </div>
        ) : null}
      </div>
      <Composer
        text={draft}
        setText={setDraft}
        workspace={workspace}
        conv={data}
        quote={quote}
        onClearQuote={() => setQuote(null)}
      />
    </>
  );
}

/** The side panel: the conversation of the user with the Grão-Mestre of a workspace. */
export function ChatPanel({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation("chat");
  const { data: overview, isPending } = useQuery(chatOverviewQuery);
  const current = useCurrentWorkspace();
  const routed = useRouteWorkspace();
  const me = useMe();
  const list = overview?.workspaces ?? [];
  // the conversation is about the current campaign (the one of the trail, else the last opened); one the user cannot talk in falls to the first that works
  const known = list.some((w) => w.slug === current.slug);
  // a campaign the user chose (the trail or the last opened) that has no conversation (paused, say) is said so,
  // never swapped for another one; only the default, the first of the list, falls to one that works
  const chosen =
    current.slug !== "" && (current.slug === routed || current.slug === me.user.last_workspace);
  const noTalk = !known && chosen && current.list.some((w) => w.slug === current.slug);
  const workspace = noTalk ? "" : known ? current.slug : (list[0]?.slug ?? "");
  const named = noTalk ? current.slug : workspace;
  const mayTalk = list.find((w) => w.slug === workspace)?.can_talk ?? true;
  return (
    <aside aria-label={t("title")} className="flex h-dvh min-h-0 flex-col bg-surface">
      <header className="flex items-center justify-between gap-2 border-b border-line px-4 py-2">
        <h2 className="text-xl font-semibold">{t("title")}</h2>
        <AriaButton
          aria-label={t("close")}
          onPress={onClose}
          className="grid size-11 place-items-center rounded-full hover:bg-accent-wash"
        >
          <svg
            viewBox="0 0 24 24"
            width="20"
            height="20"
            fill="none"
            stroke="currentColor"
            strokeWidth="2"
            strokeLinecap="round"
            aria-hidden="true"
          >
            <path d="M6 6l12 12M18 6L6 18" />
          </svg>
        </AriaButton>
      </header>
      {isPending ? <p className="p-4 text-muted">{t("loading")}</p> : null}
      {overview && list.length === 0 ? <p className="p-4 text-muted">{t("noWorkspace")}</p> : null}
      {list.length > 0 ? (
        <div className="flex flex-col border-b border-line px-4 py-1 text-sm text-muted">
          <p className="flex flex-wrap items-center gap-x-2">
            <span>
              {t("workspace")} <span className="font-semibold text-ink">{named}</span>
            </span>
            <span aria-hidden="true">·</span>
            <Link
              to="/workspaces/$workspace"
              params={{ workspace: named }}
              onClick={onClose}
              className="inline-flex min-h-11 items-center font-semibold text-accent underline underline-offset-4"
            >
              {t("openWorkspace")}
            </Link>
          </p>
          <p className="pb-1">{mayTalk ? t("privacy") : t("privacyReader")}</p>
        </div>
      ) : null}
      {noTalk ? (
        <p role="status" className="p-4 text-muted">
          {t("noTalk", { workspace: current.slug })}
        </p>
      ) : null}
      {workspace ? (
        <Conversation key={workspace} workspace={workspace} onNavigate={onClose} />
      ) : null}
    </aside>
  );
}
