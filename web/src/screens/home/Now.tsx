import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { useFormatDateTime } from "../../app/session";
import { Alert } from "../../components/ui";
import { Badge } from "../admin/kit";
import type { HomeTable } from "./reads";

type Say = (k: string, o?: object) => string;

function Row({ item }: { item: HomeTable }) {
  const { t } = useTranslation("home");
  const say = t as unknown as Say;
  const fmt = useFormatDateTime();
  const b = item.block;
  const title = b.epic_title || b.id;
  const machines = b.party.map((m) => m.machine_name).join(", ");
  return (
    <li className="border-t border-line first:border-t-0">
      <Link
        to="/workspaces/$workspace/mesas/$table"
        params={{ workspace: item.workspace, table: b.id }}
        aria-label={t("now.open", { table: title, workspace: item.workspace })}
        className="flex min-h-16 flex-wrap items-center justify-between gap-2 rounded-field px-1 py-3 hover:bg-accent-wash"
      >
        <span className="min-w-0">
          <strong>{title}</strong>
          <span className="block text-sm text-muted">
            {item.workspace} ·{" "}
            {b.party_size > 0 ? t("now.party", { count: b.party_size, machines }) : t("now.nobody")}
          </span>
          <span className="block text-sm text-muted">
            {b.last_delivery
              ? t("now.lastDelivery", { when: fmt(b.last_delivery) })
              : t("now.noDelivery")}
          </span>
        </span>
        <span className="flex items-center gap-2">
          {b.paused ? <Badge>{t("now.paused")}</Badge> : null}
          <Badge tone={b.stalled ? "warn" : "plain"}>
            {b.stalled
              ? t("now.stalled", { minutes: Math.floor(b.quiet_seconds / 60) })
              : say(`now.state.${b.state}`) === `now.state.${b.state}`
                ? b.state
                : say(`now.state.${b.state}`)}
          </Badge>
        </span>
      </Link>
    </li>
  );
}

/** "Acontecendo agora": the tables not closed, of every campaign the user sees. */
export function Now({ tables, failed }: { tables: HomeTable[]; failed: string[] }) {
  const { t } = useTranslation("home");
  return (
    <section className="mt-8" aria-labelledby="home-now">
      <h2 id="home-now" className="mb-3 text-xl">
        {t("now.title")}
      </h2>
      {failed.map((w) => (
        <Alert key={w}>{t("now.failed", { workspace: w })}</Alert>
      ))}
      {tables.length > 0 ? (
        <ul className="rounded-field border border-line bg-surface px-3">
          {tables.map((it) => (
            <Row key={`${it.workspace}/${it.block.id}`} item={it} />
          ))}
        </ul>
      ) : (
        <div className="rounded-field border border-dashed border-line p-5 text-muted">
          <strong className="block text-ink">{t("now.emptyTitle")}</strong>
          {t("now.emptyBody")}
        </div>
      )}
    </section>
  );
}
