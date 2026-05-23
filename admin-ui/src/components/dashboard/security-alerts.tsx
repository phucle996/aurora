import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Button } from "@/components/ui/button"
import { ShieldAlert, Info } from "lucide-react"
import { cn } from "@/lib/utils"

const alerts = [
  { id: 1, type: "high", message: "Unusual login activity detected", level: "High", time: "15m ago", icon: ShieldAlert, color: "text-rose-600", bg: "bg-rose-50" },
  { id: 2, type: "medium", message: "Payment failure rate increase", level: "Medium", time: "1h ago", icon: ShieldAlert, color: "text-amber-600", bg: "bg-amber-50" },
  { id: 3, type: "high", message: "Suspicious API usage pattern", level: "High", time: "2h ago", icon: ShieldAlert, color: "text-rose-600", bg: "bg-rose-50" },
  { id: 4, type: "medium", message: "Multiple failed payment attempts", level: "Medium", time: "5h ago", icon: ShieldAlert, color: "text-amber-600", bg: "bg-amber-50" },
  { id: 5, type: "low", message: "New device from unknown location", level: "Low", time: "2d ago", icon: Info, color: "text-blue-600", bg: "bg-blue-50" },
]

export function SecurityAlerts() {
  return (
    <Card className="col-span-full xl:col-span-1 border border-border/70 bg-card shadow-sm">
      <CardHeader className="flex flex-row items-center justify-between pb-4">
        <CardTitle className="text-lg font-bold">Fraud / Risk Alerts</CardTitle>
        <Button variant="link" className="h-auto p-0 text-xs font-bold text-primary hover:no-underline">View all</Button>
      </CardHeader>
      <CardContent>
        <div className="space-y-4">
          {alerts.map((alert) => {
            const Icon = alert.icon
            return (
              <div key={alert.id} className="flex items-center justify-between group">
                <div className="flex items-center gap-3">
                  <div className={cn("flex h-8 w-8 items-center justify-center rounded-lg", alert.bg, alert.color)}>
                    <Icon className="h-4 w-4" />
                  </div>
                  <span className="text-sm font-semibold text-foreground group-hover:text-primary transition-colors">{alert.message}</span>
                </div>
                <div className="flex items-center gap-4">
                  <span className={cn(
                    "text-[10px] font-bold uppercase tracking-wider px-2 py-0.5 rounded-md",
                    alert.type === 'high' ? "text-rose-600 bg-rose-50" : alert.type === 'medium' ? "text-amber-600 bg-amber-50" : "text-blue-600 bg-blue-50"
                  )}>
                    {alert.level}
                  </span>
                  <span className="text-xs font-medium text-muted-foreground w-12 text-right">{alert.time}</span>
                </div>
              </div>
            )
          })}
        </div>
      </CardContent>
    </Card>
  )
}
