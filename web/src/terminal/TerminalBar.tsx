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

/** A button of the dark chrome that is only an icon; its name is the aria-label. */
const ib =
  "inline-grid size-11 place-items-center rounded-field border-0 bg-transparent text-term-fg hover:bg-term-chrome-2 data-pressed:bg-term-chrome-2 aria-pressed:text-term-accent disabled:opacity-40";
/** The buttons of the balloon of Pausar mesa, which sits on the paper. */
const ghost =
  "inline-flex min-h-11 min-w-11 items-center justify-center rounded-field border border-line bg-surface px-3 text-base font-semibold text-ink hover:bg-accent-wash data-pressed:bg-accent-wash disabled:opacity-50";

const ICONS = {
  pin: "M12 17v5M9 3h6l-1 6 3 4H7l3-4Z",
  pause: "M8 5v14M16 5v14",
  play: "M7 4l13 8-13 8Z",
  copy: "M9 9h11v11H9ZM5 15V4h11",
  fit: "M4 12h16M8 8l-4 4 4 4M16 8l4 4-4 4",
  focus: "M9 4H4v5M15 4h5v5M9 20H4v-5M15 20h5v-5",
  unfocus: "M4 9h5V4M20 9h-5V4M4 15h5v5M20 15h-5v5",
  more: "M5 12h.01M12 12h.01M19 12h.01",
  minus: "M6 12h12",
  plus: "M12 6v12M6 12h12",
} as const;

/** The icon of an action of the bar; the name of the button is its aria-label, never the icon. */
export function Ico({ name }: { name: keyof typeof ICONS }) {
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
      <path d={ICONS[name]} />
    </svg>
  );
}

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
      <AriaButton className={ib} aria-label={t(resuming ? "bar.resume" : "bar.pause")}>
        <Ico name={resuming ? "play" : "pause"} />
      </AriaButton>
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

  const copy = (
    <AriaButton className={ib} onPress={p.onCopy} aria-label={t("bar.copy")}>
      <Ico name="copy" />
    </AriaButton>
  );
  const smaller = (
    <AriaButton className={ib} onPress={() => p.onFont(-1)} aria-label={t("bar.smaller")}>
      <Ico name="minus" />
    </AriaButton>
  );
  const bigger = (
    <AriaButton className={ib} onPress={() => p.onFont(1)} aria-label={t("bar.bigger")}>
      <Ico name="plus" />
    </AriaButton>
  );
  const fit = (
    <AriaButton className={ib} onPress={p.onFit} aria-label={t("bar.fit")}>
      <Ico name="fit" />
    </AriaButton>
  );
  const focus = (
    <AriaButton
      className={ib}
      onPress={p.onFocus}
      aria-pressed={p.focused}
      aria-label={t(p.focused ? "bar.unfocus" : "bar.focus")}
    >
      <Ico name={p.focused ? "unfocus" : "focus"} />
    </AriaButton>
  );
  // in the menu of Mais the same actions have a word beside the icon
  const row =
    "flex min-h-11 w-full items-center gap-2.5 rounded-field px-2.5 text-left text-base text-term-fg hover:bg-term-chrome data-pressed:bg-term-chrome";
  const menuItem = (
    icon: keyof typeof ICONS,
    label: string,
    onPress: () => void,
    pressed?: boolean,
  ) => (
    <AriaButton className={row} onPress={onPress} aria-pressed={pressed}>
      <Ico name={icon} />
      {label}
    </AriaButton>
  );

  return (
    <div
      role="toolbar"
      aria-label={t("bar.actions")}
      className="relative flex min-h-[52px] flex-wrap items-center gap-x-2.5 gap-y-1.5 border-b border-term-line bg-term-chrome px-2.5 py-1.5 text-term-fg"
    >
      <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1.5">
        <div className="flex min-w-0 items-center gap-2.5">
          <Dot connection={p.connection} />
          <h2 className="m-0 truncate font-display text-[22px] leading-tight font-medium text-term-fg">
            {info.name}
          </h2>
        </div>
        <div className="flex min-w-0 flex-wrap items-center gap-2.5">
          <StatusChip status={status} dark />
          <Chip>{character?.class || t("plainClass")}</Chip>
          <Chip>{t("machine", { machine: character?.machine_name || info.machine })}</Chip>
        </div>
      </div>
      <div className="flex-1" />
      <div className="flex flex-wrap items-center gap-0.5">
        {character && ctx?.canPin ? (
          <AriaButton
            className={ib}
            onPress={() => void pin()}
            aria-pressed={character.pinned}
            aria-label={t(character.pinned ? "bar.unpin" : "bar.pin")}
          >
            <Ico name="pin" />
          </AriaButton>
        ) : null}
        {ctx?.canPause && ctx.table ? <PauseBalloon ctx={ctx} notify={p.notify} /> : null}
        {p.narrow ? (
          <DialogTrigger>
            <AriaButton className={ib} aria-label={t("bar.more")}>
              <Ico name="more" />
            </AriaButton>
            <Popover placement="bottom end" offset={4}>
              <Dialog
                aria-label={t("bar.more")}
                className="flex min-w-56 flex-col rounded-field border border-term-line bg-term-chrome-2 p-1.5 shadow-pop outline-none"
              >
                {({ close }) => (
                  <>
                    {menuItem("copy", t("bar.copy"), () => {
                      p.onCopy();
                      close();
                    })}
                    {menuItem("minus", t("bar.smaller"), () => p.onFont(-1))}
                    {menuItem("plus", t("bar.bigger"), () => p.onFont(1))}
                    {menuItem("fit", t("bar.fit"), () => {
                      p.onFit();
                      close();
                    })}
                    {menuItem(
                      p.focused ? "unfocus" : "focus",
                      t(p.focused ? "bar.unfocus" : "bar.focus"),
                      () => {
                        p.onFocus();
                        close();
                      },
                      p.focused,
                    )}
                  </>
                )}
              </Dialog>
            </Popover>
          </DialogTrigger>
        ) : (
          <>
            {copy}
            <span className="mx-1.5 inline-flex items-center border-x border-term-line px-0.5">
              {smaller}
              <span
                className="min-w-[3ch] text-center font-mono text-sm font-medium text-term-dim"
                aria-live="polite"
              >
                {p.fontSize}
              </span>
              {bigger}
            </span>
            {fit}
            {focus}
          </>
        )}
      </div>
    </div>
  );
}

function Chip({ children }: { children: React.ReactNode }) {
  return (
    <span className="inline-flex items-center gap-1.5 rounded-field border border-term-line px-2 py-1.5 font-mono text-sm leading-none whitespace-nowrap text-term-dim">
      {children}
    </span>
  );
}
