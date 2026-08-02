import { useEffect, useState } from "react"
import { useMutation, useQueryClient } from "@tanstack/react-query"
import { AlertTriangle, KeyRound, Loader2, Timer } from "lucide-react"

import { SetupCredentials } from "@/components/SetupCredentials"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { ApiError } from "@/lib/api"
import { vaultsApi, type Vault, type VaultWithCredentials } from "@/lib/vaults"

function mmss(seconds: number): string {
  const m = Math.floor(seconds / 60)
  const s = seconds % 60
  return `${m}:${String(s).padStart(2, "0")}`
}

/**
 * Issues a client code and counts it down, in place on the page.
 *
 * Each issue mints a new PIN, so the previous code stops working the moment
 * this one appears - one code, one device. The countdown runs to the expiry the
 * server sent back rather than to a deadline of its own, and when it reaches
 * zero the code is burned by issuing another one and throwing it away, so what
 * was on screen is dead immediately rather than at the next sweep.
 */
export function ClientSetupPanel({ vault }: { vault: Vault }) {
  const [issued, setIssued] = useState<VaultWithCredentials | null>(null)
  const [remaining, setRemaining] = useState(0)
  const [expired, setExpired] = useState(false)
  const queryClient = useQueryClient()

  const refreshVault = () => queryClient.invalidateQueries({ queryKey: ["vaults", vault.id] })

  const issue = useMutation({
    mutationFn: () => vaultsApi.reissue(vault.id),
    onSuccess: async (data) => {
      setIssued(data)
      setExpired(false)
      await refreshVault()
    },
  })

  // Burning is the same call with the result discarded: a fresh PIN nobody has
  // seen is exactly an invalidated one.
  const burn = useMutation({
    mutationFn: () => vaultsApi.reissue(vault.id),
    onSuccess: refreshVault,
  })

  const expiresAt = issued?.vault.setupPinExpiresAt

  useEffect(() => {
    if (!expiresAt) return
    const deadline = new Date(expiresAt).getTime()

    const tick = () => {
      const left = Math.max(0, Math.round((deadline - Date.now()) / 1000))
      setRemaining(left)
      if (left === 0) {
        setIssued(null)
        setExpired(true)
      }
    }
    tick()
    const timer = setInterval(tick, 1000)
    return () => clearInterval(timer)
  }, [expiresAt])

  // Separate from the countdown so the burn fires once, not on every tick.
  useEffect(() => {
    if (expired) burn.mutate()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [expired])

  function hide() {
    setIssued(null)
    setExpired(false)
    burn.mutate()
  }

  if (!vault.secretsPersisted) {
    return (
      <Alert variant="destructive">
        <AlertTriangle aria-hidden />
        <AlertTitle>재발급할 수 없습니다</AlertTitle>
        <AlertDescription>
          이 Vault는 COUCHHUB_SECRET 없이 생성되어 자격증명이 저장되지 않았습니다. 다른 기기를 추가하려면 Vault를 다시
          만들어야 합니다.
        </AlertDescription>
      </Alert>
    )
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-3">
        <Button onClick={() => issue.mutate()} disabled={issue.isPending}>
          {issue.isPending ? <Loader2 className="animate-spin" aria-hidden /> : <KeyRound aria-hidden />}
          {issued ? "새 코드 발급" : "일회용 코드 발급"}
        </Button>

        {issued ? (
          <>
            <span
              className="text-muted-foreground flex items-center gap-1.5 text-sm tabular-nums"
              aria-live="polite"
              aria-label="남은 시간"
            >
              <Timer className="size-4" aria-hidden />
              {mmss(remaining)} 뒤 만료
            </span>
            <Button variant="outline" size="sm" onClick={hide}>
              지금 만료
            </Button>
          </>
        ) : null}
      </div>

      {expired ? (
        <Alert>
          <Timer aria-hidden />
          <AlertTitle>코드가 만료되었습니다</AlertTitle>
          <AlertDescription>
            PIN이 교체되었습니다. 기기를 추가하려면 새로 발급하세요. QR과 PIN이 함께 촬영된 경우에는 그 조합이 계속
            동작하므로, 그때는 Vault를 새로 만들거나 CouchDB 계정 비밀번호를 바꿔야 합니다.
          </AlertDescription>
        </Alert>
      ) : null}

      {issue.isError ? (
        <Alert variant="destructive">
          <AlertTriangle aria-hidden />
          <AlertTitle>발급 실패</AlertTitle>
          <AlertDescription>
            {issue.error instanceof ApiError ? issue.error.message : String(issue.error)}
          </AlertDescription>
        </Alert>
      ) : null}

      {issued ? <SetupCredentials credentials={issued.credentials} persisted /> : null}
    </div>
  )
}
