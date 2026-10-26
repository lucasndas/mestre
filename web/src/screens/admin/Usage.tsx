import { useQuery } from "@tanstack/react-query";
import { lazy, Suspense, useState } from "react";
import { useTranslation } from "react-i18next";
import { Alert } from "../../components/ui";
import type { UsageLimit, UsagePage, UsageTotals } from "../../gen/panel";
import { errorText } from "../../lib/errors";
import { formatNumber } from "../../lib/format";
import { NativeSelect, PageHeader } from "./kit";
import { type Say, usageQuery } from "./manage";

// Recharts is heavy: the charts load when the screen opens.
const ColumnChart = lazy(() => import("./UsageCharts").then((m) => ({ default: m.ColumnChart })));
const RowsChart = lazy(() => import("./UsageCharts").then((m) => ({ default: m.RowsChart })));

const PERIODS = [7, 14, 30, 90];
const GROUPS = ["workspaces", "tables", "quests", "characters", "accounts"] as const;

/** Usage by day, workspace, table, mission, character and account, and the limits with what is left. */
export function Usage() {
  const { t, i18n } = useTranslation("usage");
  const { t: te } = useTranslation("errors");
  const [days, setDays] = useState(14);
  const { data, error } = useQuery(usageQuery(days));
  const lang = i18n.language;

  return (
    <section>
      <PageHeader title={t("title")} intro={t("intro")} />
      <div className="flex max-w-5xl flex-col gap-8">
        <div className="max-w-xs">
          <NativeSelect
            label={t("period")}
            value={String(days)}
            onChange={(v) => setDays(Number(v))}
            options={PERIODS.map((d) => ({ value: String(d), label: t("days", { count: d }) }))}
          />
        </div>
        {error ? <Alert>{errorText(te, error)}</Alert> : null}
        {!data && !error ? <p className="text-muted">{t("loading")}</p> : null}
        {data ? <Body page={data} lang={lang} /> : null}
      </div>
    </section>
  );
}

function Body({ page, lang }: { page: UsagePage; lang: string }) {
  const { t } = useTranslation("usage");
  const say = t as unknown as Say;
  const empty = GROUPS.every((g) => page[g].length === 0) && page.days.length === 0;
  const usd = (n: number) =>
    new Intl.NumberFormat(lang, {
      style: "currency",
      currency: "USD",
      maximumFractionDigits: n < 10 ? 2 : 0,
    }).format(n);
  const duration = (secs: number) => {
    if (secs >= 3600)
      return t("hours", { n: formatNumber(Math.round((secs / 3600) * 10) / 10, lang) });
    if (secs >= 60) return t("minutes", { n: Math.round(secs / 60) });
    return t("seconds", { n: secs });
  };
  const hours = (n: number) => `${formatNumber(Math.round(n * 10) / 10, lang)} h`;
  return (
    <>
      <p className="text-sm text-muted">{t("noDataNote")}</p>
      {empty ? <p className="text-muted">{t("empty")}</p> : null}
      <Limits limits={page.limits} lang={lang} />
      <Daily days={page.days} usd={usd} duration={duration} hours={hours} lang={lang} />
      {GROUPS.map((g) =>
        page[g].length > 0 ? (
          <Group
            key={g}
            id={g}
            title={say(`groups.${g}`)}
            caption={say(`captions.${g}`)}
            rows={page[g]}
            usd={usd}
            duration={duration}
            lang={lang}
            withWorkspace={g === "tables" || g === "quests" || g === "characters"}
          />
        ) : null,
      )}
    </>
  );
}

type Fmt = {
  usd: (n: number) => string;
  duration: (s: number) => string;
  lang: string;
};

function costCell(t: (k: "noData") => string, v: number | undefined, usd: (n: number) => string) {
  return v === undefined || v === null ? <span className="text-muted">{t("noData")}</span> : usd(v);
}

