import { z } from "zod"

import { api } from "@/lib/api"

export type ZoneDirection = "pull" | "push" | "both"

export interface Zone {
  id: string
  name: string
  peerUrl: string
  direction: ZoneDirection
  vaultIds?: string[]
  lastSyncAt?: string
  lastSyncError?: string
  createdAt: string
  updatedAt: string
}

export interface SchedulerDoc {
  doc_id: string
  state: string
  error_count: number
  last_updated: string
  start_time: string
}

export interface SyncResult {
  zone: Zone
  replications: number
  skipped?: string[]
  states?: SchedulerDoc[] | null
}

export const createZoneSchema = z.object({
  name: z.string().trim().min(1, "존 이름을 입력하세요"),
  peerUrl: z
    .string()
    .trim()
    .refine((v) => /^https?:\/\/.+/i.test(v), "http:// 또는 https:// 로 시작해야 합니다"),
  direction: z.enum(["pull", "push", "both"]),
  /** Empty means "generate one and show it once". */
  token: z.string().trim(),
})

export type CreateZoneValues = z.infer<typeof createZoneSchema>

export const zonesApi = {
  list: () => api.get<Zone[]>("/zones"),
  create: (values: CreateZoneValues) => api.post<{ zone: Zone; token: string }>("/zones", values),
  remove: (id: string) => api.delete<void>(`/zones/${id}`),
  sync: (id: string) => api.post<SyncResult>(`/zones/${id}/sync`),
}

export const zonesQuery = {
  queryKey: ["zones"] as const,
  queryFn: zonesApi.list,
}

export const DIRECTION_LABEL: Record<ZoneDirection, string> = {
  both: "양방향",
  pull: "가져오기만",
  push: "보내기만",
}
