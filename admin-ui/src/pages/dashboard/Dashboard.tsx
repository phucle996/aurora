import { useEffect, useMemo, useState } from 'react'
import type { DragEvent, MouseEvent as ReactMouseEvent, ReactElement } from 'react'
import { ArrowLeftRight, CalendarDays, Check, GripVertical, Pencil } from 'lucide-react'

import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { StatsOverview } from '@/components/dashboard/stats-overview'
import { RevenueTrend } from '@/components/dashboard/revenue-trend'
import { PlanDistribution } from '@/components/dashboard/plan-distribution'
import { InfrastructureStatus } from '@/components/dashboard/infrastructure-status'
import { ServiceHealth } from '@/components/dashboard/service-health'
import { TenantGrowth } from '@/components/dashboard/tenant-growth'
import { SecurityAlerts } from '@/components/dashboard/security-alerts'
import { AdminActions } from '@/components/dashboard/admin-actions'
import { TopPerformingServices } from '@/components/dashboard/top-performing-services'
import { SupportTicketVolume } from '@/components/dashboard/support-ticket-volume'
import { DataCenterUtilization } from '@/components/dashboard/data-center-utilization'
import { SLOErrorBudget } from '@/components/dashboard/slo-error-budget'
import { TenantActivityStream } from '@/components/dashboard/tenant-activity-stream'
import { usePageMeta } from '@/lib/page-meta'

type PanelId =
  | 'revenue-trend'
  | 'plan-distribution'
  | 'infrastructure-status'
  | 'service-health'
  | 'top-performing-services'
  | 'support-ticket-volume'
  | 'tenant-growth'
  | 'data-center-utilization'
  | 'security-alerts'
  | 'admin-actions'
  | 'slo-error-budget'
  | 'tenant-activity-stream'

type PanelConfig = {
  id: PanelId
  render: () => ReactElement
}

type PositionedPanel = {
  id: PanelId
  panel: PanelConfig
  index: number
  row: number
  colStart: number
  span: number
}

type DropZone = {
  key: string
  row: number
  colStart: number
  span: number
  insertIndex: number
  isTail?: boolean
}

const DASHBOARD_ORDER_STORAGE_KEY = 'adminui.dashboard.panel-order.v1'
const DASHBOARD_SPAN_STORAGE_KEY = 'adminui.dashboard.panel-spans.v2'
const MIN_PANEL_SPAN = 1
const MAX_PANEL_SPAN = 12
const DEFAULT_PANEL_SPAN = 4

type PanelSpans = Partial<Record<PanelId, number>>

type ActiveResize = {
  id: PanelId
  startX: number
  startSpan: number
  columnWidth: number
}

const PANEL_REGISTRY: Record<PanelId, PanelConfig> = {
  'revenue-trend': {
    id: 'revenue-trend',
    render: () => <RevenueTrend />,
  },
  'plan-distribution': {
    id: 'plan-distribution',
    render: () => <PlanDistribution />,
  },
  'infrastructure-status': {
    id: 'infrastructure-status',
    render: () => <InfrastructureStatus />,
  },
  'service-health': {
    id: 'service-health',
    render: () => <ServiceHealth />,
  },
  'top-performing-services': {
    id: 'top-performing-services',
    render: () => <TopPerformingServices />,
  },
  'support-ticket-volume': {
    id: 'support-ticket-volume',
    render: () => <SupportTicketVolume />,
  },
  'tenant-growth': {
    id: 'tenant-growth',
    render: () => <TenantGrowth />,
  },
  'data-center-utilization': {
    id: 'data-center-utilization',
    render: () => <DataCenterUtilization />,
  },
  'security-alerts': {
    id: 'security-alerts',
    render: () => <SecurityAlerts />,
  },
  'admin-actions': {
    id: 'admin-actions',
    render: () => <AdminActions />,
  },
  'slo-error-budget': {
    id: 'slo-error-budget',
    render: () => <SLOErrorBudget />,
  },
  'tenant-activity-stream': {
    id: 'tenant-activity-stream',
    render: () => <TenantActivityStream />,
  },
}

