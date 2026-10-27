import { useTranslation } from "react-i18next";
import type { Icon, Situation, Tone } from "./situation";

const TONE: Record<Tone, string> = {
  ok: "border-signal/40 bg-signal-wash text-signal",
  plain: "border-line bg-accent-wash text-ink",
  warn: "border-warn bg-warn-wash text-warn-ink",
  accent: "border-accent bg-accent text-accent-ink",
  danger: "border-danger bg-danger text-accent-ink",
  done: "border-signal bg-signal text-accent-ink",
};

const PATHS: Record<Icon, string> = {
  open: "M12 3v4M12 17v4M3 12h4M17 12h4M6 6l2.5 2.5M15.5 15.5 18 18M18 6l-2.5 2.5M8.5 15.5 6 18",
  work: "M12 4a8 8 0 1 0 0 16 8 8 0 0 0 0-16ZM12 8v4l3 2",
  wait: "M8 5v14M16 5v14",
  hand: "M12 3l9 9-9 9-9-9ZM12 8v5M12 16v.01",
  review: "M5 4h10l4 4v12H5ZM8.5 13.5 11 16l4.5-5",
  check: "M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18ZM7.5 12.5l3 3 6-7",
  warn: "M12 3 22 20H2ZM12 9v5M12 17v.01",
  x: "M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18ZM8.5 8.5l7 7M15.5 8.5l-7 7",
};

/** A word with an icon of its own shape and a tone: the color is never the only sign. */
export function ToneChip({
  tone,
  icon,
  large = false,
  children,
}: {
  tone: Tone;
  icon: Icon;
  /** The size of the text of a card of the board (17 px), where the small chip text does not go. */
  large?: boolean;
  children: React.ReactNode;
}) {
  return (
    <span
      className={`inline-flex min-h-8 items-center gap-1.5 rounded-full border px-2.5 ${large ? "text-base" : "text-sm"} font-semibold ${TONE[tone]}`}
    >
      <svg
        viewBox="0 0 24 24"
        width="18"
        height="18"
        fill="none"
        stroke="currentColor"
        strokeWidth="2"
        strokeLinecap="round"
        strokeLinejoin="round"
        aria-hidden="true"
      >
        <path d={PATHS[icon]} />
      </svg>
      {children}
    </span>
  );
}

/** One word of the vocabulary of states for a table. */
export function SituationChip({ s }: { s: Situation }) {
  const { t } = useTranslation("workspace");
  const say = t as unknown as (k: string) => string;
  return (
    <ToneChip tone={s.tone} icon={s.icon}>
      {say(`life.situation.label.${s.label}`)}
    </ToneChip>
  );
}
