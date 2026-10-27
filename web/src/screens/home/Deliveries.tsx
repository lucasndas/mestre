import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { useFormatDateTime } from "../../app/session";
import { Badge } from "../admin/kit";
import type { HomeTable } from "./reads";

/** "Entregas recentes": closed tables with a merged PR; nothing when there is none. */
export function Deliveries({ tables }: { tables: HomeTable[] }) {
  const { t } = useTranslation("home");
  const fmt = useFormatDateTime();
  if (tables.length === 0) return null;
  return (
    <section className="mt-8" aria-labelledby="home-deliveries">
      <h2 id="home-deliveries" className="mb-3 text-xl">
        {t("deliveries.title")}
      </h2>
      <ul className="rounded-field border border-line bg-surface px-3">
        {tables.map(({ workspace, block }) => (
          <li
            key={`${workspace}/${block.id}`}
            className="flex flex-wrap items-center justify-between gap-2 border-t border-line py-3 first:border-t-0"
          >
            <span>
              <Link
                to="/workspaces/$workspace/mesas/$table"
                params={{ workspace, table: block.id }}
                className="font-semibold underline"
              >
                {block.epic_title || block.id}
              </Link>
              <span className="block text-sm text-muted">
                {workspace} ·{" "}
                {t("deliveries.item", {
                  pr: `#${block.pr}`,
                  when: block.last_delivery ? fmt(block.last_delivery) : "",
                })}
              </span>
            </span>
            <Badge tone="ok">{t("deliveries.done")}</Badge>
          </li>
        ))}
      </ul>
    </section>
  );
}
