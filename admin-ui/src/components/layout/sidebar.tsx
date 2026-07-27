import { Link } from '@tanstack/react-router'
import {
  AlertTriangle,
  BarChart3,
  Boxes,
  Cloud,
  Database,
  Gauge,
  MapPin,
  HardDrive,
  LifeBuoy,
  Network,
  ReceiptText,
  Server,
  Settings,
  ShieldAlert,
  ShieldCheck,
  UserCog,
  Users,
} from 'lucide-react'
import { cn } from '@/lib/utils'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Sheet,
  SheetContent,
  SheetTitle,
} from '@/components/ui/sheet'

type NavItem = {
  label: string
  icon: React.ElementType
  to?: string
}

const primaryItems: NavItem[] = [
  { label: 'Overview', to: '/', icon: Gauge },
  { label: 'Analytics', icon: BarChart3 },
  { label: 'Revenue', icon: BarChart3 },
  { label: 'Billing', icon: ReceiptText },
  { label: 'Tenants', to: '/users', icon: Users },
]

const infraItems: NavItem[] = [
  { label: 'Hypervisor Images', to: '/hypervisor/images', icon: Server },
  { label: 'Resources', to: '/resource-platform', icon: Database },
  { label: 'Storage', icon: HardDrive },
  { label: 'Kubernetes', icon: Boxes },
  { label: 'Database', icon: Database },
  { label: 'Network', icon: Network },
]

const opsItems: NavItem[] = [
  { label: 'Zone', to: '/zones', icon: MapPin },
  { label: 'Incidents', icon: AlertTriangle },
  { label: 'Support', icon: LifeBuoy },
  { label: 'Security & Risk', icon: ShieldAlert },
  { label: 'Compliance', icon: ShieldCheck },
  { label: 'Admin', icon: UserCog },
  { label: 'Settings', to: '/settings', icon: Settings },
]

type AppSidebarProps = {
  collapsed: boolean
  mobileOpen: boolean
  onMobileOpenChange: (open: boolean) => void
  loading?: boolean
}

function NavRow({ item, collapsed, onNavigate }: { item: NavItem; collapsed: boolean; onNavigate?: () => void }) {
  const Icon = item.icon
  const base = cn(
    'flex items-center rounded-lg transition-all duration-200',
    collapsed ? 'justify-center px-2 py-2' : 'gap-3 px-3 py-2',
    'text-sm font-medium text-muted-foreground hover:bg-accent hover:text-foreground',
  )

  if (item.to) {
    return (
      <Link
        to={item.to}
        activeProps={{ className: 'bg-primary/10 text-primary' }}
        className={base}
        title={collapsed ? item.label : undefined}
        onClick={onNavigate}
      >
        <Icon className="h-4 w-4 shrink-0" />
        {!collapsed && <span className="truncate">{item.label}</span>}
      </Link>
    )
  }

  return (
    <button type="button" className={cn(base, 'w-full text-left')} title={collapsed ? item.label : undefined}>
      <Icon className="h-4 w-4 shrink-0" />
      {!collapsed && <span className="truncate">{item.label}</span>}
    </button>
  )
}

function NavSection({ items, collapsed, onNavigate }: { items: NavItem[]; collapsed: boolean; onNavigate?: () => void }) {
  return (
    <div className="space-y-1">
      {items.map((item) => (
        <NavRow key={item.label} item={item} collapsed={collapsed} onNavigate={onNavigate} />
      ))}
    </div>
  )
}


function SidebarSkeleton({ collapsed }: { collapsed: boolean }) {
  return (
    <>
      <div className={cn('flex items-center border-b border-border/70 transition-all duration-300', collapsed ? 'justify-center px-2 py-4' : 'justify-between px-4 py-4')}>
        {collapsed ? <Skeleton className="size-7 rounded-lg" /> : <Skeleton className="h-7 w-36 rounded-lg" />}
      </div>
      <div className="flex-1 overflow-y-auto px-2 py-4 space-y-5">
        {Array.from({ length: 3 }).map((_, section) => (
          <div key={section} className="space-y-2">
            {Array.from({ length: [5,7,7][section] }).map((__, index) => (
              <div key={index} className={cn('flex items-center rounded-lg', collapsed ? 'justify-center px-2 py-2' : 'gap-3 px-3 py-2')}>
                <Skeleton className="size-4 rounded" />
                {!collapsed && <Skeleton className="h-4 flex-1 rounded" />}
              </div>
            ))}
            {section < 2 && <div className="my-4 border-t border-border/60" />}
          </div>
        ))}
      </div>
    </>
  )
}

function SidebarContent({ collapsed, onNavigate }: { collapsed: boolean; onNavigate?: () => void }) {
  return (
    <>
      <div
        className={cn(
          'flex items-center border-b border-border/70 transition-all duration-300',
          collapsed ? 'justify-center px-2 py-4' : 'justify-between px-4 py-4',
        )}
      >
        {!collapsed ? (
          <div className="flex min-w-0 items-center gap-2">
            <Cloud className="h-7 w-7 shrink-0 text-primary" />
            <span className="truncate text-lg font-bold tracking-[-0.02em] text-foreground">
              Aurora Cloud
            </span>
          </div>
        ) : (
          <Cloud className="h-7 w-7 shrink-0 text-primary" />
        )}
      </div>

      <div className="flex-1 overflow-y-auto px-2 py-4">
        <NavSection items={primaryItems} collapsed={collapsed} onNavigate={onNavigate} />
        <div className="my-4 border-t border-border/60" />
        <NavSection items={infraItems} collapsed={collapsed} onNavigate={onNavigate} />
        <div className="my-4 border-t border-border/60" />
        <NavSection items={opsItems} collapsed={collapsed} onNavigate={onNavigate} />
      </div>
    </>
  )
}

export default function AppSidebar({ collapsed, mobileOpen, onMobileOpenChange, loading = false }: AppSidebarProps) {
  return (
    <>
      {/* Desktop sidebar */}
      <aside
        className={cn(
          'hidden shrink-0 border-r border-border/70 bg-card aurora-opaque-surface transition-[width] duration-300 ease-in-out lg:flex lg:flex-col',
          collapsed ? 'w-15' : 'w-63',
        )}
      >
        {loading ? <SidebarSkeleton collapsed={collapsed} /> : <SidebarContent collapsed={collapsed} />}
      </aside>

      {/* Mobile sidebar - Sheet drawer */}
      <Sheet open={mobileOpen} onOpenChange={onMobileOpenChange}>
        <SheetContent side="left" className="w-72 p-0 bg-card aurora-opaque-surface border-border/70" showCloseButton>
          <SheetTitle className="sr-only">Navigation</SheetTitle>
          {loading ? <SidebarSkeleton collapsed={false} /> : <SidebarContent collapsed={false} onNavigate={() => onMobileOpenChange(false)} />}
        </SheetContent>
      </Sheet>
    </>
  )
}
