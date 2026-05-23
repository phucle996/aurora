import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Button } from "@/components/ui/button"

const budgets = [
  { service: "API Gateway", remaining: 84, burn: "Low" },
  { service: "Auth Service", remaining: 73, burn: "Low" },
  { service: "Billing Engine", remaining: 59, burn: "Medium" },
  { service: "Realtime Events", remaining: 42, burn: "Medium" },
  { service: "Storage API", remaining: 28, burn: "High" },
]

function BurnBadge({ burn }: { burn: string }) {
  if (burn === "High") {
    return <span className="rounded-md bg-rose-50 px-2 py-0.5 text-[10px] font-bold uppercase tracking-wider text-rose-600">High</span>
  }
  if (burn === "Medium") {
    return <span className="rounded-md bg-amber-50 px-2 py-0.5 text-[10px] font-bold uppercase tracking-wider text-amber-600">Medium</span>
  }
  return <span className="rounded-md bg-emerald-50 px-2 py-0.5 text-[10px] font-bold uppercase tracking-wider text-emerald-600">Low</span>
}

export function SLOErrorBudget() {
  return (
    <Card className="h-full border border-border/70 bg-card shadow-sm">
      <CardHeader className="flex flex-row items-center justify-between pb-4">
        <CardTitle className="text-lg font-bold">SLO / Error Budget</CardTitle>
        <Button variant="link" className="h-auto p-0 text-xs font-bold text-primary hover:no-underline">
          View all SLOs
        </Button>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="grid grid-cols-[1.3fr_1fr_auto] gap-2 text-[10px] font-bold uppercase tracking-widest text-muted-foreground">
          <span>Service</span>
          <span>Budget Remaining</span>
          <span>Burn</span>
        </div>

        {budgets.map((row) => (
          <div key={row.service} className="grid grid-cols-[1.3fr_1fr_auto] items-center gap-2">
            <span className="truncate text-sm font-semibold text-foreground">{row.service}</span>

            <div className="space-y-1">
              <div className="h-2 w-full rounded-full bg-muted">
                <div
                  className="h-full rounded-full bg-primary transition-all"
                  style={{ width: `${row.remaining}%` }}
                />
              </div>
              <span className="text-[11px] font-medium text-muted-foreground">{row.remaining}%</span>
            </div>

            <BurnBadge burn={row.burn} />
          </div>
        ))}
      </CardContent>
    </Card>
  )
}
