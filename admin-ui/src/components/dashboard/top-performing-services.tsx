import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Button } from "@/components/ui/button"

const services = [
  { name: "Compute", revenue: "$4.25M", ratio: 84 },
  { name: "Storage", revenue: "$2.31M", ratio: 58 },
  { name: "Database", revenue: "$1.64M", ratio: 42 },
  { name: "Kubernetes", revenue: "$1.28M", ratio: 31 },
  { name: "Network", revenue: "$0.71M", ratio: 18 },
]

export function TopPerformingServices() {
  return (
    <Card className="h-full border border-border/70 bg-card shadow-sm">
      <CardHeader className="flex flex-row items-center justify-between pb-4">
        <CardTitle className="text-lg font-bold">Top Performing Services</CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="grid grid-cols-[1fr_auto] gap-2 text-[10px] font-bold uppercase tracking-widest text-muted-foreground">
          <span>Service</span>
          <span>Revenue (MTD)</span>
        </div>

        <div className="space-y-3">
          {services.map((item) => (
            <div key={item.name} className="space-y-1.5">
              <div className="flex items-center justify-between gap-2">
                <span className="text-sm font-semibold text-foreground">{item.name}</span>
                <span className="text-sm font-bold text-foreground">{item.revenue}</span>
              </div>
              <div className="h-1.5 w-full rounded-full bg-muted">
                <div
                  className="h-full rounded-full bg-primary transition-all"
                  style={{ width: `${item.ratio}%` }}
                />
              </div>
            </div>
          ))}
        </div>

        <Button variant="link" className="h-auto p-0 text-xs font-bold text-primary hover:no-underline">
          View all services
        </Button>
      </CardContent>
    </Card>
  )
}
