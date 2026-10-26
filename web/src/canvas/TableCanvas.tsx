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
} from "@xyflow/react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Checkbox, Dialog, Heading, Modal, ModalOverlay } from "react-aria-components";
import { useTranslation } from "react-i18next";
import { useCan, useMe } from "../app/session";
import { useUiSocket } from "../app/socket";
import { Alert, Button, Field } from "../components/ui";
import type { CanvasState } from "../gen/panel";
import { ApiError } from "../lib/api";
import { errorText } from "../lib/errors";
import { socketFeed } from "../terminal/feed";
import { actions, canvasQuery, canvasTopic } from "./api";
import { CanvasEnvContext } from "./context";
import { EdgeMarkers, edgeTypes } from "./edges";
import { useGestures } from "./gestures";
import { type Draft, draftEdges } from "./mission";
import {
  buildEdges,
  buildNodes,
  type CanvasEdge,
  type CanvasNode,
  groupOf,
  layoutItems,
} from "./model";
import { nodeTypes } from "./nodes";
import { SidePanel } from "./SidePanel";

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
  const [grimoire, setGrimoire] = useState(false);
  const dragging = useRef(false);

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
    (changes: NodeChange<CanvasNode>[]) => {
      setNodes((prev) => {
        const next = applyNodeChanges(changes, prev);
        const resized = changes
          .filter((c) => c.type === "dimensions" && c.resizing === false)
          .map((c) => (c as { id: string }).id);
        if (resized.length > 0) void persist(next.filter((n) => resized.includes(n.id)));
        return next;
      });
    },
    [persist],
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

  const edges = useMemo(() => {
    if (!state) return [];
    return [...buildEdges(state), ...pending, ...(draft ? draftEdges(draft, state) : [])];
  }, [state, pending, draft]);

  const env = useMemo(
    () => ({ act, feed, timeZone: timezone, canType, fail, changed, drawing: draft !== null }),
    [act, feed, timezone, canType, fail, changed, draft],
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
      <div className="flex h-[calc(100dvh-12rem)] min-h-[28rem] w-full flex-col border border-line bg-paper md:flex-row">
        <section
          aria-label={t("region", { table })}
          className="relative min-h-[18rem] min-w-0 flex-1"
        >
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
            fitView
            fitViewOptions={{ maxZoom: 0.6 }}
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
              className="flex max-w-[calc(100%-1rem)] flex-wrap items-center gap-2"
            >
              {state.can_edit ? (
                <>
                  <Button
                    variant="secondary"
                    onPress={makeGroup}
                    isDisabled={groupable.length === 0}
                  >
                    {t("toolbar.group")}
                  </Button>
                  <Button
                    variant="secondary"
                    onPress={() => setGrimoire(true)}
                    isDisabled={selected.filter((n) => n.type !== "group").length === 0}
                  >
                    {t("toolbar.saveGrimoire")}
                  </Button>
                </>
              ) : null}
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
          <div className="pointer-events-none absolute inset-x-2 bottom-2 z-10 flex flex-col gap-2">
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
        </section>
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
          />
        ) : null}
      </div>
      <SaveGrimoire
        isOpen={grimoire}
        onClose={() => setGrimoire(false)}
        onSave={async (name, structureOnly, replace) => {
          try {
            await act.saveGrimoire({
              name,
              structure_only: structureOnly,
              replace,
              selection: layoutItems(selected.filter((n) => n.type !== "group")).map((i) =>
                absolute(i, nodes),
              ),
            });
            setGrimoire(false);
            setNotice("");
          } catch (err) {
            if (
              err instanceof ApiError &&
              err.code === "refused" &&
              err.params.reason === "name_taken"
            )
              return "taken";
            fail(err);
          }
          return "";
        }}
      />
    </CanvasEnvContext.Provider>
  );
}

/** The position of a node in the canvas, not in its group (what a grimoire keeps). */
function absolute(i: { key: string; x: number; y: number; parent?: string }, nodes: CanvasNode[]) {
  const p = nodes.find((n) => n.id === i.parent);
  return p ? { ...i, x: i.x + p.position.x, y: i.y + p.position.y, parent: undefined } : i;
}

function SaveGrimoire({
  isOpen,
  onClose,
  onSave,
}: {
  isOpen: boolean;
  onClose: () => void;
  onSave: (name: string, structureOnly: boolean, replace: boolean) => Promise<string | undefined>;
}) {
  const { t } = useTranslation("canvas");
  const [name, setName] = useState("");
  const [structure, setStructure] = useState(false);
  const [taken, setTaken] = useState(false);
  const [busy, setBusy] = useState(false);
  const go = async (replace: boolean) => {
    setBusy(true);
    const r = await onSave(name.trim(), structure, replace);
    setBusy(false);
    setTaken(r === "taken");
    if (r === "") setName("");
  };
  return (
    <ModalOverlay
      isOpen={isOpen}
      onOpenChange={(o) => !o && onClose()}
      isDismissable
      className="fixed inset-0 z-50 grid place-items-center bg-ink/40 p-4"
    >
      <Modal className="w-full max-w-md rounded-field border border-line bg-surface p-6 shadow-pop">
        <Dialog className="flex flex-col gap-3 outline-none">
          <Heading slot="title" className="text-xl">
            {t("grimoire.saveTitle")}
          </Heading>
          <Field
            label={t("grimoire.saveName")}
            value={name}
            onChange={(v) => {
              setName(v);
              setTaken(false);
            }}
          />
          <Checkbox
            isSelected={structure}
            onChange={setStructure}
            className="flex min-h-11 items-center gap-2"
          >
            {t("grimoire.structureOnly")}
          </Checkbox>
          {taken ? <Alert>{t("grimoire.nameTaken")}</Alert> : null}
          <div className="flex flex-wrap justify-end gap-2">
            <Button variant="quiet" onPress={onClose}>
              {t("grimoire.cancelSave")}
            </Button>
            {taken ? (
              <Button variant="danger" isDisabled={busy} onPress={() => void go(true)}>
                {t("grimoire.replace")}
              </Button>
            ) : (
              <Button isDisabled={busy || !name.trim()} onPress={() => void go(false)}>
                {t("grimoire.save")}
              </Button>
            )}
          </div>
        </Dialog>
      </Modal>
    </ModalOverlay>
  );
}
