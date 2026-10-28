import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { terminalQuery } from "../lib/queries";
import { settingsOf } from "../screens/settingsMap";
import { viewQuery } from "../workspace/queries";
import { CampaignSwitch } from "../workspace/WorkspaceSelector";

// the levels between the entry point and the page are links too, but only the entry point is loud
const LINK_QUIET =
  "inline-flex min-h-11 items-center rounded-field px-1.5 text-muted hover:text-ink hover:underline underline-offset-4";
const LINK =
  "inline-flex min-h-11 items-center rounded-field px-1.5 text-accent underline underline-offset-4";

/** The pages under Usuários that are steps of one task, and the catalog key of each in the trail. */
const INVITE_STEPS: Record<string, "invite" | "invitation"> = {
  "/usuarios/convidar": "invite",
  "/usuarios/convite": "invitation",
};

function Trail({ items }: { items: ReactNode[] }) {
  const { t } = useTranslation("shell");
  return (
    <nav aria-label={t("crumbs.label")} className="mb-3">
      <ol className="flex flex-wrap items-center gap-x-1">
        {items.map((it, i) => (
          // the trail is rebuilt whole from the address, in a fixed order
          // biome-ignore lint/suspicious/noArrayIndexKey: no item has an id of its own
          <li key={i} className="flex items-center gap-1">
            {/* the mark leads the item, so a line never ends on a lone mark */}
            {i > 0 ? (
              <span aria-hidden="true" className="text-muted">
                ›
              </span>
            ) : null}
            {it}
          </li>
        ))}
      </ol>
    </nav>
  );
}

function Here({ children }: { children: ReactNode }) {
  return (
    <span aria-current="page" className="inline-flex min-h-11 items-center px-1.5 font-semibold">
      {children}
    </span>
  );
}

function TableTrail({ workspace, table }: { workspace: string; table: string }) {
  const { t } = useTranslation("shell");
  const { data } = useQuery({ ...viewQuery(workspace) });
  const found = data?.tables.find((b) => b.id === table);
  return (
    <Trail
      items={[
        <Link key="c" to="/workspaces" className={LINK}>
          {t("nav.workspaces")}
        </Link>,
        <CampaignSwitch key="w" slug={workspace} last={false} />,
        <Here key="t">{found?.epic_title || table}</Here>,
      ]}
    />
  );
}

function TerminalTrail({ character }: { character: string }) {
  const { t } = useTranslation("shell");
  const { data } = useQuery(terminalQuery(character));
  const workspace = data?.workspace ?? "";
  const table = data?.table ?? "";
  // the title of the table is in the view of the campaign; the id stands in until it is read
  const { data: view } = useQuery({
    ...viewQuery(workspace),
    enabled: workspace !== "" && table !== "",
  });
  const found = view?.tables.find((b) => b.id === table);
  return (
    <Trail
      items={[
        <Link key="c" to="/workspaces" className={LINK}>
          {t("nav.workspaces")}
        </Link>,
        ...(workspace ? [<CampaignSwitch key="w" slug={workspace} last={false} />] : []),
        ...(workspace && table
          ? [
              <Link
                key="m"
                to="/workspaces/$workspace/mesas/$table"
                params={{ workspace, table }}
                className={LINK}
              >
                {found?.epic_title || table}
              </Link>,
            ]
          : []),
        <Here key="t">{t("crumbs.terminal", { name: data?.name ?? character })}</Here>,
      ]}
    />
  );
}

/**
 * The trail above the title of a screen with more than one level (docs/painel/design.md#estrutura-da-tela):
 * Campanhas › campanha › mesa, Configurações › grupo › tela. Each level above is a link; the last is the page.
 */
export function Breadcrumbs({ path }: { path: string }) {
  const { t } = useTranslation("shell");
  const say = t as unknown as (k: string) => string;
  const cfg = settingsOf(path);
  // the two steps of the invite sit under Usuários: the screen is a link and the step is the page
  const step = INVITE_STEPS[path.replace(/\/$/, "")];
  if (cfg && step)
    return (
      <Trail
        items={[
          <Link key="c" to="/configuracoes" className={LINK}>
            {t("nav.settings")}
          </Link>,
          <Link key="g" to="/configuracoes" hash={`grupo-${cfg.group.key}`} className={LINK_QUIET}>
            {say(`settings.groups.${cfg.group.key}.name`)}
          </Link>,
          <Link key="e" to="/usuarios" className={LINK_QUIET}>
            {say(`settings.entries.${cfg.entry.key}.name`)}
          </Link>,
          <Here key="s">{say(`crumbs.${step}`)}</Here>,
        ]}
      />
    );
  if (cfg)
    return (
      <Trail
        items={[
          <Link key="c" to="/configuracoes" className={LINK}>
            {t("nav.settings")}
          </Link>,
          <Link key="g" to="/configuracoes" hash={`grupo-${cfg.group.key}`} className={LINK_QUIET}>
            {say(`settings.groups.${cfg.group.key}.name`)}
          </Link>,
          <Here key="e">{say(`settings.entries.${cfg.entry.key}.name`)}</Here>,
        ]}
      />
    );
  if (path === "/aprovacoes/historico")
    return (
      <Trail
        items={[
          <Link key="a" to="/aprovacoes" search={{}} className={LINK}>
            {t("nav.approvals")}
          </Link>,
          <Here key="h">{t("crumbs.history")}</Here>,
        ]}
      />
    );
  const table = /^\/workspaces\/([^/]+)\/mesas\/([^/]+)/.exec(path);
  if (table)
    return (
      <TableTrail
        workspace={decodeURIComponent(table[1] ?? "")}
        table={decodeURIComponent(table[2] ?? "")}
      />
    );
  const ws = /^\/workspaces\/([^/]+)\/?$/.exec(path);
  if (ws)
    return (
      <Trail
        items={[
          <Link key="c" to="/workspaces" className={LINK}>
            {t("nav.workspaces")}
          </Link>,
          <CampaignSwitch key="w" slug={decodeURIComponent(ws[1] ?? "")} last />,
        ]}
      />
    );
  const term = /^\/terminal\/([^/]+)/.exec(path);
  if (term) return <TerminalTrail character={decodeURIComponent(term[1] ?? "")} />;
  return null;
}
