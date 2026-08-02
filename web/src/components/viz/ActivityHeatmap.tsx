import { useMemo } from "react"

import type { ActivityDay } from "@/lib/stats"
import { formatCount } from "@/lib/stats"

const WEEKS = 53
const DAY_MS = 24 * 60 * 60 * 1000
const WEEKDAY_LABELS = ["", "월", "", "수", "", "금", ""]

/**
 * Contribution-style calendar of daily write counts.
 *
 * Magnitude, not identity, so it uses the sequential blue ramp - one hue,
 * light to dark - rather than categorical slots. Zero days take the gridline
 * colour so an empty stretch recedes instead of reading as a low value.
 *
 * Hand-built rather than charted: it is a fixed 53x7 grid of squares, which a
 * charting library would only make heavier.
 */
export function ActivityHeatmap({ activity }: { activity: ActivityDay[] }) {
  const { cells, max, total } = useMemo(() => buildCalendar(activity), [activity])

  return (
    <div className="viz-root space-y-2">
      {/* The grid is wider than a phone; let it scroll in its own box rather
          than making the page scroll sideways. */}
      <div className="overflow-x-auto pb-1">
        <div className="flex gap-[3px]" style={{ minWidth: "max-content" }}>
          <div className="mr-1 flex flex-col gap-[3px]">
            {WEEKDAY_LABELS.map((label, i) => (
              <div
                key={i}
                className="h-[11px] w-4 text-[9px] leading-[11px]"
                style={{ color: "var(--viz-muted)" }}
                aria-hidden
              >
                {label}
              </div>
            ))}
          </div>

          {cells.map((week, wi) => (
            <div key={wi} className="flex flex-col gap-[3px]">
              {/* A native title rather than a tooltip component: this grid has
                  371 cells, and mounting a positioned popover per cell costs far
                  more than the hover affordance is worth. */}
              {week.map((cell, di) =>
                cell === null ? (
                  <div key={`pad-${wi}-${di}`} className="size-[11px]" />
                ) : (
                  <div
                    key={cell.day}
                    className="size-[11px] rounded-[2px]"
                    style={{ backgroundColor: `var(--heat-${level(cell.writes, max)})` }}
                    title={`${cell.day} · 쓰기 ${formatCount(cell.writes)}회`}
                  />
                ),
              )}
            </div>
          ))}
        </div>
      </div>

      <div className="flex items-center justify-between text-[11px]" style={{ color: "var(--viz-muted)" }}>
        <span>최근 1년 쓰기 {formatCount(total)}회</span>
        <span className="flex items-center gap-1">
          적음
          {[0, 1, 2, 3, 4].map((l) => (
            <span
              key={l}
              className="size-[11px] rounded-[2px]"
              style={{ backgroundColor: `var(--heat-${l})` }}
              aria-hidden
            />
          ))}
          많음
        </span>
      </div>
    </div>
  )
}

type Cell = { day: string; writes: number }

/**
 * Lays out the last WEEKS weeks as columns, each column Sunday..Saturday, with
 * the current week last. Days before the start of the window are null so the
 * first column aligns to the right weekday.
 */
function buildCalendar(activity: ActivityDay[]) {
  const byDay = new Map(activity.map((a) => [a.day, a.writes]))

  const today = new Date()
  const end = new Date(Date.UTC(today.getUTCFullYear(), today.getUTCMonth(), today.getUTCDate()))
  // Walk back to the Sunday that starts the first visible week.
  const start = new Date(end.getTime() - (WEEKS * 7 - 1) * DAY_MS)
  start.setUTCDate(start.getUTCDate() - start.getUTCDay())

  const cells: (Cell | null)[][] = []
  let max = 0
  let total = 0

  for (let w = 0; w < WEEKS; w++) {
    const week: (Cell | null)[] = []
    for (let d = 0; d < 7; d++) {
      const date = new Date(start.getTime() + (w * 7 + d) * DAY_MS)
      if (date > end) {
        week.push(null)
        continue
      }
      const day = date.toISOString().slice(0, 10)
      const writes = byDay.get(day) ?? 0
      max = Math.max(max, writes)
      total += writes
      week.push({ day, writes })
    }
    cells.push(week)
  }

  return { cells, max, total }
}

/**
 * Buckets a count into the five ramp steps.
 *
 * Quartiles of the observed maximum rather than fixed thresholds: vault write
 * volumes differ by orders of magnitude between a scratch vault and a daily
 * driver, and fixed cutoffs would leave one of them a flat block of colour.
 */
function level(writes: number, max: number): 0 | 1 | 2 | 3 | 4 {
  if (writes <= 0 || max <= 0) return 0
  const ratio = writes / max
  if (ratio <= 0.25) return 1
  if (ratio <= 0.5) return 2
  if (ratio <= 0.75) return 3
  return 4
}
