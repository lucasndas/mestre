import { Bar, BarChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";

// The charts of the usage screen, in a file of their own so Recharts loads only when the screen
// opens. The colors are the tokens of the theme (src/index.css), so they follow it. A chart
// is a picture: the tables beside it are its text alternative, and the chart hides from
// screen readers.

export interface Point {
  label: string;
  /** undefined is a gap (the provider did not say), drawn as no bar, never as zero. */
  value?: number;
}

const tick = { fill: "var(--color-muted)", fontSize: 14 };
const tooltipStyle = {
  background: "var(--color-surface)",
  border: "1px solid var(--color-line)",
  borderRadius: "var(--radius-field)",
  color: "var(--color-ink)",
};

/** Vertical bars along a series (the days). */
export function ColumnChart({
  title,
  data,
  color,
  format,
}: {
  title: string;
  data: Point[];
  color: string;
  format: (n: number) => string;
}) {
  return (
    <figure className="m-0">
      <figcaption className="mb-1 text-sm font-semibold">{title}</figcaption>
      <div aria-hidden="true" className="h-56 w-full min-w-0">
        <ResponsiveContainer width="100%" height="100%">
          <BarChart data={data} margin={{ top: 4, right: 8, bottom: 0, left: 0 }}>
            <CartesianGrid stroke="var(--color-line)" vertical={false} />
            <XAxis
              dataKey="label"
              tick={tick}
              tickLine={false}
              axisLine={{ stroke: "var(--color-line)" }}
            />
            <YAxis
              tick={tick}
              tickLine={false}
              axisLine={false}
              width={56}
              tickFormatter={format}
            />
            <Tooltip
              contentStyle={tooltipStyle}
              cursor={{ fill: "var(--color-accent-wash)" }}
              formatter={(v) => (typeof v === "number" ? format(v) : String(v))}
            />
            <Bar dataKey="value" name={title} fill={color} isAnimationActive={false} />
          </BarChart>
        </ResponsiveContainer>
      </div>
    </figure>
  );
}

/** Horizontal bars, one per name (workspace, table, character). */
export function RowsChart({
  title,
  data,
  color,
  format,
}: {
  title: string;
  data: Point[];
  color: string;
  format: (n: number) => string;
}) {
  const height = Math.max(120, data.length * 36 + 32);
  return (
    <figure className="m-0">
      <figcaption className="mb-1 text-sm font-semibold">{title}</figcaption>
      <div aria-hidden="true" className="w-full min-w-0" style={{ height }}>
        <ResponsiveContainer width="100%" height="100%">
          <BarChart
            data={data}
            layout="vertical"
            margin={{ top: 0, right: 16, bottom: 0, left: 0 }}
          >
            <CartesianGrid stroke="var(--color-line)" horizontal={false} />
            <XAxis
              type="number"
              tick={tick}
              tickLine={false}
              axisLine={false}
              tickFormatter={format}
            />
            <YAxis
              type="category"
              dataKey="label"
              tick={tick}
              tickLine={false}
              axisLine={{ stroke: "var(--color-line)" }}
              width={120}
            />
            <Tooltip
              contentStyle={tooltipStyle}
              cursor={{ fill: "var(--color-accent-wash)" }}
              formatter={(v) => (typeof v === "number" ? format(v) : String(v))}
            />
            <Bar dataKey="value" name={title} fill={color} isAnimationActive={false} />
          </BarChart>
        </ResponsiveContainer>
      </div>
    </figure>
  );
}