const DEFAULT_PANEL_ORDER: PanelId[] = [
  'revenue-trend',
  'plan-distribution',
  'infrastructure-status',
  'service-health',
  'top-performing-services',
  'support-ticket-volume',
  'tenant-growth',
  'data-center-utilization',
  'slo-error-budget',
  'security-alerts',
  'tenant-activity-stream',
  'admin-actions',
]

const DEFAULT_PANEL_SPANS: Record<PanelId, number> = {
  'revenue-trend': 6,
  'plan-distribution': 3,
  'infrastructure-status': 3,
  'service-health': 4,
  'top-performing-services': 4,
  'support-ticket-volume': 4,
  'tenant-growth': 3,
  'data-center-utilization': 3,
  'slo-error-budget': 3,
  'security-alerts': 3,
  'tenant-activity-stream': 6,
  'admin-actions': 6,
}

const PANEL_IDS = new Set<PanelId>(DEFAULT_PANEL_ORDER)

function moveItemToIndex<T>(items: T[], fromIndex: number, toIndex: number): T[] {
  const boundedIndex = Math.max(0, Math.min(items.length, toIndex))
  const next = [...items]
  const [moved] = next.splice(fromIndex, 1)
  const insertIndex = fromIndex < boundedIndex ? boundedIndex - 1 : boundedIndex
  next.splice(insertIndex, 0, moved)
  return next
}

function areOrdersEqual(a: PanelId[], b: PanelId[]): boolean {
  if (a.length !== b.length) return false
  for (let index = 0; index < a.length; index += 1) {
    if (a[index] !== b[index]) return false
  }
  return true
}

function resolvePanelSpan(id: PanelId, panelSpans: PanelSpans, columns: number): number {
  return Math.max(
    MIN_PANEL_SPAN,
    Math.min(columns, panelSpans[id] ?? DEFAULT_PANEL_SPANS[id] ?? DEFAULT_PANEL_SPAN),
  )
}

function buildPositionedPanels(
  panelOrder: PanelId[],
  panelSpans: PanelSpans,
  columns: number,
): PositionedPanel[] {
  let row = 1
  let colStart = 1

  return panelOrder.map((id, index) => {
    const span = resolvePanelSpan(id, panelSpans, columns)
    if (colStart + span - 1 > columns) {
      row += 1
      colStart = 1
    }

    const positionedPanel: PositionedPanel = {
      id,
      panel: PANEL_REGISTRY[id],
      index,
      row,
      colStart,
      span,
    }

    colStart += span
    if (colStart > columns) {
      row += 1
      colStart = 1
    }

    return positionedPanel
  })
}

function buildDropZones(
  positionedPanels: PositionedPanel[],
  columns: number,
  minRequiredSpan: number,
): DropZone[] {
  if (positionedPanels.length === 0) {
    return [
      {
        key: 'empty-grid-drop-zone',
        row: 1,
        colStart: 1,
        span: columns,
        insertIndex: 0,
        isTail: true,
      },
    ]
  }

  const rowPanelsMap = new Map<number, PositionedPanel[]>()
  for (const panel of positionedPanels) {
    const rowPanels = rowPanelsMap.get(panel.row) ?? []
    rowPanels.push(panel)
    rowPanelsMap.set(panel.row, rowPanels)
  }

  const zones: DropZone[] = []
  const orderedRows = [...rowPanelsMap.keys()].sort((a, b) => a - b)

  for (const row of orderedRows) {
    const rowPanels = [...(rowPanelsMap.get(row) ?? [])].sort((a, b) => a.colStart - b.colStart)
    let cursor = 1

    for (const panel of rowPanels) {
      const gapSpan = panel.colStart - cursor
      if (gapSpan >= minRequiredSpan) {
        zones.push({
          key: `row-${row}-gap-${cursor}`,
          row,
          colStart: cursor,
          span: gapSpan,
          insertIndex: panel.index,
        })
      }

      cursor = panel.colStart + panel.span
    }

    const trailingSpan = columns - cursor + 1
    if (trailingSpan >= minRequiredSpan) {
      zones.push({
        key: `row-${row}-tail`,
        row,
        colStart: cursor,
        span: trailingSpan,
        insertIndex: rowPanels[rowPanels.length - 1].index + 1,
      })
    }
  }

  const lastRow = positionedPanels[positionedPanels.length - 1]?.row ?? 1
  zones.push({
    key: 'append-new-row',
    row: lastRow + 1,
    colStart: 1,
    span: columns,
    insertIndex: positionedPanels.length,
    isTail: true,
  })

  return zones
}

