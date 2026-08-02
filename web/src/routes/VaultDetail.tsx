import { useState } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useNavigate, useParams, useSearchParams } from "react-router"
import {
  AlertTriangle,
  ChartLine,
  CheckCircle2,
  FileText,
  Loader2,
  QrCode,
  Settings2,
  Trash2,
  Wrench,
} from "lucide-react"

import { ClientSetupPanel } from "@/components/ClientSetupPanel"
import { ProfileSelect } from "@/components/ProfileSelect"
import { MigrateVaultCard } from "@/components/MigrateVaultCard"
import { PageHeader } from "@/components/PageHeader"
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
import { profilesQuery } from "@/lib/profiles"
import { formatBytes, formatCount, formatDate, statsApi } from "@/lib/stats"
import { vaultsApi } from "@/lib/vaults"

const TABS = ["stats", "documents", "clients", "manage"] as const
type Tab = (typeof TABS)[number]

export default function VaultDetail() {
  const { id = "" } = useParams()
  const navigate = useNavigate()
  const queryClient = useQueryClient()

  // The tab lives in the URL so a reload, a back button or a shared link all
  // land where they were rather than resetting to the first panel.
  const { data: status } = useQuery(statusQuery)
  const { data: profiles } = useQuery(profilesQuery)
  const documentsEnabled = status?.documentsEnabled ?? true

  const [searchParams, setSearchParams] = useSearchParams()
  const requested = searchParams.get("tab") as Tab | null
  let tab: Tab = requested && TABS.includes(requested) ? requested : "stats"
  // A link to the documents tab must not land on an empty panel when the
  // browser is switched off.
  if (tab === "documents" && !documentsEnabled) tab = "stats"

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

  const [metadataFrom, setMetadataFrom] = useState("")

  const repair = useMutation({
    mutationFn: () => vaultsApi.repair(id, metadataFrom),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["vaults", id] }),
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
                기기 하나를 붙일 때마다 코드를 발급합니다. QR만으로는 열리지 않고 PIN 6자리가 함께 필요하며, 5분이
                지나면 서버가 PIN을 교체해 다음 발급부터는 다른 번호를 씁니다.
              </CardDescription>
            </CardHeader>
            <CardContent>
              <ClientSetupPanel vault={vault} />
            </CardContent>
          </Card>
        </TabsContent>

        <TabsContent value="manage" className="space-y-4 pt-4">
          <Card>
            <CardHeader>
              <CardTitle role="heading" aria-level={2}>
                CouchDB 계정 복구
              </CardTitle>
              <CardDescription>
                저장된 자격증명을 CouchDB에 다시 적용합니다. 계정 <span className="font-mono">{vault.couchUser}</span>이
                있는데도 클라이언트나 복제가 인증에 실패할 때 쓰세요. 데이터베이스는 만들지도, 비우지도 않습니다.
              </CardDescription>
            </CardHeader>
            <CardContent className="space-y-3">
              {(profiles?.length ?? 0) > 1 ? (
                <Field>
                  <FieldLabel htmlFor="repair-metadata">livesync 메타데이터 가져오기 (선택)</FieldLabel>
                  <ProfileSelect
                    id="repair-metadata"
                    value={metadataFrom}
                    onChange={setMetadataFrom}
                    profiles={profiles}
                    exclude={[vault.profileId]}
                    placeholder="가져오지 않음"
                  />
                  <FieldDescription>
                    Obsidian이 &quot;Could not retrieve remote milestone&quot;을 띄우면, 예전에 이 Vault가 있던 서버를
                    고르세요. 복제는 <span className="font-mono">_local</span> 문서를 옮기지 않아 milestone과 암호화
                    파라미터가 남아 있습니다.
                  </FieldDescription>
                </Field>
              ) : null}

              <Button variant="outline" onClick={() => repair.mutate()} disabled={repair.isPending}>
                {repair.isPending ? <Loader2 className="animate-spin" aria-hidden /> : <Wrench aria-hidden />}
                계정 복구
              </Button>

              {repair.isSuccess ? (
                <Alert>
                  <CheckCircle2 aria-hidden />
                  <AlertTitle>적용했습니다</AlertTitle>
                  <AlertDescription>
                    계정 비밀번호와 데이터베이스 권한을 저장된 값으로 다시 맞췄습니다.
                    {repair.data?.metadataCopied?.length ? (
                      <ul className="mt-1 list-disc space-y-0.5 pl-4 font-mono text-xs">
                        {repair.data.metadataCopied.map((d) => (
                          <li key={d}>{d}</li>
                        ))}
                      </ul>
                    ) : null}
                  </AlertDescription>
                </Alert>
              ) : null}

              {repair.isError ? (
                <Alert variant="destructive">
                  <AlertTriangle aria-hidden />
                  <AlertTitle>복구 실패</AlertTitle>
                  <AlertDescription>
                    {repair.error instanceof ApiError ? repair.error.message : String(repair.error)}
                  </AlertDescription>
                </Alert>
              ) : null}
            </CardContent>
          </Card>

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
