import { useId, useState } from "react";
import { Button as AriaButton, Dialog, DialogTrigger, Popover } from "react-aria-components";
import { useTranslation } from "react-i18next";
import { StatusChip } from "../canvas/StatusChip";
import { Alert } from "../components/ui";
import { MaxPauseReason, type TerminalInfo } from "../gen/panel";
import { errorText } from "../lib/errors";
import { statusFromInfo, type TerminalContext } from "./useTerminalContext";

export type Connection = "live" | "reconnecting" | "noSignal" | "ended";

const DOT: Record<Connection, string> = {
  live: "bg-signal",
  reconnecting: "bg-warn motion-safe:animate-pulse",
  noSignal: "bg-muted",
  ended: "bg-muted",
};

/** What the dot of the connection says; the color is never the only sign, so the text is in the title too. */
function Dot({ connection }: { connection: Connection }) {
  const { t } = useTranslation("terminal");
  return (
    <span
      role="img"
      aria-label={t(`dot.${connection}`)}
      title={t(`dot.${connection}`)}
      className={`inline-block size-3 shrink-0 rounded-full ${DOT[connection]}`}
    />
  );
}

const ghost =
  "inline-flex min-h-11 min-w-11 items-center justify-center rounded-field border border-line bg-surface px-3 text-base font-semibold text-ink hover:bg-accent-wash data-pressed:bg-accent-wash disabled:opacity-50";

export interface BarProps {
  info: TerminalInfo;
  ctx?: TerminalContext;
  canDecide: boolean;
  connection: Connection;
  fontSize: number;
  onFont: (delta: number) => void;
  onFit: () => void;
  onCopy: () => void;
  focused: boolean;
  onFocus: () => void;
  /** A narrow window: the secondary actions go to "Mais". */
  narrow: boolean;
  /** Tells the user something happened, with an optional way back. */
  notify: (text: string, undo?: () => void) => void;
}

/** Pausing or resuming a table asks why, in a balloon under the button; the server refuses an empty reason too. */
function PauseBalloon({ ctx, notify }: { ctx: TerminalContext; notify: BarProps["notify"] }) {
  const { t } = useTranslation("terminal");
  const { t: te } = useTranslation("errors");
  const id = useId();
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const table = ctx.table ?? "";
  const resuming = ctx.paused;
  const submit = async (close: () => void) => {
    setBusy(true);
    setError("");
    try {
      await ctx.setPaused(!resuming, reason.trim());
      notify(t(resuming ? "pause.resumed" : "pause.paused"));
      setReason("");
      close();
    } catch (err) {
      setError(errorText(te, err));
    } finally {
      setBusy(false);
    }
  };
  return (
    <DialogTrigger>
      <AriaButton className={ghost}>{t(resuming ? "bar.resume" : "bar.pause")}</AriaButton>
      <Popover placement="bottom end" offset={6}>
        <Dialog
          aria-label={t(resuming ? "pause.resumeTitle" : "pause.title", { table })}
          className="w-[min(22rem,calc(100vw-2rem))] rounded-field border border-line bg-surface p-3 text-ink shadow-pop outline-none"
        >
          {({ close }) => (
            <form
              className="flex flex-col gap-2"
              onSubmit={(e) => {
                e.preventDefault();
                if (reason.trim()) void submit(close);
              }}
            >
              <label htmlFor={id} className="font-semibold">
                {t(resuming ? "pause.resumeReason" : "pause.reason")}
              </label>
              <textarea
                id={id}
                value={reason}
                maxLength={MaxPauseReason}
                placeholder={t("pause.placeholder")}
                onChange={(e) => setReason(e.target.value)}
                className="min-h-16 w-full rounded-field border border-line bg-paper p-2"
              />
              <p className="m-0 text-sm text-muted">{t("pause.help")}</p>
              {error ? <Alert>{error}</Alert> : null}
              <div className="flex flex-wrap justify-end gap-2">
                <AriaButton onPress={close} className={ghost}>
                  {t("pause.cancel")}
                </AriaButton>
                <AriaButton
                  type="submit"
                  isDisabled={busy || reason.trim().length === 0}
                  className="inline-flex min-h-11 items-center rounded-field bg-accent px-4 font-semibold text-accent-ink hover:bg-accent-strong disabled:opacity-60"
                >
                  {t(resuming ? "pause.resumeConfirm" : "pause.confirm")}
                </AriaButton>
              </div>
            </form>
          )}
        </Dialog>
      </Popover>
    </DialogTrigger>
  );
}

/** The identity bar of the terminal window: who it is, in which state, and the actions the panel already has. */
export function TerminalBar(p: BarProps) {
  const { t } = useTranslation("terminal");
  const { t: te } = useTranslation("errors");
  const { info, ctx } = p;
  const character = ctx?.character;
  const status = ctx?.status ?? statusFromInfo(info, p.canDecide);
  const [more, setMore] = useState(false);

  const pin = async () => {
    if (!character || !ctx) return;
    const next = !character.pinned;
    try {
      await ctx.pin(next);
      p.notify(t(next ? "bar.pinned" : "bar.unpinned", { name: info.name }), () => {
        void ctx.pin(!next);
      });
    } catch (err) {
      p.notify(errorText(te, err));
    }
  };

  const secondary = (
    <>
      <AriaButton className={ghost} onPress={p.onCopy}>
        {t("bar.copy")}
      </AriaButton>
      <AriaButton className={ghost} onPress={() => p.onFont(-1)} aria-label={t("bar.smaller")}>
        A−
      </AriaButton>
      <AriaButton className={ghost} onPress={() => p.onFont(1)} aria-label={t("bar.bigger")}>
        A+
      </AriaButton>
      <AriaButton className={ghost} onPress={p.onFit}>
        {t("bar.fit")}
      </AriaButton>
      <AriaButton className={ghost} onPress={p.onFocus} aria-pressed={p.focused}>
        {t(p.focused ? "bar.unfocus" : "bar.focus")}
      </AriaButton>
    </>
  );

  return (
    <div
      role="toolbar"
      aria-label={t("bar.actions")}
      className="flex flex-wrap items-center gap-x-4 gap-y-2 border-b border-line bg-surface px-3 py-2"
    >
      <div className="flex min-w-0 flex-col">
        <div className="flex min-w-0 items-center gap-2">
          <Dot connection={p.connection} />
          <h2 className="m-0 truncate font-display text-xl">{info.name}</h2>
          <StatusChip status={status} />
        </div>
        <p className="m-0 truncate text-sm text-muted">
          {character ? `${character.class || t("plainClass")} · ` : ""}
          {t("machine", { machine: character?.machine_name || info.machine })}
        </p>
      </div>
      <div className="flex-1" />
      <div className="flex flex-wrap items-center gap-2">
        {character && ctx?.canPin ? (
          <AriaButton className={ghost} onPress={() => void pin()} aria-pressed={character.pinned}>
            {t(character.pinned ? "bar.unpin" : "bar.pin")}
          </AriaButton>
        ) : null}
        {ctx?.canPause && ctx.table ? <PauseBalloon ctx={ctx} notify={p.notify} /> : null}
        {p.narrow ? (
          <>
            <AriaButton className={ghost} aria-expanded={more} onPress={() => setMore(!more)}>
              {t("bar.more")}
            </AriaButton>
            {more ? <div className="flex w-full flex-wrap gap-2">{secondary}</div> : null}
          </>
        ) : (
          <>
            {secondary}
            <span className="text-sm text-muted" aria-live="polite">
              {t("bar.size", { px: p.fontSize })}
            </span>
          </>
        )}
      </div>
    </div>
  );
}
