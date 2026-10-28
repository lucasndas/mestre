import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { useCan } from "../../app/session";
import { useDynamicT } from "../../components/ApprovalCard";
import { approvalsQuery } from "../../lib/approvalQueries";
import { useLiveEvents } from "../../lib/live";
import { useFormatTime } from "../../lib/time";
import { SituationChip } from "../../workspace/SituationChip";
import { situationOf } from "../../workspace/situation";
import { Badge } from "../admin/kit";
import type { HomeTable } from "./reads";

const ALL = { workspace: "", table: "", type: "" };

/**
 * "Precisa de você": the same name as on the campaign. The requests the user may decide (the
 * section is not even asked for who cannot decide) and the tables that are stuck or deliver nothing.
 */
export function Waiting({ tables }: { tables: HomeTable[] }) {
  const { t, i18n } = useTranslation("home");
  const ta = useDynamicT("approvals");
  const fmt = useFormatTime();
  const can = useCan("decide_approvals");
  const qc = useQueryClient();
  const { data } = useQuery({ ...approvalsQuery(ALL), enabled: can });
  useLiveEvents("approvals:", () => void qc.invalidateQueries({ queryKey: ["approvals", "list"] }));
  const pending = (data?.requests ?? []).filter((r) => !r.closed && r.answers.length > 0);
  // a table that waits for an approval is already here as the request itself
  const stuck = tables
    .map((it) => ({ it, s: situationOf(it.block, can) }))
    .filter((x) => x.s.urgent && x.s.key !== "approval");
  if ((!can || pending.length === 0) && stuck.length === 0) return null;
  return (
    <section className="mt-8" aria-labelledby="home-waiting">
      <h2 id="home-waiting" className="mb-3 text-xl">
        {t("waiting.title")}
      </h2>
      <ul className="rounded-field border border-line bg-surface px-3">
        {stuck.map(({ it, s }) => (
          <li
            key={`${it.workspace}/${it.block.id}`}
            className="border-t border-line first:border-t-0"
          >
            <Link
              to="/workspaces/$workspace/mesas/$table"
              params={{ workspace: it.workspace, table: it.block.id }}
              aria-label={t("waiting.openTable", {
                table: it.block.epic_title || it.block.id,
                workspace: it.workspace,
              })}
              className="flex min-h-16 flex-wrap items-center justify-between gap-2 rounded-field px-1 py-3 hover:bg-accent-wash"
            >
              <span className="min-w-0">
                <strong>{it.block.epic_title || it.block.id}</strong>
                <span className="block text-sm text-muted">{it.workspace}</span>
              </span>
              <SituationChip s={s} />
            </Link>
          </li>
        ))}
        {(can ? pending : []).map((r) => {
          // the words of the catalog, as on the card of the central; the `about` only for a type it lacks
          const known = i18n.exists(`approvals:readable.${r.type}.title`);
          const where = [
            r.workspace,
            r.table,
            ta(`riskBand.${r.risk}`, { defaultValue: r.risk }).toLowerCase(),
            r.deadline ? ta("card.timeLeft", { time: fmt(r.deadline) }) : "",
          ]
            .filter(Boolean)
            .join(" · ");
          return (
            <li key={r.id} className="border-t border-line first:border-t-0">
              <Link
                to="/aprovacoes"
                search={{ pedido: r.id }}
                aria-label={t("waiting.open", { id: r.id, about: r.about })}
                className="flex min-h-16 flex-wrap items-center justify-between gap-2 rounded-field px-1 py-3 hover:bg-accent-wash"
              >
                <span className="min-w-0">
                  <strong>{known ? ta(`readable.${r.type}.title`) : r.about}</strong>
                  <span className="block text-sm text-muted">{where}</span>
                </span>
                <Badge tone="warn">{t("waiting.decide")}</Badge>
              </Link>
            </li>
          );
        })}
      </ul>
    </section>
  );
}
