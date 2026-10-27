import { useQuery } from "@tanstack/react-query";
import { Handle, type NodeProps, NodeResizer, Position } from "@xyflow/react";
import { memo, useEffect, useRef, useState } from "react";
import { Button as AriaButton } from "react-aria-components";
import { useTranslation } from "react-i18next";
import { useCan } from "../app/session";
import { Term } from "../components/Term";
import type { CanvasCharacter } from "../gen/panel";
import { useGoto } from "../lib/links";
import { terminalQuery } from "../lib/queries";
import { TerminalView } from "../terminal/TerminalView";
import { ToneChip } from "../workspace/SituationChip";
import { useCanvasEnv, useNodeView } from "./context";
import { MarkdownEditor } from "./MarkdownEditor";
import type { CanvasNode, CharacterData, GroupData, ScrollData, TomeData } from "./model";
import { type CharacterStatus, characterStatus, missionLine } from "./status";

/** The pin of a pinned character: a drawing, not an emoji, so it looks the same on every machine. */
function PinIcon({ filled }: { filled: boolean }) {
  return (
    <svg
      width="20"
      height="20"
      viewBox="0 0 24 24"
      aria-hidden="true"
      fill={filled ? "currentColor" : "none"}
      stroke="currentColor"
      strokeWidth="1.8"
      strokeLinejoin="round"
      strokeLinecap="round"
    >
      <path d="M9 3h6l-1 6 3 3v2H7v-2l3-3-1-6z" />
      <path d="M12 14v7" />
    </svg>
  );
}

function Handles() {
  return (
    <>
      <Handle type="target" position={Position.Left} className="canvas-handle" />
      <Handle type="source" position={Position.Right} className="canvas-handle" />
    </>
  );
}

/** "aguardando permissão", "aguardando Revisor"…: the state and its reason in words. */
export function useStateLabel() {
  const { t } = useTranslation("canvas");
  return (c: CanvasCharacter): string => {
    const reason = c.reason ?? "";
    if (c.state === "blocked") {
      if (reason === "permission") return t("state.blocked_permission");
      if (reason === "quota") return t("state.blocked_quota");
      return t("state.blocked_input");
    }
    if (c.state === "waiting" && reason.startsWith("approval")) return t("state.waiting_approval");
    if (c.state === "waiting" && reason) return t("state.waiting_for", { reason });
    return t(`state.${c.state}`, { defaultValue: c.state });
  };
}

/** The word of a character's state (the vocabulary of the design) with its sign, and who it waits for. */
export function StatusChip({ status }: { status: CharacterStatus }) {
  const { t } = useTranslation("canvas");
  const say = t as unknown as (k: string, o?: object) => string;
  return (
    <ToneChip tone={status.tone} icon={status.icon} large>
      {say(`status.${status.key}`, { reason: status.reason })}
    </ToneChip>
  );
}

/** "Missão entrega · Etapa 1 de 2", with one bar per step: done, now, to do. */
function MissionLine({ line }: { line: NonNullable<ReturnType<typeof missionLine>> }) {
  const { t } = useTranslation("canvas");
  return (
    <p className="m-0 flex items-center gap-2 text-base">
      <span aria-hidden="true" className="inline-flex gap-1">
        {line.bars.map((b, i) => (
          <span
            // the steps of one mission, in order
            // biome-ignore lint/suspicious/noArrayIndexKey: the bars follow the steps by position
            key={i}
            className={`h-1.5 w-4 rounded-full ${b === "done" ? "bg-signal" : b === "current" ? "bg-accent" : "bg-line"}`}
          />
        ))}
      </span>
      <span>{t("node.mission", { name: line.quest, step: line.step, total: line.total })}</span>
    </p>
  );
}

