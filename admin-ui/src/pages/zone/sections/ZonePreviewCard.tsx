import { Eye, FileText } from 'lucide-react'

import { Badge } from '@/components/ui/badge'
import { cn } from '@/lib/utils'

interface ZonePreviewCardProps {
  zoneName: string
  location: string
  description: string
}

function NoteItem({ title, text }: { title: string; text: string }) {
  return (
    <div className="grid grid-cols-[12px_1fr] gap-4">
      <span className="mt-1.5 size-2 rounded-full bg-primary" />
      <div>
        <p className="text-sm font-semibold text-foreground">{title}</p>
        <p className="mt-1 text-sm leading-6 text-muted-foreground">{text}</p>
      </div>
    </div>
  )
}

export default function ZonePreviewCard({
  zoneName,
  location,
  description,
}: ZonePreviewCardProps) {
  return (
    <aside className="space-y-6">
      <div className="rounded-xl border border-border bg-card p-6 shadow-xs md:p-7">
        <div className="mb-6 flex items-center justify-between">
          <h2 className="text-xl font-semibold tracking-[-0.02em] text-foreground">Zone Preview</h2>
          <Eye className="size-5 text-primary" />
        </div>

        <div className="space-y-5 text-sm">
          <div className="flex items-center justify-between gap-6">
            <span className="font-medium text-primary">Zone Name</span>
            <span className="text-right font-medium text-muted-foreground">{zoneName || '—'}</span>
          </div>
          <div className="flex items-center justify-between gap-6">
            <span className="font-medium text-primary">Location</span>
            <span className="text-right font-medium text-muted-foreground">{location || '—'}</span>
          </div>
          <div className="flex items-center justify-between gap-6">
            <span className="font-medium text-primary">Status</span>
            <Badge
              variant="outline"
              className={cn('h-8 rounded-lg px-3 text-sm font-medium', 'border-amber-200 bg-amber-50 text-amber-700')}
            >
              Planned
            </Badge>
          </div>
        </div>

        <div className="my-7 border-t border-border" />

        <div>
          <p className="text-sm font-medium text-primary">Description</p>
          <p className="mt-4 text-sm italic leading-6 text-muted-foreground">
            {description.trim() || 'No description provided.'}
          </p>
        </div>
      </div>

      <div className="rounded-xl border border-border bg-card p-6 shadow-xs md:p-7">
        <div className="mb-6 flex items-center gap-3">
          <FileText className="size-5 text-primary" />
          <h2 className="text-xl font-semibold tracking-[-0.02em] text-foreground">Creation Notes</h2>
        </div>

        <div className="space-y-6">
          <NoteItem
            title="Choose a clear name and code"
            text="Use a descriptive name and a unique code so the zone is easy to identify."
          />
          <NoteItem
            title="Select the correct location"
            text="Pick the right location to match your deployment plan. Status starts as Planned automatically."
          />
          <NoteItem
            title="Enable only needed services"
            text="Turn on only the platform services required for this zone to keep it simple and secure."
          />
        </div>
      </div>
    </aside>
  )
}
