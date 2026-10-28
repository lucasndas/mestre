import { useCallback, useEffect, useId, useRef, useState } from "react";
import { Button as AriaButton } from "react-aria-components";
import { useTranslation } from "react-i18next";
import { useConnectionState } from "../app/socket";
import { Alert } from "../components/ui";
import type { APIError, TerminalInfo } from "../gen/panel";
import { ApiError } from "../lib/api";
import { errorText } from "../lib/errors";
import { formatTime } from "../lib/format";
import { defaultBudget, type WebglBudget } from "./budget";
import { type LiveTerm, type TermFactory, termColors, xtermFactory } from "./factory";
import type { TerminalFeed } from "./feed";
import { MessageComposer, useMessageQueue } from "./MessageComposer";
import { isTerminalReply } from "./replies";
import { OutputCursor } from "./stream";
import { type Connection, TerminalBar } from "./TerminalBar";
import { type Mode, TerminalStatus } from "./TerminalStatus";
import type { TerminalContext } from "./useTerminalContext";

/** How often the snapshot of a terminal that is not live is drawn again, in ms. */
export const SNAPSHOT_EVERY = 2000;

/** The sizes of the letters in px: the screen, the card of the canvas and a narrow window; and how far A− and A+ go. */
export const FONT = { screen: 15, card: 13, phone: 12, min: 11, max: 22 } as const;
/** A window this narrow (px) is a phone: the actions go to "Mais" and the 200 columns scroll sideways. */
export const PHONE_MAX = 700;
/** The width of a cell of the packaged font, in em. */
const CELL_EM = 0.6;
const LINE_HEIGHT = 1.4;
const ENDED = new Set(["stopped", "stopping", "interrupted"]);

export interface TerminalViewProps {
  terminalId: string;
  /** What the server said about the terminal (the answer of GET /api/characters/{id}/terminal). */
  info: TerminalInfo;
  /** Where the output comes from and where the typing goes (socketFeed, or a fake one). */
  feed: TerminalFeed;
  /** The user may type (type_in_terminal); the server checks it again at each input. */
  canType: boolean;
  /** Asks the server for the state of the terminal again, when the keyboard is released. */
  refreshInfo?: () => Promise<TerminalInfo>;
  /** The Guild time zone, for "máquina sem sinal desde HH:MM". */
  timeZone: string;
  /** The canvas says the terminal is on screen and near enough to be read. A terminal that is not gets a snapshot. */
  visible?: boolean;
  near?: boolean;
  budget?: WebglBudget;
  factory?: TermFactory;
  snapshotEvery?: number;
  /** Keeps the screen as text in the page as well (for screen readers); the standalone screen sets it. */
  readable?: boolean;
  /** Inside a node of the canvas: the node shows the machine and the state, and the screen fills the room. */
  compact?: boolean;
  /** The Mestre layer: mission, step, approval, delivery and the actions of the bar (the screen sets it). */
  context?: TerminalContext;
  /** The user may decide approvals (for the word of the state when the canvas is not there). */
  canDecide?: boolean;
  /** Where "Decidir" and "Ver a mesa" lead. */
  onDecide?: () => void;
  onSeeTable?: () => void;
  /** Forces the phone layout (or the wide one); by default the width of the window decides. */
  phone?: boolean;
  className?: string;
}