function CharacterNodeView({ data, selected, id }: NodeProps<CanvasNode>) {
  const d = data as CharacterData;
  const c = d.character;
  const { t } = useTranslation("canvas");
  const env = useCanvasEnv();
  const canDecide = useCan("decide_approvals");
  const goto = useGoto();
  const { visible, near } = useNodeView(id);
  const term = useQuery({ ...terminalQuery(c.id), enabled: c.has_terminal });
  const isGM = c.role === "grand_master";
  const status = characterStatus(c, d.state, {
    noSignal: c.has_terminal && term.data?.signal === false,
    canDecide,
  });
  const mission = missionLine(c, d.state);
  const off = status.key === "noSignal" || status.key === "ended";

  const pin = async () => {
    try {
      await env.act.pin(c.id, !c.pinned);
      env.changed();
    } catch (err) {
      env.fail(err);
    }
  };

  return (
    <article
      aria-label={t("node.character", { name: c.name })}
      className={`flex h-full w-full flex-col overflow-hidden rounded-field border bg-surface shadow-card ${
        selected ? "border-accent ring-2 ring-accent/30" : "border-line"
      }`}
    >
      <NodeResizer isVisible={selected && d.canEdit} minWidth={320} minHeight={300} />
      <Handles />
      <header className="flex flex-wrap items-center gap-x-3 gap-y-1 border-b border-line bg-paper px-3 py-2">
        <h3 className="m-0 text-lg">{c.name}</h3>
        <span className="text-base text-muted">
          {isGM ? (
            <Term name="Grão-Mestre" />
          ) : c.role === "table_master" ? (
            <Term name="Mestre da mesa" />
          ) : (
            c.class || <Term name="Terminal simples" />
          )}
        </span>
        <span className="rounded-field border border-line px-2 text-base" title={c.machine}>
          {t("node.machine", { machine: c.machine_name })}
        </span>
        <span role="status">
          <StatusChip status={status} />
        </span>
        <span className="flex-1" />
        {c.pinned ? (
          <span className="rounded-field border border-line px-2 text-base">{t("node.fixed")}</span>
        ) : null}
        {c.reduced ? (
          <span className="text-base text-danger" title={c.reduced}>
            {t("node.reduced")}
          </span>
        ) : null}
      </header>
      <div
        className={`flex min-h-0 flex-1 flex-col overflow-hidden p-2 ${off ? "opacity-70 grayscale" : ""}`}
      >
        {c.has_terminal && term.data && env.feed ? (
          <TerminalView
            terminalId={term.data.terminal_id}
            info={term.data}
            feed={env.feed}
            canType={false}
            timeZone={env.timeZone}
            visible={visible}
            near={near}
            compact
            refreshInfo={() => term.refetch().then((r) => r.data ?? term.data)}
          />
        ) : (
          <p className="m-0 text-base text-muted">
            {c.has_terminal ? t("node.loadingTerminal") : t("node.noTerminal")}
          </p>
        )}
      </div>
      {mission || status.needsYou ? (
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 pb-1">
          {mission ? <MissionLine line={mission} /> : null}
          {status.needsYou ? (
            <AriaButton
              onPress={() => goto("/aprovacoes")}
              className="nodrag min-h-11 rounded-field bg-accent px-3 font-semibold text-accent-ink hover:bg-accent-strong"
            >
              {t("node.decide")}
            </AriaButton>
          ) : null}
        </div>
      ) : null}
      <footer className="flex flex-wrap items-center gap-2 border-t border-line px-3 py-2">
        {c.has_terminal ? (
          <AriaButton
            aria-label={t("node.openTerminalNamed", { name: c.name })}
            onPress={() => goto(`/terminal/${encodeURIComponent(c.id)}`)}
            className="nodrag min-h-11 rounded-field border border-line bg-surface px-3 font-semibold hover:bg-accent-wash data-focus-visible:ring-2 data-focus-visible:ring-accent"
          >
            {t("node.openTerminal")}
          </AriaButton>
        ) : null}
        <AriaButton
          aria-label={
            c.pinned ? t("node.unpin", { name: c.name }) : t("node.pin", { name: c.name })
          }
          aria-pressed={c.pinned}
          isDisabled={!d.canEdit || isGM}
          onPress={() => void pin()}
          className="nodrag inline-flex min-h-11 items-center gap-1 rounded-field border border-line bg-surface px-3 font-semibold hover:bg-accent-wash disabled:opacity-60 data-focus-visible:ring-2 data-focus-visible:ring-accent"
        >
          <PinIcon filled={c.pinned} />
          {c.pinned ? t("node.unpinShort") : t("node.pinShort")}
        </AriaButton>
        <span className="text-base text-muted">
          {c.pinned ? t("node.pinnedHelp") : t("node.pinHelp")}
        </span>
      </footer>
    </article>
  );
}
export const CharacterNode = memo(CharacterNodeView);

