import { BaseEdge, EdgeLabelRenderer, type EdgeProps, getBezierPath } from "@xyflow/react";
import { useTranslation } from "react-i18next";
import type { CanvasEdge } from "./model";

/** How far each kind of line bends, so lines between the same two nodes do not hide each other. */
const CURVATURE = [0.25, 0.45, 0.65, 0.05];

/**
 * The line of the canvas, in four kinds (docs/painel/canvas.md#elementos): a solid line for a
 * connection, an arrow with the number of the step for a mission (the current one in the
 * accent color, a return going back), and a dashed line with the reason for a wait (red
 * when it is part of a deadlock). The kind is never told by color alone: the shape and the
 * label say it too.
 */
export function CanvasEdgeView(props: EdgeProps<CanvasEdge>) {
  const { sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition, data, selected, id } =
    props;
  const { t } = useTranslation("canvas");
  const kind = data?.kind ?? "connection";
  const lane = data?.lane ?? 0;
  const [path, lx, ly] = getBezierPath({
    sourceX,
    sourceY,
    targetX,
    targetY,
    sourcePosition,
    targetPosition,
    curvature: CURVATURE[Math.min(lane, CURVATURE.length - 1)],
  });
  let stroke = "var(--color-muted)";
  let width = 2;
  let dash: string | undefined;
  let marker: string | undefined;
  let label = data?.label;
  if (kind === "step" || kind === "return") {
    stroke = data?.current ? "var(--color-accent)" : "var(--color-ink)";
    width = data?.current ? 4 : 2.5;
    marker = `url(#arrow-${data?.current ? "current" : "step"})`;
    if (kind === "return") dash = "2 5";
  } else if (kind === "wait") {
    stroke = data?.deadlock ? "var(--color-danger)" : "var(--color-muted)";
    dash = "10 6";
    marker = `url(#arrow-${data?.deadlock ? "deadlock" : "wait"})`;
  } else if (data?.pending) {
    dash = "4 4";
  }
  if (kind === "wait" && label) label = t("edge.waiting", { reason: label });
  if (kind === "return" && label) label = t("edge.return", { n: label });
  const title =
    kind === "connection" && data?.reasons?.length
      ? t("edge.reasons", { reasons: data.reasons.join(", ") })
      : undefined;
  return (
    <>
      <BaseEdge
        id={id}
        path={path}
        markerEnd={marker}
        interactionWidth={22}
        style={{
          stroke,
          strokeWidth: selected ? width + 2 : width,
          strokeDasharray: dash,
          opacity: data?.pending ? 0.6 : 1,
        }}
      />
      {label ? (
        <EdgeLabelRenderer>
          <span
            data-edge-kind={kind}
            data-current={data?.current ? "true" : undefined}
            data-deadlock={data?.deadlock ? "true" : undefined}
            title={title}
            style={{ transform: `translate(-50%, -50%) translate(${lx}px, ${ly}px)` }}
            className={`pointer-events-none absolute rounded-field border px-2 py-0.5 text-sm font-semibold ${
              data?.deadlock
                ? "border-danger bg-danger-wash text-danger"
                : data?.current
                  ? "border-accent bg-accent text-accent-ink"
                  : "border-line bg-surface text-ink"
            }`}
          >
            {label}
          </span>
        </EdgeLabelRenderer>
      ) : null}
    </>
  );
}

/** The arrowheads the lines use; one svg, defined once in the canvas. */
export function EdgeMarkers() {
  const head = (id: string, color: string) => (
    <marker
      id={id}
      viewBox="0 0 10 10"
      refX="9"
      refY="5"
      markerWidth="8"
      markerHeight="8"
      orient="auto-start-reverse"
    >
      <path d="M 0 0 L 10 5 L 0 10 z" fill={color} />
    </marker>
  );
  return (
    <svg width="0" height="0" aria-hidden="true" style={{ position: "absolute" }}>
      <defs>
        {head("arrow-step", "var(--color-ink)")}
        {head("arrow-current", "var(--color-accent)")}
        {head("arrow-wait", "var(--color-muted)")}
        {head("arrow-deadlock", "var(--color-danger)")}
      </defs>
    </svg>
  );
}

export const edgeTypes = { canvas: CanvasEdgeView };
