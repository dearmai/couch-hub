import { useEffect, useState } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { AlertTriangle, CheckCircle2, Download, Loader2, Package, RotateCcw, X } from "lucide-react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button, buttonVariants } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { ApiError } from "@/lib/api"
import { exportsApi, isExportActive, type ExportStatus } from "@/lib/exports"
import { formatBytes, formatCount } from "@/lib/stats"
import type { Vault } from "@/lib/vaults"

const STATE_LABEL: Record<ExportStatus["state"], string> = {
  listing: "문서 목록을 읽는 중",
  packing: "복호화해서 압축하는 중",
  ready: "준비 완료",
  failed: "실패",
  canceled: "취소됨",
}

/**
 * Packs a vault's decrypted contents into a zip and hands it over.
 *
 * The work runs on the server, detached from any request: every file is
 * reassembled from its chunks and decrypted, which is minutes rather than
 * seconds on a real vault. This panel starts it, polls it, and only offers the
 * download once the archive is complete - a half-written zip is not a partial
 * backup, it is a file that will not open.
 */
export function ExportVaultCard({ vault, enabled }: { vault: Vault; enabled: boolean }) {
  const queryClient = useQueryClient()
  const key = ["vaults", vault.id, "export"]

  const { data: status } = useQuery({
    queryKey: key,
    queryFn: () => exportsApi.status(vault.id),
    enabled,
    // Once a second while there is something to watch, then never: a finished
    // archive does not change until someone asks for another one.
    refetchInterval: (query) => (isExportActive(query.state.data) ? 1_000 : false),
  })

  const start = useMutation({
    mutationFn: () => exportsApi.start(vault.id),
    onSuccess: (data) => queryClient.setQueryData(key, data),
  })

  const discard = useMutation({
    mutationFn: () => exportsApi.discard(vault.id),
    onSuccess: () => {
      queryClient.setQueryData(key, null)
      start.reset()
    },
  })

  if (!enabled) {
    return (
      <Card>
        <CardHeader>
          <CardTitle role="heading" aria-level={2}>
            Vault 내보내기
          </CardTitle>
          <CardDescription>
            내보내기는 Vault 전체를 서버에서 복호화합니다. 문서 열람과 같은 권한이라 같은 스위치로 함께 꺼집니다.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <p className="text-muted-foreground text-sm">
            <span className="font-mono">COUCHHUB_DOCUMENTS=true</span>로 다시 시작하면 쓸 수 있습니다.
          </p>
        </CardContent>
      </Card>
    )
  }

  const active = isExportActive(status)
  const error = [start.error, discard.error].find(Boolean)

  return (
    <Card>
      <CardHeader>
        <CardTitle role="heading" aria-level={2}>
          Vault 내보내기
        </CardTitle>
        <CardDescription>
          저장된 노트를 청크에서 모아 복호화한 뒤 zip으로 묶습니다. 압축이 끝나야 내려받을 수 있고, 만들어진 파일은 암호화
          없는 Vault 원본이라 30분 뒤 서버에서 지워집니다.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        {error ? (
          <Alert variant="destructive">
            <AlertTriangle aria-hidden />
            <AlertTitle>실패</AlertTitle>
            <AlertDescription>{error instanceof ApiError ? error.message : String(error)}</AlertDescription>
          </Alert>
        ) : null}

        {status ? (
          <ExportProgress
            vaultId={vault.id}
            status={status}
            // The server deletes the archive on its own schedule, so the panel
            // has to stop offering a link that has just stopped working.
            onExpired={() => queryClient.invalidateQueries({ queryKey: key })}
          />
        ) : null}

        <div className="flex flex-wrap gap-2">
          {active ? (
            <Button variant="outline" onClick={() => discard.mutate()} disabled={discard.isPending}>
              {discard.isPending ? <Loader2 className="animate-spin" aria-hidden /> : <X aria-hidden />}
              중단
            </Button>
          ) : (
            <>
              <Button onClick={() => start.mutate()} disabled={start.isPending}>
                {start.isPending ? (
                  <Loader2 className="animate-spin" aria-hidden />
                ) : status ? (
                  <RotateCcw aria-hidden />
                ) : (
                  <Package aria-hidden />
                )}
                {status ? "다시 만들기" : "내보내기"}
              </Button>
              {status ? (
                <Button variant="outline" onClick={() => discard.mutate()} disabled={discard.isPending}>
                  <X aria-hidden /> 서버에서 지우기
                </Button>
              ) : null}
            </>
          )}
        </div>
      </CardContent>
    </Card>
  )
}

