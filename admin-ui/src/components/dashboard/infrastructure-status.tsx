import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Button } from "@/components/ui/button"
import { cn } from "@/lib/utils"

const regions = [
  { id: "us-east-1", name: "US East (N. Virginia)", status: "Healthy", utilization: "78%", color: "bg-emerald-500" },
  { id: "us-west-2", name: "US West (Oregon)", status: "Healthy", utilization: "65%", color: "bg-emerald-500" },
  { id: "eu-central-1", name: "Europe (Frankfurt)", status: "Healthy", utilization: "72%", color: "bg-emerald-500" },
  { id: "ap-southeast-1", name: "Asia Pacific (Singapore)", status: "Degraded", utilization: "58%", color: "bg-amber-500" },
  { id: "ap-northeast-1", name: "Asia Pacific (Tokyo)", status: "Healthy", utilization: "68%", color: "bg-emerald-500" },
  { id: "sa-east-1", name: "South America (São Paulo)", status: "Healthy", utilization: "61%", color: "bg-emerald-500" },
]

export function InfrastructureStatus() {
  return (
    <Card className="col-span-full lg:col-span-1 border border-border/70 bg-card shadow-sm">
      <CardHeader className="flex flex-row items-center justify-between pb-2">
        <CardTitle className="text-lg font-bold">Regional Infrastructure Status</CardTitle>
        <Button variant="link" className="h-auto p-0 text-xs font-bold text-primary hover:no-underline">View all regions</Button>
      </CardHeader>
      <CardContent>
        <div className="grid grid-cols-1 xl:grid-cols-2 gap-8 py-2">
          {/* Mock Map */}
          <div className="relative aspect-[1.6/1] overflow-hidden rounded-xl border border-dashed border-border bg-muted/40 flex items-center justify-center">
            <div className="absolute inset-0 opacity-[0.05]" 
                 style={{ backgroundImage: 'radial-gradient(circle at 2px 2px, #2563EB 1px, transparent 0)', backgroundSize: '16px 16px' }} />
            <div className="flex flex-col items-center gap-2">
              <div className="text-xs font-semibold text-muted-foreground uppercase tracking-widest">Global Network View</div>
              <div className="flex gap-1.5">
                {[1, 2, 3, 4, 5].map((i) => (
                  <div key={i} className="h-1.5 w-1.5 rounded-full bg-primary/20" />
                ))}
              </div>
            </div>
            {/* Pulsing dots for status */}
            <div className="absolute top-[30%] left-[25%] h-3 w-3 rounded-full bg-emerald-500 shadow-[0_0_10px_rgba(16,185,129,0.5)]" />
            <div className="absolute top-[35%] left-[20%] h-2.5 w-2.5 rounded-full bg-emerald-500" />
            <div className="absolute top-[40%] left-[45%] h-3 w-3 rounded-full bg-emerald-500" />
            <div className="absolute top-[50%] left-[70%] h-3 w-3 rounded-full bg-amber-500 animate-pulse shadow-[0_0_10px_rgba(245,158,11,0.5)]" />
            <div className="absolute top-[65%] left-[80%] h-3 w-3 rounded-full bg-emerald-500" />
          </div>

          <div className="space-y-4">
            {regions.map((region) => (
              <div key={region.id} className="flex items-center justify-between group">
                <div className="flex items-center gap-3 min-w-0">
                  <div className={cn("h-2 w-2 rounded-full", region.color)} />
                  <span className="text-sm font-medium text-muted-foreground truncate group-hover:text-foreground transition-colors">{region.name}</span>
                </div>
                <div className="flex items-center gap-6">
                  <span className={cn(
                    "text-[10px] font-bold uppercase tracking-wider px-2 py-0.5 rounded-md",
                    region.status === 'Healthy' ? "text-emerald-600 bg-emerald-50" : "text-amber-600 bg-amber-50"
                  )}>
                    {region.status}
                  </span>
                  <span className="text-sm font-bold text-foreground w-8 text-right">{region.utilization}</span>
                </div>
              </div>
            ))}
          </div>
        </div>
      </CardContent>
    </Card>
  )
}
