import { useCallback, useEffect, useId, useRef, useState } from "react";
import { Button as AriaButton, TextArea } from "react-aria-components";
import { useTranslation } from "react-i18next";
import { Term } from "../components/Term";

export interface QueuedMessage {
  id: number;
  text: string;
}

/** What goes to the terminal for a message: the text and Enter; several lines go as one paste, so none submits early. */
export function encodeMessage(text: string): string {
  // control characters (ESC above all) never go inside the paste: a typed ESC[201~ would end it early
  // and the rest would reach the agent as keys
  const clean = [...text.replace(/\r\n?/g, "\n")]
    .filter((ch) => {
      const c = ch.codePointAt(0) ?? 0;
      return ch === "\n" || ch === "\t" || (c >= 0x20 && c !== 0x7f && (c < 0x80 || c > 0x9f));
    })
    .join("")
    // a Tab would reach the agent as a key (completion, mode switch)
    .replace(/\t/g, "  ");
  return clean.includes("\n") ? `\x1b[200~${clean}\x1b[201~\r` : `${clean}\r`;
}

/** How long to wait for a character to start working after a message before the next one may go. */
export const START_WAIT = 5000;

/**
 * The messages that wait for a character to be free. They live in this page only: a submit goes at once when
 * the character is free and the queue is empty, else it queues; when the character is free again the queue
 * leaves by itself, one message at a time, in order. A message the Citadel refuses goes back to the head.
 * docs/painel/terminal-no-painel.md#digitar.
 */
export function useMessageQueue(o: {
  busy: boolean;
  /** The queue may leave (the composer is open and the terminal can be typed into). */
  active: boolean;
  send: (data: string) => void;
}) {
  const [queue, setQueue] = useState<QueuedMessage[]>([]);
  const [sentBack, setSentBack] = useState(false);
  const next = useRef(1);
  const lastSent = useRef<string | null>(null);
  // after a message left: "sent" until the character is seen working, then "working" until it is free
  const phase = useRef<"none" | "sent" | "working">("none");
  // a refused message stays at the head and the queue stops until the user acts: sending it again by itself would only be refused again
  const held = useRef(false);
  const [, wake] = useState(0);
  const { busy, active, send } = o;
  const busyNow = useRef(busy);
  busyNow.current = busy;

  const deliver = useCallback(
    (text: string) => {
      lastSent.current = text;
      // already working: the next turn is when it is free again
      phase.current = busyNow.current ? "working" : "sent";
      send(encodeMessage(text));
    },
    [send],
  );

  const submit = (text: string) => {
    const t = text.trim();
    if (!t) return;
    held.current = false;
    if (!busy && queue.length === 0 && phase.current === "none") deliver(t);
    else setQueue((q) => [...q, { id: next.current++, text: t }]);
  };
  const remove = (id: number) => {
    held.current = false;
    setQueue((q) => q.filter((m) => m.id !== id));
  };
  const sendNow = (id: number) => {
    const m = queue.find((x) => x.id === id);
    if (!m) return;
    held.current = false;
    remove(id);
    deliver(m.text);
  };
  /** The Citadel refused the last message: it goes back to the head of the queue. */
  const refused = () => {
    const text = lastSent.current;
    lastSent.current = null;
    phase.current = "none";
    if (text === null) return;
    held.current = true;
    setSentBack(true);
    setQueue((q) => [{ id: next.current++, text }, ...q]);
  };

  // the character working, then free again: the turn of the next message
  useEffect(() => {
    if (busy && phase.current === "sent") phase.current = "working";
    if (!busy && phase.current === "working") {
      phase.current = "none";
      lastSent.current = null;
      wake((n) => n + 1);
    }
  }, [busy]);
  // a character that never started working after a message does not hold the queue for ever
  useEffect(() => {
    if (phase.current !== "sent") return;
    const timer = setTimeout(() => {
      if (phase.current === "sent") {
        phase.current = "none";
        lastSent.current = null;
        wake((n) => n + 1);
      }
    }, START_WAIT);
    return () => clearTimeout(timer);
  });
  useEffect(() => {
    if (!active || busy || held.current || phase.current !== "none") return;
    const head = queue[0];
    if (!head) return;
    setQueue((q) => q.slice(1));
    deliver(head.text);
  });

  return {
    queue,
    submit,
    remove,
    sendNow,
    refused,
    sentBack,
    clearSentBack: () => setSentBack(false),
  };
}

