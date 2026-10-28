import { Button as AriaButton } from "react-aria-components";
import { useTranslation } from "react-i18next";
import { Term } from "../components/Term";
import { Button } from "../components/ui";

/**
 * Above the board: the way to select, and what can be done with the selection, in words. With nothing selected it
 * is a fixed hint; with cards selected it names each action and says why one is off.
 */
export function SelectionStrip({
  canEdit,
  count,
  multi,
  setMulti,
  onGroup,
  onSaveGrimoire,
  onClear,
}: {
  canEdit: boolean;
  /** The cards selected (groups are not counted). */
  count: number;
  multi: boolean;
  setMulti: (v: boolean) => void;
  onGroup: () => void;
  onSaveGrimoire: () => void;
  onClear: () => void;
}) {
  const { t } = useTranslation("canvas");
  if (!canEdit)
    return (
      <p className="m-0 flex min-h-11 items-center border-b border-line bg-surface px-3 text-base text-muted">
        {t("selection.readOnly")}
      </p>
    );
  if (count === 0)
    return (
      <div className="flex min-h-11 items-center gap-3 border-b border-line bg-surface px-3 py-1 text-base text-muted">
        <p className="m-0 min-w-0 flex-1">{t("selection.hint")}</p>
        <Button
          variant="secondary"
          aria-pressed={multi}
          onPress={() => setMulti(!multi)}
          className={`shrink-0 ${multi ? "!border-accent !bg-accent-wash" : ""}`}
        >
          {multi ? t("selection.multiOn") : t("selection.multi")}
        </Button>
      </div>
    );
  return (
    <div
      role="toolbar"
      aria-label={t("selection.label")}
      className="flex flex-wrap items-center gap-x-2 gap-y-1 border-b border-line bg-ink px-3 py-2 text-accent-ink"
    >
      <span className="font-semibold">{t("selection.count", { count })}</span>
      <Button
        variant="secondary"
        isDisabled={count < 2}
        onPress={onGroup}
        className="!bg-white/10 !text-accent-ink"
      >
        {t("selection.group")}
      </Button>
      <Button variant="primary" onPress={onSaveGrimoire} className="!bg-accent-wash !text-ink">
        {t("selection.saveGrimoire")}
      </Button>
      <AriaButton
        onPress={onClear}
        className="min-h-11 rounded-field px-3 font-semibold underline underline-offset-4 hover:bg-white/10"
      >
        {t("selection.clear")}
      </AriaButton>
      <p className="m-0 basis-full text-sm opacity-90">
        {count < 2 ? `${t("selection.needTwo")} ` : ""}
        {t("selection.groupHelp")} <Term name="Grimório" /> {t("selection.grimoireHelp")}
      </p>
    </div>
  );
}

/** "Como ler o quadro": the four lines of the board, drawn. */
export function Legend({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation("canvas");
  const rows: { key: string; svg: React.ReactNode }[] = [
    { key: "together", svg: <path d="M0 7h56" stroke="var(--color-muted)" strokeWidth="2.2" /> },
    {
      key: "order",
      svg: (
        <>
          <path d="M0 7h50" stroke="var(--color-accent)" strokeWidth="3" />
          <path d="M44 2l9 5-9 5z" fill="var(--color-accent)" />
        </>
      ),
    },
    {
      key: "waits",
      svg: <path d="M0 7h56" stroke="var(--color-muted)" strokeWidth="2.2" strokeDasharray="7 6" />,
    },
    {
      key: "deadlock",
      svg: (
        <path d="M0 7h56" stroke="var(--color-danger)" strokeWidth="2.2" strokeDasharray="7 6" />
      ),
    },
  ];
  return (
    <section
      aria-label={t("legend.title")}
      className="absolute right-2 bottom-2 z-20 max-w-xs rounded-field border border-line bg-surface p-3 shadow-card"
    >
      <h2 className="m-0 flex items-center justify-between text-lg">
        {t("legend.title")}
        <Button variant="quiet" onPress={onClose}>
          {t("legend.close")}
        </Button>
      </h2>
      <ul className="m-0 mt-1 flex list-none flex-col gap-1 p-0">
        {rows.map((r) => (
          <li key={r.key} className="flex items-center gap-3">
            <svg width="56" height="14" viewBox="0 0 56 14" aria-hidden="true">
              {r.svg}
            </svg>
            {(t as unknown as (k: string) => string)(`legend.${r.key}`)}
          </li>
        ))}
      </ul>
    </section>
  );
}
