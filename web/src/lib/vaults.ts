import { z } from "zod"

import { api, ApiError } from "@/lib/api"

export interface Vault {
  id: string
  profileId: string
  name: string
  dbName: string
  couchUser: string
  /** False for vaults created without COUCHHUB_SECRET; their Setup URI cannot be reissued. */
  secretsPersisted: boolean
  /** True for a database CouchHub did not create; removal defaults to detaching. */
  adopted: boolean
  /** True for a vault whose contents are stored unencrypted. */
  e2eeDisabled: boolean
  /**
   * When the PIN currently on display stops working. The server replaces the
   * PIN at this moment, so a countdown to it is a countdown to the code
   * actually dying rather than to the page hiding it. Absent when no code is
   * outstanding.
   */
  setupPinExpiresAt?: string
  createdAt: string
  updatedAt: string
}

export interface Credentials {
  couchUser: string
  couchPassword: string
  e2eePassphrase: string
  setupPin: string

  /** Encrypted with setupPin; Obsidian prompts for the PIN on import. */
  setupUri: string
  /** Empty when the URI exceeds QR capacity; qrError then explains why. */
  qrSvg: string
  /** Width of the code in modules, quiet zone included. The UI sizes from it. */
  qrModules: number
  qrError?: string

  /**
   * livesync's `?settingsQR=` form: unencrypted, so the client reads the
   * passphrase straight from it and asks for nothing. Anyone who sees the code
   * gets the same access.
   */
  plainSetupUri: string
  plainQrSvg: string
  plainQrModules: number
  plainQrError?: string
}

export interface VaultWithCredentials {
  vault: Vault
  credentials: Credentials
  secretsPersisted: boolean
}

export const createVaultSchema = z.object({
  /** Empty means the primary CouchDB. */
  profileId: z.string(),
  name: z.string().trim().min(1, "Vault 이름을 입력하세요"),
  dbName: z
    .string()
    .trim()
    .refine(
      (v) => v === "" || /^[a-z][a-z0-9_$()+/-]*$/.test(v),
      "소문자로 시작하고 a-z 0-9 _ $ ( ) + - / 만 쓸 수 있습니다",
    ),
})

export type CreateVaultValues = z.infer<typeof createVaultSchema>

/** An existing CouchDB database offered for adoption. */
export interface DatabaseCandidate {
  name: string
  docCount: number
  sizeFile: number
  /** Already managed by a vault. */
  registered: boolean
}

export const adoptVaultSchema = z
  .object({
    /** Which CouchDB the database lives on. Empty means the primary. */
    profileId: z.string(),
    dbName: z.string().trim().min(1, "데이터베이스를 선택하세요"),
    name: z.string().trim(),
    e2eePassphrase: z.string(),
    e2eeDisabled: z.boolean(),
  })
  .refine((v) => v.e2eeDisabled || v.e2eePassphrase.trim().length > 0, {
    // Generating one would make everything already stored unreadable, so it has
    // to come from the operator.
    message: "기존 Vault가 쓰던 패스프레이즈를 입력하세요",
    path: ["e2eePassphrase"],
  })

export type AdoptVaultValues = z.infer<typeof adoptVaultSchema>

/** CouchDB's own view of the copy behind a migration. */
export interface ReplicationStatus {
  docId: string
  exists: boolean
  /** initializing, running, pending, crashing, error, completed, failed. */
  state: string
  error?: string
  docsRead: number
  docsWritten: number
  /** -1 when CouchDB does not report a backlog, which includes finished jobs. */
  changesPending: number
  lastUpdated?: string
  startTime?: string
}

/** A vault being copied to another CouchDB. */
export interface Migration {
  vaultId: string
  sourceProfileId: string
  sourceName: string
  targetProfileId: string
  targetName: string
  dbName: string
  deleteSource: boolean
  startedAt: string
  status: ReplicationStatus
  /** Documents on the source, so progress can be shown against a total. */
  sourceDocCount: number
  /** The copy has finished; only the switch-over is left. */
  ready: boolean
  /** Finishing changes the address clients use. */
  setupUriChanged: boolean
}

export interface RepairResult {
  vault: Vault
  /** _local documents brought over from another server, if any were asked for. */
  metadataCopied?: string[]
}

export interface StartMigrationValues {
  targetProfileId: string
  deleteSource: boolean
}

export interface FinishMigrationResult {
  vault: Vault
  setupUriChanged: boolean
  sourceRemoved: boolean
  /** Cleanup that failed after the vault had already moved. */
  sourceError?: string
}

export const vaultsApi = {
  databases: (profileId?: string) =>
    api.get<DatabaseCandidate[]>(`/couch/databases${profileId ? `?profileId=${encodeURIComponent(profileId)}` : ""}`),
  adopt: (values: AdoptVaultValues) => api.post<VaultWithCredentials>("/vaults/adopt", values),
  list: () => api.get<Vault[]>("/vaults"),
  get: (id: string) => api.get<Vault>(`/vaults/${id}`),
  create: (values: CreateVaultValues) => api.post<VaultWithCredentials>("/vaults", values),
  /** Mints a fresh PIN, which invalidates whatever was issued before it. */
  reissue: (id: string) => api.post<VaultWithCredentials>(`/vaults/${id}/setup-uri`),
  /**
   * Writes the stored CouchDB account back to the server, and optionally
   * carries livesync's _local documents over from another one.
   */
  repair: (id: string, metadataFromProfileId = "") =>
    api.post<RepairResult>(`/vaults/${id}/repair`, { metadataFromProfileId }),
  remove: (id: string, confirm: string, keepData = false) =>
    api.delete<void>(
      `/vaults/${id}?confirm=${encodeURIComponent(confirm)}${keepData ? "&keepData=true" : ""}`,
    ),

  startMigration: (id: string, values: StartMigrationValues) =>
    api.post<Migration>(`/vaults/${id}/migrate`, values),
  /** null when no move is in flight, which is the ordinary answer. */
  migration: async (id: string): Promise<Migration | null> => {
    try {
      return await api.get<Migration>(`/vaults/${id}/migrate`)
    } catch (err) {
      if (err instanceof ApiError && err.status === 404) return null
      throw err
    }
  },
  finishMigration: (id: string) => api.post<FinishMigrationResult>(`/vaults/${id}/migrate/finish`),
  cancelMigration: (id: string) => api.delete<void>(`/vaults/${id}/migrate`),
}

export const vaultsQuery = {
  queryKey: ["vaults"] as const,
  queryFn: vaultsApi.list,
}
