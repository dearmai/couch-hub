import { api } from "@/lib/api"
import type { Vault } from "@/lib/vaults"

export interface Snapshot {
  vaultId: string
  at: string
  docCount: number
  docDelCount: number
  sizeFile: number
  sizeActive: number
  sizeExternal: number
  updateSeqNum: number
}

export interface ActivityDay {
  day: string
  writes: number
}

export interface VaultStats {
  vault: Vault
  latest: Snapshot | null
  snapshots: Snapshot[] | null
  activity: ActivityDay[] | null
}

export interface Dashboard {
  totals: {
    vaults: number
    documents: number
    /** On-disk size, including revisions a compaction would reclaim. */
    sizeFile: number
    /** Live data size. */
    sizeActive: number
  }
  vaults: { vault: Vault; latest: Snapshot | null }[]
  activity: ActivityDay[] | null
  /** True until the poller has recorded at least one snapshot. */
  stale: boolean
}

export const statsApi = {
  dashboard: () => api.get<Dashboard>("/dashboard"),
  vault: (id: string) => api.get<VaultStats>(`/vaults/${id}/stats`),
  refresh: () => api.post<{ refreshed: boolean }>("/metrics/refresh"),
}

export const dashboardQuery = {
  queryKey: ["dashboard"] as const,
  queryFn: statsApi.dashboard,
}

/** Formats a byte count with a binary prefix. */
export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return "0 B"
  const units = ["B", "KiB", "MiB", "GiB", "TiB"]
  const exp = Math.min(Math.floor(Math.log2(bytes) / 10), units.length - 1)
  const value = bytes / 1024 ** exp
  return `${value.toFixed(exp === 0 ? 0 : value < 10 ? 1 : 0)} ${units[exp]}`
}

export function formatCount(n: number): string {
  return new Intl.NumberFormat("ko-KR").format(n)
}

export function formatDate(iso: string): string {
  return new Intl.DateTimeFormat("ko-KR", { dateStyle: "medium", timeStyle: "short" }).format(new Date(iso))
}
