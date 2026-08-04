import { api, ApiError } from "@/lib/api"

/**
 * Where an export has got to.
 *
 * `listing` has no total to show a percentage against yet - the walk over the
 * vault's entries is what produces one.
 */
export type ExportState = "listing" | "packing" | "ready" | "failed" | "canceled"

export interface ExportStatus {
  vaultId: string
  state: ExportState
  /** Files to pack. Zero until the listing finishes. */
  total: number
  done: number
  /** Files left out of the archive, each with a line in `problems`. */
  skipped: number
  /** Content packed so far, before compression. */
  bytes: number
  filename: string
  /** The finished archive's size on disk. */
  sizeBytes: number
  error?: string
  /** The skipped files, capped server-side. */
  problems?: string[]
  startedAt: string
  finishedAt?: string
  /** When the server deletes the archive. */
  expiresAt?: string
}

export function isExportActive(status: ExportStatus | null | undefined): boolean {
  return status?.state === "listing" || status?.state === "packing"
}

export const exportsApi = {
  start: (id: string) => api.post<ExportStatus>(`/vaults/${id}/export`),
  /** null when the vault has no export, which is the ordinary answer. */
  status: async (id: string): Promise<ExportStatus | null> => {
    try {
      return await api.get<ExportStatus>(`/vaults/${id}/export`)
    } catch (err) {
      if (err instanceof ApiError && err.status === 404) return null
      throw err
    }
  },
  discard: (id: string) => api.delete<void>(`/vaults/${id}/export`),
  /**
   * A plain URL rather than a fetch: the archive is a file of arbitrary size,
   * and letting the browser stream it to disk beats holding it in a blob.
   */
  downloadUrl: (id: string) => `/api/vaults/${id}/export/download`,
}
