import { useTranslation } from "react-i18next";
import { Button } from "../components/ui";
import type { CanvasState } from "../gen/panel";
import { useGoto } from "../lib/links";
import { useStateLabel } from "./nodes";
import { plainLines } from "./plain";

/** "Em palavras simples": a sentence for each character about what it does and waits for. */
export function PlainWords({ state }: { state: CanvasState }) {
  const { t } = useTranslation("canvas");
  const lines = plainLines(state);
  const say = t as unknown as (key: string, opts?: object) => string;
  return (
    <aside
      aria-labelledby="plain-words"
      className="w-full shrink-0 border-t border-line bg-surface p-4 md:w-72 md:overflow-auto md:border-t-0 md:border-l"
    >
      <h2 id="plain-words" className="m-0 text-xl">
        {t("plain.title")}
      </h2>
      {lines.length === 0 ? (
        <p className="text-muted">{t("plain.empty")}</p>
      ) : (
        <ul className="m-0 mt-2 flex list-none flex-col gap-2 p-0">
          {lines.map((l) => (
            <li key={l.id}>{say(`plain.${l.key}`, l.params)}</li>
          ))}
        </ul>
      )}
    </aside>
  );
}

/**
 * The board on a phone: the characters one under the other, joined by a line (dashed
 * where the next one waits), each with the way to its terminal.
 */
export function CharacterList({ state }: { state: CanvasState }) {
  const { t } = useTranslation("canvas");
  const label = useStateLabel();
  const goto = useGoto();
  const waiting = new Set(state.waits.map((w) => w.waiter));
  return (
    <ol
      aria-label={t("list.title", { table: state.table })}
      className="m-0 flex list-none flex-col p-4"
    >
      {state.characters.map((c, i) => {
        const last = i === state.characters.length - 1;
        const next = state.characters[i + 1];
        return (
          <li key={c.id} className="flex flex-col items-stretch">
            <article
              aria-label={t("list.character", { name: c.name })}
              className={`rounded-field border bg-surface p-4 ${c.state === "busy" ? "border-accent" : "border-line"}`}
            >
              <h3 className="m-0 text-lg">{c.name}</h3>
              <p className="m-0 mt-1 flex flex-wrap gap-2">
                <span className="rounded-field border border-line px-2 text-base">
                  {t("node.machine", { machine: c.machine_name })}
                </span>
                <span className="rounded-field bg-accent-wash px-2 text-base">{label(c)}</span>
              </p>
              {c.has_terminal ? (
                <div className="mt-2">
                  <Button
                    variant="secondary"
                    aria-label={t("list.viewTerminalNamed", { name: c.name })}
                    onPress={() => goto(`/terminal/${encodeURIComponent(c.id)}`)}
                  >
                    {t("list.viewTerminal")}
                  </Button>
                </div>
              ) : null}
            </article>
            {last ? null : (
              <span
                aria-hidden="true"
                data-testid="board-link"
                className={`mx-auto h-8 border-l-2 ${
                  next && waiting.has(next.id) ? "border-dashed border-muted" : "border-accent"
                }`}
              />
            )}
          </li>
        );
      })}
    </ol>
  );
}
