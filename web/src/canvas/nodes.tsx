import { useQuery } from "@tanstack/react-query";
import { Handle, type NodeProps, NodeResizer, Position } from "@xyflow/react";
import { memo, useEffect, useRef, useState } from "react";
import { Button as AriaButton } from "react-aria-components";
import { useTranslation } from "react-i18next";
import { Term } from "../components/Term";
import type { CanvasCharacter } from "../gen/panel";
import { terminalQuery } from "../lib/queries";
import { TerminalView } from "../terminal/TerminalView";
import { useCanvasEnv, useNodeView } from "./context";
import { MarkdownEditor } from "./MarkdownEditor";
import type { CanvasNode, CharacterData, GroupData, ScrollData, TomeData } from "./model";

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

function CharacterNodeView({ data, selected, id }: NodeProps<CanvasNode>) {
  const d = data as CharacterData;
  const c = d.character;
  const { t } = useTranslation("canvas");
  const env = useCanvasEnv();
  const label = useStateLabel();
  const { visible, near } = useNodeView(id);
  const term = useQuery({ ...terminalQuery(c.id), enabled: c.has_terminal });
  const grey = c.state === "stopped" || c.state === "interrupted";
  const isGM = c.role === "grand_master";

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
      <NodeResizer isVisible={selected && d.canEdit} minWidth={320} minHeight={240} />
      <Handles />
      <header className="flex flex-wrap items-center gap-x-3 gap-y-1 border-b border-line bg-paper px-3 py-2">
        <h3 className="m-0 text-lg">{c.name}</h3>
        <span className="text-sm text-muted">
          {isGM ? (
            <Term name="Grão-Mestre" />
          ) : c.role === "table_master" ? (
            <Term name="Mestre da mesa" />
          ) : (
            c.class || <Term name="Terminal simples" />
          )}
        </span>
        <span className="rounded-field border border-line px-2 text-sm" title={c.machine}>
          {t("node.machine", { machine: c.machine_name })}
        </span>
        <span
          className={`rounded-field px-2 text-sm ${grey ? "bg-line text-muted" : "bg-accent-wash"}`}
          role="status"
        >
          {label(c)}
        </span>
        <span className="flex-1" />
        {c.reduced ? (
          <span className="text-sm text-danger" title={c.reduced}>
            {t("node.reduced")}
          </span>
        ) : null}
        <AriaButton
          aria-label={
            c.pinned ? t("node.unpin", { name: c.name }) : t("node.pin", { name: c.name })
          }
          aria-pressed={c.pinned}
          isDisabled={!d.canEdit || isGM}
          onPress={() => void pin()}
          className={`nodrag grid size-11 place-items-center rounded-field hover:bg-accent-wash ${c.pinned ? "text-accent" : "text-muted"} disabled:opacity-60 data-focus-visible:ring-2 data-focus-visible:ring-accent`}
        >
          <PinIcon filled={c.pinned} />
        </AriaButton>
      </header>
      <div className="flex min-h-0 flex-1 flex-col p-2">
        {c.has_terminal && term.data && env.feed ? (
          <TerminalView
            terminalId={term.data.terminal_id}
            info={term.data}
            feed={env.feed}
            canType={env.canType}
            timeZone={env.timeZone}
            visible={visible}
            near={near}
            compact
            refreshInfo={() => term.refetch().then((r) => r.data ?? term.data)}
          />
        ) : (
          <p className="m-0 text-sm text-muted">
            {c.has_terminal ? t("node.loadingTerminal") : t("node.noTerminal")}
          </p>
        )}
      </div>
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
        <p role="alert" className="m-0 bg-danger-wash px-3 py-1 text-sm text-danger">
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
      <p className="m-0 text-sm text-muted">{t("node.tomeCount", { count: d.tome.scrolls })}</p>
    </article>
  );
}
export const TomeNode = memo(TomeNodeView);

function GroupNodeView({ data, selected }: NodeProps<CanvasNode>) {
  const d = data as GroupData;
  const { t } = useTranslation("canvas");
  return (
    <section
      aria-label={t("node.group", { name: d.label })}
      className={`h-full w-full rounded-field border border-dashed bg-accent-wash/30 ${
        selected ? "border-accent" : "border-muted"
      }`}
    >
      <NodeResizer isVisible={selected && d.canEdit} minWidth={160} minHeight={120} />
      <p className="m-0 px-3 py-1 text-sm font-semibold text-muted">{d.label}</p>
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
