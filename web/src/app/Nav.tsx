import { Link } from "@tanstack/react-router";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { useApprovalsCount } from "../components/TopBarSlots";
import { type Section, sectionOf } from "../screens/settingsMap";
import { useCan } from "./session";

const ICONS: Record<Section, ReactNode> = {
  home: <path d="M3 11l9-8 9 8v9a1 1 0 0 1-1 1h-5v-6h-6v6H4a1 1 0 0 1-1-1z" />,
  approvals: (
    <>
      <path d="M12 3l8 3v6c0 5-3.5 8-8 9-4.5-1-8-4-8-9V6z" />
      <path d="M8.5 12l2.5 2.5L16 9.5" />
    </>
  ),
  workspaces: (
    <>
      <path d="M12 3l9 5-9 5-9-5z" />
      <path d="M3 13l9 5 9-5" />
    </>
  ),
  settings: (
    <>
      <path d="M4 7h10M18 7h2M4 17h2M10 17h10" />
      <circle cx="16" cy="7" r="2" />
      <circle cx="8" cy="17" r="2" />
    </>
  ),
};

function Icon({ section }: { section: Section }) {
  return (
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
      className="shrink-0"
    >
      {ICONS[section]}
    </svg>
  );
}

const ITEM =
  "relative flex min-h-14 flex-col items-center justify-center gap-0.5 px-0.5 text-[0.875rem] leading-tight tracking-tight text-muted transition-colors hover:bg-accent-wash aria-[current=page]:bg-accent-wash aria-[current=page]:font-semibold aria-[current=page]:text-ink md:min-h-12 md:flex-row md:justify-start md:gap-3 md:rounded-field md:px-3 md:text-base";

function Count({ n }: { n: number }) {
  if (n <= 0) return null;
  return (
    <span
      aria-hidden="true"
      className="absolute top-1 left-[calc(50%+0.5rem)] grid min-w-5 place-items-center rounded-full bg-accent px-1 text-xs font-semibold text-accent-ink md:static md:ml-auto"
    >
      {n > 99 ? "99+" : n}
    </span>
  );
}

/**
 * The menu of four items (docs/painel/design.md#estrutura-da-tela): a column at the left on the
 * computer, four tabs at the bottom on the phone. The item of the section of the screen is marked.
 */
export function Nav({ path }: { path: string }) {
  const { t } = useTranslation("shell");
  const { t: ta } = useTranslation("approvals");
  const canDecide = useCan("decide_approvals");
  const pending = useApprovalsCount();
  const current = sectionOf(path);
  const mark = (s: Section) => (current === s ? ("page" as const) : undefined);
  return (
    <nav
      aria-label={t("nav.main")}
      className="fixed inset-x-0 bottom-0 z-30 grid auto-cols-fr grid-flow-col border-t border-line bg-surface md:static md:z-auto md:flex md:flex-col md:gap-1 md:border-t-0 md:border-r md:p-3"
    >
      <Link to="/" activeProps={{}} inactiveProps={{}} aria-current={mark("home")} className={ITEM}>
        <Icon section="home" />
        <span>{t("nav.home")}</span>
      </Link>
      {canDecide ? (
        <Link
          to="/aprovacoes"
          search={{}}
          activeProps={{}}
          inactiveProps={{}}
          aria-current={mark("approvals")}
          aria-label={pending > 0 ? ta("counter.label", { count: pending }) : ta("counter.none")}
          className={ITEM}
        >
          <Icon section="approvals" />
          <span aria-hidden="true">{t("nav.approvals")}</span>
          <Count n={pending} />
        </Link>
      ) : null}
      <Link
        to="/workspaces"
        activeProps={{}}
        inactiveProps={{}}
        aria-current={mark("workspaces")}
        className={ITEM}
      >
        <Icon section="workspaces" />
        <span>{t("nav.workspaces")}</span>
      </Link>
      <Link
        to="/configuracoes"
        activeProps={{}}
        inactiveProps={{}}
        aria-current={mark("settings")}
        className={ITEM}
      >
        <Icon section="settings" />
        <span>{t("nav.settings")}</span>
      </Link>
    </nav>
  );
}
