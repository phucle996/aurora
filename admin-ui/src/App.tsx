import {
  Link,
  RouterProvider,
  createRootRoute,
  createRoute,
  createRouter,
  useNavigate,
} from '@tanstack/react-router'
import { Suspense, lazy, useEffect, type ComponentType } from 'react'
import { toast } from 'sonner'

import AppLayout from '@/components/layout/layout'
import { Toaster } from '@/components/ui/sonner'
import { Skeleton } from '@/components/ui/skeleton'
import { AdminSessionProvider } from '@/hooks/AdminSessionProvider'
import { useAdminSession } from '@/hooks/useAdminSession'

import './App.css'

const AdminAPIKeyLoginPage = lazy(() => import('@/pages/auth/Login'))
const DashboardPage = lazy(() => import('@/pages/dashboard/Dashboard'))
const HypervisorPage = lazy(() => import('@/pages/hypervisor/Hypervisor'))
const DetailHypervisorPage = lazy(() => import('@/pages/hypervisor/DetailHypervisor'))
const MailPage = lazy(() => import('@/pages/mail/MailPage'))
const NewMailEndpointPage = lazy(() => import('@/pages/mail/NewMailEndpoint'))
const EditMailEndpointPage = lazy(() => import('@/pages/mail/EditMailEndpoint'))
const DeliveryAttemptsPage = lazy(() => import('@/pages/mail/DeliveryAttempts'))
const RuntimeStatusPage = lazy(() => import('@/pages/mail/RuntimeStatus'))
const ZoneManagementPage = lazy(() => import('@/pages/zone/ZoneManagement'))
const NewZonePage = lazy(() => import('@/pages/zone/NewZone'))
const ZoneDetailPage = lazy(() => import('@/pages/zone/ZoneDetail'))
const ResourcePlatformAdminPage = lazy(() => import('@/pages/resource-platform/ResourcePlatformAdmin'))

function LazyPage({ component: Component }: { component: ComponentType }) {
  return (
    <Suspense fallback={<RouteFallback />}>
      <Component />
    </Suspense>
  )
}

function RequireAdminSession({ component }: { component: ComponentType }) {
  const navigate = useNavigate()
  const state = useAdminSession()

  useEffect(() => {
    if (!state.loading && !state.authenticated) {
      if (state.notice === 'session_expired') {
        toast.info('Session expired. Please sign in again.', {
          id: 'admin-session-expired',
          duration: 3200,
        })
        state.consumeNotice()
      }
      void navigate({ to: '/auth/admin', replace: true })
    }
  }, [navigate, state])

  if (state.loading) {
    return <RouteFallback />
  }
  if (!state.authenticated) {
    return null
  }

  return <LazyPage component={component} />
}

function withAdminSession(Component: ComponentType) {
  const WrappedComponent = () => <RequireAdminSession component={Component} />
  WrappedComponent.displayName = `withAdminSession(${Component.displayName || Component.name || 'Component'})`
  return WrappedComponent
}

function AdminLoginGate() {
  const navigate = useNavigate()
  const state = useAdminSession()

  useEffect(() => {
    if (!state.loading && state.authenticated) {
      void navigate({ to: '/', replace: true })
    }
  }, [navigate, state.authenticated, state.loading])

  if (state.authenticated) {
    return <RouteFallback />
  }
  return <LazyPage component={AdminAPIKeyLoginPage} />
}

