import { lazy, Suspense } from "react"
import { Navigate, Route, Routes, useLocation } from "react-router"
import { useQuery } from "@tanstack/react-query"
import { Toaster } from "@/components/ui/sonner"

import { AppShell } from "@/components/AppShell"
import { statusQuery } from "@/lib/api"
import Settings from "@/routes/Settings"
import Vaults from "@/routes/Vaults"
import Zones from "@/routes/Zones"

// Split the two heavy routes out of the initial bundle: the charts pull in
// recharts and the wizard pulls in a markdown parser, and neither is needed to
// render the vault list a visitor usually lands on.
const Dashboard = lazy(() => import("@/routes/Dashboard"))
const VaultDetail = lazy(() => import("@/routes/VaultDetail"))
const Setup = lazy(() => import("@/routes/Setup"))

function RouteFallback() {
  return <div className="text-muted-foreground p-6 text-sm">불러오는 중…</div>
}

export default function App() {
  const { data: status, isPending, isError } = useQuery(statusQuery)
  const location = useLocation()

  if (isPending) {
    return <div className="grid min-h-svh place-items-center text-sm text-muted-foreground">불러오는 중…</div>
  }

  if (isError) {
    return (
      <div className="grid min-h-svh place-items-center p-6 text-center">
        <div className="space-y-2">
          <p className="font-medium">CouchHub API에 연결할 수 없습니다.</p>
          <p className="text-sm text-muted-foreground">서버가 실행 중인지 확인하세요.</p>
        </div>
      </div>
    )
  }

  // Until a CouchDB server has been provisioned there is nothing else to show,
  // so every route funnels into the install wizard.
  if (status.needsSetup && location.pathname !== "/setup") {
    return <Navigate to="/setup" replace />
  }

  return (
    <>
      <Suspense fallback={<RouteFallback />}>
        <Routes>
          <Route path="/setup" element={<Setup />} />
          <Route
            path="*"
            element={
              <AppShell>
                <Routes>
                  <Route index element={<Dashboard />} />
                  <Route path="vaults" element={<Vaults />} />
                  <Route path="vaults/:id" element={<VaultDetail />} />
                  <Route path="zones" element={<Zones />} />
                  <Route path="settings" element={<Settings />} />
                  <Route path="*" element={<Navigate to="/" replace />} />
                </Routes>
              </AppShell>
            }
          />
        </Routes>
      </Suspense>
      <Toaster />
    </>
  )
}
