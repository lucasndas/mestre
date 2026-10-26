import { useQueryClient } from "@tanstack/react-query";
import { Link, Outlet, useNavigate } from "@tanstack/react-router";
import { useEffect, useState } from "react";
import { Button as AriaButton, Menu, MenuItem, MenuTrigger, Popover } from "react-aria-components";
import { useTranslation } from "react-i18next";
import { api } from "../lib/api";
import { type SocketState, UiSocket } from "../lib/ws";
import { Brand } from "./AuthLayout";
import { LanguageSwitcher } from "./LanguageSwitcher";
import { useMe } from "./session";

/** The band that tells the Citadel is out of reach; the state is reloaded when it is back. */
function useConnection(): SocketState {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [state, setState] = useState<SocketState>("connecting");
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
    return () => sock.stop();
  }, [qc, navigate]);
  return state;
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

function NavLink({ to, children }: { to: "/" | "/sessoes" | "/senha"; children: string }) {
  return (
    <Link
      to={to}
      search={to === "/sessoes" ? { todas: false } : undefined}
      activeOptions={{ exact: true }}
      className="block rounded-field px-3 py-2 whitespace-nowrap text-muted transition-colors hover:bg-accent-wash [&.active]:bg-accent-wash [&.active]:font-semibold [&.active]:text-ink"
    >
      {children}
    </Link>
  );
}

function Nav() {
  const { t } = useTranslation("shell");
  return (
    <nav
      aria-label={t("nav.main")}
      className="flex gap-1 overflow-x-auto border-b border-line bg-surface p-2 md:flex-col md:border-r md:border-b-0 md:p-3"
    >
      <NavLink to="/">{t("nav.overview")}</NavLink>
      <NavLink to="/sessoes">{t("nav.sessions")}</NavLink>
      <NavLink to="/senha">{t("nav.password")}</NavLink>
    </nav>
  );
}

function TopBar() {
  const { t } = useTranslation("shell");
  const me = useMe();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const logout = async () => {
    try {
      await api("POST", "/api/logout");
    } finally {
      qc.clear();
      void navigate({ to: "/login", search: {} });
    }
  };
  return (
    <header className="flex items-center gap-3 border-b border-line bg-surface px-4 py-2">
      <Brand />
      {/* Slots of the later units: the workspace selector, the counter of pending
          approvals and the bell of the notices (docs/painel/design.md#a-barra-de-cima). */}
      <div data-slot="workspace-selector" className="flex items-center" />
      <div className="flex-1" />
      <div data-slot="approvals-counter" className="flex items-center" />
      <div data-slot="notices-bell" className="flex items-center" />
      <LanguageSwitcher />
      <MenuTrigger>
        <AriaButton
          aria-label={t("top.menu")}
          className="grid size-9 place-items-center rounded-full bg-accent-wash text-sm font-semibold hover:bg-line"
        >
          {me.user.name.slice(0, 2).toUpperCase()}
        </AriaButton>
        <Popover className="min-w-40 rounded-field border border-line bg-surface p-1 shadow-pop">
          <Menu className="outline-none" onAction={(k) => k === "logout" && void logout()}>
            <MenuItem
              id="logout"
              className="cursor-pointer rounded-field px-3 py-2 outline-none data-[focused]:bg-accent-wash"
            >
              {t("top.logout")}
            </MenuItem>
          </Menu>
        </Popover>
      </MenuTrigger>
    </header>
  );
}

export function Shell() {
  const { t } = useTranslation("shell");
  const { i18n } = useTranslation();
  const me = useMe();
  const state = useConnection();
  // the language the user chose follows him to any browser
  useEffect(() => {
    const l = me.user.language;
    if ((l === "pt-BR" || l === "en") && l !== i18n.language) void i18n.changeLanguage(l);
  }, [me.user.language, i18n]);
  useEffect(() => {
    document.documentElement.lang = i18n.language;
  }, [i18n.language]);
  return (
    <div className="grid min-h-dvh grid-rows-[auto_auto_1fr] md:grid-cols-[14rem_1fr] md:grid-rows-[auto_auto_1fr]">
      <a
        href="#content"
        className="sr-only focus:not-sr-only focus:absolute focus:z-50 focus:bg-surface focus:p-2"
      >
        {t("top.skip")}
      </a>
      <div className="md:col-span-2">
        <TopBar />
        <ReconnectingBand state={state} />
      </div>
      <Nav />
      <main id="content" className="min-w-0 px-5 py-8 md:px-10">
        <Outlet />
      </main>
    </div>
  );
}
