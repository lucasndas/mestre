import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, Outlet, useNavigate, useRouterState } from "@tanstack/react-router";
import { useEffect, useMemo, useState } from "react";
import { Button as AriaButton } from "react-aria-components";
import { useTranslation } from "react-i18next";
import { ChatPanel } from "../components/ChatPanel";
import { NoticesBell } from "../components/TopBarSlots";
import type { ChatConversation, ChatStatusEvent } from "../gen/panel";
import { approvalCountQuery } from "../lib/approvalQueries";
import { chatOverviewQuery, chatQuery } from "../lib/chatQueries";
import { type Live, LiveContext, useLiveEvents } from "../lib/live";
import { type SocketState, UiSocket } from "../lib/ws";
import { TransferBanner } from "../screens/admin/Transfer";
import { AccountMenu } from "./AccountMenu";
import { Brand } from "./AuthLayout";
import { Breadcrumbs } from "./Breadcrumbs";
import { ChatControlContext } from "./chatControl";
import { Nav } from "./Nav";
import { useMe } from "./session";
import { ConnectionContext, SocketContext } from "./socket";

/** The band that tells the Citadel is out of reach; the state is reloaded when it is back. */
function useConnection(): { state: SocketState; socket: UiSocket | null } {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [state, setState] = useState<SocketState>("connecting");
  const [socket, setSocket] = useState<UiSocket | null>(null);
  useEffect(() => {
    const proto = location.protocol === "https:" ? "wss:" : "ws:";
    const sock = new UiSocket({
      url: `${proto}//${location.host}/ws/ui`,
      onState: setState,
      onReconnected: () => void qc.invalidateQueries(),
      onSessionEnded: () => {
        qc.clear();
        void navigate({ to: "/login", search: {} });
      },
    });
    sock.start();
    setSocket(sock);
    return () => {
      sock.stop();
      setSocket(null);
    };
  }, [qc, navigate]);
  return { state, socket };
}

export function ReconnectingBand({ state }: { state: SocketState }) {
  const { t } = useTranslation("shell");
  if (state !== "reconnecting") return null;
  return (
    <div
      role="status"
      className="bg-warn px-4 py-1.5 text-center text-sm font-semibold text-warn-ink"
    >
      {t("reconnecting")}
    </div>
  );
}

/** What the conversation tells the screens: a change reads it again, the state of the Grão-Mestre is put in place. */
function ChatLive() {
  const qc = useQueryClient();
  useLiveEvents("chat:", (_topic, event, data) => {
    if (event === "status") {
      const s = data as ChatStatusEvent;
      qc.setQueryData<ChatConversation>(chatQuery(s.workspace).queryKey, (old) =>
        old ? { ...old, status: s.status } : old,
      );
      return;
    }
    void qc.invalidateQueries({ queryKey: ["chat"] });
  });
  return null;
}

/** The button of the side panel, with the answers that wait for the user; only the icon on the phone. */
function ChatToggle({ open, onToggle }: { open: boolean; onToggle: () => void }) {
  const { t } = useTranslation("chat");
  const { data } = useQuery(chatOverviewQuery);
  const n = data?.unread ?? 0;
  return (
    <AriaButton
      aria-label={n > 0 ? t("toggleUnread", { count: n }) : t("toggleOpen")}
      aria-expanded={open}
      onPress={onToggle}
      className="relative flex min-h-11 min-w-11 shrink-0 items-center justify-center gap-2 rounded-field bg-accent px-3 text-sm font-semibold text-accent-ink hover:bg-accent-strong"
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
        <path d="M4 5h16v11H9l-5 4z" />
      </svg>
      <span aria-hidden="true" className="hidden md:inline">
        {t("toggle")}
      </span>
      {n > 0 ? (
        <span
          aria-hidden="true"
          className="absolute -top-1 -right-1 grid min-w-5 place-items-center rounded-full border-2 border-surface bg-ink px-1 text-xs font-semibold text-surface"
        >
          {n > 99 ? "99+" : n}
        </span>
      ) : null}
    </AriaButton>
  );
}

