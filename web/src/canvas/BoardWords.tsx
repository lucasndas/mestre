import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { Button as AriaButton } from "react-aria-components";
import { useTranslation } from "react-i18next";
import { useCan } from "../app/session";
import { Button } from "../components/ui";
import type { CanvasCharacter, CanvasState, TableBlock } from "../gen/panel";
import { formatNumber } from "../lib/format";
import { useGoto } from "../lib/links";
import { terminalQuery } from "../lib/queries";
import { PauseForm } from "../workspace/TableBlockCard";
import { useCanvasEnv } from "./context";
import { keyOf } from "./model";
import { plainLines } from "./plain";
import { StatusChip } from "./StatusChip";
import { characterStatus, missionLine } from "./status";

/** "Em palavras simples": a sentence for each character about what it does and waits for, then what the table has proved and used, and Pausar mesa. */
export function PlainWords({
  state,
  block,
}: {
  state: CanvasState;
  /** The table as the view of the campaign shows it (absent while it loads). */
  block?: TableBlock;
}) {
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
      {block ? <TableFacts block={block} workspace={state.workspace} /> : null}
    </aside>
  );
}

/** What the table has proved and spent, and the way to pause it (the same form as its card in the campaign). */
function TableFacts({ block, workspace }: { block: TableBlock; workspace: string }) {
  const { t, i18n } = useTranslation("canvas");
  const { t: tw } = useTranslation("workspace");
  const canPause = useCan("pause_buttons");
  const [pausing, setPausing] = useState(false);
  const c = block.criteria;
  const u = block.usage;
  const percent = u?.has_data ? Math.round(u.percent) : undefined;
  return (
    <div className="mt-3 flex flex-col gap-2">
      <dl className="m-0 grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-base">
        <dt className="text-muted">{t("plain.proofs")}</dt>
        <dd className="m-0">
          {c && c.total > 0
            ? t("plain.proofsOf", { proven: c.proven, total: c.total })
            : t("plain.noProofs")}
        </dd>
        {u ? (
          <>
            <dt className="text-muted">{t("plain.usage")}</dt>
            <dd className="m-0">
              {percent !== undefined
                ? t("plain.usageOf", { percent: formatNumber(percent, i18n.language) })
                : t("plain.noUsage")}
            </dd>
          </>
        ) : null}
      </dl>
      {canPause && !block.paused ? (
        pausing ? (
          <PauseForm workspace={workspace} block={block} onClose={() => setPausing(false)} />
        ) : (
          <div>
            <Button variant="secondary" onPress={() => setPausing(true)}>
              {tw("block.pause")}
            </Button>
          </div>
        )
      ) : null}
    </div>
  );
}

/** What the list of the phone needs of the screen: the selection to keep as a grimoire. */
export interface ListSelection {
  /** The keys (`character:<id>`) selected. */
  keys: Set<string>;
  toggle: (key: string) => void;
  clear: () => void;
  /** Opens the form "Guardar a seleção como grimório" of the side panel. */
  saveGrimoire: () => void;
}

