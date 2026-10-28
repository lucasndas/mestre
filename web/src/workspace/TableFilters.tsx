import { useTranslation } from "react-i18next";
import { FILTERS, type FilterKey } from "./situation";

export type ViewMode = "board" | "list";

/** The filters by situation, each with its count, and the way to see the tables (the board is only on a wide screen). */
export function TableFilters({
  counts,
  filter,
  onFilter,
  view,
  onView,
  wide,
  ended,
  showEnded,
  onShowEnded,
}: {
  counts: Record<FilterKey, number>;
  filter: FilterKey;
  onFilter: (f: FilterKey) => void;
  view: ViewMode;
  onView: (v: ViewMode) => void;
  wide: boolean;
  /** How many ended tables the view has, hidden unless the chip shows them. */
  ended: number;
  showEnded: boolean;
  onShowEnded: (on: boolean) => void;
}) {
  const { t } = useTranslation("workspace");
  const chip = (on: boolean) =>
    `inline-flex min-h-11 items-center gap-1.5 rounded-full border px-3 text-sm ${
      on ? "border-ink bg-ink text-paper" : "border-line bg-surface hover:bg-accent-wash"
    }`;
  return (
    <div className="flex flex-wrap items-center gap-2">
      <fieldset className="m-0 flex min-w-0 flex-wrap gap-2 border-0 p-0">
        <legend className="sr-only">{t("life.filters.label")}</legend>
        {FILTERS.map((f) => (
          <button
            key={f}
            type="button"
            aria-pressed={filter === f}
            onClick={() => onFilter(f)}
            className={chip(filter === f)}
          >
            {t(`life.filters.${f}`)}{" "}
            <span className="font-mono text-xs opacity-75">{counts[f]}</span>
          </button>
        ))}
        {ended > 0 ? (
          <button
            type="button"
            aria-pressed={showEnded}
            onClick={() => onShowEnded(!showEnded)}
            className={chip(showEnded)}
          >
            {t("ended.show")} <span className="font-mono text-xs opacity-75">{ended}</span>
          </button>
        ) : null}
      </fieldset>
      {wide ? (
        <fieldset className="m-0 ml-auto flex min-w-0 border-0 p-0">
          <legend className="sr-only">{t("life.view.label")}</legend>
          {(["board", "list"] as const).map((v) => (
            <button
              key={v}
              type="button"
              aria-pressed={view === v}
              onClick={() => onView(v)}
              className={`min-h-11 border border-line px-3 text-sm first:rounded-l-field last:rounded-r-field ${
                view === v ? "border-ink bg-ink text-paper" : "bg-surface hover:bg-accent-wash"
              }`}
            >
              {t(`life.view.${v}`)}
            </button>
          ))}
        </fieldset>
      ) : null}
    </div>
  );
}
