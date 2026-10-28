import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { SensitiveButton } from "../../components/PinField";
import { Button } from "../../components/ui";
import type { GuildMachineRow } from "../../gen/panel";

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
  const [done, setDone] = useState<{ name: string; at: string } | null>(null);
  const citadelName = machines.find((m) => m.citadel)?.name ?? "";
  useEffect(() => {
    if (suspended) {
      was.current = suspended.name;
      setDone(null);
    } else if (was.current) {
      was.current = null;
      setDone({
        name: citadelName,
        at: new Date().toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }),
      });
    }
  }, [suspended, citadelName]);

  if (suspended) {
    return (
      <section
        aria-label={t("timeline.title")}
        className="max-w-3xl rounded-field border border-warn-ink/40 bg-warn p-4 text-warn-ink"
      >
        <h2 className="text-lg">{t("timeline.title")}</h2>
        <ol className="mt-2 flex flex-col gap-1">
          <li>
            <span aria-hidden="true">✓ </span>
            {t("timeline.stopped", { name: suspended.name })}
          </li>
          <li>
            <span aria-hidden="true">○ </span>
            {t("timeline.takes")}
          </li>
          <li>
            <span aria-hidden="true">○ </span>
            {t("timeline.panel")}
          </li>
        </ol>
      </section>
    );
  }
  if (done?.name) {
    return (
      <section
        role="status"
        aria-label={t("timeline.doneTitle")}
        className="max-w-3xl rounded-field border border-signal bg-surface p-4"
      >
        <p className="font-semibold">{t("timeline.done", { name: done.name, at: done.at })}</p>
        <p className="text-sm">{t("timeline.check")}</p>
      </section>
    );
  }
  return null;
}
