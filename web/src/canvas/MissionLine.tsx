import { useTranslation } from "react-i18next";
import type { missionLine } from "./status";

/** "Missão entrega · Etapa 1 de 2", with one bar per step: done, now, to do. */
export function MissionLine({
  line,
  dark = false,
}: {
  line: NonNullable<ReturnType<typeof missionLine>>;
  /** On the dark chrome of the terminal window. */
  dark?: boolean;
}) {
  const { t } = useTranslation("canvas");
  return (
    <p className={`m-0 flex items-center gap-2 ${dark ? "text-sm text-term-fg" : "text-base"}`}>
      <span aria-hidden="true" className="inline-flex gap-1">
        {line.bars.map((b, i) => (
          <span
            // the steps of one mission, in order
            // biome-ignore lint/suspicious/noArrayIndexKey: the bars follow the steps by position
            key={i}
            className={`h-1.5 w-4 ${dark ? "rounded-xs" : "rounded-full"} ${
              dark
                ? b === "done"
                  ? "bg-term-ok"
                  : b === "current"
                    ? "bg-term-cursor"
                    : "bg-term-line"
                : b === "done"
                  ? "bg-signal"
                  : b === "current"
                    ? "bg-accent"
                    : "bg-line"
            }`}
          />
        ))}
      </span>
      <span>{t("node.mission", { name: line.quest, step: line.step, total: line.total })}</span>
    </p>
  );
}
