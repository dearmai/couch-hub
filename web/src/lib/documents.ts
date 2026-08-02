import { api } from "@/lib/api"

export interface VaultDocument {
  id: string
  path: string
  type: string
  chunks: number
  size: number
  /** Plugin modification time, in milliseconds. */
  mtime: number
  deleted: boolean
  /** Set when the path could not be decrypted; the document still exists. */
  pathError?: string
}

export interface DocumentContent extends VaultDocument {
  text: string
  /** Binary content is listed but not rendered. */
  binary: boolean
}

export const documentsApi = {
  list: (vaultId: string, limit = 200) =>
    api.get<VaultDocument[] | null>(`/vaults/${vaultId}/documents?limit=${limit}`),
  // docId travels as a query parameter: livesync keys notes by their vault
  // path, so ids contain slashes and cannot be a path segment.
  get: (vaultId: string, docId: string) =>
    api.get<DocumentContent>(`/vaults/${vaultId}/document?docId=${encodeURIComponent(docId)}`),
}

/** Formats the plugin's millisecond mtime, which is 0 for entries that lack one. */
export function formatMtime(mtime: number): string {
  if (!mtime) return "—"
  return new Intl.DateTimeFormat("ko-KR", { dateStyle: "short", timeStyle: "short" }).format(new Date(mtime))
}
