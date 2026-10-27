import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useEffect } from "react";
import { useTranslation } from "react-i18next";
import { useCan } from "../../app/session";
import { useLive } from "../../lib/live";
import { useFormatTime } from "../../lib/time";
import { guildQuery } from "../admin/Guild";
import { Badge } from "../admin/kit";

/** "Saúde da Guilda": the machines and since when one has no signal; only for who sees the Guild. */
export function Health() {
  const { t } = useTranslation("home");
  const can = useCan("manage_machines");
  const fmtTime = useFormatTime();
  const { data } = useQuery({ ...guildQuery, enabled: can });
  const live = useLive();
  useEffect(() => (can ? live.subscribe(["guild:main"]) : undefined), [live, can]);
  if (!can || !data) return null;
  const machines = data.machines.filter((m) => !m.excluded);
  return (
    <aside
      aria-labelledby="home-health"
      className="rounded-field border border-line bg-surface p-4"
    >
      <h2 id="home-health" className="text-lg">
        {t("health.title")}
      </h2>
      <ul className="mt-2">
        {machines.map((m) => (
          <li
            key={m.id}
            className="flex items-center justify-between gap-2 border-t border-line py-2 first:border-t-0"
          >
            <span>
              {m.name}
              <small className="block text-muted">
                {m.online
                  ? t("health.connected")
                  : m.last_seen
                    ? t("health.since", { time: fmtTime(m.last_seen) })
                    : t("health.never")}
              </small>
            </span>
            <Badge tone={m.online ? "ok" : "danger"}>
              {m.online ? t("health.ok") : t("health.down")}
            </Badge>
          </li>
        ))}
      </ul>
      <Link to="/guilda" className="mt-3 inline-flex min-h-11 items-center underline">
        {t("health.see")}
      </Link>
    </aside>
  );
}