function RouteFallback() {
  return (
    <section className="space-y-6">
      <div className="rounded-3xl border border-border/60 bg-card p-6 shadow-sm">
        <div className="flex flex-col gap-4 xl:flex-row xl:items-start xl:justify-between">
          <div className="space-y-3">
            <Skeleton className="h-4 w-28 rounded-full" />
            <Skeleton className="h-9 w-65 max-w-full rounded-xl" />
            <Skeleton className="h-4 w-105 max-w-full rounded-xl" />
          </div>
          <div className="grid w-full max-w-90 gap-3 sm:grid-cols-2">
            <Skeleton className="h-11 rounded-2xl" />
            <Skeleton className="h-11 rounded-2xl" />
          </div>
        </div>
      </div>

      <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
        {Array.from({ length: 4 }).map((_, index) => (
          <div key={index} className="rounded-3xl border border-border/60 bg-card p-5 shadow-sm">
            <Skeleton className="h-4 w-24 rounded-full" />
            <Skeleton className="mt-5 h-10 w-28 rounded-xl" />
            <Skeleton className="mt-4 h-4 w-36 rounded-full" />
          </div>
        ))}
      </div>

      <div className="grid gap-6 xl:grid-cols-[minmax(0,1.3fr)_minmax(320px,0.7fr)]">
        <div className="rounded-3xl border border-border/60 bg-card p-6 shadow-sm">
          <div className="flex items-start justify-between gap-4">
            <div className="space-y-3">
              <Skeleton className="h-6 w-40 rounded-xl" />
              <Skeleton className="h-4 w-64 rounded-xl" />
            </div>
            <Skeleton className="h-10 w-28 rounded-2xl" />
          </div>
          <div className="mt-8 flex h-70 items-end gap-4">
            {Array.from({ length: 12 }).map((_, index) => (
              <Skeleton
                key={index}
                className="flex-1 rounded-t-2xl"
                style={{ height: `${40 + ((index % 5) + 1) * 28}px` }}
              />
            ))}
          </div>
        </div>

        <div className="space-y-6">
          <div className="rounded-3xl border border-border/60 bg-card p-6 shadow-sm">
            <Skeleton className="h-6 w-36 rounded-xl" />
            <div className="mt-5 space-y-4">
              {Array.from({ length: 4 }).map((_, index) => (
                <div key={index} className="flex items-center gap-3 rounded-2xl border border-border/50 p-4">
                  <Skeleton className="size-10 rounded-2xl" />
                  <div className="min-w-0 flex-1 space-y-2">
                    <Skeleton className="h-4 w-32 rounded-full" />
                    <Skeleton className="h-4 w-full rounded-full" />
                  </div>
                </div>
              ))}
            </div>
          </div>

          <div className="rounded-3xl border border-border/60 bg-card p-6 shadow-sm">
            <Skeleton className="h-6 w-28 rounded-xl" />
            <div className="mt-5 space-y-3">
              {Array.from({ length: 5 }).map((_, index) => (
                <div key={index} className="flex items-center justify-between gap-3 rounded-2xl border border-border/50 p-4">
                  <div className="space-y-2">
                    <Skeleton className="h-4 w-28 rounded-full" />
                    <Skeleton className="h-4 w-40 rounded-full" />
                  </div>
                  <Skeleton className="h-9 w-20 rounded-2xl" />
                </div>
              ))}
            </div>
          </div>
        </div>
      </div>
    </section>
  )
}

function NotFoundPage() {
  return (
    <section className="rounded-3xl border border-border/60 bg-card px-6 py-10 text-center shadow-sm">
      <p className="text-sm font-semibold uppercase tracking-[0.24em] text-primary/80">404</p>
      <h1 className="mt-3 text-3xl font-semibold tracking-tight text-foreground">Page not found</h1>
      <p className="mt-3 text-sm text-muted-foreground">
        The page you are looking for does not exist in the admin console.
      </p>
      <Link className="text-sm font-medium text-primary hover:underline" to="/">
        Back to dashboard
      </Link>
    </section>
  )
}

const rootRoute = createRootRoute({
  component: AppLayout,
  notFoundComponent: NotFoundPage,
})

const dashboardRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/',
  component: withAdminSession(DashboardPage),
})

const usersRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/users',
  component: withAdminSession(DashboardPage),
})

const settingsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/settings',
  component: withAdminSession(DashboardPage),
})

const zonesRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/zones',
  component: withAdminSession(ZoneManagementPage),
})

const zoneDetailRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/zones/$zoneId',
  component: withAdminSession(ZoneDetailPage),
})

const newZoneRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/zones/new',
  component: withAdminSession(NewZonePage),
})

const hypervisorRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/hypervisor',
  component: withAdminSession(HypervisorPage),
})


const detailHypervisorRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/hypervisor/$agentId',
  component: withAdminSession(DetailHypervisorPage),
})

const mailRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/mail',
  component: withAdminSession(MailPage),
})

const newMailEndpointRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/mail/endpoints/new',
  component: withAdminSession(NewMailEndpointPage),
})

const editMailEndpointRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/mail/endpoints/$id/edit',
  component: withAdminSession(EditMailEndpointPage),
})

const deliveryAttemptsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/mail/delivery-attempts',
  component: withAdminSession(DeliveryAttemptsPage),
})

const runtimeStatusRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/mail/runtime-status',
  component: withAdminSession(RuntimeStatusPage),
})

const resourcePlatformRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/resource-platform',
  component: withAdminSession(ResourcePlatformAdminPage),
})

const adminLoginRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/auth/admin',
  component: AdminLoginGate,
})

const routeTree = rootRoute.addChildren([
  dashboardRoute,
  usersRoute,
  settingsRoute,
  zonesRoute,
  zoneDetailRoute,
  newZoneRoute,
  hypervisorRoute,
  detailHypervisorRoute,
  mailRoute,
  newMailEndpointRoute,
  editMailEndpointRoute,
  deliveryAttemptsRoute,
  runtimeStatusRoute,
  resourcePlatformRoute,
  adminLoginRoute,
])

const router = createRouter({
  routeTree,
})

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router
  }
}

export default function App() {
  return (
    <AdminSessionProvider>
      <RouterProvider router={router} />
      <Toaster
        position="bottom-right"
        richColors
        closeButton={false}
        duration={4500}
      />
    </AdminSessionProvider>
  )
}
