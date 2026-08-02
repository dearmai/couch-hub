import { NavLink } from "react-router"
import { Database, LayoutDashboard, Settings, Share2 } from "lucide-react"

import { cn } from "@/lib/utils"

const NAV = [
  { to: "/", label: "대시보드", icon: LayoutDashboard, end: true },
  { to: "/vaults", label: "Vault", icon: Database, end: false },
  { to: "/zones", label: "존 동기화", icon: Share2, end: false },
  { to: "/settings", label: "설정", icon: Settings, end: false },
]

/**
 * Responsive chrome: a left rail on desktop, a bottom bar on phones.
 *
 * A bottom bar rather than a hamburger drawer because this panel is mostly
 * consulted one-handed on a phone - the nav targets stay in thumb reach and
 * nothing has to be opened first.
 */
export function AppShell({ children }: { children: React.ReactNode }) {
  return (
    <div className="min-h-svh bg-background text-foreground">
      <aside className="fixed inset-y-0 left-0 z-20 hidden w-56 flex-col border-r bg-sidebar md:flex">
        <div className="flex h-14 items-center gap-2 border-b px-4">
          <Database className="size-5 shrink-0" aria-hidden />
          <span className="font-semibold tracking-tight">CouchHub</span>
        </div>
        <nav className="flex-1 space-y-1 p-2">
          {NAV.map(({ to, label, icon: Icon, end }) => (
            <NavLink
              key={to}
              to={to}
              end={end}
              className={({ isActive }) =>
                cn(
                  "flex items-center gap-3 rounded-md px-3 py-2 text-sm transition-colors",
                  isActive
                    ? "bg-sidebar-accent text-sidebar-accent-foreground font-medium"
                    : "text-muted-foreground hover:bg-sidebar-accent/60 hover:text-foreground",
                )
              }
            >
              <Icon className="size-4 shrink-0" aria-hidden />
              {label}
            </NavLink>
          ))}
        </nav>
      </aside>

      <header className="sticky top-0 z-10 flex h-14 items-center gap-2 border-b bg-background/80 px-4 backdrop-blur md:hidden">
        <Database className="size-5" aria-hidden />
        <span className="font-semibold tracking-tight">CouchHub</span>
      </header>

      {/* pb-20 clears the mobile bottom bar; md:pl-56 clears the desktop rail. */}
      <main className="px-4 pt-4 pb-20 md:pl-60 md:pr-6 md:pb-8">
        <div className="mx-auto w-full max-w-5xl">{children}</div>
      </main>

      <nav className="fixed inset-x-0 bottom-0 z-20 grid grid-cols-4 border-t bg-background/95 pb-[env(safe-area-inset-bottom)] backdrop-blur md:hidden">
        {NAV.map(({ to, label, icon: Icon, end }) => (
          <NavLink
            key={to}
            to={to}
            end={end}
            className={({ isActive }) =>
              cn(
                "flex flex-col items-center gap-1 py-2 text-[11px] transition-colors",
                isActive ? "text-foreground font-medium" : "text-muted-foreground",
              )
            }
          >
            <Icon className="size-5" aria-hidden />
            {label}
          </NavLink>
        ))}
      </nav>
    </div>
  )
}
