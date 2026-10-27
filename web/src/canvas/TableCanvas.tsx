import "@xyflow/react/dist/style.css";
import "./canvas.css";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  applyNodeChanges,
  Background,
  ConnectionMode,
  Controls,
  MiniMap,
  type NodeChange,
  type OnBeforeDelete,
  Panel,
  ReactFlow,
  ReactFlowProvider,
  useNodesInitialized,
  useReactFlow,
} from "@xyflow/react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useCan, useMe } from "../app/session";
import { useUiSocket } from "../app/socket";
import { Alert, Button } from "../components/ui";
import type { CanvasState } from "../gen/panel";
import { ApiError } from "../lib/api";
import { errorText } from "../lib/errors";
import { socketFeed } from "../terminal/feed";
import { actions, canvasQuery, canvasTopic } from "./api";
import { CharacterList, PlainWords } from "./BoardWords";
import { CanvasEnvContext } from "./context";
import { EdgeMarkers, edgeTypes } from "./edges";
import { useGestures } from "./gestures";
import { type Draft, draftEdges } from "./mission";
import {
  buildEdges,
  buildNodes,
  type CanvasEdge,
  type CanvasNode,
  focusCharacter,
  groupOf,
  initialView,
  layoutItems,
} from "./model";
import { nodeTypes } from "./nodes";
import { SidePanel } from "./SidePanel";
import { Legend, SelectionStrip } from "./Strip";

/** What a refused action says, in the language of the user: the engine's reason, else its own words. */
export function useRefusalText() {
  const { t } = useTranslation("canvas");
  const { t: te } = useTranslation("errors");
  return (err: unknown): string => {
    if (err instanceof ApiError && err.code === "refused") {
      const reason = String(err.params.reason ?? "");
      const key = `refusal.${reason}`;
      const params = {
        ...err.params,
        reasons: Array.isArray(err.params.reasons) ? err.params.reasons.join(", ") : "",
      };
      const text = t(key, { ...params, defaultValue: "" });
      return text || err.message || errorText(te, err);
    }
    return errorText(te, err);
  };
}

/** The canvas of one table: the live state of the engine, drawn and acted on (docs/painel/canvas.md). */
export function TableCanvas({ workspace, table }: { workspace: string; table: string }) {
  return (
    <ReactFlowProvider>
      <Canvas workspace={workspace} table={table} />
    </ReactFlowProvider>
  );
}

