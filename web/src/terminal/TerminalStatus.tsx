import { Button as AriaButton } from "react-aria-components";
import { useTranslation } from "react-i18next";
import { MissionLine } from "../canvas/MissionLine";
import { Term } from "../components/Term";
import { formatDateTime } from "../lib/format";
import type { TerminalContext } from "./useTerminalContext";

export type Mode = "read" | "message" | "keys";

function Lock() {
  return (
    <svg
      viewBox="0 0 24 24"
      width="16"
      height="16"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <rect x="5" y="11" width="14" height="9" rx="1" />
      <path d="M8 11V8a4 4 0 0 1 8 0v3" />
    </svg>
  );
}

function KeyboardIcon() {
  return (
    <svg
      viewBox="0 0 24 24"
      width="20"
      height="20"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.75"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <rect x="3" y="6" width="18" height="12" rx="1" />
      <path d="M7 10h.01M11 10h.01M15 10h.01M7 14h10" />
    </svg>
  );
}

/** The mode in one word: LEITURA, MENSAGEM or TECLADO DIRETO, each one a term with its help. */
export function ModeWord({ mode, locked }: { mode: Mode; locked: boolean }) {
  return (
    <span
      className={`inline-flex min-h-11 items-center gap-2 self-stretch px-3.5 font-mono font-bold tracking-wider uppercase ${
        mode === "read" ? "bg-term-chrome-2 text-term-fg" : "bg-accent text-accent-ink"
      }`}
    >
      {locked ? <Lock /> : null}
      <Term
        name={mode === "read" ? "Leitura" : mode === "message" ? "Mensagem" : "Teclado direto"}
        border={mode === "read" ? "border-term-dim" : "border-accent-ink"}
      />
    </span>
  );
}

/**
 * The status line under the screen: the mode, the mission and step of the character, the approval that waits,
 * the last delivery and, at the right, Digitar with the sentence that supports it.
 */
export function TerminalStatus({
  mode,
  locked,
  compact,
  ctx,
  name,
  support,
  typing,
  showType,
  typeDisabled,
  onType,
  onStopTyping,
  onDecide,
  narrow,
  timeZone,
}: {
  mode: Mode;
  locked: boolean;
  compact: boolean;
  ctx?: TerminalContext;
  name: string;
  support: string;
  typing: boolean;
  /** The Digitar button is there (not in the card of the canvas); it is disabled when typing is not possible. */
  showType: boolean;
  typeDisabled: boolean;
  onType: () => void;
  onStopTyping: () => void;
  onDecide: () => void;
  narrow: boolean;
  timeZone: string;
}) {
  const { t, i18n } = useTranslation("terminal");
  return (
    <div className="flex flex-wrap items-center gap-x-3.5 gap-y-1.5 border-t border-term-line bg-term-chrome py-1.5 pr-2.5 pl-0 font-mono text-sm text-term-dim">
      <ModeWord mode={mode} locked={locked} />
      {!compact && ctx?.mission ? <MissionLine line={ctx.mission} dark /> : null}
      {!compact && ctx?.status?.needsYou ? (
        <AriaButton
          aria-label={t("status.decideNamed", { name })}
          onPress={onDecide}
          className="inline-flex min-h-8 items-center gap-2 rounded-field border border-accent bg-accent px-2 py-1.5 text-accent-ink hover:bg-accent-strong"
        >
          {t("status.decide")}
        </AriaButton>
      ) : null}
      {!compact && ctx?.delivery ? (
        <span className="text-term-fg">
          {t("status.delivery", { when: formatDateTime(ctx.delivery, i18n.language, timeZone) })}
        </span>
      ) : null}
      <div className="flex-1" />
      {support ? <span className="text-sm text-term-dim">{support}</span> : null}
      {showType ? (
        typing ? (
          <AriaButton
            onPress={onStopTyping}
            className={`${narrow ? "min-h-12" : "min-h-11"} rounded-field border border-term-line bg-transparent px-4 font-body text-base text-term-fg hover:bg-term-chrome-2`}
          >
            {t("stopTyping")}
          </AriaButton>
        ) : (
          <AriaButton
            onPress={onType}
            isDisabled={typeDisabled}
            className={`${narrow ? "min-h-12" : "min-h-11"} inline-flex items-center gap-2.5 rounded-field border border-accent bg-accent px-4 font-body text-[17px] font-semibold text-accent-ink hover:bg-accent-strong disabled:opacity-60`}
          >
            <KeyboardIcon />
            {t("type")}
          </AriaButton>
        )
      ) : null}
    </div>
  );
}
