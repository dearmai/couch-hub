import { useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { AlertTriangle, FileText, Loader2 } from "lucide-react"

import { DocumentViewer } from "@/components/DocumentViewer"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Skeleton } from "@/components/ui/skeleton"
import { ApiError } from "@/lib/api"
import { documentsApi, formatMtime, type VaultDocument } from "@/lib/documents"
import { formatBytes } from "@/lib/stats"

/**
 * Lists a vault's notes and opens one to compare its markdown before and after
 * rendering.
 *
 * Reading a note means reassembling it from its chunks and decrypting each one,
 * so the list is deliberately capped and the content is fetched only when a
 * document is opened.
 */
export function VaultDocuments({ vaultId }: { vaultId: string }) {
  const [openDoc, setOpenDoc] = useState<VaultDocument | null>(null)

  const {
    data: documents,
    isPending,
    isError,
    error,
  } = useQuery({
    queryKey: ["vaults", vaultId, "documents"],
    queryFn: () => documentsApi.list(vaultId),
    // Vault content is decrypted server-side; do not keep it around longer than
    // the screen that asked for it.
    gcTime: 0,
    staleTime: 0,
  })

  const content = useQuery({
    queryKey: ["vaults", vaultId, "documents", openDoc?.id],
    queryFn: () => documentsApi.get(vaultId, openDoc!.id),
    enabled: openDoc !== null,
    gcTime: 0,
    staleTime: 0,
  })

  if (isPending) return <Skeleton className="h-32 w-full" />

  if (isError) {
    return (
      <Alert variant="destructive">
        <AlertTriangle aria-hidden />
        <AlertTitle>문서를 불러올 수 없습니다</AlertTitle>
        <AlertDescription>{error instanceof ApiError ? error.message : String(error)}</AlertDescription>
      </Alert>
    )
  }

  const docs = documents ?? []
  if (docs.length === 0) {
    return <p className="text-muted-foreground text-sm">이 Vault에는 아직 문서가 없습니다.</p>
  }

  return (
    <>
      <ul className="divide-y rounded-md border">
        {docs.map((d) => (
          <li key={d.id}>
            <button
              type="button"
              onClick={() => setOpenDoc(d)}
              className="hover:bg-accent/50 flex w-full items-center justify-between gap-3 p-3 text-left transition-colors"
            >
              <div className="flex min-w-0 items-center gap-2">
                <FileText className="text-muted-foreground size-4 shrink-0" aria-hidden />
                <div className="min-w-0">
                  <div className="truncate text-sm">
                    {d.path || <span className="text-muted-foreground font-mono text-xs">{d.id}</span>}
                  </div>
                  {d.pathError ? (
                    <div className="text-destructive truncate text-xs">경로 복호화 실패: {d.pathError}</div>
                  ) : null}
                </div>
              </div>
              <div className="text-muted-foreground flex shrink-0 items-center gap-2 text-xs">
                {d.deleted ? (
                  <Badge variant="secondary" className="text-[10px]">
                    삭제됨
                  </Badge>
                ) : null}
                <span>{formatBytes(d.size)}</span>
                <span>{formatMtime(d.mtime)}</span>
              </div>
            </button>
          </li>
        ))}
      </ul>

      <Dialog open={openDoc !== null} onOpenChange={(open) => !open && setOpenDoc(null)}>
        <DialogContent className="max-h-[90svh] overflow-y-auto sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle className="truncate">{openDoc?.path || openDoc?.id}</DialogTitle>
            <DialogDescription>
              청크 {openDoc?.chunks ?? 0}개 · {formatBytes(openDoc?.size ?? 0)}
            </DialogDescription>
          </DialogHeader>

          {content.isPending ? (
            <div className="text-muted-foreground flex items-center gap-2 py-8 text-sm">
              <Loader2 className="size-4 animate-spin" aria-hidden /> 청크를 모아 복호화하는 중…
            </div>
          ) : content.isError ? (
            <Alert variant="destructive">
              <AlertTriangle aria-hidden />
              <AlertTitle>읽을 수 없습니다</AlertTitle>
              <AlertDescription>
                {content.error instanceof ApiError ? content.error.message : String(content.error)}
              </AlertDescription>
            </Alert>
          ) : content.data ? (
            <DocumentViewer content={content.data} />
          ) : null}

          <DialogFooter>
            <Button onClick={() => setOpenDoc(null)}>닫기</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}
