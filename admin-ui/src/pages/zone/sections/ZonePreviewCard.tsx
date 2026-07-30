import { Server, Database, Boxes, Brain, Mail, CloudCog } from 'lucide-react'
import { type ServiceKey } from './ZoneForm'

interface ZonePreviewCardProps {
  zoneName: string
  zoneCode: string
  location: string
  description: string
  services: Record<ServiceKey, boolean>
}

const serviceIcons: Record<ServiceKey, React.ComponentType<{ className?: string }>> = {
  hypervisor: Server,
  storage: Database,
  k8s: Boxes,
  ai: Brain,
  mail: Mail,
  managed_service: CloudCog,
}

const serviceNames: Record<ServiceKey, string> = {
  hypervisor: 'Hypervisor',
  storage: 'Storage',
  k8s: 'Kubernetes',
  ai: 'AI Services',
  mail: 'Mail Services',
  managed_service: 'Managed Services',
}

export default function ZonePreviewCard({
  zoneName,
  zoneCode,
  location,
  services,
}: ZonePreviewCardProps) {
  return (
    <aside className="space-y-6">
      {/* Zone summary Cardless */}
      <div className="space-y-6">
        <div>
          <h2 className="text-base font-semibold text-slate-950 dark:text-slate-50">Zone summary</h2>
        </div>

        {/* Section 1: Basic Info */}
        <div className="space-y-3.5 text-[13px]">
          <div className="grid grid-cols-[120px_1fr] gap-4 items-center">
            <span className="text-slate-500 dark:text-slate-400">Status</span>
            <span className="font-semibold text-amber-600 dark:text-amber-400">Planned</span>
          </div>

          <div className="grid grid-cols-[120px_1fr] gap-4 py-1 border-t border-slate-100 dark:border-slate-800/40">
            <span className="text-slate-500 dark:text-slate-400">Created by</span>
            <span className="font-medium text-slate-900 dark:text-slate-100 break-words">admin@aurora.local</span>
          </div>

          <div className="grid grid-cols-[120px_1fr] gap-4 py-1 border-t border-slate-100 dark:border-slate-800/40">
            <span className="text-slate-500 dark:text-slate-400">Created on</span>
            <span className="text-slate-400 dark:text-slate-600">—</span>
          </div>

          <div className="grid grid-cols-[120px_1fr] gap-4 py-1 border-t border-slate-100 dark:border-slate-800/40">
            <span className="text-slate-500 dark:text-slate-400">Estimated Capacity</span>
            <span className="text-slate-400 dark:text-slate-600">—</span>
          </div>
        </div>

        {/* Section 2: Configuration Preview */}
        <div className="border-t border-slate-200/60 dark:border-slate-800/80 pt-5 space-y-4">
          <h3 className="text-xs font-semibold text-slate-450 dark:text-slate-500 uppercase tracking-wider">Configuration Preview</h3>

          <div className="space-y-3.5 text-[13px]">
            <div className="grid grid-cols-[120px_1fr] gap-4">
              <span className="text-slate-500 dark:text-slate-400">Name</span>
              <span className="font-semibold text-slate-900 dark:text-slate-100 break-words">{zoneName || '—'}</span>
            </div>

            <div className="grid grid-cols-[120px_1fr] gap-4 py-1 border-t border-slate-100 dark:border-slate-800/40">
              <span className="text-slate-500 dark:text-slate-400">Code</span>
              <span className="font-semibold text-slate-900 dark:text-slate-100 break-words">{zoneCode || '—'}</span>
            </div>

            <div className="grid grid-cols-[120px_1fr] gap-4 py-1 border-t border-slate-100 dark:border-slate-800/40">
              <span className="text-slate-500 dark:text-slate-400">Location</span>
              <span className="font-semibold text-slate-900 dark:text-slate-100 break-words">{location || '—'}</span>
            </div>

            <div className="grid grid-cols-[120px_1fr] gap-4 py-1 border-t border-slate-100 dark:border-slate-800/40 items-center">
              <span className="text-slate-500 dark:text-slate-400">Services</span>
              <div className="flex flex-wrap gap-2">
                {Object.entries(services)
                  .filter(([, enabled]) => enabled)
                  .map(([key]) => {
                    const Icon = serviceIcons[key as ServiceKey]
                    if (!Icon) return null
                    return (
                      <div
                        key={key}
                        title={serviceNames[key as ServiceKey]}
                        className="flex items-center justify-center p-1.5 rounded-lg bg-slate-50 dark:bg-slate-800 text-slate-650 dark:text-slate-300 border border-slate-200/50 dark:border-slate-700/50"
                      >
                        <Icon className="size-4" />
                      </div>
                    )
                  })}
                {Object.values(services).every((v) => !v) && (
                  <span className="text-slate-450 dark:text-slate-600">—</span>
                )}
              </div>
            </div>
          </div>
        </div>
      </div>
    </aside>
  )
}
