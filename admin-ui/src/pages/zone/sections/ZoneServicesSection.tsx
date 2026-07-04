import { Server, Database, Layers3, Cpu, PackageCheck, Clock } from 'lucide-react'
import { cn } from '@/lib/utils'
import { type ZoneServiceHealth } from './ZoneServicesPanel'

const fullServiceCatalog = [
  { key: 'hypervisor', label: 'Hypervisor', description: 'Compute virtualization and VM management.' },
  { key: 'storage', label: 'Storage', description: 'Block, file, and object storage services.' },
  { key: 'kubernetes', label: 'Kubernetes', description: 'Managed Kubernetes clusters.' },
  { key: 'ai', label: 'AI Services', description: 'AI/ML workloads and GPU acceleration.' },
  { key: 'mail', label: 'Mail Services', description: 'Email and messaging services.' },
]

export const ServiceIcon = ({ serviceKey }: { serviceKey: string }) => {
  const Icon = serviceKey === 'hypervisor'
    ? Server
    : serviceKey === 'storage'
      ? Database
      : serviceKey === 'kubernetes'
        ? Layers3
        : serviceKey === 'ai'
          ? Cpu
          : serviceKey === 'mail'
            ? PackageCheck
            : Clock
  return <Icon className="size-4 text-muted-foreground/80" />
}

interface ZoneServicesSectionProps {
  enabledServices: ZoneServiceHealth[]
  zoneStatus?: string
}

export default function ZoneServicesSection({
  enabledServices,
  zoneStatus = 'planned',
}: ZoneServicesSectionProps) {
  // Helpers to compute operational status based on enablement and zone state
  const getServiceStatus = (isEnabled: boolean) => {
    if (!isEnabled) {
      return { label: 'Inactive', dotClass: 'bg-slate-400', textClass: 'text-slate-500' }
    }
    switch (zoneStatus.toLowerCase()) {
      case 'active':
        return { label: 'Healthy', dotClass: 'bg-emerald-500', textClass: 'text-emerald-600' }
      case 'planned':
        return { label: 'Planned', dotClass: 'bg-sky-500', textClass: 'text-sky-600' }
      case 'maintenance':
        return { label: 'Maintenance', dotClass: 'bg-violet-500', textClass: 'text-violet-600' }
      case 'draining':
        return { label: 'Draining', dotClass: 'bg-amber-500', textClass: 'text-amber-600' }
      case 'disabled':
        return { label: 'Disabled', dotClass: 'bg-slate-400', textClass: 'text-slate-500' }
      default:
        return { label: 'Unknown', dotClass: 'bg-slate-300', textClass: 'text-muted-foreground' }
    }
  }

  return (
    <div className="border border-border bg-card rounded-lg overflow-hidden shadow-[0_1px_2px_rgba(0,0,0,0.02)] p-5">
      <div className="overflow-x-auto">
        <table className="w-full text-xs">
          <thead>
            <tr className="border-b border-border text-muted-foreground font-medium text-left">
              <th className="pb-2.5 font-medium w-1/4">Service</th>
              <th className="pb-2.5 font-medium w-1/6">Config</th>
              <th className="pb-2.5 font-medium w-1/6">Status (Actual)</th>
              <th className="pb-2.5 font-medium">Description</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border/50">
            {fullServiceCatalog.map((svc) => {
              const matched = enabledServices.find((s) => s.key === svc.key)
              const isEnabled = !!matched
              const statusInfo = getServiceStatus(isEnabled)

              return (
                <tr key={svc.key} className="hover:bg-accent/5">
                  <td className="py-3 font-semibold text-foreground flex items-center gap-2">
                    <ServiceIcon serviceKey={svc.key} />
                    <span>{svc.label}</span>
                  </td>
                  <td className="py-3">
                    <span className={cn(
                      "inline-flex items-center rounded-md px-2 py-0.5 text-[10px] font-medium ring-1 ring-inset",
                      isEnabled 
                        ? "bg-emerald-500/10 text-emerald-700 ring-emerald-600/20" 
                        : "bg-slate-400/10 text-slate-600 ring-slate-500/10"
                    )}>
                      {isEnabled ? "Enabled" : "Disabled"}
                    </span>
                  </td>
                  <td className="py-3">
                    <div className="flex items-center gap-1.5 font-medium">
                      <span className={cn("size-2 rounded-full", statusInfo.dotClass)} />
                      <span className={statusInfo.textClass}>
                        {statusInfo.label}
                      </span>
                    </div>
                  </td>
                  <td className="py-3 text-muted-foreground">{svc.description}</td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>
    </div>
  )
}