/** The bar on top, in one line at any width: the brand, the bell, the Grão-Mestre and the account. */
function TopBar({ chatOpen, onChat }: { chatOpen: boolean; onChat: () => void }) {
  const { t } = useTranslation("shell");
  return (
    <header className="flex items-center gap-1 border-b border-line bg-surface px-2 py-1.5 md:gap-2 md:px-4">
      <Link
        to="/"
        aria-label={t("top.home")}
        className="mr-auto flex min-h-11 min-w-0 items-center rounded-field"
      >
        <Brand />
      </Link>
      <div data-slot="notices-bell" className="flex items-center">
        <NoticesBell />
      </div>
      <ChatToggle open={chatOpen} onToggle={onChat} />
      <AccountMenu />
    </header>
  );
}

const TITLE = "Mestre";

export function Shell() {
  const { t } = useTranslation("shell");
  const { i18n } = useTranslation();
  const me = useMe();
  const { state, socket } = useConnection();
  const path = useRouterState({ select: (s) => s.location.pathname });
  const [chatOpen, setChatOpen] = useState(false);
  const chatControl = useMemo(
    () => ({
      open: chatOpen,
      toggle: () => setChatOpen((o) => !o),
      show: () => setChatOpen(true),
    }),
    [chatOpen],
  );
  // the events of the channel for the screens (the counter, the bell, the central), and the
  // topics of this user: his approvals and his notices
  const userId = me.user.id;
  const canDecide = me.capabilities.includes("decide_approvals");
  const live = useMemo<Live>(
    () => ({
      subscribe: (topics) => socket?.subscribe(topics),
      listen: (fn) => (socket ? socket.listen({ onEvent: fn }) : () => {}),
    }),
    [socket],
  );
  useEffect(() => {
    socket?.subscribe([
      `notices:${userId}`,
      `chat:${userId}`,
      ...(canDecide ? [`approvals:${userId}`] : []),
    ]);
  }, [socket, userId, canDecide]);
  // the language the user chose follows him to any browser
  useEffect(() => {
    const l = me.user.language;
    if ((l === "pt-BR" || l === "en") && l !== i18n.language) void i18n.changeLanguage(l);
  }, [me.user.language, i18n]);
  // "(N) Mestre": the approvals that wait for the user show in the tab, too (docs/painel/vida-da-mesa.md)
  const { data: count } = useQuery({ ...approvalCountQuery, enabled: canDecide });
  const waiting = canDecide ? (count?.pending ?? 0) : 0;
  useEffect(() => {
    document.title = waiting > 0 ? `(${waiting}) ${TITLE}` : TITLE;
  }, [waiting]);
  useEffect(() => {
    document.documentElement.lang = i18n.language;
  }, [i18n.language]);
  return (
    <LiveContext.Provider value={live}>
      <ConnectionContext.Provider value={state}>
        <ChatLive />
        <ChatControlContext.Provider value={chatControl}>
          <div
            className={`grid min-h-dvh grid-rows-[auto_1fr] ${chatOpen ? "md:grid-cols-[14rem_1fr_26rem]" : "md:grid-cols-[14rem_1fr]"}`}
          >
            <a
              href="#content"
              className="sr-only focus:not-sr-only focus:absolute focus:z-50 focus:bg-surface focus:p-2"
            >
              {t("top.skip")}
            </a>
            {/* placed by hand: the chat holds column 3 of both rows, and an auto-placed bar
                that spans every column would drop below it */}
            <div className="md:col-start-1 md:col-end-3 md:row-start-1">
              <TopBar chatOpen={chatOpen} onChat={() => setChatOpen((o) => !o)} />
              <ReconnectingBand state={state} />
              <TransferBanner />
            </div>
            <Nav path={path} />
            <main id="content" className="min-w-0 px-5 pt-5 pb-24 md:px-10 md:pt-6 md:pb-8">
              <SocketContext.Provider value={socket}>
                <Breadcrumbs path={path} />
                <Outlet />
              </SocketContext.Provider>
            </main>
            {chatOpen ? (
              <div className="fixed inset-0 z-40 md:sticky md:inset-auto md:top-0 md:z-auto md:col-start-3 md:row-span-2 md:row-start-1 md:h-dvh md:border-l md:border-line">
                <ChatPanel onClose={() => setChatOpen(false)} />
              </div>
            ) : null}
          </div>
        </ChatControlContext.Provider>
      </ConnectionContext.Provider>
    </LiveContext.Provider>
  );
}