function ExportProgress({
  vaultId,
  status,
  onExpired,
}: {
  vaultId: string
  status: ExportStatus
  onExpired: () => void
}) {
  // A percentage is only honest once the listing has produced a total.
  const percent =
    status.state === "ready"
      ? 100
      : status.total > 0
        ? Math.min(99, Math.round((status.done / status.total) * 100))
        : null

  return (
    <div className="space-y-3">
      <div className="space-y-1">
        <div className="flex items-center justify-between gap-2 text-xs">
          <span>{STATE_LABEL[status.state]}</span>
          <span className="text-muted-foreground">
            {status.total > 0 ? `파일 ${formatCount(status.done)} / ${formatCount(status.total)}` : "집계 중"}
            {percent !== null ? ` · ${percent}%` : ""}
            {status.bytes > 0 ? ` · ${formatBytes(status.bytes)}` : ""}
          </span>
        </div>
        <div className="bg-muted h-1.5 w-full overflow-hidden rounded-full">
          <div
            className="bg-primary h-full transition-[width] duration-500"
            style={{ width: `${percent ?? 5}%` }}
            role="progressbar"
            aria-valuenow={percent ?? undefined}
            aria-valuemin={0}
            aria-valuemax={100}
            aria-label="내보내기 진행률"
          />
        </div>
      </div>

      {status.state === "ready" ? (
        <Alert>
          <CheckCircle2 aria-hidden />
          <AlertTitle>내려받을 수 있습니다</AlertTitle>
          <AlertDescription className="space-y-1">
            <div className="font-mono text-xs break-all">{status.filename}</div>
            <div>
              {formatBytes(status.sizeBytes)} · 파일 {formatCount(status.done)}개
              {status.expiresAt ? <Expiry at={status.expiresAt} onExpired={onExpired} /> : null}
            </div>
            {/* An anchor rather than a Button: the archive is a file of
                arbitrary size, and letting the browser stream it to disk beats
                pulling it through fetch into a blob first. */}
            <a
              href={exportsApi.downloadUrl(vaultId)}
              download={status.filename}
              className={`${buttonVariants()} mt-1`}
            >
              <Download aria-hidden /> zip 내려받기
            </a>
          </AlertDescription>
        </Alert>
      ) : null}

      {status.state === "failed" ? (
        <Alert variant="destructive">
          <AlertTriangle aria-hidden />
          <AlertTitle>내보내지 못했습니다</AlertTitle>
          <AlertDescription>{status.error}</AlertDescription>
        </Alert>
      ) : null}

      {status.skipped > 0 ? (
        <details className="text-muted-foreground text-xs">
          <summary className="text-foreground cursor-pointer text-sm">
            {formatCount(status.skipped)}개를 제외했습니다
          </summary>
          <p className="mt-1">
            압축에 들어가지 않은 파일입니다. 여기에는 앞부분만 보여주며, 전체 목록은 zip 안의{" "}
            <span className="font-mono">_couchhub-export.txt</span>에 있습니다.
          </p>
          <ul className="mt-1 max-h-40 space-y-0.5 overflow-y-auto font-mono break-all">
            {(status.problems ?? []).map((p, i) => (
              // Two entries can fail identically - same reason, same empty
              // path - so the index is the only stable key here.
              // eslint-disable-next-line react/no-array-index-key
              <li key={i}>{p}</li>
            ))}
          </ul>
        </details>
      ) : null}
    </div>
  )
}

/** Counts down to the moment the server deletes the archive. */
function Expiry({ at, onExpired }: { at: string; onExpired: () => void }) {
  const [left, setLeft] = useState(() => Math.max(0, new Date(at).getTime() - Date.now()))

  useEffect(() => {
    const deadline = new Date(at).getTime()
    const tick = () => {
      const remaining = Math.max(0, deadline - Date.now())
      setLeft(remaining)
      if (remaining === 0) {
        clearInterval(timer)
        onExpired()
      }
    }
    const timer = setInterval(tick, 1_000)
    tick()
    return () => clearInterval(timer)
    // onExpired is a fresh closure every render; re-arming the timer on each
    // one would reset the countdown a second at a time.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [at])

  const minutes = Math.floor(left / 60_000)
  const seconds = Math.floor((left % 60_000) / 1000)

  return (
    <span>
      {left === 0
        ? " · 보관 기간이 지나 곧 삭제됩니다"
        : ` · ${minutes}분 ${String(seconds).padStart(2, "0")}초 뒤 서버에서 삭제`}
    </span>
  )
}
