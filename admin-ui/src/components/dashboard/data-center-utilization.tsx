import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Button } from "@/components/ui/button"

const regions = [
  { region: "US East (N. Virginia)", compute: 72, storage: 22, network: 65 },
  { region: "US West (Oregon)", compute: 64, storage: 60, network: 58 },
  { region: "Europe (Frankfurt)", compute: 77, storage: 68, network: 63 },
  { region: "Asia Pacific (Singapore)", compute: 54, storage: 45, network: 50 },
  { region: "Asia Pacific (Tokyo)", compute: 74, storage: 66, network: 61 },
]

function MiniBar({ value }: { value: number }) {
  return (
    <div className="h-1.5 w-full rounded-full bg-muted">
      <div className="h-full rounded-full bg-emerald-500" style={{ width: `${value}%` }} />
    </div>
  )
}

export function DataCenterUtilization() {
  return (
    <Card className="h-full border border-border/70 bg-card shadow-sm">
      <CardHeader className="flex flex-row items-center justify-between pb-4">
        <CardTitle className="text-lg font-bold">Data Center Utilization</CardTitle>
        <Button variant="link" className="h-auto p-0 text-xs font-bold text-primary hover:no-underline">
          View all
        </Button>
      </CardHeader>
      <CardContent className="space-y-3">
        <div className="grid grid-cols-[1.4fr_1fr_1fr_1fr] gap-2 text-[10px] font-bold uppercase tracking-widest text-muted-foreground">
          <span>Region</span>
          <span>Compute</span>
          <span>Storage</span>
          <span>Network</span>
        </div>

        {regions.map((row) => (
          <div key={row.region} className="grid grid-cols-[1.4fr_1fr_1fr_1fr] items-center gap-2">
            <span className="truncate text-xs font-semibold text-foreground">{row.region}</span>

            <div className="space-y-1">
              <MiniBar value={row.compute} />
              <p className="text-[11px] font-semibold text-muted-foreground">{row.compute}%</p>
            </div>

            <div className="space-y-1">
              <MiniBar value={row.storage} />
              <p className="text-[11px] font-semibold text-muted-foreground">{row.storage}%</p>
            </div>

            <div className="space-y-1">
              <MiniBar value={row.network} />
              <p className="text-[11px] font-semibold text-muted-foreground">{row.network}%</p>
            </div>
          </div>
        ))}

        <Button variant="link" className="h-auto p-0 pt-2 text-xs font-bold text-primary hover:no-underline">
          View capacity planning
        </Button>
      </CardContent>
    </Card>
  )
}
