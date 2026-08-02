import { useState } from "react"
import { useForm } from "react-hook-form"
import { zodResolver } from "@hookform/resolvers/zod"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { AlertTriangle, Loader2, Plus, RefreshCw, Share2, Trash2 } from "lucide-react"

import { CopyField } from "@/components/CopyField"
import { PageHeader } from "@/components/PageHeader"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Skeleton } from "@/components/ui/skeleton"
import { ApiError } from "@/lib/api"
import { formatDate } from "@/lib/stats"
import {
  createZoneSchema,
  DIRECTION_LABEL,
  zonesApi,
  zonesQuery,
  type CreateZoneValues,
  type SyncResult,
  type Zone,
} from "@/lib/zones"

export default function Zones() {
  const [createOpen, setCreateOpen] = useState(false)
  const [issuedToken, setIssuedToken] = useState<string | null>(null)
  const [syncResult, setSyncResult] = useState<SyncResult | null>(null)
  const queryClient = useQueryClient()

  const { data: zones, isPending } = useQuery(zonesQuery)

  const form = useForm<CreateZoneValues>({
    resolver: zodResolver(createZoneSchema),
    defaultValues: { name: "", peerUrl: "", direction: "both", token: "" },
  })

  const create = useMutation({
    mutationFn: zonesApi.create,
    onSuccess: async (data) => {
      setCreateOpen(false)
      setIssuedToken(data.token)
      form.reset()
      await queryClient.invalidateQueries({ queryKey: ["zones"] })
    },
  })

  const sync = useMutation({
    mutationFn: zonesApi.sync,
    onSuccess: async (data) => {
      setSyncResult(data)
      await queryClient.invalidateQueries({ queryKey: ["zones"] })
    },
  })

  const remove = useMutation({
    mutationFn: zonesApi.remove,
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["zones"] }),
  })

  return (
    <>
      <PageHeader
        title="존 동기화"
        description="다른 CouchHub와 같은 이름의 Vault를 CouchDB 복제로 연결합니다."
        action={
          <Button onClick={() => setCreateOpen(true)}>
            <Plus aria-hidden /> 존 추가
          </Button>
        }
      />

      {sync.isError ? (
        <Alert variant="destructive" className="mb-4">
          <AlertTriangle aria-hidden />
          <AlertTitle>동기화 실패</AlertTitle>
          <AlertDescription>
            {sync.error instanceof ApiError ? sync.error.message : String(sync.error)}
          </AlertDescription>
        </Alert>
      ) : null}

      {isPending ? (
        <Skeleton className="h-24 w-full" />
      ) : !zones?.length ? (
        <Card>
          <CardContent className="flex flex-col items-center gap-3 py-10 text-center">
            <Share2 className="text-muted-foreground size-8" aria-hidden />
            <p className="text-muted-foreground max-w-md text-sm">
              존을 만들면 상대 CouchHub의 Vault 목록을 가져와, 같은 데이터베이스 이름을 가진 Vault끼리 CouchDB 복제를
              겁니다. 양쪽 모두에 같은 토큰으로 존을 만들어야 합니다.
            </p>
            <Button onClick={() => setCreateOpen(true)}>
              <Plus aria-hidden /> 첫 존 만들기
            </Button>
          </CardContent>
        </Card>
      ) : (
        <ul className="space-y-2">
          {zones.map((z) => (
            <ZoneRow
              key={z.id}
              zone={z}
              syncing={sync.isPending && sync.variables === z.id}
              onSync={() => sync.mutate(z.id)}
              onRemove={() => remove.mutate(z.id)}
              removing={remove.isPending && remove.variables === z.id}
            />
          ))}
        </ul>
      )}

      <Dialog open={createOpen} onOpenChange={setCreateOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>존 추가</DialogTitle>
            <DialogDescription>
              상대 CouchHub 주소와 공유 토큰을 입력합니다. 토큰을 비워두면 새로 만들어서 한 번 보여줍니다.
            </DialogDescription>
          </DialogHeader>

          <form id="create-zone" onSubmit={form.handleSubmit((v) => create.mutate(v))}>
            <FieldGroup>
              <Field>
                <FieldLabel htmlFor="zone-name">존 이름</FieldLabel>
                <Input id="zone-name" autoComplete="off" placeholder="집 ↔ 사무실" {...form.register("name")} />
                <FieldError errors={[form.formState.errors.name].filter(Boolean)} />
              </Field>

              <Field>
                <FieldLabel htmlFor="zone-peer">상대 CouchHub 주소</FieldLabel>
                <Input
                  id="zone-peer"
                  autoComplete="off"
                  placeholder="https://hub.example.com"
                  {...form.register("peerUrl")}
                />
                <FieldDescription>이 서버에서 접근 가능한 주소여야 합니다.</FieldDescription>
                <FieldError errors={[form.formState.errors.peerUrl].filter(Boolean)} />
              </Field>

              <Field>
                <FieldLabel htmlFor="zone-direction">방향</FieldLabel>
                <Select
                  value={form.watch("direction")}
                  onValueChange={(v) => form.setValue("direction", (v ?? "both") as CreateZoneValues["direction"])}
                >
                  <SelectTrigger id="zone-direction">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="both">양방향</SelectItem>
                    <SelectItem value="pull">가져오기만</SelectItem>
                    <SelectItem value="push">보내기만</SelectItem>
                  </SelectContent>
                </Select>
              </Field>

              <Field>
                <FieldLabel htmlFor="zone-token">공유 토큰 (선택)</FieldLabel>
                <Input id="zone-token" autoComplete="off" placeholder="비우면 새로 생성" {...form.register("token")} />
                <FieldDescription>양쪽 CouchHub가 같은 토큰을 가져야 합니다.</FieldDescription>
              </Field>
            </FieldGroup>

            {create.isError ? (
              <Alert variant="destructive" className="mt-4">
                <AlertTriangle aria-hidden />
                <AlertTitle>생성 실패</AlertTitle>
                <AlertDescription>
                  {create.error instanceof ApiError ? create.error.message : String(create.error)}
                </AlertDescription>
              </Alert>
            ) : null}
          </form>

          <DialogFooter>
            <Button variant="outline" onClick={() => setCreateOpen(false)} disabled={create.isPending}>
              취소
            </Button>
            <Button type="submit" form="create-zone" disabled={create.isPending}>
              {create.isPending ? <Loader2 className="animate-spin" aria-hidden /> : null}
              만들기
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={issuedToken !== null} onOpenChange={(open) => !open && setIssuedToken(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>존 토큰</DialogTitle>
            <DialogDescription>상대 CouchHub에 같은 토큰으로 존을 만드세요. 다시 볼 수 없습니다.</DialogDescription>
          </DialogHeader>
          {issuedToken ? <CopyField label="존 토큰" value={issuedToken} secret /> : null}
          <Alert variant="destructive">
            <AlertTriangle aria-hidden />
            <AlertTitle>이 토큰은 모든 Vault 자격증명을 내줍니다</AlertTitle>
            <AlertDescription>
              토큰을 가진 쪽은 이 서버의 Vault 목록과 각 Vault의 CouchDB 계정을 받아갑니다. 반드시 HTTPS로 노출하세요.
            </AlertDescription>
          </Alert>
          <DialogFooter>
            <Button onClick={() => setIssuedToken(null)}>닫기</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={syncResult !== null} onOpenChange={(open) => !open && setSyncResult(null)}>
        <DialogContent className="max-h-[90svh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>동기화 결과</DialogTitle>
            <DialogDescription>복제 문서 {syncResult?.replications ?? 0}개를 적용했습니다.</DialogDescription>
          </DialogHeader>

          {syncResult?.skipped?.length ? (
            <Alert>
              <AlertTriangle aria-hidden />
              <AlertTitle>건너뛴 Vault {syncResult.skipped.length}개</AlertTitle>
              <AlertDescription>
                <ul className="mt-1 list-disc space-y-0.5 pl-4 text-xs">
                  {syncResult.skipped.map((s) => (
                    <li key={s}>{s}</li>
                  ))}
                </ul>
              </AlertDescription>
            </Alert>
          ) : null}

          {syncResult?.states?.length ? (
            <ul className="space-y-1 text-xs">
              {syncResult.states.map((s) => (
                <li key={s.doc_id} className="flex items-center justify-between gap-2">
                  <span className="truncate font-mono">{s.doc_id}</span>
                  <Badge variant={s.state === "running" ? "default" : "secondary"}>{s.state}</Badge>
                </li>
              ))}
            </ul>
          ) : null}

          <DialogFooter>
            <Button onClick={() => setSyncResult(null)}>닫기</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}

function ZoneRow({
  zone,
  onSync,
  syncing,
  onRemove,
  removing,
}: {
  zone: Zone
  onSync: () => void
  syncing: boolean
  onRemove: () => void
  removing: boolean
}) {
  return (
    <li className="rounded-lg border p-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <span className="truncate font-medium">{zone.name}</span>
            <Badge variant="secondary" className="text-[10px]">
              {DIRECTION_LABEL[zone.direction]}
            </Badge>
          </div>
          <div className="text-muted-foreground truncate font-mono text-xs">{zone.peerUrl}</div>
          <div className="text-muted-foreground mt-1 text-xs">
            {zone.lastSyncError ? (
              <span className="text-destructive">마지막 동기화 실패: {zone.lastSyncError}</span>
            ) : zone.lastSyncAt ? (
              `마지막 동기화 ${formatDate(zone.lastSyncAt)}`
            ) : (
              "아직 동기화하지 않음"
            )}
          </div>
        </div>

        <div className="flex shrink-0 gap-2">
          <Button variant="outline" size="sm" onClick={onSync} disabled={syncing}>
            {syncing ? <Loader2 className="animate-spin" aria-hidden /> : <RefreshCw aria-hidden />}
            동기화
          </Button>
          <Button
            variant="ghost"
            size="icon"
            aria-label={`${zone.name} 삭제`}
            onClick={onRemove}
            disabled={removing}
          >
            <Trash2 aria-hidden />
          </Button>
        </div>
      </div>
    </li>
  )
}