function Daily({
  days,
  usd,
  duration,
  hours,
  lang,
}: Fmt & { days: UsageTotals[]; hours: (n: number) => string }) {
  const { t } = useTranslation("usage");
  if (days.length === 0) return null;
  const label = (key: string) =>
    new Intl.DateTimeFormat(lang, { day: "2-digit", month: "2-digit", timeZone: "UTC" }).format(
      new Date(`${key}T00:00:00Z`),
    );
  return (
    <section aria-labelledby="usage-daily" className="flex flex-col gap-4">
      <h2 id="usage-daily" className="text-2xl">
        {t("daily.title")}
      </h2>
      <div className="grid gap-6 md:grid-cols-2">
        <Suspense fallback={<p className="text-muted">{t("loading")}</p>}>
          <ColumnChart
            title={t("daily.chart")}
            color="var(--color-accent)"
            format={usd}
            data={days.map((d) => ({ label: label(d.key), value: d.cost_usd ?? undefined }))}
          />
          <ColumnChart
            title={t("daily.chartHours")}
            color="var(--color-ink)"
            format={hours}
            data={days.map((d) => ({ label: label(d.key), value: d.active_seconds / 3600 }))}
          />
        </Suspense>
      </div>
      <div className="overflow-x-auto">
        <table className="w-full min-w-[28rem] border-collapse text-left">
          <caption className="sr-only">{t("daily.caption")}</caption>
          <thead>
            <tr className="border-b border-line text-sm text-muted">
              <th scope="col" className="py-2 pr-4">
                {t("daily.day")}
              </th>
              <th scope="col" className="py-2 pr-4">
                {t("cost")}
              </th>
              <th scope="col" className="py-2 pr-4">
                {t("tokens")}
              </th>
              <th scope="col" className="py-2 pr-4">
                {t("active")}
              </th>
              <th scope="col" className="py-2">
                {t("turns")}
              </th>
            </tr>
          </thead>
          <tbody>
            {days.map((d) => (
              <tr key={d.key} className="border-b border-line">
                <th scope="row" className="py-2 pr-4 font-semibold">
                  {d.key}
                </th>
                <td className="py-2 pr-4">{costCell(t, d.cost_usd, usd)}</td>
                <td className="py-2 pr-4">
                  {d.tokens == null ? (
                    <span className="text-muted">{t("noData")}</span>
                  ) : (
                    formatNumber(d.tokens, lang)
                  )}
                </td>
                <td className="py-2 pr-4">{duration(d.active_seconds)}</td>
                <td className="py-2">{d.turns}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}

function Group({
  id,
  title,
  caption,
  rows,
  usd,
  duration,
  lang,
  withWorkspace,
}: Fmt & {
  id: string;
  title: string;
  caption: string;
  rows: UsageTotals[];
  withWorkspace: boolean;
}) {
  const { t } = useTranslation("usage");
  // the bars show the cost; a row with no cost is a gap, and the time is in the table
  const hasCost = rows.some((r) => r.cost_usd != null);
  return (
    <section aria-labelledby={`usage-${id}`} className="flex flex-col gap-4">
      <h2 id={`usage-${id}`} className="text-2xl">
        {title}
      </h2>
      {hasCost ? (
        <Suspense fallback={<p className="text-muted">{t("loading")}</p>}>
          <RowsChart
            title={`${title} · ${t("cost")}`}
            color="var(--color-accent)"
            format={usd}
            data={rows.map((r) => ({ label: r.label, value: r.cost_usd ?? undefined }))}
          />
        </Suspense>
      ) : null}
      <div className="overflow-x-auto">
        <table className="w-full min-w-[32rem] border-collapse text-left">
          <caption className="sr-only">{caption}</caption>
          <thead>
            <tr className="border-b border-line text-sm text-muted">
              <th scope="col" className="py-2 pr-4">
                {t("name")}
              </th>
              {withWorkspace ? (
                <th scope="col" className="py-2 pr-4">
                  {t("workspace")}
                </th>
              ) : null}
              <th scope="col" className="py-2 pr-4">
                {t("cost")}
              </th>
              <th scope="col" className="py-2 pr-4">
                {t("tokens")}
              </th>
              <th scope="col" className="py-2 pr-4">
                {t("active")}
              </th>
              <th scope="col" className="py-2">
                {t("turns")}
              </th>
            </tr>
          </thead>
          <tbody>
            {rows.map((r) => (
              <tr key={r.key} className="border-b border-line">
                <th scope="row" className="py-2 pr-4 font-semibold">
                  {r.label}
                </th>
                {withWorkspace ? <td className="py-2 pr-4">{r.workspace}</td> : null}
                <td className="py-2 pr-4">{costCell(t, r.cost_usd, usd)}</td>
                <td className="py-2 pr-4">
                  {r.tokens == null ? (
                    <span className="text-muted">{t("noData")}</span>
                  ) : (
                    formatNumber(r.tokens, lang)
                  )}
                </td>
                <td className="py-2 pr-4">{duration(r.active_seconds)}</td>
                <td className="py-2">{r.turns}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}

function Limits({ limits, lang }: { limits: UsageLimit[]; lang: string }) {
  const { t } = useTranslation("usage");
  const say = t as unknown as Say;
  const num = (l: UsageLimit, n: number) => {
    const v =
      l.unit === "usd"
        ? new Intl.NumberFormat(lang, { style: "currency", currency: "USD" }).format(n)
        : formatNumber(Math.round(n * 100) / 100, lang);
    return l.unit === "usd" ? v : `${v} ${say(`limits.unit.${l.unit}`)}`;
  };
  return (
    <section aria-labelledby="usage-limits" className="flex flex-col gap-3">
      <h2 id="usage-limits" className="text-2xl">
        {t("limits.title")}
      </h2>
      {limits.length === 0 ? <p className="text-muted">{t("limits.empty")}</p> : null}
      <ul className="flex flex-col gap-4">
        {limits.map((l) => {
          const name = `${say(`limits.scope.${l.scope}`)}${l.key ? ` ${l.key}` : ""}`;
          const pct = Math.min(100, Math.round(l.percent));
          return (
            <li
              key={`${l.scope}/${l.key}/${l.unit}`}
              className="rounded-field border border-line bg-surface p-4"
            >
              <p className="font-semibold">
                {name}{" "}
                <span className="font-normal text-muted">
                  ({say(`limits.unit.${l.unit}`)} · {t("limits.period", { period: l.period })})
                </span>
              </p>
              {l.has_data ? (
                <>
                  <progress
                    className={`mt-2 h-3 w-full ${l.percent >= 100 ? "accent-danger" : "accent-accent"}`}
                    max={100}
                    value={pct}
                    aria-label={name}
                    aria-valuetext={t("limits.percent", {
                      percent: pct,
                      effective: num(l, l.effective),
                    })}
                  />
                  <p className="mt-1 text-sm">
                    {t("limits.used")}: {num(l, l.used)} · {t("limits.left")}: {num(l, l.left)} ·{" "}
                    {t("limits.percent", { percent: pct, effective: num(l, l.effective) })}
                  </p>
                </>
              ) : (
                <p className="mt-1 text-sm text-muted">
                  {t("limits.limit")}: {num(l, l.effective)} · {t("limits.noData")}
                </p>
              )}
            </li>
          );
        })}
      </ul>
    </section>
  );
}
