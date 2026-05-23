import { CheckCircle2 } from 'lucide-react'
import { Card, CardContent } from '@/components/ui/card'

export function NewNodeRequirements() {
  const requirements = [
    'Fresh Linux host ready',
    'KVM / libvirt installed',
    'Outbound HTTPS access available',
    'Host reachable from management network',
    'Time sync enabled',
    'Root or sudo access available'
  ]

  return (
    <Card className="border-none shadow-[0_2px_15px_-3px_rgba(0,0,0,0.07)]">
      <CardContent className="p-6 space-y-5">
        <h3 className="text-sm font-black text-slate-900 uppercase tracking-wider">Requirements</h3>
        <div className="space-y-4">
          {requirements.map((req, i) => (
            <div key={i} className="flex items-center gap-3">
              <CheckCircle2 className="h-5 w-5 text-emerald-500" />
              <span className="text-[13px] font-semibold text-slate-600">{req}</span>
            </div>
          ))}
        </div>
      </CardContent>
    </Card>
  )
}
