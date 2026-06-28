import { Edit3 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

type ZoneStatus = 'planned' | 'active' | 'draining' | 'maintenance' | 'disabled'

const statusLabels: Record<ZoneStatus, string> = {
  planned: 'Planned',
  active: 'Active',
  draining: 'Draining',
  maintenance: 'Maintenance',
  disabled: 'Disabled',
}

interface ZoneEssentialsSectionProps {
  zoneName: string
  zoneCode: string
  location: string
  status: ZoneStatus
  description: string
  created_at?: string
  updated_at?: string
  statusDotColor: (status: string) => string
  titleCase: (v: string) => string
  formatDateLong: (v?: string) => string
  formatRelative: (v?: string) => string
  editingField: 'name' | 'description' | null
  draftDescription: string
  setDraftDescription: (val: string) => void
  beginEdit: (field: 'description') => void
  cancelEdit: () => void
  saveInlineEdit: () => void
}

export default function ZoneEssentialsSection({
  zoneName,
  zoneCode,
  location,
  status,
  description,
  created_at,
  updated_at,
  statusDotColor,
  titleCase,
  formatDateLong,
  formatRelative,
  editingField,
  draftDescription,
  setDraftDescription,
  beginEdit,
  cancelEdit,
  saveInlineEdit,
}: ZoneEssentialsSectionProps) {
  return (
    <div className="space-y-3">
      <div className="grid md:grid-cols-2 gap-x-12 gap-y-3 text-xs leading-5">
        {/* Cột trái */}
        <div className="space-y-3">
          <div className="flex items-start">
            <span className="w-28 shrink-0 text-muted-foreground">Zone name</span>
            <span className="mr-3 text-muted-foreground">:</span>
            <span className="font-medium text-foreground">{zoneName}</span>
          </div>
          <div className="flex items-start">
            <span className="w-28 shrink-0 text-muted-foreground">Zone code</span>
            <span className="mr-3 text-muted-foreground">:</span>
            <span className="font-medium text-foreground">{zoneCode}</span>
          </div>
          <div className="flex items-start">
            <span className="w-28 shrink-0 text-muted-foreground">Location</span>
            <span className="mr-3 text-muted-foreground">:</span>
            <span className="font-medium text-foreground">{location}</span>
          </div>
          <div className="flex items-start">
            <span className="w-28 shrink-0 text-muted-foreground">Status</span>
            <span className="mr-3 text-muted-foreground">:</span>
            <div className="flex items-center gap-1.5 font-medium text-foreground">
              <span className={cn("size-2 rounded-full", statusDotColor(status))} />
              <span>{statusLabels[status] ?? titleCase(status)}</span>
            </div>
          </div>
          <div className="flex items-start">
            <span className="w-28 shrink-0 text-muted-foreground">Description</span>
            <span className="mr-3 text-muted-foreground">:</span>
            {editingField === 'description' ? (
              <div className="flex-1 space-y-2">
                <textarea
                  value={draftDescription}
                  onChange={(e) => setDraftDescription(e.target.value)}
                  className="min-h-16 text-xs w-full max-w-lg p-2 border border-border rounded-md bg-background focus:outline-none focus:ring-1 focus:ring-primary"
                  autoFocus
                />
                <div className="flex gap-2">
                  <Button size="xs" variant="outline" className="text-[10px]" onClick={cancelEdit}>Cancel</Button>
                  <Button size="xs" className="text-[10px]" onClick={saveInlineEdit}>Save</Button>
                </div>
              </div>
            ) : (
              <div className="group flex items-center gap-1.5 font-medium text-foreground flex-1">
                <span>{description}</span>
                <button
                  type="button"
                  onClick={() => beginEdit('description')}
                  className="opacity-0 group-hover:opacity-100 transition-opacity text-muted-foreground hover:text-foreground"
                >
                  <Edit3 className="size-3" />
                </button>
              </div>
            )}
          </div>
        </div>

        {/* Cột phải */}
        <div className="space-y-3">
          <div className="flex items-start">
            <span className="w-32 shrink-0 text-muted-foreground">Created on</span>
            <span className="mr-3 text-muted-foreground">:</span>
            <span className="font-medium text-foreground">{formatDateLong(created_at)}</span>
          </div>
          <div className="flex items-start">
            <span className="w-32 shrink-0 text-muted-foreground">Last updated</span>
            <span className="mr-3 text-muted-foreground">:</span>
            <span className="font-medium text-foreground">{formatRelative(updated_at)}</span>
          </div>
          <div className="flex items-start">
            <span className="w-32 shrink-0 text-muted-foreground">Estimated capacity</span>
            <span className="mr-3 text-muted-foreground">:</span>
            <span className="font-medium text-foreground">—</span>
          </div>
        </div>
      </div>
    </div>
  )
}
