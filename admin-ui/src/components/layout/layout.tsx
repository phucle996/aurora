import { type PropsWithChildren, useState } from 'react'
import { Outlet, useRouterState } from '@tanstack/react-router'

import AppHeader from './header'
import { useAdminSession } from '@/hooks/useAdminSession'
import AppSidebar from './sidebar'
import { cn } from '@/lib/utils'

export default function AppLayout() {
  const pathname = useRouterState({
    select: (state) => state.location.pathname,
  })
  const [collapsed, setCollapsed] = useState(false)
  const [mobileSidebarOpen, setMobileSidebarOpen] = useState(false)
  const session = useAdminSession()

  if (pathname.startsWith('/auth/')) {
    return <Outlet />
  }

  return (
    <div className="flex h-dvh w-full overflow-hidden bg-background">
      <AppSidebar
        collapsed={collapsed}
        mobileOpen={mobileSidebarOpen}
        onMobileOpenChange={setMobileSidebarOpen}
        loading={session.loading}
      />

      <div className="flex min-w-0 flex-1 flex-col">
        <AppHeader
          onToggleSidebar={() => setCollapsed((prev) => !prev)}
          onOpenMobileSidebar={() => setMobileSidebarOpen(true)}
        />

        <main className="min-w-0 flex-1 overflow-y-auto overflow-x-hidden bg-background px-4 py-4 md:px-6 md:py-5">
          <Outlet />
        </main>
      </div>
    </div>
  )
}

export function PageContent({ children, className }: PropsWithChildren<{ className?: string }>) {
  return <section className={cn('min-w-0 space-y-6 pb-10', className)}>{children}</section>
}
