import { z } from "zod"

import { api } from "@/lib/api"
import { httpUrl, type Diagnosis, type StepResult } from "@/lib/setup"

/** One CouchDB server CouchHub manages. */
export interface Profile {
  id: string
  name: string
  /** How CouchHub reaches the server, usually a private address. */
  adminBaseUrl: string
  /** What goes into Setup URIs - the address Obsidian on a phone can reach. */
  publicBaseUrl: string
  adminUser: string
  provisioned: boolean
  /** Where a vault is created when no server is chosen. Exactly one server has it. */
  primary: boolean
  /** Registered vaults; a server holding any of them cannot be removed. */
  vaultCount: number
  createdAt: string
  updatedAt: string
}

export interface ProfileResponse {
  profile: Profile
  steps?: StepResult[]
  diagnosis: Diagnosis
}

export const profileSchema = z.object({
  name: z.string().trim().min(1, "이름을 입력하세요"),
  adminBaseUrl: httpUrl,
  publicBaseUrl: httpUrl,
  adminUser: z.string().trim().min(1, "관리자 계정을 입력하세요"),
  // Empty on edit means "keep the stored password"; the form cannot show it, so
  // an untouched field must not read as clearing it.
  adminPassword: z.string(),
})

export type ProfileValues = z.infer<typeof profileSchema>

export const profilesApi = {
  list: () => api.get<Profile[]>("/profiles"),
  create: (values: ProfileValues) => api.post<ProfileResponse>("/profiles", values),
  update: (id: string, values: ProfileValues) => api.put<ProfileResponse>(`/profiles/${id}`, values),
  remove: (id: string) => api.delete<void>(`/profiles/${id}`),
  setPrimary: (id: string) => api.post<Profile[]>(`/profiles/${id}/primary`),
  diagnose: (id: string) => api.post<ProfileResponse>(`/profiles/${id}/diagnose`),
}

export const profilesQuery = {
  queryKey: ["profiles"] as const,
  queryFn: profilesApi.list,
}

/** The server a new vault lands on when the operator does not pick one. */
export function primaryProfile(profiles: Profile[] | undefined): Profile | undefined {
  if (!profiles?.length) return undefined
  return profiles.find((p) => p.primary) ?? profiles[0]
}

export function profileName(profiles: Profile[] | undefined, id: string): string {
  return profiles?.find((p) => p.id === id)?.name ?? id
}

/** How a server is named wherever one is being chosen. */
export function profileLabel(profile: Profile): string {
  return profile.primary ? `${profile.name} · 주 서버` : profile.name
}
