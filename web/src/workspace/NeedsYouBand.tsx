import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import type { TableBlock } from "../gen/panel";
import { SituationChip } from "./SituationChip";
import type { Situation } from "./situation";
import { useSituationLine } from "./useSituationLine";

export interface Pending {
  block: TableBlock;
  situation: Situation;
}

/**
 * "Precisa de você": the only name of what waits for the user, here and on the Home: the approvals the
 * user may decide and the tables that are stuck or deliver nothing. Without any, it says so.
 */
export function NeedsYouBand({ items, workspace }: { items: Pending[]; workspace: string }) {
  const { t } = useTranslation("workspace");
  const line = useSituationLine();
  if (items.length === 0) {
    return <p className="m-0 text-muted">{t("life.need.none")}</p>;
  }
  return (
    <section
      aria-label={t("life.need.title", { count: items.length })}
      className="flex flex-col gap-2 rounded-field border border-accent bg-accent-wash p-3"
    >
      <h2 className="m-0 text-lg">{t("life.need.title", { count: items.length })}</h2>
      <ul className="m-0 flex list-none flex-col gap-2 p-0">
        {items.map(({ block, situation }) => (
          <li
            key={block.id}
            className="flex flex-wrap items-center gap-x-3 gap-y-2 rounded-field border border-line bg-surface p-2"
          >
            <SituationChip s={situation} />
            <span className="min-w-[14rem] flex-1">
              <strong>{block.epic_title || block.id}</strong>
              <span className="block text-sm text-muted">{line(block, situation)}</span>
            </span>
            {situation.key === "approval" ? (
              <Link
                to="/aprovacoes"
                search={{ mesa: block.id, campanha: workspace }}
                className="inline-flex min-h-11 items-center rounded-field bg-accent px-4 font-semibold text-accent-ink hover:bg-accent-strong"
              >
                {t("life.card.viewRequest")}
              </Link>
            ) : (
              <Link
                to="/workspaces/$workspace/mesas/$table"
                params={{ workspace, table: block.id }}
                aria-label={t("life.card.openNamed", { table: block.id })}
                className="inline-flex min-h-11 items-center rounded-field bg-accent px-4 font-semibold text-accent-ink hover:bg-accent-strong"
              >
                {t("life.card.openTable")}
              </Link>
            )}
          </li>
        ))}
      </ul>
    </section>
  );
}
