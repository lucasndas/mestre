import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useCan, useFormatDateTime } from "../app/session";
import { Alert, Button } from "../components/ui";
import { api } from "../lib/api";
import { deviceLabel } from "../lib/device";
import { errorText } from "../lib/errors";
import { sessionsQuery } from "../lib/queries";

/** My sessions; for the Owner, also everyone's (`todas` in the address). */
export function Sessions({ all }: { all: boolean }) {
  const { t } = useTranslation("sessions");
  const { t: te } = useTranslation("errors");
  const owner = useCan("manage_users");
  const showAll = all && owner;
  const fmt = useFormatDateTime();
  const qc = useQueryClient();
  const { data, error } = useQuery(sessionsQuery(showAll));
  const [notice, setNotice] = useState<string | null>(null);
  const end = useMutation({
    mutationFn: (id: string) => api("DELETE", `/api/sessions/${id}`),
    onSuccess: async () => {
      setNotice(t("ended"));
      await qc.invalidateQueries({ queryKey: ["sessions"] });
    },
  });
  const list = data?.sessions ?? [];
  return (
    <section>
      <h1 className="text-3xl">{showAll ? t("titleAll") : t("title")}</h1>
      <p className="mt-1 mb-6 max-w-2xl text-muted">{showAll ? t("introAll") : t("intro")}</p>
      {owner ? (
        <nav className="mb-4 flex gap-2 text-sm font-semibold">
          <Link
            to="/sessoes"
            search={{ todas: false }}
            className="rounded-field px-3 py-1.5 text-muted [&.active]:bg-accent-wash [&.active]:text-ink"
            activeOptions={{ includeSearch: true }}
          >
            {t("mine")}
          </Link>
          <Link
            to="/sessoes"
            search={{ todas: true }}
            className="rounded-field px-3 py-1.5 text-muted [&.active]:bg-accent-wash [&.active]:text-ink"
            activeOptions={{ includeSearch: true }}
          >
            {t("everyone")}
          </Link>
        </nav>
      ) : null}
      <div className="flex max-w-3xl flex-col gap-3">
        {notice ? <Alert tone="ok">{notice}</Alert> : null}
        {error || end.error ? <Alert>{errorText(te, error ?? end.error)}</Alert> : null}
        {data && list.length === 0 ? <p className="text-muted">{t("empty")}</p> : null}
        <ul className="flex flex-col gap-3">
          {list.map((s) => (
            <li
              key={s.id}
              className="flex flex-wrap items-center gap-x-6 gap-y-2 rounded-field border border-line bg-surface p-4"
            >
              <div className="min-w-0 flex-1">
                <p className="truncate font-semibold">
                  {showAll && s.user_name ? `${s.user_name} · ` : ""}
                  {deviceLabel(s.device)}
                  {s.current ? (
                    <span className="ml-2 rounded-full bg-accent-wash px-2 py-0.5 text-xs">
                      {t("current")}
                    </span>
                  ) : null}
                </p>
                <p className="text-sm text-muted">
                  {s.ip} · {t("created")} {fmt(s.created_at)} · {t("lastUsed")}{" "}
                  {fmt(s.last_used_at)}
                </p>
              </div>
              {s.current ? null : (
                <Button
                  variant="secondary"
                  aria-label={t("endNamed", { device: deviceLabel(s.device) })}
                  isDisabled={end.isPending}
                  onPress={() => end.mutate(s.id)}
                >
                  {t("end")}
                </Button>
              )}
            </li>
          ))}
        </ul>
      </div>
    </section>
  );
}
