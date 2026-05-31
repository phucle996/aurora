import { Users } from 'lucide-react'

import { Badge } from '@/components/ui/badge'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Panel } from './ZoneOverviewPanel'
import { StatusBadge } from './ZoneKpiSection'

export type ZoneWorkspace = {
  id: string
  name: string
  tenant_name: string
  status: string
  services: string[]
  updated_at?: string
}

interface ZoneWorkspacesPanelProps {
  workspaces: ZoneWorkspace[]
  totalCount: number
}

function titleCase(value: string) {
  return value
    .replace(/[_-]+/g, ' ')
    .split(' ')
    .filter(Boolean)
    .map((item) => item.charAt(0).toUpperCase() + item.slice(1))
    .join(' ')
}

function formatRelative(value?: string) {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '—'
  const seconds = Math.max(1, Math.floor((Date.now() - date.getTime()) / 1000))
  if (seconds < 60) return `${seconds}s ago`
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes}m ago`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h ago`
  const days = Math.floor(hours / 24)
  return `${days}d ago`
}

export default function ZoneWorkspacesPanel({
  workspaces,
  totalCount,
}: ZoneWorkspacesPanelProps) {
  return (
    <Panel title="Workspaces in this Zone" icon={Users}>
      <Table>
        <TableHeader>
          <TableRow className="hover:bg-transparent">
            <TableHead>Workspace</TableHead>
            <TableHead>Tenant</TableHead>
            <TableHead>Status</TableHead>
            <TableHead>Services</TableHead>
            <TableHead className="text-right">Updated</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {workspaces.length === 0 && (
            <TableRow className="hover:bg-transparent">
              <TableCell colSpan={5} className="h-28 text-center text-sm text-muted-foreground">
                No workspaces in this zone yet.
              </TableCell>
            </TableRow>
          )}
          {workspaces.map((workspace) => (
            <TableRow key={workspace.id}>
              <TableCell className="font-semibold text-primary">{workspace.name}</TableCell>
              <TableCell>{workspace.tenant_name || '—'}</TableCell>
              <TableCell>
                <StatusBadge status={workspace.status} />
              </TableCell>
              <TableCell>
                <div className="flex flex-wrap gap-2">
                  {workspace.services.length === 0 && <span className="text-sm text-muted-foreground">—</span>}
                  {workspace.services.map((service) => (
                    <Badge key={service} variant="outline" className="rounded-md text-xs">
                      {titleCase(service)}
                    </Badge>
                  ))}
                </div>
              </TableCell>
              <TableCell className="text-right text-muted-foreground">
                {formatRelative(workspace.updated_at)}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
      <p className="mt-4 text-sm text-muted-foreground">
        Showing {workspaces.length} of {totalCount} workspaces
      </p>
    </Panel>
  )
}
