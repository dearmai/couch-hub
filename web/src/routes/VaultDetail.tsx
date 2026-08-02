import { useState } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useNavigate, useParams, useSearchParams } from "react-router"
import { AlertTriangle, ChartLine, FileText, KeyRound, Loader2, QrCode, Settings2, Trash2 } from "lucide-react"

import { MigrateVaultCard } from "@/components/MigrateVaultCard"
import { PageHeader } from "@/components/PageHeader"
import { SetupCredentials } from "@/components/SetupCredentials"
import { VaultDocuments } from "@/components/VaultDocuments"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
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
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { Switch } from "@/components/ui/switch"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { ActivityHeatmap } from "@/components/viz/ActivityHeatmap"
import { TrendChart } from "@/components/viz/TrendChart"
import { ApiError, statusQuery } from "@/lib/api"
import { formatBytes, formatCount, formatDate, statsApi } from "@/lib/stats"
import { vaultsApi, type VaultWithCredentials } from "@/lib/vaults"

const TABS = ["stats", "documents", "clients", "manage"] as const
type Tab = (typeof TABS)[number]

export default function VaultDetail() {
  const { id = "" } = useParams()
  const navigate = useNavigate()
  const queryClient = useQueryClient()

  // The tab lives in the URL so a reload, a back button or a shared link all
  // land where they were rather than resetting to the first panel.
  const { data: status } = useQuery(statusQuery)
  const documentsEnabled = status?.documentsEnabled ?? true

  const [searchParams, setSearchParams] = useSearchParams()
  const requested = searchParams.get("tab") as Tab | null
  let tab: Tab = requested && TABS.includes(requested) ? requested : "stats"
  // A link to the documents tab must not land on an empty panel when the
  // browser is switched off.
  if (tab === "documents" && !documentsEnabled) tab = "stats"

  const [issued, setIssued] = useState<VaultWithCredentials | null>(null)
  const [deleteOpen, setDeleteOpen] = useState(false)
  const [confirmName, setConfirmName] = useState("")
  // Adopted databases predate CouchHub, so forgetting one defaults to leaving
  // its documents alone rather than dropping them.
  const [keepData, setKeepData] = useState(false)

  const { data: vault, isPending } = useQuery({
    queryKey: ["vaults", id],
    queryFn: () => vaultsApi.get(id),
  })

  const { data: stats } = useQuery({
    queryKey: ["vaults", id, "stats"],
    queryFn: () => statsApi.vault(id),
    refetchInterval: 60_000,
  })
  const snapshots = stats?.snapshots ?? []

  const reissue = useMutation({
    mutationFn: (rotatePin: boolean) => vaultsApi.reissue(id, rotatePin),
    onSuccess: (data) => setIssued(data),
  })

  const remove = useMutation({
    mutationFn: () => vaultsApi.remove(id, confirmName, keepData),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ["vaults"] })
      await queryClient.invalidateQueries({ queryKey: ["status"] })
      navigate("/vaults")
    },
  })

  if (isPending) return <Skeleton className="h-40 w-full" />
  if (!vault) return <PageHeader title="Vault를 찾을 수 없습니다" />

  return (
    <>
      <PageHeader
        title={vault.name}
        description={vault.dbName}
        action={
          <div className="flex gap-1">
            {vault.adopted ? <Badge variant="secondary">기존 DB</Badge> : null}
            {vault.e2eeDisabled ? <Badge variant="secondary">암호화 없음</Badge> : null}
          </div>
        }
      />

      <Tabs value={tab} onValueChange={(v) => setSearchParams({ tab: v ?? "stats" }, { replace: true })}>
        {/* Scrolls rather than wrapping: four labels do not fit a phone, and a
            wrapped row pushes the panel below the fold. */}
        <TabsList className="w-full overflow-x-auto">
          <TabsTrigger value="stats" className="flex-1">
            <ChartLine aria-hidden /> 현황
          </TabsTrigger>
          {documentsEnabled ? (
            <TabsTrigger value="documents" className="flex-1">
              <FileText aria-hidden /> 문서
            </TabsTrigger>
          ) : null}
          <TabsTrigger value="clients" className="flex-1">
            <QrCode aria-hidden /> 연결
          </TabsTrigger>
          <TabsTrigger value="manage" className="flex-1">
            <Settings2 aria-hidden /> 관리
          </TabsTrigger>
        </TabsList>

        <TabsContent value="stats" className="pt-4">
          <Card>
            <CardHeader>
              <CardTitle role="heading" aria-level={2}>
                현황
              </CardTitle>
              <CardDescription>
                {stats?.latest
                  ? `문서 ${formatCount(stats.latest.docCount)} · 디스크 ${formatBytes(stats.latest.sizeFile)} · ${formatDate(stats.latest.at)}`
                  : "아직 수집된 통계가 없습니다"}
              </CardDescription>
            </CardHeader>
            <CardContent className="space-y-6">
              {snapshots.length > 1 ? (
                <>
                  {/* Two measures on different scales, so two charts - never one
                      chart with two y-axes. */}
                  <section>
                    <h3 className="text-muted-foreground mb-2 text-xs font-medium">용량</h3>
                    <TrendChart
                      snapshots={snapshots}
                      format={formatBytes}
                      series={[
                        { key: "sizeFile", label: "디스크", slot: 1 },
                        { key: "sizeActive", label: "실데이터", slot: 2 },
                      ]}
                    />
                  </section>
                  <section>
                    <h3 className="text-muted-foreground mb-2 text-xs font-medium">문서 수</h3>
                    <TrendChart
                      snapshots={snapshots}
                      format={formatCount}
                      series={[{ key: "docCount", label: "문서", slot: 1 }]}
                    />
                  </section>
                </>
              ) : (
                <p className="text-muted-foreground text-sm">
                  추이를 그리려면 최소 두 번의 수집이 필요합니다. 대시보드에서 새로고침하거나 잠시 기다리세요.
                </p>
              )}

              <section>
                <h3 className="text-muted-foreground mb-2 text-xs font-medium">쓰기 활동</h3>
                <ActivityHeatmap activity={stats?.activity ?? []} />
              </section>
            </CardContent>
          </Card>
        </TabsContent>

        <TabsContent value="documents" className="pt-4" hidden={!documentsEnabled}>
          <Card>
            <CardHeader>
              <CardTitle role="heading" aria-level={2}>
                문서
              </CardTitle>
              <CardDescription>
                저장된 노트를 열어 원문 마크다운과 렌더링 결과를 비교합니다. 청크를 모아 서버에서 복호화합니다.
              </CardDescription>
            </CardHeader>
            <CardContent>
              {/* Mounted only while this tab is open: listing a vault decrypts
                  every path, which is not work to do for a panel nobody opened. */}
              {tab === "documents" ? <VaultDocuments vaultId={vault.id} /> : null}
            </CardContent>
          </Card>
        </TabsContent>

        <TabsContent value="clients" className="pt-4">
          <Card>
            <CardHeader>
              <CardTitle role="heading" aria-level={2}>
                클라이언트 연결
              </CardTitle>
              <CardDescription>
                새 기기를 추가할 때 Setup URI를 다시 발급합니다. PIN을 새로 만들면 이전에 배포한 QR은 더 이상 열리지
                않습니다.
              </CardDescription>
            </CardHeader>
            <CardContent className="space-y-3">
              {!vault.secretsPersisted ? (
                <Alert variant="destructive">
                  <AlertTriangle aria-hidden />
                  <AlertTitle>재발급할 수 없습니다</AlertTitle>
                  <AlertDescription>
                    이 Vault는 COUCHHUB_SECRET 없이 생성되어 자격증명이 저장되지 않았습니다. 다른 기기를 추가하려면
                    Vault를 다시 만들어야 합니다.
                  </AlertDescription>
                </Alert>
              ) : (
                <div className="flex flex-wrap gap-2">
                  <Button onClick={() => reissue.mutate(false)} disabled={reissue.isPending}>
                    {reissue.isPending ? <Loader2 className="animate-spin" aria-hidden /> : <QrCode aria-hidden />}
                    Setup URI 보기
                  </Button>
                  <Button variant="outline" onClick={() => reissue.mutate(true)} disabled={reissue.isPending}>
                    <KeyRound aria-hidden /> PIN 새로 발급
                  </Button>
                </div>
              )}

              {reissue.isError ? (
                <Alert variant="destructive">
                  <AlertTriangle aria-hidden />
                  <AlertTitle>실패</AlertTitle>
                  <AlertDescription>
                    {reissue.error instanceof ApiError ? reissue.error.message : String(reissue.error)}
                  </AlertDescription>
                </Alert>
              ) : null}
            </CardContent>
          </Card>
        </TabsContent>

        <TabsContent value="manage" className="space-y-4 pt-4">
          <MigrateVaultCard vault={vault} />

          <Card>
            <CardHeader>
              <CardTitle role="heading" aria-level={2}>
                Vault 삭제
              </CardTitle>
              <CardDescription>
                {vault.adopted
                  ? "CouchHub가 만들지 않은 데이터베이스입니다. 기본적으로 데이터를 남기고 목록에서만 제외합니다."
                  : "데이터베이스와 전용 계정이 함께 삭제됩니다. 서버에 저장된 노트는 복구할 수 없습니다."}
              </CardDescription>
            </CardHeader>
            <CardContent>
              <Button
                variant="destructive"
                onClick={() => {
                  // Set the default here rather than in onOpenChange: opening the
                  // dialog from this button never calls that handler.
                  setKeepData(vault.adopted)
                  setDeleteOpen(true)
                }}
              >
                <Trash2 aria-hidden /> 삭제
              </Button>
            </CardContent>
          </Card>
        </TabsContent>
      </Tabs>

      <Dialog open={issued !== null} onOpenChange={(open) => !open && setIssued(null)}>
        <DialogContent className="max-h-[90svh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>{vault.name} Setup URI</DialogTitle>
            <DialogDescription>Obsidian에서 불러오면 이 기기가 동기화에 참여합니다.</DialogDescription>
          </DialogHeader>
          {issued ? <SetupCredentials credentials={issued.credentials} persisted /> : null}
          <DialogFooter>
            <Button onClick={() => setIssued(null)}>닫기</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog
        open={deleteOpen}
        onOpenChange={(open) => {
          setDeleteOpen(open)
          if (!open) setConfirmName("")
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{vault.name} 삭제</DialogTitle>
            <DialogDescription>
              {keepData ? (
                <>
                  계정 <span className="font-mono">{vault.couchUser}</span>만 제거하고 CouchHub 목록에서 내립니다.
                  데이터베이스 <span className="font-mono">{vault.dbName}</span>와 그 안의 문서는 그대로 남습니다.
                </>
              ) : (
                <>
                  이 작업은 되돌릴 수 없습니다. CouchDB 데이터베이스 <span className="font-mono">{vault.dbName}</span>와
                  계정 <span className="font-mono">{vault.couchUser}</span>가 삭제됩니다.
                </>
              )}
            </DialogDescription>
          </DialogHeader>

          <Field orientation="horizontal">
            <div className="space-y-0.5">
              <div className="text-sm font-medium">데이터는 남기기</div>
              <div className="text-muted-foreground text-xs">
                데이터베이스를 지우지 않고 CouchHub 관리에서만 제외합니다.
              </div>
            </div>
            <Switch checked={keepData} onCheckedChange={setKeepData} aria-label="데이터는 남기기" />
          </Field>

          <Field>
            <FieldLabel htmlFor="confirm-name">확인을 위해 Vault 이름을 입력하세요</FieldLabel>
            <Input
              id="confirm-name"
              autoComplete="off"
              value={confirmName}
              onChange={(e) => setConfirmName(e.target.value)}
              placeholder={vault.name}
            />
            <FieldDescription>정확히 일치해야 삭제됩니다.</FieldDescription>
          </Field>

          {remove.isError ? (
            <Alert variant="destructive">
              <AlertTriangle aria-hidden />
              <AlertTitle>삭제 실패</AlertTitle>
              <AlertDescription>
                {remove.error instanceof ApiError ? remove.error.message : String(remove.error)}
              </AlertDescription>
            </Alert>
          ) : null}

          <DialogFooter>
            <Button variant="outline" onClick={() => setDeleteOpen(false)} disabled={remove.isPending}>
              취소
            </Button>
            <Button
              variant="destructive"
              onClick={() => remove.mutate()}
              disabled={remove.isPending || confirmName !== vault.name}
            >
              {remove.isPending ? <Loader2 className="animate-spin" aria-hidden /> : <Trash2 aria-hidden />}
              {keepData ? "목록에서 제거" : "영구 삭제"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}
