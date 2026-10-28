import "@xyflow/react/dist/style.css";
import "../canvas/canvas.css";
import { useQuery } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import {
  applyNodeChanges,
  Background,
  Controls,
  Handle,
  MiniMap,
  type NodeChange,
  type NodeProps,
  Position,
  ReactFlow,
  ReactFlowProvider,
} from "@xyflow/react";
import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useMe } from "../app/session";
import { useUiSocket } from "../app/socket";
import { useNodeView } from "../canvas/context";
import { Term } from "../components/Term";
import type { WorkspaceView } from "../gen/panel";
import { api } from "../lib/api";
import { terminalQuery } from "../lib/queries";
import { socketFeed } from "../terminal/feed";
import { TerminalView } from "../terminal/TerminalView";
import {
  type BlockData,
  type BuildOptions,
  buildEdges,
  buildNodes,
  type GmData,
  layoutOf,
  type ViewNode,
} from "./model";
import { enc } from "./queries";
import { TableBlockCard } from "./TableBlockCard";

// The handles sit at the center of the node and are not seen: the line of a table runs
// from the middle of the Grand Master to the middle of the table, under both.
const hidden = { opacity: 0, left: "50%", top: "50%", pointerEvents: "none" as const };

function Anchors() {
  return (
    <>
      <Handle type="target" position={Position.Top} style={hidden} isConnectable={false} />
      <Handle type="source" position={Position.Top} style={hidden} isConnectable={false} />
    </>
  );
}

function GmNodeView({ data, id }: NodeProps<ViewNode>) {
  const { gm, canType } = data as GmData;
  const { t } = useTranslation("workspace");
  const { timezone } = useMe();
  const sock = useUiSocket();
  const feed = useMemo(() => (sock ? socketFeed(sock) : null), [sock]);
  const { visible, near } = useNodeView(id);
  const term = useQuery({ ...terminalQuery(gm?.id ?? ""), enabled: !!gm?.has_terminal });
  return (
    <article
      aria-label={gm ? t("gm.node", { name: gm.name }) : t("gm.none")}
      className="flex h-full w-full flex-col overflow-hidden rounded-field border-2 border-accent bg-surface shadow-card"
    >
      <Anchors />
      <header className="flex flex-wrap items-center gap-x-3 gap-y-1 border-b border-line bg-paper px-3 py-2">
        <h3 className="m-0 text-lg">{gm ? gm.name : <Term name="Grão-Mestre" />}</h3>
        {gm ? (
          <>
            <span className="text-sm text-muted">
              <Term name="Grão-Mestre" />
            </span>
            <span className="rounded-field border border-line px-2 text-sm" title={gm.machine}>
              {t("gm.machine", { machine: gm.machine_name })}
            </span>
            <span className="rounded-field bg-accent-wash px-2 text-sm">
              {t(`gm.states.${gm.state}`, { defaultValue: gm.state })}
            </span>
          </>
        ) : null}
      </header>
      <div className="nodrag nowheel flex min-h-0 flex-1 flex-col p-2">
        {gm?.has_terminal && term.data && feed ? (
          <TerminalView
            terminalId={term.data.terminal_id}
            info={term.data}
            feed={feed}
            canType={canType}
            timeZone={timezone}
            visible={visible}
            near={near}
            compact
            refreshInfo={() => term.refetch().then((r) => r.data ?? term.data)}
          />
        ) : (
          <p className="m-0 text-sm text-muted">
            {gm ? (gm.has_terminal ? t("gm.loadingTerminal") : t("gm.noTerminal")) : t("gm.none")}
          </p>
        )}
      </div>
    </article>
  );
}

function TableNodeView({ data }: NodeProps<ViewNode>) {
  const d = data as BlockData;
  return (
    <>
      <Anchors />
      <TableBlockCard
        block={d.block}
        workspace={d.workspace}
        repo={d.repo}
        canPause={d.canPause}
        situation={d.situation}
        newNotices={d.newNotices}
      />
    </>
  );
}

const nodeTypes = { gm: GmNodeView, table: TableNodeView };

/** The canvas of the workspace: the Grand Master at the top and a block for each table in rows below it. */
export function WorkspaceCanvas({
  view,
  showEnded,
  build,
}: {
  view: WorkspaceView;
  showEnded: boolean;
  build?: BuildOptions;
}) {
  return (
    <ReactFlowProvider>
      <Canvas view={view} showEnded={showEnded} build={build} />
    </ReactFlowProvider>
  );
}

function Canvas({
  view,
  showEnded,
  build,
}: {
  view: WorkspaceView;
  showEnded: boolean;
  build?: BuildOptions;
}) {
  const { t } = useTranslation("workspace");
  const navigate = useNavigate();
  const slug = view.workspace.slug;
  const [nodes, setNodes] = useState<ViewNode[]>([]);
  // the server is the truth: the nodes follow it, keeping what the screen holds (selection)
  useEffect(() => {
    const built = buildNodes(view, showEnded, build);
    setNodes((prev) => {
      const sel = new Set(prev.filter((n) => n.selected).map((n) => n.id));
      return built.map((n) => ({ ...n, selected: sel.has(n.id) }));
    });
  }, [view, showEnded, build]);
  const edges = useMemo(() => buildEdges(nodes), [nodes]);

  const save = (list: ViewNode[]) => {
    void api("PUT", `/api/workspaces/${enc(slug)}/view/layout`, { items: layoutOf(list) }).catch(
      () => {
        // the place is only a convenience: the next read of the view puts the blocks back
      },
    );
  };

  return (
    <section
      aria-label={t("region", { workspace: slug })}
      className="h-[calc(100dvh-22rem)] min-h-[30rem] w-full rounded-field border border-line"
    >
      <ReactFlow<ViewNode>
        nodes={nodes}
        edges={edges}
        nodeTypes={nodeTypes}
        onNodesChange={(changes: NodeChange<ViewNode>[]) =>
          setNodes((prev) => applyNodeChanges(changes, prev))
        }
        onNodeDragStop={(_e, _n, all) => save(all.length > 0 ? all : nodes)}
        onNodeClick={(e, node) => {
          if (node.type !== "table") return;
          // a link or a button inside the block does its own thing
          if ((e.target as HTMLElement).closest("a, button, summary, textarea, input, label"))
            return;
          void navigate({
            to: "/workspaces/$workspace/mesas/$table",
            params: { workspace: slug, table: node.id.replace(/^table:/, "") },
          });
        }}
        nodesConnectable={false}
        deleteKeyCode={null}
        fitView
        fitViewOptions={{ padding: 0.12, maxZoom: 1 }}
        minZoom={0.1}
        proOptions={{ hideAttribution: true }}
      >
        <Background />
        <Controls showInteractive={false} aria-label={t("region", { workspace: slug })} />
        <MiniMap pannable zoomable />
      </ReactFlow>
    </section>
  );
}