function Canvas({ workspace, table }: { workspace: string; table: string }) {
  const { t } = useTranslation("canvas");
  const { timezone } = useMe();
  const canType = useCan("type_in_terminal");
  const sock = useUiSocket();
  const qc = useQueryClient();
  const refusalText = useRefusalText();
  const { data: state, error, isPending } = useQuery(canvasQuery(workspace, table));
  const feed = useMemo(() => (sock ? socketFeed(sock) : null), [sock]);
  const act = useMemo(() => actions(workspace, table), [workspace, table]);

  const [nodes, setNodes] = useState<CanvasNode[]>([]);
  const [notice, setNotice] = useState("");
  const [draft, setDraft] = useState<Draft | null>(null);
  const [side, setSide] = useState(false);
  const [tab, setTab] = useState("missions");
  const [saving, setSaving] = useState(false);
  const [multi, setMulti] = useState(false);
  const [legend, setLegend] = useState(false);
  const dragging = useRef(false);
  const frame = useRef<HTMLDivElement>(null);
  const flow = useReactFlow();
  const ready = useNodesInitialized();
  const opened = useRef(false);

  // the board opens at zoom 100%: all of the table if it fits, else centered on who works
  useEffect(() => {
    if (!ready || !state || opened.current) return;
    opened.current = true;
    const box = frame.current?.getBoundingClientRect();
    const view = initialView(nodes, focusCharacter(state), {
      width: box?.width ?? 0,
      height: box?.height ?? 0,
    });
    void flow.fitView({ ...view, minZoom: 1, maxZoom: 1, padding: 0.05 });
  }, [ready, state, nodes, flow]);

  // live: the snapshot opens the state, and each "changed" reads it again
  useEffect(() => {
    if (!sock) return;
    const topic = canvasTopic(workspace, table);
    const off = sock.listen({
      onEvent: (tp, ev, data) => {
        if (tp !== topic) return;
        if (ev === "snapshot")
          qc.setQueryData(canvasQuery(workspace, table).queryKey, data as CanvasState);
        else if (ev === "changed")
          void qc.invalidateQueries({ queryKey: canvasQuery(workspace, table).queryKey });
      },
    });
    sock.subscribe([topic]);
    return () => {
      off();
      sock.unsubscribe([topic]);
    };
  }, [sock, qc, workspace, table]);

  // the state of the server is the truth: nodes follow it, keeping what the screen holds (selection)
  useEffect(() => {
    if (!state || dragging.current) return;
    const built = buildNodes(state);
    setNodes((prev) => {
      const sel = new Set(prev.filter((n) => n.selected).map((n) => n.id));
      return built.map((n) => ({ ...n, selected: sel.has(n.id) }));
    });
  }, [state]);

  const fail = useCallback((err: unknown) => setNotice(refusalText(err)), [refusalText]);
  const changed = useCallback(
    () => void qc.invalidateQueries({ queryKey: canvasQuery(workspace, table).queryKey }),
    [qc, workspace, table],
  );

  const { pending, connect, removeEdges } = useGestures({
    state,
    act,
    changed,
    fail,
    draft,
    setDraft,
    clear: () => setNotice(""),
    onlyCharacters: () => setNotice(t("mission.onlyCharacters")),
  });

  const persist = useCallback(
    async (list: CanvasNode[]) => {
      if (list.length === 0) return;
      try {
        await act.saveLayout({ items: layoutItems(list) });
      } catch (err) {
        fail(err);
        changed();
      }
    },
    [act, fail, changed],
  );

  const onNodesChange = useCallback(
    (raw: NodeChange<CanvasNode>[]) => {
      setNodes((prev) => {
        let changes = raw;
        // "Selecionar vários": a click marks or unmarks a card and keeps the others (the way without Shift)
        if (multi && raw.some((c) => c.type === "select")) {
          const clicked = raw.flatMap((c) => (c.type === "select" && c.selected ? [c.id] : []));
          changes = [
            ...raw.filter((c) => c.type !== "select"),
            ...clicked.map(
              (id): NodeChange<CanvasNode> => ({
                type: "select",
                id,
                selected: !prev.find((n) => n.id === id)?.selected,
              }),
            ),
          ];
        }
        const next = applyNodeChanges(changes, prev);
        const resized = changes
          .filter((c) => c.type === "dimensions" && c.resizing === false)
          .map((c) => (c as { id: string }).id);
        if (resized.length > 0) void persist(next.filter((n) => resized.includes(n.id)));
        return next;
      });
    },
    [persist, multi],
  );

  // Delete: the engine decides. Nothing is removed on the screen before it answers.
  const onBeforeDelete: OnBeforeDelete<CanvasNode, CanvasEdge> = useCallback(
    async ({ nodes: gone, edges: cut }) => {
      setNotice("");
      await removeEdges(cut);
      const groups = gone.filter((n) => n.type === "group");
      if (groups.length > 0) {
        const keep = nodes.map((n) =>
          n.parentId && groups.some((g) => g.id === n.parentId) ? { ...n, parentId: undefined } : n,
        );
        const freed = keep.filter((n, i) => n.parentId !== nodes[i]?.parentId);
        const parents = new Map(nodes.map((n) => [n.id, n]));
        const absolute = freed.map((n) => {
          const p = parents.get(nodes.find((x) => x.id === n.id)?.parentId ?? "");
          return p
            ? { ...n, position: { x: n.position.x + p.position.x, y: n.position.y + p.position.y } }
            : n;
        });
        try {
          await act.saveLayout({ items: layoutItems(absolute), remove: groups.map((g) => g.id) });
        } catch (err) {
          fail(err);
        }
      }
      changed();
      return false;
    },
    [act, nodes, fail, changed, removeEdges],
  );

  const selected = nodes.filter((n) => n.selected);
  const groupable = selected.filter((n) => n.type !== "group" && !n.parentId);

  const makeGroup = () => {
    if (!state || groupable.length === 0) return;
    const id = `group:g${Date.now().toString(36)}`;
    const made = groupOf(
      groupable.map((n) => ({ ...n, selected: false })),
      id,
      t("group.default"),
      state.can_edit,
    );
    const ids = new Set(groupable.map((n) => n.id));
    setNodes([...made.slice(0, 1), ...nodes.filter((n) => !ids.has(n.id)), ...made.slice(1)]);
    void persist(made);
  };

  // a group is only visual: undoing it frees its cards where they stand
  const ungroup = useCallback(
    async (groupId: string) => {
      const parent = nodes.find((n) => n.id === groupId);
      if (!parent) return;
      const freed = nodes
        .filter((n) => n.parentId === groupId)
        .map((n) => ({
          ...n,
          parentId: undefined,
          position: { x: n.position.x + parent.position.x, y: n.position.y + parent.position.y },
        }));
      setNodes(
        nodes.filter((n) => n.id !== groupId).map((n) => freed.find((f) => f.id === n.id) ?? n),
      );
      try {
        await act.saveLayout({ items: layoutItems(freed), remove: [groupId] });
      } catch (err) {
        fail(err);
        changed();
      }
    },
    [nodes, act, fail, changed],
  );
  const renameGroup = useCallback(
    (groupId: string, label: string) => {
      const next = nodes.map((n) =>
        n.id === groupId ? { ...n, data: { ...n.data, label } as CanvasNode["data"] } : n,
      );
      setNodes(next);
      void persist(next.filter((n) => n.id === groupId));
    },
    [nodes, persist],
  );

  const edges = useMemo(() => {
    if (!state) return [];
    return [...buildEdges(state), ...pending, ...(draft ? draftEdges(draft, state) : [])];
  }, [state, pending, draft]);

  const env = useMemo(
    () => ({
      act,
      feed,
      timeZone: timezone,
      canType,
      fail,
      changed,
      drawing: draft !== null,
      ungroup: (id: string) => void ungroup(id),
      renameGroup,
    }),
    [act, feed, timezone, canType, fail, changed, draft, ungroup, renameGroup],
  );

  if (error) {
    return (
      <Alert>
        {error instanceof ApiError && error.code === "not_found"
          ? t("notFound")
          : refusalText(error)}
      </Alert>
    );
  }
  if (isPending || !state) {
    return (
      <p role="status" className="text-muted">
        {t("loading")}
      </p>
    );
  }

  return (
    <CanvasEnvContext.Provider value={env}>
      <div className="flex md:h-[calc(100dvh-12rem)] md:min-h-[28rem] w-full flex-col border border-line bg-paper md:flex-row">
        <section
          aria-label={t("region", { table })}
          className="hidden min-h-[18rem] min-w-0 flex-1 md:flex md:flex-col"
        >
          <SelectionStrip
            canEdit={state.can_edit}
            count={selected.filter((n) => n.type !== "group").length}
            multi={multi}
            setMulti={setMulti}
            onGroup={makeGroup}
            onSaveGrimoire={() => {
              setSide(true);
              setTab("grimoires");
              setSaving(true);
            }}
            onClear={() => {
              setNodes(nodes.map((n) => ({ ...n, selected: false })));
              setMulti(false);
            }}
          />
          <div ref={frame} className="relative min-h-0 flex-1">
            <EdgeMarkers />
            <ReactFlow<CanvasNode, CanvasEdge>
              nodes={nodes}
              edges={edges}
              nodeTypes={nodeTypes}
              edgeTypes={edgeTypes}
              onNodesChange={onNodesChange}
              onConnect={(c) => void connect(c)}
              onBeforeDelete={onBeforeDelete}
              onNodeDragStart={() => {
                dragging.current = true;
              }}
              onNodeDragStop={(_, __, dragged) => {
                dragging.current = false;
                void persist(dragged);
              }}
              connectionMode={ConnectionMode.Loose}
              isValidConnection={(c) => c.source !== c.target}
              minZoom={0.05}
              maxZoom={2}
              defaultViewport={{ x: 0, y: 0, zoom: 1 }}
              selectionOnDrag={false}
              deleteKeyCode={state.can_edit ? ["Backspace", "Delete"] : null}
              nodesConnectable={state.can_edit}
              elementsSelectable
              proOptions={{ hideAttribution: false }}
            >
              <Background gap={32} color="var(--color-line)" />
              <Controls showInteractive={false} aria-label={t("controls")} />
              <MiniMap pannable zoomable ariaLabel={t("minimap")} className="!hidden md:!block" />
              <Panel
                position="top-left"
                className="pointer-events-none flex max-w-[calc(100%-1rem)] flex-wrap items-center gap-2 *:pointer-events-auto"
              >
                <Button
                  variant="secondary"
                  onPress={() => void flow.fitView({ padding: 0.12, minZoom: 0.05, maxZoom: 1 })}
                >
                  {t("fit")}
                </Button>
                <Button
                  variant="secondary"
                  onPress={() => setLegend(!legend)}
                  aria-expanded={legend}
                >
                  {t("legend.open")}
                </Button>
                <Button
                  variant="secondary"
                  onPress={() => {
                    setSide(!side);
                  }}
                  aria-expanded={side}
                >
                  {side ? t("toolbar.hideSide") : t("toolbar.showSide")}
                </Button>
              </Panel>
              {draft ? (
                <Panel position="bottom-center">
                  <p
                    role="status"
                    className="m-0 rounded-field border border-accent bg-surface px-3 py-1 text-sm"
                  >
                    {t("mission.drawingBanner")}
                  </p>
                </Panel>
              ) : null}
            </ReactFlow>
            {legend ? <Legend onClose={() => setLegend(false)} /> : null}
            <div className="pointer-events-none absolute bottom-2 left-14 z-10 flex w-[min(30rem,calc(100%-4rem))] flex-col gap-2">
              {state.deadlocks.map((d) => (
                <div key={d.text} className="pointer-events-auto">
                  <Alert>{t("deadlock", { cycle: d.text })}</Alert>
                </div>
              ))}
              {notice ? (
                <div className="pointer-events-auto flex items-start gap-2">
                  <div className="flex-1">
                    <Alert>{notice}</Alert>
                  </div>
                  <Button variant="secondary" onPress={() => setNotice("")}>
                    {t("dismiss")}
                  </Button>
                </div>
              ) : null}
            </div>
          </div>
        </section>
        <div className="min-h-0 flex-1 overflow-auto md:hidden">
          <CharacterList
            state={state}
            selection={{
              keys: new Set(selected.map((n) => n.id)),
              toggle: (key) =>
                setNodes(nodes.map((n) => (n.id === key ? { ...n, selected: !n.selected } : n))),
              clear: () => setNodes(nodes.map((n) => ({ ...n, selected: false }))),
              saveGrimoire: () => {
                setSide(true);
                setTab("grimoires");
                setSaving(true);
              },
            }}
          />
          <div className="px-4 pb-4">
            <Button variant="secondary" onPress={() => setSide(!side)} aria-expanded={side}>
              {side ? t("toolbar.hideSide") : t("toolbar.showSide")}
            </Button>
          </div>
        </div>
        <PlainWords state={state} />
        {side || draft ? (
          <SidePanel
            state={state}
            draft={draft}
            setDraft={(d) => {
              setDraft(d);
              if (d) {
                setSide(true);
                setTab("missions");
              }
            }}
            tab={tab}
            setTab={setTab}
            selectedCount={selected.filter((n) => n.type !== "group").length}
            saving={saving}
            setSaving={setSaving}
            onSaveGrimoire={async (name, structureOnly, replace) => {
              try {
                await act.saveGrimoire({
                  name,
                  structure_only: structureOnly,
                  replace,
                  selection: layoutItems(selected.filter((n) => n.type !== "group")).map((i) =>
                    absolute(i, nodes),
                  ),
                });
                setNotice("");
              } catch (err) {
                if (
                  err instanceof ApiError &&
                  err.code === "refused" &&
                  err.params.reason === "name_taken"
                )
                  return "taken";
                fail(err);
                return undefined;
              }
              return "";
            }}
          />
        ) : null}
      </div>
    </CanvasEnvContext.Provider>
  );
}

/** The position of a node in the canvas, not in its group (what a grimoire keeps). */
function absolute(i: { key: string; x: number; y: number; parent?: string }, nodes: CanvasNode[]) {
  const p = nodes.find((n) => n.id === i.parent);
  return p ? { ...i, x: i.x + p.position.x, y: i.y + p.position.y, parent: undefined } : i;
}