function normalizeOrder(order: PanelId[]): PanelId[] {
  const seen = new Set<PanelId>()
  const valid = order.filter((id) => PANEL_IDS.has(id) && !seen.has(id) && seen.add(id))
  const missing = DEFAULT_PANEL_ORDER.filter((id) => !seen.has(id))
  return [...valid, ...missing]
}

function readPanelOrderFromStorage(): PanelId[] {
  if (typeof window === 'undefined') return DEFAULT_PANEL_ORDER

  try {
    const raw = window.localStorage.getItem(DASHBOARD_ORDER_STORAGE_KEY)
    if (!raw) return DEFAULT_PANEL_ORDER

    const parsed = JSON.parse(raw) as PanelId[]
    if (!Array.isArray(parsed)) return DEFAULT_PANEL_ORDER

    return normalizeOrder(parsed)
  } catch {
    return DEFAULT_PANEL_ORDER
  }
}

function readPanelSpansFromStorage(): PanelSpans {
  if (typeof window === 'undefined') return {}

  try {
    const raw = window.localStorage.getItem(DASHBOARD_SPAN_STORAGE_KEY)
    if (!raw) return {}

    const parsed = JSON.parse(raw) as Record<string, unknown>
    const next: PanelSpans = {}

    for (const [id, value] of Object.entries(parsed)) {
      if (!PANEL_IDS.has(id as PanelId)) continue
      if (typeof value !== 'number' || Number.isNaN(value)) continue
      next[id as PanelId] = Math.max(MIN_PANEL_SPAN, Math.min(MAX_PANEL_SPAN, Math.round(value)))
    }

    return next
  } catch {
    return {}
  }
}

function getCurrentGridColumns() {
  if (typeof window === 'undefined') return 12
  if (window.innerWidth >= 768) return 12
  return 1
}

