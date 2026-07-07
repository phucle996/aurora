import { Server, Settings2, Box, ShieldCheck } from 'lucide-react'

interface ZoneQuickLinksSectionProps {
  onManageServices: () => void
}

export default function ZoneQuickLinksSection({
  onManageServices,
}: ZoneQuickLinksSectionProps) {
  return (
    <div className="border border-border bg-card rounded-lg overflow-hidden shadow-[0_1px_2px_rgba(0,0,0,0.02)] p-5">
      <h3 className="text-sm font-bold text-foreground mb-4">Quick links</h3>
      <div className="grid grid-cols-2 gap-x-4 gap-y-4 text-xs text-primary font-semibold">
        <button type="button" className="flex items-center gap-2 hover:underline text-left">
          <Server className="size-4 text-muted-foreground/80" />
          Connect hypervisor
        </button>
        <button
          type="button"
          onClick={onManageServices}
          className="flex items-center gap-2 hover:underline text-left"
        >
          <Settings2 className="size-4 text-muted-foreground/80" />
          Manage services
        </button>
        <button type="button" className="flex items-center gap-2 hover:underline text-left">
          <Box className="size-4 text-muted-foreground/80" />
          Create workspace
        </button>
        <button type="button" className="flex items-center gap-2 hover:underline text-left">
          <ShieldCheck className="size-4 text-muted-foreground/80" />
          View audit logs
        </button>
      </div>
    </div>
  )
}