/** The box that writes a message to the agent, and the queue of the ones that wait. */
export function MessageComposer({
  name,
  busy,
  queue,
  onSubmit,
  onSendNow,
  onRemove,
  onKeys,
  keysDisabled,
  sentBack,
}: {
  name: string;
  busy: boolean;
  queue: QueuedMessage[];
  onSubmit: (text: string) => void;
  onSendNow: (id: number) => void;
  onRemove: (id: number) => void;
  onKeys: () => void;
  keysDisabled: boolean;
  sentBack: boolean;
}) {
  const { t } = useTranslation("terminal");
  const [text, setText] = useState("");
  const id = useId();
  const submit = () => {
    if (!text.trim()) return;
    onSubmit(text);
    setText("");
  };
  return (
    <div className="flex flex-col gap-2 border-t border-line bg-surface px-3 py-2">
      {queue.length > 0 ? (
        <section aria-label={t("composer.queued", { count: queue.length, name })}>
          <p className="m-0 font-semibold">{t("composer.queued", { count: queue.length, name })}</p>
          <ol className="m-0 flex list-none flex-col gap-1 p-0">
            {queue.map((m) => (
              <li key={m.id} className="flex flex-wrap items-center gap-2">
                <span className="min-w-0 flex-1 truncate">{m.text}</span>
                <button
                  type="button"
                  aria-label={t("composer.sendNowNamed", { text: m.text })}
                  title={t("composer.sendNowHint")}
                  onClick={() => onSendNow(m.id)}
                  className="min-h-11 rounded-field border border-warn bg-warn-wash px-3 text-base font-semibold text-warn-ink"
                >
                  {t("composer.sendNow")}
                </button>
                <AriaButton
                  aria-label={t("composer.removeNamed", { text: m.text })}
                  onPress={() => onRemove(m.id)}
                  className="min-h-11 rounded-field border border-line px-3 text-base font-semibold text-ink hover:bg-accent-wash"
                >
                  {t("composer.remove")}
                </AriaButton>
              </li>
            ))}
          </ol>
          <p className="m-0 text-sm text-muted">{t("composer.queueBrowser")}</p>
        </section>
      ) : null}
      {sentBack ? (
        <p role="status" className="m-0 text-sm text-danger">
          {t("composer.sentBack")}
        </p>
      ) : null}
      <div className="flex flex-wrap items-end gap-2">
        <label htmlFor={id} className="sr-only">
          {t("composer.label", { name })}
        </label>
        <TextArea
          id={id}
          value={text}
          rows={2}
          maxLength={32000}
          placeholder={t("composer.placeholder", { name })}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
              e.preventDefault();
              submit();
            }
          }}
          className="min-h-14 min-w-0 flex-1 basis-60 rounded-field border border-line bg-paper p-2 text-base"
        />
        <AriaButton
          onPress={submit}
          isDisabled={!text.trim()}
          className="min-h-11 rounded-field bg-accent px-4 font-semibold text-accent-ink hover:bg-accent-strong disabled:opacity-60"
        >
          {busy ? t("composer.queue") : t("composer.send")}
        </AriaButton>
        <span className={`inline-flex min-h-11 items-center ${keysDisabled ? "opacity-50" : ""}`}>
          {keysDisabled ? (
            <span>{t("composer.keys")}</span>
          ) : (
            <Term name="Teclado direto" onAct={onKeys} />
          )}
        </span>
      </div>
    </div>
  );
}