export default function DashboardPage() {
  usePageMeta('Dashboard | Aurora Admin', 'Operational overview for Aurora controlplane services.')
  const [editMode, setEditMode] = useState(false)
  const [panelOrder, setPanelOrder] = useState<PanelId[]>(readPanelOrderFromStorage)
  const [panelSpans, setPanelSpans] = useState<PanelSpans>(readPanelSpansFromStorage)
  const [gridColumns, setGridColumns] = useState(getCurrentGridColumns)
  const [draggingId, setDraggingId] = useState<PanelId | null>(null)
  const [dropTargetId, setDropTargetId] = useState<PanelId | null>(null)
  const [dropZoneKey, setDropZoneKey] = useState<string | null>(null)
  const [activeResize, setActiveResize] = useState<ActiveResize | null>(null)

  useEffect(() => {
    window.localStorage.setItem(DASHBOARD_ORDER_STORAGE_KEY, JSON.stringify(panelOrder))
  }, [panelOrder])

  useEffect(() => {
    window.localStorage.setItem(DASHBOARD_SPAN_STORAGE_KEY, JSON.stringify(panelSpans))
  }, [panelSpans])

  useEffect(() => {
    function onResize() {
      setGridColumns(getCurrentGridColumns())
    }

    onResize()
    window.addEventListener('resize', onResize)
    return () => window.removeEventListener('resize', onResize)
  }, [])

  useEffect(() => {
    if (!activeResize) return
    const resizing = activeResize

    function onMouseMove(event: MouseEvent) {
      const deltaX = event.clientX - resizing.startX
      const spanDelta = Math.round(deltaX / resizing.columnWidth)
      const nextSpan = Math.max(
        MIN_PANEL_SPAN,
        Math.min(gridColumns, resizing.startSpan + spanDelta),
      )

      setPanelSpans((current) => ({
        ...current,
        [resizing.id]: nextSpan,
      }))
    }

    function onMouseUp() {
      setActiveResize(null)
    }

    window.addEventListener('mousemove', onMouseMove)
    window.addEventListener('mouseup', onMouseUp)
    return () => {
      window.removeEventListener('mousemove', onMouseMove)
      window.removeEventListener('mouseup', onMouseUp)
    }
  }, [activeResize, gridColumns])

  const positionedPanels = useMemo(
    () => buildPositionedPanels(panelOrder, panelSpans, gridColumns),
    [panelOrder, panelSpans, gridColumns],
  )
  const draggingSpan = draggingId ? resolvePanelSpan(draggingId, panelSpans, gridColumns) : null
  const dropZones = useMemo(() => {
    if (!editMode || !draggingId) return []
    return buildDropZones(positionedPanels, gridColumns, draggingSpan ?? MIN_PANEL_SPAN)
  }, [editMode, draggingId, positionedPanels, gridColumns, draggingSpan])

  function reorderDraggingPanel(getInsertIndex: (current: PanelId[]) => number) {
    if (!draggingId) return

    setPanelOrder((current) => {
      const fromIndex = current.indexOf(draggingId)
      if (fromIndex === -1) return current

      const next = moveItemToIndex(current, fromIndex, getInsertIndex(current))
      return areOrdersEqual(next, current) ? current : next
    })
  }

  function handleDragStart(event: DragEvent<HTMLDivElement>, id: PanelId) {
    if (!editMode) return
    event.dataTransfer.effectAllowed = 'move'
    event.dataTransfer.setData('text/plain', id)
    setDraggingId(id)
    setDropTargetId(id)
    setDropZoneKey(null)
  }

  function handlePanelDragOver(event: DragEvent<HTMLDivElement>, id: PanelId) {
    event.preventDefault()
    if (!editMode) return
    if (!draggingId || draggingId === id) return
    if (dropTargetId !== id) {
      setDropTargetId(id)
    }
    if (dropZoneKey) {
      setDropZoneKey(null)
    }

    reorderDraggingPanel((current) => current.indexOf(id))
  }

  function handleDropZoneDragOver(event: DragEvent<HTMLDivElement>, zone: DropZone) {
    event.preventDefault()
    if (!editMode) return
    if (!draggingId) return
    if ((draggingSpan ?? MIN_PANEL_SPAN) > zone.span) return

    if (dropZoneKey !== zone.key) {
      setDropZoneKey(zone.key)
    }
    if (dropTargetId) {
      setDropTargetId(null)
    }

    reorderDraggingPanel(() => zone.insertIndex)
  }

  function handleDrop(event: DragEvent<HTMLDivElement>) {
    event.preventDefault()
    if (!editMode) return
    setDraggingId(null)
    setDropTargetId(null)
    setDropZoneKey(null)
  }

  function handleDragEnd() {
    setDraggingId(null)
    setDropTargetId(null)
    setDropZoneKey(null)
  }

  function handleResizeStart(event: ReactMouseEvent<HTMLButtonElement>, id: PanelId) {
    if (!editMode) return
    event.preventDefault()
    event.stopPropagation()

    const gridContainer = event.currentTarget.closest('[data-panel-grid]') as HTMLDivElement | null
    const gridWidth = gridContainer?.getBoundingClientRect().width ?? 1200
    const columnWidth = Math.max(1, gridWidth / Math.max(1, gridColumns))

    const startSpan = Math.max(
      MIN_PANEL_SPAN,
      Math.min(gridColumns, panelSpans[id] ?? DEFAULT_PANEL_SPANS[id] ?? DEFAULT_PANEL_SPAN),
    )

    setActiveResize({
      id,
      startX: event.clientX,
      startSpan,
      columnWidth,
    })
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div className="space-y-1">
          <h1 className="m-0 text-[40px] font-black leading-none tracking-tight text-foreground md:text-[46px]">
            Welcome back, System Admin
          </h1>
          <p className="text-base font-medium text-muted-foreground">
            Platform overview and key business metrics
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Button variant="outline" className="h-10 rounded-lg border-border/80 bg-background shadow-sm">
            <CalendarDays className="mr-2 h-4 w-4" />
            May 12 - Jun 11, 2024
          </Button>
          <Button
            variant={editMode ? 'default' : 'outline'}
            className="h-10 rounded-lg border-border/80 shadow-sm"
            onClick={() => setEditMode((current) => !current)}
          >
            {editMode ? <Check className="mr-2 h-4 w-4" /> : <Pencil className="mr-2 h-4 w-4" />}
            {editMode ? 'Done' : 'Edit'}
          </Button>
        </div>
      </div>

      <StatsOverview />

      <div
        data-panel-grid
        className="grid auto-rows-auto items-stretch gap-4 grid-cols-1 md:grid-cols-12"
        onDragOver={(event) => {
          if (editMode && draggingId) {
            event.preventDefault()
          }
        }}
      >
        {dropZones.map((zone) => (
          <div
            key={zone.key}
            onDragOver={(event) => handleDropZoneDragOver(event, zone)}
            onDrop={handleDrop}
            className={cn(
              'relative rounded-xl border-2 border-dashed transition-colors duration-150 md:[grid-column:var(--panel-grid-column)] md:[grid-row:var(--panel-grid-row)]',
              zone.isTail ? 'min-h-[72px]' : 'min-h-[220px]',
              dropZoneKey === zone.key
                ? 'border-primary/70 bg-primary/12'
                : 'border-primary/30 bg-primary/5',
            )}
            style={{
              '--panel-grid-column': `${zone.colStart} / span ${zone.span}`,
              '--panel-grid-row': `${zone.row}`,
            } as React.CSSProperties}
          >
            <div className="pointer-events-none absolute inset-0 flex items-center justify-center text-[11px] font-semibold text-primary/80">
              {zone.isTail
                ? 'Drop to move panel to new row'
                : `Empty space · ${zone.span} col available`}
            </div>
          </div>
        ))}

        {positionedPanels.map((positionedPanel) => {
          const panel = positionedPanel.panel

          return (
          <div
            key={panel.id}
            data-panel-card
            draggable={editMode}
            onDragStart={(event) => handleDragStart(event, panel.id)}
            onDragOver={(event) => handlePanelDragOver(event, panel.id)}
            onDrop={handleDrop}
            onDragEnd={handleDragEnd}
            className={cn(
              'group relative rounded-xl transition-all duration-150 md:[grid-column:var(--panel-grid-column)] md:[grid-row:var(--panel-grid-row)]',
              editMode && "cursor-grab active:cursor-grabbing",
              draggingId === panel.id && "opacity-65",
              activeResize?.id === panel.id && "select-none ring-2 ring-primary/50 ring-offset-2 ring-offset-background",
              dropTargetId === panel.id &&
                draggingId !== panel.id &&
                "ring-2 ring-primary/40 ring-offset-2 ring-offset-background",
            )}
            style={{
              '--panel-grid-column': `${positionedPanel.colStart} / span ${positionedPanel.span}`,
              '--panel-grid-row': `${positionedPanel.row}`,
            } as React.CSSProperties}
          >
            {editMode && (
              <div className="pointer-events-none absolute right-2 top-2 z-20 inline-flex items-center gap-1 rounded-md border border-border/80 bg-background/90 px-2 py-1 text-[11px] font-semibold text-muted-foreground backdrop-blur">
                <GripVertical className="h-3.5 w-3.5" />
                Drag · {positionedPanel.span} col
              </div>
            )}

            <div className="h-full min-h-[260px] [&>*]:h-full">
              {panel.render()}
            </div>

            {editMode && (
              <button
                type="button"
                onMouseDown={(event) => handleResizeStart(event, panel.id)}
                className="absolute inset-y-0 right-0 z-20 flex w-6 cursor-col-resize items-center justify-center rounded-r-xl border-l border-border/70 bg-background/70 text-muted-foreground backdrop-blur transition-colors hover:bg-accent hover:text-foreground"
              >
                <ArrowLeftRight className="h-3.5 w-3.5" />
              </button>
            )}
          </div>
          )
        })}
      </div>
    </div>
  )
}
