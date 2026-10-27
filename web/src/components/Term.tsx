import { useRef, useState } from "react";
import { Button, Dialog, Popover } from "react-aria-components";
import { useTranslation } from "react-i18next";
import { glossary, type TermKey } from "../gen/glossary";
import type { Language } from "../i18n";

/**
 * A term of the vocabulary, underlined with dots, that opens its help. Hover
 * shows it and a press (or a tap) keeps it open. The texts come from the
 * glossary, never from this code: `name` must be a row of
 * docs/produto/glossario.md, or tsc fails.
 */
export function Term({ name }: { name: TermKey }) {
  const { t, i18n } = useTranslation("common");
  const text = glossary[name][i18n.language as Language] ?? glossary[name]["pt-BR"];
  const ref = useRef<HTMLButtonElement>(null);
  const [open, setOpen] = useState(false);
  const pinned = useRef(false);
  return (
    <span className="whitespace-nowrap">
      <Button
        ref={ref}
        aria-label={t("help.open", { term: text.name })}
        aria-expanded={open}
        onHoverStart={() => setOpen(true)}
        onHoverEnd={() => {
          if (!pinned.current) setOpen(false);
        }}
        onPress={() => {
          pinned.current = !pinned.current || !open;
          setOpen(pinned.current);
        }}
        className="cursor-help border-b border-dotted border-muted text-inherit hover:border-ink"
      >
        {text.name}
      </Button>
      <Popover
        triggerRef={ref}
        isOpen={open}
        isNonModal
        onOpenChange={(o) => {
          pinned.current = o;
          setOpen(o);
        }}
        placement="bottom start"
        offset={6}
        className="max-w-[18rem] rounded-field bg-ink px-3.5 py-3 text-[0.85rem] leading-snug whitespace-normal text-paper shadow-pop"
      >
        <Dialog aria-label={text.name} className="outline-none">
          <p>{text.help}</p>
          <p className="mt-2 text-[0.7rem] tracking-wider text-paper/70 uppercase">
            {t("help.real")}
          </p>
          <p>{text.real}</p>
        </Dialog>
      </Popover>
    </span>
  );
}