/** The width of an element, kept by a ResizeObserver; 0 while unknown (and in jsdom). */
function useWidth(ref: React.RefObject<HTMLElement | null>): number {
  const [width, setWidth] = useState(0);
  useEffect(() => {
    const el = ref.current;
    if (!el || typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(() => setWidth(el.getBoundingClientRect().width));
    ro.observe(el);
    setWidth(el.getBoundingClientRect().width);
    return () => ro.disconnect();
  }, [ref]);
  return width;
}

/**
 * The terminal of a character as a window: identity bar, the screen of its PTY at the fixed size the machine
 * set (live with WebGL while the budget allows it and a text snapshot, every 2 s, otherwise) and a status line,
 * with the box of messages and its queue under it. The standalone screen and the node of the canvas both embed
 * it. docs/painel/terminal-no-painel.md and docs/painel/canvas.md#terminais-no-canvas.
 */
export function TerminalView({
  terminalId,
  info: initial,
  feed,
  canType,
  refreshInfo,
  timeZone,
  visible = true,
  near = true,
  budget = defaultBudget,
  factory = xtermFactory,
  snapshotEvery = SNAPSHOT_EVERY,
  readable = false,
  compact = false,
  context,
  canDecide = false,
  onDecide,
  onSeeTable,
  phone,
  className = "",
}: TerminalViewProps) {
  const { t, i18n } = useTranslation("terminal");
  const { t: te } = useTranslation("errors");
  const slot = useId();
  const rootRef = useRef<HTMLElement>(null);
  const hostRef = useRef<HTMLDivElement>(null);
  const scrollerRef = useRef<HTMLDivElement>(null);
  const liveRef = useRef<LiveTerm | null>(null);
  const [info, setInfo] = useState(initial);
  const [granted, setGranted] = useState(false);
  const [rows, setRows] = useState<string[]>([]);
  const [mode, setMode] = useState<Mode>("read");
  const [confirm, setConfirm] = useState(false);
  const [refusal, setRefusal] = useState<APIError | null>(null);
  const [permission, setPermission] = useState(false);
  const [userPx, setUserPx] = useState<number | null>(null);
  const [wrap, setWrap] = useState(false);
  const [focused, setFocused] = useState(false);
  const [away, setAway] = useState(false);
  const [behind, setBehind] = useState(0);
  const [notice, setNotice] = useState<{ text: string; undo?: () => void } | null>(null);
  const keys = mode === "keys";
  const keysRef = useRef(false);
  keysRef.current = keys;
  const tRef = useRef(t);
  tRef.current = t;
  const cursorRow = useRef(0);
  const following = useRef(true);

  const connection = useConnectionState();
  const reconnecting = connection === "reconnecting";
  const width = useWidth(rootRef);
  const narrow = !compact && (phone ?? (width > 0 && width <= PHONE_MAX));
  const defaultPx = compact ? FONT.card : narrow ? FONT.phone : FONT.screen;
  const px = userPx ?? defaultPx;
  const pxRef = useRef(px);
  pxRef.current = px;

  useEffect(() => setInfo(initial), [initial]);
  // a new state of the character lifts the "decide in the approval" block that a refusal put up
  const state = info.state;
  // biome-ignore lint/correctness/useExhaustiveDependencies: the state is the trigger
  useEffect(() => setPermission(false), [state]);

  const wantsLive = visible && near;
  const renderer = granted && !wrap ? "live" : "snapshot";

  // a place in the budget of WebGL terminals, only while the terminal is on screen and near
  useEffect(() => {
    if (!wantsLive) {
      setGranted(false);
      return;
    }
    const id = `${terminalId}/${slot}`;
    budget.request(id, setGranted);
    return () => budget.release(id);
  }, [wantsLive, budget, terminalId, slot]);

  const send = useCallback((data: string) => feed.type(terminalId, data), [feed, terminalId]);
  const noSignal = !info.signal;
  const ended = ENDED.has(info.state);
  const status = context?.status;
  const needsDecision = Boolean(status && (status.key === "decision" || status.key === "approval"));
  const decision = permission || needsDecision;
  const locked = !canType;
  const blocked = locked || noSignal || reconnecting || ended || decision;
  const queue = useMessageQueue({
    busy: info.busy,
    active: mode === "message" && !blocked,
    send,
  });
  const refusedQueue = queue.refused;
  const refusedRef = useRef(refusedQueue);
  refusedRef.current = refusedQueue;

  // where the screen ends: the row of the cursor, so the window shows the end of the output
  const followEnd = useCallback(() => {
    const el = scrollerRef.current;
    if (!el || !following.current) return;
    const bottom = (cursorRow.current + 2) * pxRef.current * LINE_HEIGHT;
    el.scrollTop = Math.max(0, bottom - el.clientHeight);
  }, []);

  // the terminal itself: every change of mode makes a new one and follows the stream
  // again, which starts from offset 0, so the new screen is never behind
  const cols = info.cols;
  const rowsCount = info.rows;
  useEffect(() => {
    const size = { cols, rows: rowsCount };
    let timer: ReturnType<typeof setInterval> | undefined;
    let write: (d: Uint8Array | string) => void;
    let reset: () => void;
    let dispose: () => void;
    if (renderer === "live" && hostRef.current) {
      const live = factory.live(hostRef.current, size, termColors(), {
        readable,
        fontSize: pxRef.current,
      });
      liveRef.current = live;
      live.onData((d) => {
        // the answers of xterm.js to the program are not typing (see replies.ts)
        if (keysRef.current && !isTerminalReply(d)) feed.type(terminalId, d);
      });
      live.onScrolled((a, n) => {
        setBehind(n);
        if (a) setAway(true);
      });
      live.onCursor((row) => {
        cursorRow.current = row;
        followEnd();
      });
      live.setTyping(keysRef.current);
      ({ write, reset } = live);
      dispose = () => {
        liveRef.current = null;
        live.dispose();
      };
    } else {
      const snap = factory.snapshot(size);
      timer = setInterval(() => setRows(snap.rows()), snapshotEvery);
      ({ write, reset } = snap);
      dispose = () => snap.dispose();
    }
    const cursor = new OutputCursor({
      write: (d) => write(d),
      reset: () => reset(),
      lost: () => write(`\r\n\x1b[2m… ${tRef.current("lost")} …\x1b[0m\r\n`),
    });
    setRows([]);
    const stop = feed.follow(terminalId, {
      info: setInfo,
      frame: (off, data) => cursor.push(off, data),
      reset: () => cursor.reset(),
      screen: (s) => cursor.screen(s.text, s.offset),
      signal: (s) =>
        setInfo((i) => ({
          ...i,
          signal: s.signal,
          no_signal_since: s.signal ? undefined : s.since,
        })),
      refused: (e) => {
        setRefusal(e);
        if (e.code === "forbidden_level") setMode("read");
        if (e.code === "permission_prompt") setPermission(true);
        // only a refused message goes back to the queue, never a refused key of the direct keyboard
        if (modeRef.current === "message") refusedRef.current();
      },
    });
    return () => {
      stop();
      if (timer) clearInterval(timer);
      dispose();
    };
  }, [renderer, feed, terminalId, factory, snapshotEvery, cols, rowsCount, readable, followEnd]);

  // the keyboard: on only for a live terminal and while the user asked for it
  useEffect(() => {
    liveRef.current?.setTyping(keys && renderer === "live");
  }, [keys, renderer]);
  useEffect(() => {
    liveRef.current?.setFontSize(px);
    followEnd();
  }, [px, followEnd]);

  const leave = useCallback(() => {
    setMode("read");
    setConfirm(false);
    feed.endTyping(terminalId);
  }, [feed, terminalId]);
  const leaveRef = useRef(leave);
  leaveRef.current = leave;
  // a terminal that stops being live, or leaves, releases the keyboard
  useEffect(() => {
    if (keys && renderer !== "live") leaveRef.current();
  }, [keys, renderer]);
  // a block (no signal, reconnecting, ended, a decision) turns the typing off
  useEffect(() => {
    if (blocked && mode !== "read") leaveRef.current();
  }, [blocked, mode]);
  const modeRef = useRef(mode);
  modeRef.current = mode;
  useEffect(
    () => () => {
      if (modeRef.current !== "read") feed.endTyping(terminalId);
    },
    [feed, terminalId],
  );

  const start = async () => {
    if (blocked) return;
    setRefusal(null);
    queue.clearSentBack();
    if (refreshInfo) {
      try {
        setInfo(await refreshInfo());
      } catch {
        // the state we have is the best we know
      }
    }
    setMode("message");
  };
  const keysAsked = async () => {
    if (blocked) return;
    let now = info;
    if (refreshInfo) {
      try {
        now = await refreshInfo();
        setInfo(now);
      } catch {
        // the state we have is the best we know
      }
    }
    if (now.busy) setConfirm(true);
    else setMode("keys");
  };

  const notify = useCallback((text: string, undo?: () => void) => setNotice({ text, undo }), []);
  useEffect(() => {
    if (!notice) return;
    const timer = setTimeout(() => setNotice(null), 8000);
    return () => clearTimeout(timer);
  }, [notice]);

  const changeFont = (delta: number) =>
    setUserPx((now) => Math.min(FONT.max, Math.max(FONT.min, (now ?? defaultPx) + delta)));
  const fit = () => {
    const have = Math.floor((scrollerRef.current?.clientWidth ?? width) - 16);
    const target = Math.floor(have / (cols * CELL_EM));
    if (target < FONT.min) {
      setUserPx(FONT.min);
      notify(
        t("bar.fitNo", {
          cols,
          need: Math.ceil(cols * CELL_EM * FONT.min),
          have: Math.max(have, 0),
        }),
      );
      return;
    }
    const next = Math.min(FONT.max, target);
    setUserPx(next);
    notify(t("bar.fitOk", { px: next }));
  };
  const copy = async () => {
    const text = liveRef.current ? liveRef.current.text() : rows.join("\n");
    try {
      await navigator.clipboard.writeText(text);
      notify(t("bar.copied"));
    } catch {
      notify(t("bar.copyFailed"));
    }
  };
  const toEnd = () => {
    following.current = true;
    liveRef.current?.toEnd();
    setAway(false);
    setBehind(0);
    followEnd();
  };

  const since = info.no_signal_since;
  const label = t("region", { name: info.name });
  const live = renderer === "live";
  const typeDisabled = locked || blocked;
  const support = (() => {
    if (locked) return t("support.lock");
    if (ended) return t("support.ended");
    if (noSignal) return t("support.noSignal");
    if (reconnecting) return t("support.reconnecting");
    if (decision) return t("support.decision");
    if (keys) return t("support.typing");
    return t("support.fixed", { cols, rows: rowsCount });
  })();
  const conn: Connection = reconnecting
    ? "reconnecting"
    : ended
      ? "ended"
      : noSignal
        ? "noSignal"
        : "live";
  const tone = keys || mode === "message";
  const dimmed = noSignal || reconnecting;
  const dashedAt = cols * CELL_EM * px + 8;

  return (
    <section
      ref={rootRef}
      aria-label={label}
      data-mode={mode}
      data-phone={narrow ? "yes" : "no"}
      onKeyDownCapture={(e) => {
        // Ctrl+Alt+K lets go of the keyboard without sending anything to the agent
        if (keys && e.ctrlKey && e.altKey && (e.code === "KeyK" || e.key.toLowerCase() === "k")) {
          e.preventDefault();
          e.stopPropagation();
          leave();
        }
      }}
      className={`flex min-w-0 flex-col ${focused ? "fixed inset-0 z-40 bg-term-bg" : ""} ${
        compact
          ? "min-h-0 flex-1"
          : `overflow-hidden rounded-field border bg-term-bg text-term-fg shadow-pop ${
              focused
                ? ""
                : narrow
                  ? "h-[clamp(420px,calc(100dvh-var(--term-offset,340px)),860px)]"
                  : "h-[clamp(460px,calc(100dvh-var(--term-offset,300px)),860px)]"
            }`
      } ${tone && !compact ? "border-accent ring-2 ring-accent" : "border-term-line"} ${className}`}
    >
      {compact ? null : (
        <TerminalBar
          info={info}
          ctx={context}
          canDecide={canDecide}
          connection={conn}
          fontSize={px}
          onFont={changeFont}
          onFit={fit}
          onCopy={() => void copy()}
          focused={focused}
          onFocus={() => setFocused(!focused)}
          narrow={narrow}
          notify={notify}
        />
      )}
      {reconnecting ? (
        <p className="m-0 shrink-0 border-b border-term-line bg-warn px-3.5 py-2 text-warn-ink">
          {t("banner.reconnecting")}
        </p>
      ) : null}
      {context?.paused ? (
        <p className="m-0 shrink-0 border-b border-term-line bg-warn px-3.5 py-2 text-warn-ink">
          {context.pausedReason
            ? t("pause.banner", { reason: context.pausedReason })
            : t("pause.bannerNoReason")}
        </p>
      ) : null}
      {noSignal ? (
        <p
          role="status"
          className="m-0 shrink-0 border-b border-term-line bg-term-chrome-2 px-3.5 py-2 text-term-fg"
        >
          {since
            ? t("noSignal", { time: formatTime(since, i18n.language, timeZone) })
            : t("noSignalNoTime")}
        </p>
      ) : null}
      {!compact && decision && !ended ? (
        <div className="flex shrink-0 flex-wrap items-center gap-3 bg-accent px-3.5 py-2 text-accent-ink">
          <span>
            {t("banner.decision", {
              name: info.name,
            })}
          </span>
          <AriaButton
            onPress={() => onDecide?.()}
            className="min-h-11 rounded-field border border-accent-ink px-3 font-semibold text-accent-ink hover:bg-accent-strong"
          >
            {t("banner.decideIn")}
          </AriaButton>
        </div>
      ) : null}
      {!compact && ended ? (
        <div className="flex flex-wrap items-center gap-3 border-b border-term-line bg-term-chrome-2 px-3.5 py-2 text-term-fg">
          <span>{t("banner.ended", { name: info.name })}</span>
          {onSeeTable ? (
            <AriaButton
              onPress={onSeeTable}
              className="min-h-11 rounded-field border border-current px-3 text-term-fg hover:bg-term-chrome"
            >
              {t("banner.seeTable")}
            </AriaButton>
          ) : null}
        </div>
      ) : null}
      {refusal ? (
        <Alert>
          {t("refused", {
            reason: errorText(te, new ApiError(0, refusal.code, refusal.message, refusal.params)),
          })}
        </Alert>
      ) : null}
      {narrow ? (
        <div className="flex flex-wrap items-center gap-2 border-b border-term-line bg-term-chrome-2 px-3 py-1 text-sm whitespace-nowrap text-term-fg">
          <span>{t("phone.strip")}</span>
          <AriaButton
            aria-pressed={wrap}
            onPress={() => setWrap(!wrap)}
            className="min-h-11 rounded-field border border-term-line bg-transparent px-3 text-term-fg"
          >
            {wrap ? t("phone.unwrap") : t("phone.wrap")}
          </AriaButton>
        </div>
      ) : null}
      {/* nodrag, nowheel and nopan: inside the canvas the mouse and the wheel go to the terminal */}
      <div className="relative min-h-0 flex-1">
        <div
          ref={scrollerRef}
          onScroll={(e) => {
            const el = e.currentTarget;
            const end = Math.max(
              0,
              (cursorRow.current + 2) * pxRef.current * LINE_HEIGHT - el.clientHeight,
            );
            const atEnd = el.scrollTop >= end - 4;
            following.current = atEnd;
            setAway(!atEnd && !compact);
          }}
          className={`nodrag nowheel nopan overflow-auto bg-term-bg ${
            compact ? "h-full min-h-0 rounded-field border border-line" : "h-full"
          } ${dimmed || ended ? "opacity-60" : ""} ${noSignal ? "grayscale" : ""}`}
          data-renderer={live ? "webgl" : "snapshot"}
          data-signal={noSignal ? "lost" : "ok"}
        >
          {live ? (
            <div className="relative w-max p-2">
              <div ref={hostRef} />
              {narrow ? null : (
                <div
                  aria-hidden="true"
                  className="pointer-events-none absolute inset-y-0 border-l border-dashed border-term-fg/25"
                  style={{ left: dashedAt }}
                />
              )}
            </div>
          ) : (
            <section aria-label={t("snapshotLabel", { name: info.name })}>
              <pre
                className={`m-0 min-h-24 p-2 font-mono text-term-fg ${
                  wrap ? "whitespace-pre-wrap" : "whitespace-pre"
                }`}
                style={{ fontSize: px, lineHeight: LINE_HEIGHT }}
              >
                {rows.length > 0 ? rows.join("\n") : " "}
              </pre>
            </section>
          )}
        </div>
        {!live ? (
          <span className="pointer-events-none absolute top-2 right-4 rounded-field border border-term-line bg-term-chrome px-2 py-1 font-mono text-sm text-term-dim">
            {t("pictureLabel")}
          </span>
        ) : null}
        {away && !compact ? (
          <AriaButton
            onPress={toEnd}
            className="absolute right-4 bottom-3 min-h-11 rounded-field border border-term-line bg-term-chrome-2 px-3 font-mono text-sm font-medium text-term-fg shadow-pop hover:bg-term-chrome"
          >
            {behind > 0 ? t("toEndBehind", { count: behind }) : t("toEnd")}
          </AriaButton>
        ) : null}
        {notice ? (
          <div
            role="status"
            className="absolute right-3 bottom-3 z-10 flex max-w-[calc(100%-1.5rem)] items-center gap-3 rounded-field bg-ink px-3 py-2 text-paper shadow-pop"
          >
            <span>{notice.text}</span>
            {notice.undo ? (
              <AriaButton
                onPress={() => {
                  notice.undo?.();
                  setNotice(null);
                }}
                className="min-h-11 rounded-field px-2 font-semibold text-warn underline"
              >
                {t("bar.undo")}
              </AriaButton>
            ) : null}
          </div>
        ) : null}
      </div>
      {mode === "message" ? (
        <MessageComposer
          name={info.name}
          busy={info.busy}
          queue={queue.queue}
          onSubmit={queue.submit}
          onSendNow={queue.sendNow}
          onRemove={queue.remove}
          onKeys={() => void keysAsked()}
          keysDisabled={!live}
          sentBack={queue.sentBack}
        />
      ) : null}
      {confirm ? (
        <section
          aria-label={t("banner.busyTitle")}
          className="flex flex-wrap items-center gap-3 border-t border-term-line bg-warn px-3.5 py-2 text-warn-ink"
        >
          <span className="font-semibold">{t("busy.title")}</span>
          <span className="min-w-0 flex-1 basis-60">{t("busy.body", { name: info.name })}</span>
          <AriaButton
            onPress={() => setConfirm(false)}
            className="min-h-11 rounded-field border border-warn-ink px-3 font-semibold"
          >
            {t("busy.cancel")}
          </AriaButton>
          <AriaButton
            onPress={() => {
              setConfirm(false);
              setMode("keys");
            }}
            className="min-h-11 rounded-field bg-warn-ink px-3 font-semibold text-warn-wash"
          >
            {t("busy.confirm")}
          </AriaButton>
        </section>
      ) : (
        <TerminalStatus
          mode={mode}
          locked={locked}
          compact={compact}
          ctx={context}
          name={info.name}
          support={compact ? "" : support}
          typing={mode !== "read"}
          showType={!compact}
          typeDisabled={typeDisabled || (mode === "read" && !visible)}
          onType={() => void start()}
          onStopTyping={leave}
          onDecide={() => onDecide?.()}
          narrow={narrow}
          timeZone={timeZone}
        />
      )}
      {keys ? (
        <p className="m-0 border-t border-term-line bg-term-chrome px-3 py-1 font-mono text-sm text-term-dim">
          {t("composer.keysOn")}
        </p>
      ) : null}
    </section>
  );
}
