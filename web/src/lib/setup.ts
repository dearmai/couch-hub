import { z } from "zod"

import { api } from "@/lib/api"

/** One CouchDB configuration value livesync requires. */
export interface Setting {
  section: string
  key: string
  value: string
  why: string
}

/** A desired setting compared against the live server. */
export interface Check extends Setting {
  current: string
  matches: boolean
  present: boolean
}

export interface Diagnosis {
  version: string
  singleNode: boolean
  nodeCount: number
  checks: Check[]
  missingSystemDbs: string[] | null
  ready: boolean
}

export interface StepResult {
  step: string
  ok: boolean
  skipped: boolean
  error?: string
}

export interface ApplyResponse {
  profileId: string
  steps: StepResult[]
  diagnosis: Diagnosis
}

export interface Profile {
  id: string
  name: string
  adminBaseUrl: string
  publicBaseUrl: string
  adminUser: string
  provisioned: boolean
  createdAt: string
  updatedAt: string
}

const httpUrl = z
  .string()
  .trim()
  .min(1, "필수 항목입니다")
  .refine((v) => /^https?:\/\/.+/i.test(v), "http:// 또는 https:// 로 시작해야 합니다")

export const connectSchema = z.object({
  name: z.string().trim().min(1, "이름을 입력하세요"),
  adminBaseUrl: httpUrl,
  publicBaseUrl: httpUrl,
  adminUser: z.string().trim().min(1, "관리자 계정을 입력하세요"),
  adminPassword: z.string().min(1, "비밀번호를 입력하세요"),
})

export type ConnectValues = z.infer<typeof connectSchema>

export const setupApi = {
  desired: () => api.get<{ settings: Setting[]; systemDatabases: string[] }>("/setup/desired"),
  diagnose: (values: ConnectValues) => api.post<Diagnosis>("/setup/diagnose", values),
  apply: (values: ConnectValues) => api.post<ApplyResponse>("/setup/apply", values),
  profiles: () => api.get<Profile[]>("/profiles"),
}
