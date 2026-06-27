import { LocationAutocomplete, type ZoneLocation } from '@/components/zone/location-autocomplete'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { Checkbox } from '@/components/ui/checkbox'
import { Button } from '@/components/ui/button'
import { Link } from '@tanstack/react-router'
import { slugify } from '@/lib/slugify'

export type ServiceKey = 'hypervisor' | 'storage' | 'mail' | 'k8s' | 'ai'

const serviceItems: Array<{ key: ServiceKey; label: string; description: string }> = [
  { key: 'hypervisor', label: 'Hypervisor', description: 'Compute virtualization and VM management.' },
  { key: 'storage', label: 'Storage', description: 'Block, file, and object storage services.' },
  { key: 'k8s', label: 'Kubernetes', description: 'Managed Kubernetes clusters.' },
  { key: 'ai', label: 'AI Services', description: 'AI/ML workloads and GPU acceleration.' },
  { key: 'mail', label: 'Mail Services', description: 'Email and messaging services.' },
]

function Required() {
  return <span className="text-destructive ml-0.5">*</span>
}

interface ZoneFormProps {
  zoneName: string
  setZoneName: (val: string) => void
  zoneCode: string
  setZoneCode: (val: string) => void
  isZoneCodeManuallyEdited: boolean
  setIsZoneCodeManuallyEdited: (val: boolean) => void
  location: string
  setLocation: (val: string) => void
  description: string
  setDescription: (val: string) => void
  services: Record<ServiceKey, boolean>
  toggleService: (key: ServiceKey) => void
  selectLocation: (item: ZoneLocation) => void
  onSubmit: () => void
  disabled: boolean
}

export default function ZoneForm({
  zoneName,
  setZoneName,
  zoneCode,
  setZoneCode,
  isZoneCodeManuallyEdited,
  setIsZoneCodeManuallyEdited,
  location,
  description,
  setDescription,
  services,
  toggleService,
  selectLocation,
  onSubmit,
  disabled,
}: ZoneFormProps) {
  return (
    <div className="space-y-8">
      <div>
        <div className="space-y-6">
          <div className="grid gap-6 md:grid-cols-2">
            <div className="space-y-2">
              <Label className="text-sm font-semibold text-slate-700 dark:text-slate-350">
                Zone name <Required />
              </Label>
              <Input
                value={zoneName}
                onChange={(event) => {
                  const val = event.target.value
                  setZoneName(val)
                  if (!isZoneCodeManuallyEdited) {
                    setZoneCode(slugify(val))
                  }
                }}
                placeholder="US East 1"
                className="h-10 border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-950 px-3 text-sm focus-visible:ring-blue-500/20"
              />
              <p className="text-xs text-slate-400 dark:text-slate-500 mt-1.5">A friendly name to identify this zone.</p>
            </div>

            <div className="space-y-2">
              <Label className="text-sm font-semibold text-slate-700 dark:text-slate-350">
                Zone code <Required />
              </Label>
              <Input
                value={zoneCode}
                onChange={(event) => {
                  const val = event.target.value
                  setIsZoneCodeManuallyEdited(val !== '')
                  setZoneCode(slugify(val))
                }}
                placeholder="us-east-1"
                className="h-10 border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-955 px-3 text-sm focus-visible:ring-blue-500/20"
              />
              <p className="text-xs text-slate-400 dark:text-slate-500 mt-1.5">A unique code used for API and automation.</p>
            </div>
          </div>

          <div className="space-y-2">
            <Label className="text-sm font-semibold text-slate-700 dark:text-slate-350">
              Location <Required />
            </Label>
            <LocationAutocomplete value={location} onSelect={selectLocation} className="mt-0" />
            <p className="text-xs text-slate-400 dark:text-slate-500 mt-1.5">Select the geographic location for this zone.</p>
          </div>

          <div className="space-y-2">
            <Label className="text-sm font-semibold text-slate-700 dark:text-slate-350">Description</Label>
            <Textarea
              value={description}
              onChange={(event) => setDescription(event.target.value)}
              placeholder="Primary zone for US East region. Supports general workloads, managed services, and tenant deployments."
              rows={4}
              className="border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-950 px-3 py-2 text-sm focus-visible:ring-blue-500/20"
            />
            <p className="text-xs text-slate-400 dark:text-slate-500 mt-1.5">Provide details about this zone and its intended use.</p>
          </div>
        </div>
      </div>

      {/* Platform Capabilities Section */}
      <div>
        <div className="border border-slate-200/80 dark:border-slate-800 rounded overflow-hidden bg-white dark:bg-slate-955">
          <table className="w-full text-left border-collapse">
            <thead>
              <tr className="border-b border-slate-200/80 dark:border-slate-800/80 bg-slate-50/50 dark:bg-slate-950/20">
                <th className="p-4 pl-6 text-xs font-semibold text-slate-500 uppercase tracking-wider w-1/4">Service</th>
                <th className="p-4 text-xs font-semibold text-slate-500 uppercase tracking-wider w-1/4">Status</th>
                <th className="p-4 pr-6 text-xs font-semibold text-slate-500 uppercase tracking-wider">Description</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-150 dark:divide-slate-800/80">
              {serviceItems.map((item) => {
                const isEnabled = services[item.key]
                return (
                  <tr key={item.key} className="hover:bg-slate-50/25 dark:hover:bg-slate-955/10 transition-colors">
                    <td className="p-4 pl-6">
                      <label className="flex items-center gap-3 cursor-pointer select-none">
                        <Checkbox
                          checked={isEnabled}
                          onCheckedChange={() => toggleService(item.key)}
                          className="data-checked:bg-blue-600 data-checked:border-blue-600 dark:data-checked:bg-blue-600"
                        />
                        <span className="text-[13px] font-semibold text-slate-800 dark:text-slate-200">{item.label}</span>
                      </label>
                    </td>
                    <td className="p-4">
                      {isEnabled ? (
                        <span className="text-[13px] font-semibold text-emerald-600 dark:text-emerald-400">
                          Enabled
                        </span>
                      ) : (
                        <span className="text-[13px] font-semibold text-slate-400 dark:text-slate-500">
                          Disabled
                        </span>
                      )}
                    </td>
                    <td className="p-4 pr-6 text-[13px] text-slate-500 dark:text-slate-400">
                      {item.description}
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      </div>

      {/* Action Buttons Section */}
      <div className="flex items-center gap-3 pt-2">
        <Button
          type="button"
          onClick={onSubmit}
          disabled={disabled}
          className="h-11 rounded-lg bg-blue-600 hover:bg-blue-700 text-white font-semibold px-6 shadow-md shadow-blue-500/10 disabled:opacity-50"
        >
          Review + create
        </Button>
        <Button asChild variant="outline" className="h-11 rounded-lg px-6 font-semibold border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-955 hover:bg-slate-50 text-slate-700 dark:text-slate-300">
          <Link to="/zones">Cancel</Link>
        </Button>
      </div>
    </div>
  )
}
