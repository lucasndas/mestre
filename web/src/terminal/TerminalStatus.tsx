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

/** The mode in one word: LEITURA, MENSAGEM or TECLADO DIRETO, each one a term with its help. */
export function ModeWord({ mode, locked }: { mode: Mode; locked: boolean }) {
  return (
    <span className="inline-flex items-center gap-1.5 font-semibold tracking-wider uppercase">
      {locked ? <Lock /> : null}
      <Term
        name={mode === "read" ? "Leitura" : mode === "message" ? "Mensagem" : "Teclado direto"}
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
    <div className="flex flex-wrap items-center gap-x-4 gap-y-2 border-t border-line bg-surface px-3 py-2 text-base">
      <ModeWord mode={mode} locked={locked} />
      {!compact && ctx?.mission ? <MissionLine line={ctx.mission} /> : null}
      {!compact && ctx?.status?.needsYou ? (
        <AriaButton
          aria-label={t("status.decideNamed", { name })}
          onPress={onDecide}
          className="min-h-11 rounded-field bg-accent px-3 font-semibold text-accent-ink hover:bg-accent-strong"
        >
          {t("status.decide")}
        </AriaButton>
      ) : null}
      {!compact && ctx?.delivery ? (
        <span className="text-muted">
          {t("status.delivery", { when: formatDateTime(ctx.delivery, i18n.language, timeZone) })}
        </span>
      ) : null}
      <div className="flex-1" />
      {support ? <span className="text-sm text-muted">{support}</span> : null}
      {showType ? (
        typing ? (
          <AriaButton
            onPress={onStopTyping}
            className={`${narrow ? "min-h-12" : "min-h-11"} rounded-field border border-line bg-paper px-4 font-semibold text-ink hover:bg-accent-wash`}
          >
            {t("stopTyping")}
          </AriaButton>
        ) : (
          <AriaButton
            onPress={onType}
            isDisabled={typeDisabled}
            className={`${narrow ? "min-h-12" : "min-h-11"} rounded-field bg-accent px-4 font-semibold text-accent-ink hover:bg-accent-strong disabled:opacity-60`}
          >
            {t("type")}
          </AriaButton>
        )
      ) : null}
    </div>
  );
}
