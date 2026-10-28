import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { SensitiveButton } from "../../components/PinField";
import { Button } from "../../components/ui";
import type { GuildMachineRow } from "../../gen/panel";
import { StatusStrip } from "./kit";

/**
 * "Passar a Cidadela para X": the button is off, with the reason, when the machine cannot receive
 * it; otherwise it opens the summary of what happens, read before the PIN is asked.
 */
export function PassCitadel({
  m,
  citadel,
  reason,
  describeError,
  onPass,
}: {
  m: GuildMachineRow;
  /** The machine that is the Citadel now: its agents are the ones interrupted. */
  citadel: GuildMachineRow | undefined;
  reason: string;
  describeError: (err: unknown) => string;
  onPass: (pin: string) => Promise<void>;
}) {
  const { t } = useTranslation("guild");
  const [open, setOpen] = useState(false);
  const label = t("actions.passNamed", { name: m.name });
  return (
    <div className="flex flex-col gap-2">
      {open && !reason ? (
        <fieldset className="flex max-w-2xl flex-col gap-3 rounded-field border border-accent bg-accent-wash p-5">
          <legend className="px-1 text-xl">{t("pass.title", { name: m.name })}</legend>
          <ul className="ml-5 list-disc text-sm">
            <li>{t("pass.becomesTower", { from: citadel?.name ?? "" })}</li>
            <li>{t("pass.waits", { name: m.name })}</li>
            <li>{t("pass.panel")}</li>
            <li>
              {t("pass.agents", { count: citadel?.agents.length ?? 0, from: citadel?.name ?? "" })}
            </li>
            <li>{t("pass.noUndo")}</li>
          </ul>
          <div className="flex flex-wrap items-start gap-3">
            <SensitiveButton describeError={describeError} onConfirm={onPass}>
              {label}
            </SensitiveButton>
            <Button variant="quiet" onPress={() => setOpen(false)}>
              {t("pass.cancel")}
            </Button>
          </div>
        </fieldset>
      ) : (
        <Button isDisabled={Boolean(reason)} aria-expanded={open} onPress={() => setOpen(true)}>
          {label}
        </Button>
      )}
      {reason ? <p className="text-sm text-muted">{reason}</p> : null}
    </div>
  );
}

/**
 * What the snapshot of the Guild says about a switch of Citadel, and nothing more: no time is
 * promised. When the suspension ends, the line says who is the Citadel and since when, by the
 * clock of this browser.
 */
export function HandoverTimeline({ machines }: { machines: GuildMachineRow[] }) {
  const { t } = useTranslation("guild");
  const suspended = machines.find((m) => m.suspended);
  const was = useRef<string | null>(null);
  const [done, setDone] = useState<{ name: string; at: string; from: string } | null>(null);
  const citadelName = machines.find((m) => m.citadel)?.name ?? "";
  useEffect(() => {
    if (suspended) {
      was.current = suspended.name;
      setDone(null);
    } else if (was.current) {
      const from = was.current;
      was.current = null;
      setDone({
        name: citadelName,
        from,
        at: new Date().toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }),
      });
    }
  }, [suspended, citadelName]);

  if (suspended) {
    const steps = [
      {
        key: "stopped",
        dot: "done",
        title: t("timeline.stopped", { name: suspended.name }),
        hint: t("timeline.stoppedHint"),
      },
      { key: "takes", dot: "now", title: t("timeline.takes"), hint: t("timeline.takesHint") },
      { key: "panel", dot: "next", title: t("timeline.panel"), hint: t("timeline.panelHint") },
    ];
    return (
      <section
        aria-label={t("timeline.title")}
        className="max-w-3xl rounded-field border border-line bg-surface p-5"
      >
        <h2 className="text-xl">{t("timeline.title")}</h2>
        <p className="mt-1 text-sm text-muted">{t("timeline.intro")}</p>
        <ol className="mt-4 flex flex-col gap-4">
          {steps.map((s) => (
            <li key={s.key} className="flex items-start gap-3">
              <span
                aria-hidden="true"
                className={`mt-1 size-4 shrink-0 rounded-full border ${
                  s.dot === "done"
                    ? "border-signal bg-signal"
                    : s.dot === "now"
                      ? "border-warn-ink bg-warn"
                      : "border-line bg-paper"
                }`}
              />
              <div>
                <p className="font-semibold">{s.title}</p>
                <p className="text-sm text-muted">{s.hint}</p>
              </div>
            </li>
          ))}
        </ol>
      </section>
    );
  }
  if (done?.name) {
    return (
      <StatusStrip
        tone="ok"
        title={t("timeline.done", { name: done.name, at: done.at })}
        hint={t("timeline.check", { from: done.from })}
      />
    );
  }
  return null;
}
