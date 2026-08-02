// Thin fetch wrapper around the Go API.
//
// Errors come back from the server as {error, code}; ApiError preserves both so
// callers can branch on code and still show a useful message.

export class ApiError extends Error {
  // Written out longhand rather than as constructor parameter properties:
  // tsconfig has erasableSyntaxOnly, which bans syntax that emits runtime code.
  status: number
  code?: string

  constructor(message: string, status: number, code?: string) {
    super(message)
    this.name = "ApiError"
    this.status = status
    this.code = code
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`/api${path}`, {
    ...init,
    headers: {
      ...(init?.body ? { "Content-Type": "application/json" } : {}),
      ...init?.headers,
    },
  })

  if (!res.ok) {
    let message = `${res.status} ${res.statusText}`
    let code: string | undefined
    try {
      const body = await res.json()
      if (body?.error) message = body.error
      if (body?.code) code = body.code
    } catch {
      // Non-JSON error body (a proxy error page, say) - keep the status text.
    }
    throw new ApiError(message, res.status, code)
  }

  if (res.status === 204) return undefined as T
  return (await res.json()) as T
}

export const api = {
  get: <T>(path: string) => request<T>(path),
  post: <T>(path: string, body?: unknown) =>
    request<T>(path, { method: "POST", body: body === undefined ? undefined : JSON.stringify(body) }),
  put: <T>(path: string, body?: unknown) =>
    request<T>(path, { method: "PUT", body: body === undefined ? undefined : JSON.stringify(body) }),
  delete: <T>(path: string) => request<T>(path, { method: "DELETE" }),
}

// --- shared types, mirroring the Go structs -------------------------------

export interface Status {
  needsSetup: boolean
  profileCount: number
  vaultCount: number
  /** False when COUCHHUB_SECRET is unset: credentials are shown once, never stored. */
  secretEnabled: boolean
  /** False when COUCHHUB_DOCUMENTS=false: the document browser is switched off. */
  documentsEnabled: boolean
}

export const statusQuery = {
  queryKey: ["status"] as const,
  queryFn: () => api.get<Status>("/status"),
}