function ScrollNodeView({ data, selected }: NodeProps<CanvasNode>) {
  const d = data as ScrollData;
  const s = d.scroll;
  const { t } = useTranslation("canvas");
  const env = useCanvasEnv();
  const [draft, setDraft] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const base = useRef(s.revision);
  const conflict = draft !== null && s.revision !== base.current;
  // a draft is dropped when the scroll is saved by the person or follows what an agent wrote
  useEffect(() => {
    if (draft === null) base.current = s.revision;
  }, [draft, s.revision]);

  const save = async () => {
    if (draft === null) return;
    setBusy(true);
    try {
      await env.act.writeScroll(s.id, draft, base.current);
      setDraft(null);
      env.changed();
    } catch (err) {
      env.fail(err);
    } finally {
      setBusy(false);
    }
  };

  return (
    <article
      aria-label={t("node.scroll", { name: s.name })}
      className={`flex h-full w-full flex-col overflow-hidden rounded-field border bg-surface shadow-card ${
        selected ? "border-accent ring-2 ring-accent/30" : "border-line"
      }`}
    >
      <NodeResizer isVisible={selected && d.canEdit} minWidth={220} minHeight={140} />
      <Handles />
      <header className="flex items-center gap-2 border-b border-line bg-paper px-3 py-1">
        <h3 className="m-0 min-w-0 flex-1 truncate text-base" title={s.name}>
          <Term name="Pergaminho" />: {s.name}
        </h3>
        {draft !== null ? (
          <>
            <AriaButton
              onPress={() => void save()}
              isDisabled={busy}
              className="nodrag min-h-11 rounded-field bg-accent px-3 font-semibold text-accent-ink disabled:opacity-60"
            >
              {t("scroll.save")}
            </AriaButton>
            <AriaButton
              onPress={() => setDraft(null)}
              className="nodrag min-h-11 rounded-field px-3 text-accent hover:bg-accent-wash"
            >
              {t("scroll.discard")}
            </AriaButton>
          </>
        ) : null}
      </header>
      {conflict ? (
        <p role="alert" className="m-0 bg-danger-wash px-3 py-1 text-base text-danger">
          {t("scroll.changed")}
        </p>
      ) : null}
      <div className="min-h-0 flex-1 p-1">
        <MarkdownEditor
          value={draft ?? s.content}
          editable={d.canEdit}
          label={t("scroll.editor", { name: s.name })}
          onChange={(text) => {
            if (text !== s.content) setDraft(text);
            else setDraft(null);
          }}
        />
      </div>
    </article>
  );
}
export const ScrollNode = memo(ScrollNodeView);

function TomeNodeView({ data, selected }: NodeProps<CanvasNode>) {
  const d = data as TomeData;
  const { t } = useTranslation("canvas");
  return (
    <article
      aria-label={t("node.tome", { name: d.tome.name })}
      className={`relative h-full w-full rounded-field border bg-surface p-3 shadow-card ${
        selected ? "border-accent ring-2 ring-accent/30" : "border-line"
      }`}
      style={{ boxShadow: "6px 6px 0 -1px var(--color-surface), 6px 6px 0 0 var(--color-line)" }}
    >
      <Handles />
      <h3 className="m-0 text-base">
        <Term name="Tomo" />: {d.tome.name}
      </h3>
      <p className="m-0 text-base text-muted">{t("node.tomeCount", { count: d.tome.scrolls })}</p>
    </article>
  );
}
export const TomeNode = memo(TomeNodeView);

function GroupNodeView({ data, selected, id }: NodeProps<CanvasNode>) {
  const d = data as GroupData;
  const { t } = useTranslation("canvas");
  const env = useCanvasEnv();
  const [name, setName] = useState(d.label);
  useEffect(() => setName(d.label), [d.label]);
  return (
    <section
      aria-label={t("node.group", { name: d.label })}
      className={`h-full w-full rounded-field border border-dashed bg-accent-wash/30 ${
        selected ? "border-accent" : "border-muted"
      }`}
    >
      <NodeResizer isVisible={selected && d.canEdit} minWidth={160} minHeight={120} />
      <div className="flex items-center gap-2 px-2 py-1">
        {d.canEdit ? (
          <>
            <input
              aria-label={t("node.groupName")}
              value={name}
              onChange={(e) => setName(e.target.value)}
              onBlur={() => name.trim() && name !== d.label && env.renameGroup(id, name.trim())}
              className="nodrag min-h-11 w-40 rounded-field border border-transparent bg-transparent px-2 text-base font-semibold text-muted hover:border-line focus:border-accent focus:bg-surface focus:text-ink"
            />
            <AriaButton
              onPress={() => env.ungroup(id)}
              className="nodrag min-h-11 rounded-field px-2 font-semibold text-accent underline underline-offset-4 hover:bg-accent-wash"
            >
              {t("node.ungroup")}
            </AriaButton>
          </>
        ) : (
          <p className="m-0 px-1 text-base font-semibold text-muted">{d.label}</p>
        )}
      </div>
    </section>
  );
}
export const GroupNode = memo(GroupNodeView);

export const nodeTypes = {
  character: CharacterNode,
  scroll: ScrollNode,
  tome: TomeNode,
  group: GroupNode,
};
