import {
  Bell,
  ChevronDown,
  CircleHelp,
  Globe2,
  LogOut,
  Menu,
  PanelLeft,
  Search,
} from 'lucide-react'

import { Avatar, AvatarFallback } from '@/components/ui/avatar'
import { Input } from '@/components/ui/input'
import { Button } from '@/components/ui/button'
import ThemeSwitcher from '@/components/layout/theme-switcher'
import { useAdminSession } from '@/hooks/useAdminSession'
import { useZoneStore } from '@/hooks/useZoneStore'
import { useEffect, useMemo } from 'react'
import { Fetch } from '@/lib/fetch'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'

type AppHeaderProps = {
  onToggleSidebar: () => void
  onOpenMobileSidebar: () => void
}

export default function AppHeader({ onToggleSidebar, onOpenMobileSidebar }: AppHeaderProps) {
  const { clearSession } = useAdminSession()
  const { zones, activeZone, fetchZones, setActiveZone } = useZoneStore()

  useEffect(() => {
    void fetchZones()
  }, [fetchZones])

  const activeZoneLabel = useMemo(() => {
    if (!activeZone) return 'Global'
    return zones.find(z => z.id === activeZone)?.name || activeZone
  }, [activeZone, zones])

  const handleLogout = async () => {
    try {
      await Fetch('/admin/auth/logout', { method: 'POST' })
    } catch (err) {
      console.error('Failed to log out on server:', err)
    } finally {
      clearSession()
    }
  }

  return (
    <header className="sticky top-0 z-20 border-b border-border/70 bg-card shadow-[0_1px_0_rgba(15,23,42,0.03)]">
      <div className="flex min-h-14 flex-wrap items-center gap-3 px-4 py-3 md:px-6 md:py-0">
        <button
          type="button"
          onClick={onOpenMobileSidebar}
          className="inline-flex h-8 w-8 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-foreground lg:hidden"
          aria-label="Open navigation"
        >
          <Menu className="h-4 w-4" />
        </button>

        <button
          type="button"
          onClick={onToggleSidebar}
          className="hidden h-8 w-8 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-foreground lg:inline-flex"
          aria-label="Collapse sidebar"
        >
          <PanelLeft className="h-4 w-4" />
        </button>

        <div className="order-3 relative hidden sm:block md:order-2 w-64">
          <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            placeholder="Search dashboard, metrics, tenants..."
            className="h-9 w-full border-border/80 bg-background pl-9 text-sm shadow-none"
          />
        </div>

        {/* Global Zone Selector Dropdown next to Search */}
        <div className="order-3 md:order-2 w-64">
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button
                variant="outline"
                className="h-9 w-full justify-between gap-2 border-border/80 bg-background px-3 text-xs shadow-none cursor-pointer hover:bg-accent"
              >
                <span className="flex items-center gap-1.5 font-medium truncate">
                  <Globe2 className="size-3.5 text-muted-foreground shrink-0" />
                  <span className="truncate">{activeZoneLabel}</span>
                </span>
                <ChevronDown className="size-3.5 text-muted-foreground shrink-0" />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="start" className="w-64">
              <DropdownMenuItem onClick={() => setActiveZone(null)} className="cursor-pointer">
                Global (All Zones)
              </DropdownMenuItem>
              {zones.map((zone) => (
                <DropdownMenuItem
                  key={zone.id}
                  onClick={() => setActiveZone(zone.id)}
                  className="cursor-pointer"
                >
                  {zone.name}
                </DropdownMenuItem>
              ))}
            </DropdownMenuContent>
          </DropdownMenu>
        </div>

        <div className="order-2 ml-auto flex items-center gap-1 md:order-3 md:gap-2">
          <button
            type="button"
            className="relative inline-flex h-8 w-8 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
            aria-label="Notifications"
          >
            <Bell className="h-4 w-4" />
            <span className="absolute right-1 top-1 h-2.5 w-2.5 rounded-full bg-primary" />
          </button>

          <button
            type="button"
            className="hidden h-8 w-8 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-foreground sm:inline-flex"
            aria-label="Help"
          >
            <CircleHelp className="h-4 w-4" />
          </button>

          <ThemeSwitcher />

          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <button
                type="button"
                className="inline-flex items-center gap-2 rounded-md px-1 py-1 transition-colors hover:bg-accent sm:px-2 sm:py-1.5 focus:outline-none cursor-pointer"
              >
                <Avatar className="h-8 w-8 border border-border/70">
                  <AvatarFallback className="bg-primary text-xs font-bold text-primary-foreground">SA</AvatarFallback>
                </Avatar>
                <div className="hidden min-w-0 text-left lg:block">
                  <p className="truncate text-xs font-bold text-foreground">System Admin</p>
                  <p className="truncate text-[11px] text-muted-foreground">Platform Administrator</p>
                </div>
                <ChevronDown className="hidden h-4 w-4 text-muted-foreground lg:block" />
              </button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="w-40">
              <DropdownMenuItem onClick={handleLogout} variant="destructive" className="cursor-pointer flex items-center gap-2">
                <LogOut className="h-4 w-4" />
                <span>Đăng xuất</span>
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      </div>
    </header>
  )
}
