import { useState } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { AlertTriangle, ArrowRight, CheckCircle2, Loader2, Server } from "lucide-react"

import { ProfileSelect } from "@/components/ProfileSelect"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field"
import { Switch } from "@/components/ui/switch"
import { ApiError } from "@/lib/api"
import { profileName, profilesQuery } from "@/lib/profiles"
import { formatCount } from "@/lib/stats"
import { vaultsApi, type FinishMigrationResult, type Migration, type Vault } from "@/lib/vaults"

const STATE_LABEL: Record<string, string> = {
  initializing: "시작 중",
  running: "복사 중",
  pending: "대기 중",
  crashing: "재시도 중",
  error: "오류",
  completed: "복사 완료",
  failed: "실패",
}

function stateLabel(m: Migration): string {
  if (!m.status.exists) return "복제 문서 없음"
  return STATE_LABEL[m.status.state] ?? m.status.state
}

/**
 * Moves a vault's database to another CouchDB.
 *
 * The copy runs inside CouchDB, so this panel starts it, polls it, and only
 * then offers the switch-over: the vault keeps serving from the old server the
 * whole time, and a copy that fails costs nothing but the target database.
 */
export function MigrateVaultCard({ vault }: { vault: Vault }) {
  const [open, setOpen] = useState(false)
  const [target, setTarget] = useState("")
  const [deleteSource, setDeleteSource] = useState(false)
  const [finished, setFinished] = useState<FinishMigrationResult | null>(null)
  const queryClient = useQueryClient()

  const { data: profiles } = useQuery(profilesQuery)

  const { data: migration } = useQuery({
    queryKey: ["vaults", vault.id, "migration"],
    queryFn: () => vaultsApi.migration(vault.id),
    // Poll while CouchDB is still working; stop once there is nothing moving.
    refetchInterval: (query) => (query.state.data && !query.state.data.ready ? 3_000 : false),
  })

  const refresh = async () => {
    await queryClient.invalidateQueries({ queryKey: ["vaults"] })
    await queryClient.invalidateQueries({ queryKey: ["profiles"] })
  }

  const start = useMutation({
    mutationFn: () => vaultsApi.startMigration(vault.id, { targetProfileId: target, deleteSource }),
    onSuccess: async (data) => {
      setOpen(false)
      queryClient.setQueryData(["vaults", vault.id, "migration"], data)
      await refresh()
    },
  })

  const finish = useMutation({
    mutationFn: () => vaultsApi.finishMigration(vault.id),
    onSuccess: async (data) => {
      setFinished(data)
      queryClient.setQueryData(["vaults", vault.id, "migration"], null)
      await refresh()
    },
  })

  const cancel = useMutation({
    mutationFn: () => vaultsApi.cancelMigration(vault.id),
    onSuccess: async () => {
      queryClient.setQueryData(["vaults", vault.id, "migration"], null)
      await refresh()
    },
  })

  const error = [start.error, finish.error, cancel.error].find(Boolean)
  const others = (profiles ?? []).filter((p) => p.id !== vault.profileId)

  return (
    <>
      <Card>
        <CardHeader>
          <CardTitle role="heading" aria-level={2}>
            CouchDB 변경
          </CardTitle>
          <CardDescription>
            현재 <span className="font-medium">{profileName(profiles, vault.profileId)}</span>에 있습니다. 다른 서버로
            데이터를 복사한 뒤 전환합니다. 복사가 끝날 때까지 기존 서버가 계속 동기화를 처리합니다.
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

          {migration ? (
            <MigrationProgress
              migration={migration}
              onFinish={() => finish.mutate()}
              finishing={finish.isPending}
              onCancel={() => cancel.mutate()}
              cancelling={cancel.isPending}
            />
          ) : others.length === 0 ? (
            <p className="text-muted-foreground text-sm">
              등록된 CouchDB가 하나뿐입니다. CouchDB 관리에서 옮길 서버를 먼저 추가하세요.
            </p>
          ) : (
            <Button
              onClick={() => {
                setTarget("")
                setDeleteSource(false)
                start.reset()
                setOpen(true)
              }}
            >
              <Server aria-hidden /> CouchDB 변경
            </Button>
          )}
        </CardContent>
      </Card>

      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="max-h-[90svh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>{vault.name} 옮기기</DialogTitle>
            <DialogDescription>
              대상 서버에 같은 이름의 데이터베이스와 전용 계정을 만들고, CouchDB 복제로 문서를 복사합니다.
            </DialogDescription>
          </DialogHeader>

          <Field>
            <FieldLabel htmlFor="migrate-target">대상 CouchDB</FieldLabel>
            <ProfileSelect
              id="migrate-target"
              value={target}
              onChange={setTarget}
              profiles={profiles}
              exclude={[vault.profileId]}
            />
            <FieldDescription>대상 서버에 데이터베이스 {vault.dbName}가 이미 있으면 시작하지 않습니다.</FieldDescription>
          </Field>

          <Field orientation="horizontal">
            <div className="space-y-0.5">
              <div className="text-sm font-medium">전환 후 원본 삭제</div>
              <div className="text-muted-foreground text-xs">
                {vault.adopted
                  ? "원본 서버에서 CouchHub가 추가한 계정만 제거합니다. 데이터베이스는 그대로 남습니다."
                  : "원본 데이터베이스와 계정을 삭제합니다. 되돌릴 수 없습니다."}
              </div>
            </div>
            <Switch checked={deleteSource} onCheckedChange={setDeleteSource} aria-label="전환 후 원본 삭제" />
          </Field>

          <Alert>
            <AlertTriangle aria-hidden />
            <AlertTitle>복사 중에는 원본이 계속 쓰입니다</AlertTitle>
            <AlertDescription>
              복사가 시작된 뒤에 저장된 노트는 옮겨지지 않습니다. 전환 직전에 클라이언트 동기화를 멈추거나, 전환 후
              한 번 더 확인하세요. 전환하면 Setup URI 주소가 바뀔 수 있어 기기마다 다시 발급해야 합니다.
            </AlertDescription>
          </Alert>

          {start.isError ? (
            <Alert variant="destructive">
              <AlertTriangle aria-hidden />
              <AlertTitle>시작 실패</AlertTitle>
              <AlertDescription>
                {start.error instanceof ApiError ? start.error.message : String(start.error)}
              </AlertDescription>
            </Alert>
          ) : null}

          <DialogFooter>
            <Button variant="outline" onClick={() => setOpen(false)} disabled={start.isPending}>
              취소
            </Button>
            <Button onClick={() => start.mutate()} disabled={start.isPending || target === ""}>
              {start.isPending ? <Loader2 className="animate-spin" aria-hidden /> : null}
              복사 시작
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={finished !== null} onOpenChange={(o) => !o && setFinished(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>전환 완료</DialogTitle>
            <DialogDescription>{vault.name}이 새 CouchDB에서 동작합니다.</DialogDescription>
          </DialogHeader>

          {finished?.setupUriChanged ? (
            <Alert variant="destructive">
              <AlertTriangle aria-hidden />
              <AlertTitle>Setup URI를 다시 발급하세요</AlertTitle>
              <AlertDescription>
                서버 주소가 바뀌었습니다. 연결 탭에서 Setup URI를 새로 발급해 기기마다 적용하기 전까지 동기화되지
                않습니다.
              </AlertDescription>
            </Alert>
          ) : (
            <Alert>
              <CheckCircle2 aria-hidden />
              <AlertTitle>클라이언트 설정은 그대로입니다</AlertTitle>
              <AlertDescription>두 서버의 Obsidian 연동 주소가 같아 Setup URI가 바뀌지 않았습니다.</AlertDescription>
            </Alert>
          )}

          {finished?.sourceError ? (
            <Alert variant="destructive">
              <AlertTriangle aria-hidden />
              <AlertTitle>원본 정리에 실패했습니다</AlertTitle>
              <AlertDescription>{finished.sourceError}</AlertDescription>
            </Alert>
          ) : null}

          <DialogFooter>
            <Button onClick={() => setFinished(null)}>닫기</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}

function MigrationProgress({
  migration,
  onFinish,
  finishing,
  onCancel,
  cancelling,
}: {
  migration: Migration
  onFinish: () => void
  finishing: boolean
  onCancel: () => void
  cancelling: boolean
}) {
  const total = migration.sourceDocCount
  const written = migration.status.docsWritten
  // A percentage is only honest when the total is known and the copy has not
  // already been declared complete.
  const percent = migration.ready ? 100 : total > 0 ? Math.min(99, Math.round((written / total) * 100)) : null

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2 text-sm">
        <span className="font-medium">{migration.sourceName}</span>
        <ArrowRight className="size-4 shrink-0" aria-hidden />
        <span className="font-medium">{migration.targetName}</span>
        <span className="text-muted-foreground font-mono text-xs">{migration.dbName}</span>
      </div>

      <div className="space-y-1">
        <div className="flex items-center justify-between text-xs">
          <span>{stateLabel(migration)}</span>
          <span className="text-muted-foreground">
            문서 {formatCount(written)}
            {total > 0 ? ` / ${formatCount(total)}` : ""}
            {percent !== null ? ` · ${percent}%` : ""}
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
            aria-label="복사 진행률"
          />
        </div>
      </div>

      {migration.status.error ? (
        <Alert variant="destructive">
          <AlertTriangle aria-hidden />
          <AlertTitle>CouchDB가 복사를 끝내지 못했습니다</AlertTitle>
          <AlertDescription>
            {migration.status.error}
            <br />
            대상 서버가 원본 주소에 접근할 수 있는지 확인하세요. 복제는 대상 CouchDB가 직접 수행합니다.
          </AlertDescription>
        </Alert>
      ) : null}

      {migration.ready ? (
        <Alert>
          <CheckCircle2 aria-hidden />
          <AlertTitle>복사가 끝났습니다</AlertTitle>
          <AlertDescription>
            전환하면 이 Vault가 {migration.targetName}를 바라봅니다.
            {migration.setupUriChanged ? " 주소가 달라 Setup URI를 다시 발급해야 합니다." : ""}
          </AlertDescription>
        </Alert>
      ) : null}

      <div className="flex flex-wrap gap-2">
        <Button onClick={onFinish} disabled={!migration.ready || finishing}>
          {finishing ? <Loader2 className="animate-spin" aria-hidden /> : null}
          전환하기
        </Button>
        <Button variant="outline" onClick={onCancel} disabled={cancelling}>
          {cancelling ? <Loader2 className="animate-spin" aria-hidden /> : null}
          이동 취소
        </Button>
      </div>
    </div>
  )
}