/** One card of the list: the same facts as the card of the board, in a column. */
function ListCard({
  c,
  state,
  selecting,
  picked,
  onPick,
}: {
  c: CanvasCharacter;
  state: CanvasState;
  selecting: boolean;
  picked: boolean;
  onPick: () => void;
}) {
  const { t } = useTranslation("canvas");
  const env = useCanvasEnv();
  const goto = useGoto();
  const canDecide = useCan("decide_approvals");
  const term = useQuery({ ...terminalQuery(c.id), enabled: c.has_terminal });
  const status = characterStatus(c, state, {
    noSignal: c.has_terminal && term.data?.signal === false,
    canDecide,
  });
  const mission = missionLine(c, state);
  const [menu, setMenu] = useState(false);
  const [target, setTarget] = useState<string | null>(null);
  const others = state.characters.filter((x) => x.id !== c.id);
  const can = state.can_edit && c.role !== "grand_master";
  const run = async (fn: () => Promise<void>) => {
    try {
      await fn();
      env.changed();
    } catch (err) {
      env.fail(err);
    }
    setMenu(false);
    setTarget(null);
  };
  return (
    <article
      aria-label={t("list.character", { name: c.name })}
      className={`relative rounded-field border bg-surface p-4 ${c.state === "busy" ? "border-accent" : "border-line"}`}
    >
      <h3 className="m-0 flex items-center gap-2 text-lg">
        {selecting ? (
          <label className="flex min-h-11 flex-1 items-center gap-3">
            <input
              type="checkbox"
              checked={picked}
              onChange={onPick}
              aria-label={t("list.select", { name: c.name })}
              className="size-6 accent-accent"
            />
            {c.name}
          </label>
        ) : (
          <>
            <span className="min-w-0 flex-1">{c.name}</span>
            <AriaButton
              aria-label={t("list.more", { name: c.name })}
              aria-expanded={menu}
              onPress={() => setMenu(!menu)}
              className="grid size-11 place-items-center rounded-field text-xl hover:bg-accent-wash"
            >
              <span aria-hidden="true">⋯</span>
            </AriaButton>
          </>
        )}
      </h3>
      <p className="m-0 mt-1 flex flex-wrap items-center gap-2">
        <StatusChip status={status} />
        {c.class ? (
          <span className="rounded-full bg-accent-wash px-2.5 text-base">{c.class}</span>
        ) : null}
        <span className="rounded-full bg-accent-wash px-2.5 text-base">
          {t("node.machine", { machine: c.machine_name })}
        </span>
        {c.pinned ? (
          <span className="rounded-full bg-accent-wash px-2.5 text-base">{t("node.fixed")}</span>
        ) : null}
      </p>
      {mission ? (
        <p className="m-0 mt-1 text-base text-muted">
          {t("node.mission", { name: mission.quest, step: mission.step, total: mission.total })}
        </p>
      ) : null}
      <div className="mt-2 flex flex-wrap gap-2">
        {c.has_terminal ? (
          <Button
            variant="secondary"
            aria-label={t("list.viewTerminalNamed", { name: c.name })}
            onPress={() => goto(env.terminalPath(c.id))}
          >
            {t("list.viewTerminal")}
          </Button>
        ) : null}
        {status.needsYou ? (
          <Button onPress={() => goto("/aprovacoes")}>{t("node.decide")}</Button>
        ) : null}
      </div>
      {menu ? (
        <div
          role="menu"
          className="absolute top-14 right-3 z-10 flex min-w-60 flex-col rounded-field border border-line bg-surface p-1 shadow-card"
        >
          <AriaButton
            isDisabled={!can}
            onPress={() => void run(() => env.act.pin(c.id, !c.pinned))}
            className="min-h-11 rounded-field px-3 text-left hover:bg-accent-wash disabled:opacity-60"
          >
            {c.pinned ? t("node.unpinShort") : t("node.pinShort")} · {t("node.pinHelp")}
          </AriaButton>
          <AriaButton
            isDisabled={!state.can_edit}
            onPress={() => setTarget(others[0]?.id ?? "")}
            className="min-h-11 rounded-field px-3 text-left hover:bg-accent-wash disabled:opacity-60"
          >
            {t("list.connect")}
          </AriaButton>
          {!state.can_edit ? (
            <p className="m-0 px-3 py-1 text-sm text-muted">{t("readOnlyHere")}</p>
          ) : null}
        </div>
      ) : null}
      {target !== null ? (
        <div className="mt-2 flex flex-wrap items-end gap-2">
          <label className="flex flex-col gap-1 text-sm">
            <span className="font-semibold">{t("list.connectTo", { name: c.name })}</span>
            <select
              className="min-h-11 rounded-field border border-line bg-paper px-2"
              value={target}
              onChange={(e) => setTarget(e.target.value)}
            >
              {others.map((o) => (
                <option key={o.id} value={o.id}>
                  {o.name}
                </option>
              ))}
            </select>
          </label>
          <Button
            isDisabled={!target}
            onPress={() =>
              void run(() =>
                env.act.connect({ kind: "character", id: c.id }, { kind: "character", id: target }),
              )
            }
          >
            {t("list.connectDo")}
          </Button>
          <Button variant="quiet" onPress={() => setTarget(null)}>
            {t("list.connectCancel")}
          </Button>
        </div>
      ) : null}
    </article>
  );
}

/**
 * The board on a phone: the characters one under the other, joined by a line (dashed where the next one
 * waits), each with the way to its terminal. "Selecionar" turns on the check boxes to keep some as a grimoire.
 */
export function CharacterList({
  state,
  selection,
}: {
  state: CanvasState;
  selection: ListSelection;
}) {
  const { t } = useTranslation("canvas");
  const [selecting, setSelecting] = useState(false);
  const waiting = new Set(state.waits.map((w) => w.waiter));
  const count = selection.keys.size;
  return (
    <div className="flex flex-col gap-2 p-4">
      {state.can_edit ? (
        <div className="flex items-center justify-between gap-2">
          <p className="m-0 text-base text-muted">
            {selecting ? t("list.selectHint") : t("list.count", { count: state.characters.length })}
          </p>
          <Button
            variant="secondary"
            aria-pressed={selecting}
            onPress={() => {
              if (selecting) selection.clear();
              setSelecting(!selecting);
            }}
          >
            {selecting ? t("list.selectDone") : t("list.selectMode")}
          </Button>
        </div>
      ) : (
        <p className="m-0 text-base text-muted">{t("selection.readOnly")}</p>
      )}
      <ol
        aria-label={t("list.title", { table: state.table })}
        className="m-0 flex list-none flex-col p-0"
      >
        {state.characters.map((c, i) => {
          const last = i === state.characters.length - 1;
          const next = state.characters[i + 1];
          const key = keyOf("character", c.id);
          return (
            <li key={c.id} className="flex flex-col items-stretch">
              <ListCard
                c={c}
                state={state}
                selecting={selecting}
                picked={selection.keys.has(key)}
                onPick={() => selection.toggle(key)}
              />
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
      {selecting && count > 0 ? (
        <div
          role="toolbar"
          aria-label={t("selection.label")}
          className="sticky bottom-0 flex flex-wrap items-center gap-2 rounded-field bg-ink px-3 py-2 text-accent-ink"
        >
          <span className="font-semibold">{t("selection.count", { count })}</span>
          <Button onPress={selection.saveGrimoire} className="!bg-accent-wash !text-ink">
            {t("selection.saveGrimoire")}
          </Button>
          <AriaButton onPress={selection.clear} className="min-h-11 px-3 font-semibold underline">
            {t("selection.clear")}
          </AriaButton>
        </div>
      ) : null}
      <p className="m-0 mt-2 text-sm text-muted">{t("list.desktopOnly")}</p>
    </div>
  );
}
