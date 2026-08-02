import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { Link, useNavigate } from "react-router"
import { Database, HardDrive, Loader2, RefreshCw } from "lucide-react"

import { PageHeader } from "@/components/PageHeader"
import { ActivityHeatmap } from "@/components/viz/ActivityHeatmap"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import { dashboardQuery, formatBytes, formatCount, formatDate, statsApi } from "@/lib/stats"

export default function Dashboard() {
  const queryClient = useQueryClient()
  const navigate = useNavigate()

  const { data, isPending } = useQuery({
    ...dashboardQuery,
    // The poller runs on its own schedule; this only keeps an open tab current.
    refetchInterval: 60_000,
  })

  const refresh = useMutation({
    mutationFn: statsApi.refresh,
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["dashboard"] }),
  })

  if (isPending) {
    return (
      <div className="space-y-4">
        <Skeleton className="h-8 w-40" />
        <Skeleton className="h-24 w-full" />
        <Skeleton className="h-40 w-full" />
      </div>
    )
  }

  const totals = data?.totals
  const reclaimable = totals ? Math.max(0, totals.sizeFile - totals.sizeActive) : 0

  return (
    <>
      <PageHeader
        title="대시보드"
        description="Vault별 용량과 쓰기 활동"
        action={
          <Button variant="outline" onClick={() => refresh.mutate()} disabled={refresh.isPending}>
            {refresh.isPending ? <Loader2 className="animate-spin" aria-hidden /> : <RefreshCw aria-hidden />}
            새로고침
          </Button>
        }
      />

      {data?.stale ? (
        <Alert className="mb-4">
          <Database aria-hidden />
          <AlertTitle>아직 수집된 통계가 없습니다</AlertTitle>
          <AlertDescription>잠시 후 자동 수집되거나, 새로고침으로 지금 가져올 수 있습니다.</AlertDescription>
        </Alert>
      ) : null}

      <div className="mb-4 grid gap-3 sm:grid-cols-3">
        <StatTile label="Vault" value={formatCount(totals?.vaults ?? 0)} />
        <StatTile label="문서" value={formatCount(totals?.documents ?? 0)} />
        <StatTile
          label="디스크 사용"
          value={formatBytes(totals?.sizeFile ?? 0)}
          // The gap between file and active size is what a compaction frees;
          // showing only one of the two hides why disk grows without documents.
          hint={reclaimable > 0 ? `압축 시 ${formatBytes(reclaimable)} 회수 가능` : undefined}
        />
      </div>

      <Card className="mb-4">
        <CardHeader>
          <CardTitle role="heading" aria-level={2}>
            쓰기 활동
          </CardTitle>
          <CardDescription>전체 Vault의 일별 쓰기 횟수</CardDescription>
        </CardHeader>
        <CardContent>
          <ActivityHeatmap activity={data?.activity ?? []} />
        </CardContent>
      </Card>

      {data?.vaults.length ? (
        <ul className="space-y-2">
          {data.vaults.map(({ vault, latest }) => (
            <li key={vault.id}>
              <Link
                to={`/vaults/${vault.id}`}
                className="hover:bg-accent/50 flex items-center justify-between gap-3 rounded-lg border p-4 transition-colors"
              >
                <div className="min-w-0">
                  <div className="truncate font-medium">{vault.name}</div>
                  <div className="text-muted-foreground truncate font-mono text-xs">{vault.dbName}</div>
                </div>
                <div className="shrink-0 text-right text-xs">
                  {latest ? (
                    <>
                      <div className="flex items-center justify-end gap-1 font-medium">
                        <HardDrive className="size-3.5" aria-hidden />
                        {formatBytes(latest.sizeFile)}
                      </div>
                      <div className="text-muted-foreground">
                        문서 {formatCount(latest.docCount)} · {formatDate(latest.at)}
                      </div>
                    </>
                  ) : (
                    <span className="text-muted-foreground">수집 대기</span>
                  )}
                </div>
              </Link>
            </li>
          ))}
        </ul>
      ) : (
        <Card>
          <CardContent className="flex flex-col items-center gap-3 py-10 text-center">
            <Database className="text-muted-foreground size-8" aria-hidden />
            <p className="text-muted-foreground text-sm">아직 Vault가 없습니다.</p>
            <Button onClick={() => navigate("/vaults")}>Vault 만들기</Button>
          </CardContent>
        </Card>
      )}
    </>
  )
}

function StatTile({ label, value, hint }: { label: string; value: string; hint?: string }) {
  return (
    <div className="rounded-lg border p-4">
      <div className="text-muted-foreground text-xs font-medium">{label}</div>
      {/* aria-label so the figure is identifiable on its own - "Vault" also
          appears as a nav item and a heading on this page. */}
      <div className="mt-1 text-2xl font-semibold tabular-nums" aria-label={label}>
        {value}
      </div>
      {hint ? <div className="text-muted-foreground mt-1 text-xs">{hint}</div> : null}
    </div>
  )
}
