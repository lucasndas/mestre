import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { useCan } from "../../app/session";
import { approvalsQuery } from "../../lib/approvalQueries";
import { useLiveEvents } from "../../lib/live";
import { Badge } from "../admin/kit";

const ALL = { workspace: "", table: "", type: "" };

/** "Esperam por você": the requests the user may decide; the section is not even asked for who cannot decide. */
export function Waiting() {
  const { t } = useTranslation("home");
  const can = useCan("decide_approvals");
  const qc = useQueryClient();
  const { data } = useQuery({ ...approvalsQuery(ALL), enabled: can });
  useLiveEvents("approvals:", () => void qc.invalidateQueries({ queryKey: ["approvals", "list"] }));
  const pending = (data?.requests ?? []).filter((r) => !r.closed && r.answers.length > 0);
  if (!can || pending.length === 0) return null;
  return (
    <section className="mt-8" aria-labelledby="home-waiting">
      <h2 id="home-waiting" className="mb-3 text-xl">
        {t("waiting.title")}
      </h2>
      <ul className="rounded-field border border-line bg-surface px-3">
        {pending.map((r) => (
          <li key={r.id} className="border-t border-line first:border-t-0">
            <Link
              to="/aprovacoes"
              search={{ pedido: r.id }}
              aria-label={t("waiting.open", { id: r.id, about: r.about })}
              className="flex min-h-16 flex-wrap items-center justify-between gap-2 rounded-field px-1 py-3 hover:bg-accent-wash"
            >
              <span className="min-w-0">
                <strong>{r.about}</strong>
                <span className="block text-sm text-muted">
                  {t("waiting.item", {
                    id: r.id,
                    where: [r.workspace, r.table].filter(Boolean).join(" · "),
                  })}
                </span>
              </span>
              <Badge tone="warn">{t("waiting.decide")}</Badge>
            </Link>
          </li>
        ))}
      </ul>
    </section>
  );
}
