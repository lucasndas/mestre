import { useEffect, useId, useRef, useState } from "react";
import { Dialog, Heading, Modal, ModalOverlay } from "react-aria-components";
import { useTranslation } from "react-i18next";
import { Alert, Button } from "../components/ui";
import type { APIError, TerminalInfo } from "../gen/panel";
import { ApiError } from "../lib/api";
import { errorText } from "../lib/errors";
import { formatTime } from "../lib/format";
import { defaultBudget, type WebglBudget } from "./budget";
import { type LiveTerm, type TermFactory, termColors, xtermFactory } from "./factory";
import type { TerminalFeed } from "./feed";
import { isTerminalReply } from "./replies";
import { OutputCursor } from "./stream";

/** How often the snapshot of a terminal that is not live is drawn again, in ms. */
export const SNAPSHOT_EVERY = 2000;

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
  className?: string;
}

/**
 * The terminal of a character: the output of its PTY at the fixed size the machine
 * set, live with WebGL while the budget allows it and a text snapshot (every 2 s)
 * otherwise. Reusable: the standalone screen and the canvas both embed it.
 * docs/painel/canvas.md#terminais-no-canvas.
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
  className = "",
}: TerminalViewProps) {
  const { t, i18n } = useTranslation("terminal");
  const { t: te } = useTranslation("errors");
  const slot = useId();
  const hostRef = useRef<HTMLDivElement>(null);
  const liveRef = useRef<LiveTerm | null>(null);
  const [info, setInfo] = useState(initial);
  const [granted, setGranted] = useState(false);
  const [rows, setRows] = useState<string[]>([]);
  const [typing, setTyping] = useState(false);
  const [confirm, setConfirm] = useState(false);
  const [refusal, setRefusal] = useState<APIError | null>(null);
  const typingRef = useRef(false);
  typingRef.current = typing;
  const tRef = useRef(t);
  tRef.current = t;

  useEffect(() => setInfo(initial), [initial]);

  const wantsLive = visible && near;
  const mode = granted ? "live" : "snapshot";

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
    if (mode === "live" && hostRef.current) {
      const live = factory.live(hostRef.current, size, termColors(), { readable });
      liveRef.current = live;
      live.onData((d) => {
        // the answers of xterm.js to the program are not typing (see replies.ts)
        if (typingRef.current && !isTerminalReply(d)) feed.type(terminalId, d);
      });
      live.setTyping(typingRef.current);
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
        if (e.code === "forbidden_level") setTyping(false);
      },
    });
    return () => {
      stop();
      if (timer) clearInterval(timer);
      dispose();
    };
  }, [mode, feed, terminalId, factory, snapshotEvery, cols, rowsCount, readable]);

  // the keyboard: on only for a live terminal and while the user asked for it
  useEffect(() => {
    liveRef.current?.setTyping(typing && mode === "live");
  }, [typing, mode]);

  const stopTyping = () => {
    setTyping(false);
    feed.endTyping(terminalId);
  };
  const stopTypingRef = useRef(stopTyping);
  stopTypingRef.current = stopTyping;
  // a terminal that stops being live, or leaves, releases the keyboard
  useEffect(() => {
    if (typing && mode !== "live") stopTypingRef.current();
  }, [typing, mode]);
  useEffect(
    () => () => {
      if (typingRef.current) stopTypingRef.current();
    },
    [],
  );

  const start = async () => {
    setRefusal(null);
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
    else setTyping(true);
  };

  const noSignal = !info.signal;
  const since = info.no_signal_since;
  const label = t("region", { name: info.name });
  return (
    <section aria-label={label} className={`flex min-w-0 flex-col gap-2 ${className}`}>
      <header className="flex flex-wrap items-center gap-x-4 gap-y-2">
        {compact ? null : (
          <p className="text-sm text-muted">
            {t("machine", { machine: info.machine })} · {t("state", { state: info.state })}
          </p>
        )}
        <div className="flex-1" />
        {canType ? (
          typing ? (
            <Button variant="secondary" onPress={stopTyping}>
              {t("stopTyping")}
            </Button>
          ) : (
            <Button
              variant="primary"
              onPress={() => void start()}
              isDisabled={mode !== "live" || noSignal}
            >
              {t("type")}
            </Button>
          )
        ) : null}
      </header>
      {noSignal ? (
        <Alert tone="ok">
          {since
            ? t("noSignal", { time: formatTime(since, i18n.language, timeZone) })
            : t("noSignalNoTime")}
        </Alert>
      ) : null}
      {typing ? <p className="text-sm text-muted">{t("typingOn")}</p> : null}
      {!canType ? <p className="text-sm text-muted">{t("readOnly")}</p> : null}
      {canType && mode !== "live" && !typing ? (
        <p className="text-sm text-muted">{t("typingNeedsLive")}</p>
      ) : null}
      {refusal ? (
        <Alert>
          {t("refused", {
            reason: errorText(te, new ApiError(0, refusal.code, refusal.message, refusal.params)),
          })}
        </Alert>
      ) : null}
      {/* nodrag, nowheel and nopan: inside the canvas the mouse and the wheel go to the terminal */}
      <div
        className={`nodrag nowheel nopan overflow-auto rounded-field border border-line bg-term-bg ${
          compact ? "min-h-0 flex-1" : ""
        } ${noSignal ? "opacity-60 grayscale" : ""}`}
        data-renderer={mode === "live" ? "webgl" : "snapshot"}
        data-signal={noSignal ? "lost" : "ok"}
        style={compact ? undefined : { maxHeight: "min(75dvh, 60rem)" }}
      >
        {mode === "live" ? (
          <div ref={hostRef} />
        ) : (
          <section aria-label={t("snapshotLabel", { name: info.name })}>
            <pre className="m-0 min-h-24 p-2 font-mono text-sm leading-[1.25] whitespace-pre text-term-fg">
              {rows.length > 0 ? rows.join("\n") : " "}
            </pre>
          </section>
        )}
      </div>
      {mode !== "live" && !compact ? (
        <p className="text-sm text-muted">{t("snapshotHint")}</p>
      ) : null}
      <ModalOverlay
        isOpen={confirm}
        onOpenChange={setConfirm}
        isDismissable
        className="fixed inset-0 z-50 grid place-items-center bg-ink/40 p-4"
      >
        <Modal className="w-full max-w-md rounded-field border border-line bg-surface p-6 shadow-pop">
          <Dialog role="alertdialog" className="outline-none">
            {({ close }) => (
              <>
                <Heading slot="title" className="text-xl">
                  {t("busy.title")}
                </Heading>
                <p className="mt-2">{t("busy.body", { name: info.name })}</p>
                <div className="mt-5 flex flex-wrap justify-end gap-2">
                  <Button variant="quiet" onPress={close}>
                    {t("busy.cancel")}
                  </Button>
                  <Button
                    onPress={() => {
                      setTyping(true);
                      close();
                    }}
                  >
                    {t("busy.confirm")}
                  </Button>
                </div>
              </>
            )}
          </Dialog>
        </Modal>
      </ModalOverlay>
    </section>
  );
}
