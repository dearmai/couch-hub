import { CartesianGrid, Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts"

import type { Snapshot } from "@/lib/stats"

export interface Series {
  /** Key on the snapshot to plot. */
  key: keyof Snapshot
  label: string
  /** Categorical slot, assigned by entity and never by rank. */
  slot: 1 | 2
}

/**
 * Change over time for one measure.
 *
 * Deliberately never dual-axis: size and document count live on different
 * scales, so they are separate charts rather than two y-axes on one. Series
 * colours come from the validated categorical slots via CSS custom properties,
 * so light and dark each get their own step.
 */
export function TrendChart({
  snapshots,
  series,
  format,
  height = 180,
}: {
  snapshots: Snapshot[]
  series: Series[]
  format: (value: number) => string
  height?: number
}) {
  const data = snapshots.map((s) => ({
    at: s.at,
    label: new Date(s.at).toLocaleDateString("ko-KR", { month: "numeric", day: "numeric" }),
    ...Object.fromEntries(series.map((x) => [x.key, s[x.key] as number])),
  }))

  return (
    <div className="viz-root">
      {series.length > 1 ? (
        <div className="mb-2 flex flex-wrap gap-3 text-xs">
          {series.map((s) => (
            <span key={String(s.key)} className="text-muted-foreground flex items-center gap-1.5">
              <span
                className="size-2 rounded-full"
                style={{ backgroundColor: `var(--series-${s.slot})` }}
                aria-hidden
              />
              {s.label}
            </span>
          ))}
        </div>
      ) : null}

      <ResponsiveContainer width="100%" height={height}>
        <LineChart data={data} margin={{ top: 4, right: 8, bottom: 0, left: 0 }}>
          <CartesianGrid stroke="var(--viz-grid)" strokeDasharray="0" vertical={false} />
          <XAxis
            dataKey="label"
            tick={{ fontSize: 11, fill: "var(--viz-muted)" }}
            tickLine={false}
            axisLine={{ stroke: "var(--viz-axis)" }}
            minTickGap={24}
          />
          <YAxis
            tick={{ fontSize: 11, fill: "var(--viz-muted)" }}
            tickLine={false}
            axisLine={false}
            width={56}
            tickFormatter={format}
          />
          <Tooltip
            cursor={{ stroke: "var(--viz-axis)" }}
            content={({ active, payload, label }) => {
              if (!active || !payload?.length) return null
              return (
                <div className="bg-popover text-popover-foreground rounded-md border px-2.5 py-1.5 text-xs shadow-md">
                  <div className="text-muted-foreground mb-1">{label}</div>
                  {payload.map((p) => (
                    <div key={String(p.dataKey)} className="flex items-center gap-1.5">
                      <span
                        className="size-2 rounded-full"
                        style={{ backgroundColor: p.color as string }}
                        aria-hidden
                      />
                      {series.find((s) => s.key === p.dataKey)?.label}
                      <span className="ml-auto font-medium tabular-nums">{format(p.value as number)}</span>
                    </div>
                  ))}
                </div>
              )
            }}
          />
          {series.map((s) => (
            <Line
              key={String(s.key)}
              type="monotone"
              dataKey={s.key as string}
              stroke={`var(--series-${s.slot})`}
              strokeWidth={2}
              dot={false}
              // Big enough to grab without cluttering the line.
              activeDot={{ r: 4, strokeWidth: 0 }}
              isAnimationActive={false}
            />
          ))}
        </LineChart>
      </ResponsiveContainer>
    </div>
  )
}
