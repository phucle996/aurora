import type { ReactNode } from 'react'
import { Eye, Edit3 } from 'lucide-react'
import type { LucideIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'

export function Panel({ title, icon: Icon, children }: { title: string; icon: LucideIcon; children: ReactNode }) {
  return (
    <section className="rounded-xl border border-border bg-card p-6 shadow-xs">
      <div className="mb-5 flex items-center justify-between">
        <h2 className="text-xl font-semibold tracking-[-0.02em] text-foreground">{title}</h2>
        <Icon className="size-5 text-primary" />
      </div>
      {children}
    </section>
  )
}

function OverviewLabel({ children }: { children: ReactNode }) {
  return <p className="font-medium text-muted-foreground">{children}</p>
}

function OverviewValue({ children }: { children: ReactNode }) {
  return <p className="font-medium leading-6 text-foreground">{children}</p>
}

interface ZoneOverviewPanelProps {
  zoneName: string
  zoneCode: string
  description: string
  created_at?: string
  updated_at?: string
  editingField: 'name' | 'description' | null
  draftName: string
  setDraftName: (val: string) => void
  draftDescription: string
  setDraftDescription: (val: string) => void
  beginEdit: (field: 'name' | 'description') => void
  cancelEdit: () => void
  saveInlineEdit: () => void
}

function formatDate(value?: string) {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '—'
  return new Intl.DateTimeFormat('en', { month: 'short', day: 'numeric', year: 'numeric' }).format(date)
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

function EditableOverviewValue({
  children,
  editing,
  onCancel,
  onEdit,
  onSave,
}: {
  children: ReactNode
  editing: boolean
  onCancel: () => void
  onEdit: () => void
  onSave: () => void
}) {
  return (
    <div className="group min-w-0">
      <div className="flex items-start gap-2">
        <div className="min-w-0 flex-1 font-medium leading-6 text-foreground">{children}</div>
        {!editing && (
          <button
            type="button"
            onClick={onEdit}
            className="mt-0.5 inline-flex size-7 shrink-0 items-center justify-center rounded-md text-muted-foreground opacity-0 transition-all hover:bg-accent hover:text-foreground group-hover:opacity-100 focus-visible:opacity-100"
            aria-label="Edit field"
          >
            <Edit3 className="size-3.5" />
          </button>
        )}
      </div>
      {editing && (
        <div className="mt-3 flex justify-end gap-2">
          <Button type="button" size="sm" variant="outline" onClick={onCancel}>Cancel</Button>
          <Button type="button" size="sm" onClick={onSave}>Save</Button>
        </div>
      )}
    </div>
  )
}

export default function ZoneOverviewPanel({
  zoneName,
  zoneCode,
  description,
  created_at,
  updated_at,
  editingField,
  draftName,
  setDraftName,
  draftDescription,
  setDraftDescription,
  beginEdit,
  cancelEdit,
  saveInlineEdit,
}: ZoneOverviewPanelProps) {
  return (
    <Panel title="Zone Overview" icon={Eye}>
      <div className="grid gap-4 text-sm sm:grid-cols-[180px_1fr]">
        <OverviewLabel>Zone Name</OverviewLabel>
        <EditableOverviewValue
          editing={editingField === 'name'}
          onEdit={() => beginEdit('name')}
          onCancel={cancelEdit}
          onSave={saveInlineEdit}
        >
          {editingField === 'name' ? (
            <Input value={draftName} onChange={(event) => setDraftName(event.target.value)} className="h-10" autoFocus />
          ) : zoneName}
        </EditableOverviewValue>
        <OverviewLabel>Zone Code</OverviewLabel>
        <OverviewValue>{zoneCode}</OverviewValue>
        <OverviewLabel>Description</OverviewLabel>
        <EditableOverviewValue
          editing={editingField === 'description'}
          onEdit={() => beginEdit('description')}
          onCancel={cancelEdit}
          onSave={saveInlineEdit}
        >
          {editingField === 'description' ? (
            <Textarea value={draftDescription} onChange={(event) => setDraftDescription(event.target.value)} className="min-h-24" autoFocus />
          ) : description}
        </EditableOverviewValue>
        <OverviewLabel>Created By</OverviewLabel>
        <OverviewValue>System Admin</OverviewValue>
        <OverviewLabel>Created At</OverviewLabel>
        <OverviewValue>{formatDate(created_at)}</OverviewValue>
        <OverviewLabel>Last Updated</OverviewLabel>
        <OverviewValue>{formatRelative(updated_at)}</OverviewValue>
      </div>
    </Panel>
  )
}
