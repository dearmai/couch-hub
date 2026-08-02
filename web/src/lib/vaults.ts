import { z } from "zod"

import { api } from "@/lib/api"

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
  qrError?: string

  /**
   * livesync's `?settingsQR=` form: unencrypted, so the client reads the
   * passphrase straight from it and asks for nothing. Anyone who sees the code
   * gets the same access.
   */
  plainSetupUri: string
  plainQrSvg: string
  plainQrError?: string
}

export interface VaultWithCredentials {
  vault: Vault
  credentials: Credentials
  secretsPersisted: boolean
}

export const createVaultSchema = z.object({
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

export const vaultsApi = {
  databases: () => api.get<DatabaseCandidate[]>("/couch/databases"),
  adopt: (values: AdoptVaultValues) => api.post<VaultWithCredentials>("/vaults/adopt", values),
  list: () => api.get<Vault[]>("/vaults"),
  get: (id: string) => api.get<Vault>(`/vaults/${id}`),
  create: (values: CreateVaultValues) => api.post<VaultWithCredentials>("/vaults", values),
  reissue: (id: string, rotatePin: boolean) =>
    api.post<VaultWithCredentials>(`/vaults/${id}/setup-uri`, { rotatePin }),
  remove: (id: string, confirm: string, keepData = false) =>
    api.delete<void>(
      `/vaults/${id}?confirm=${encodeURIComponent(confirm)}${keepData ? "&keepData=true" : ""}`,
    ),
}

export const vaultsQuery = {
  queryKey: ["vaults"] as const,
  queryFn: vaultsApi.list,
}
