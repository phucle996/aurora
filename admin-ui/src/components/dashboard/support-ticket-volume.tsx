import { Pie, PieChart, ResponsiveContainer, Cell } from "recharts"

import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Button } from "@/components/ui/button"
import { Clock3 } from "lucide-react"

const ticketData = [
  { name: "Open", value: 312, percent: "24%", color: "#ef4444" },
  { name: "In Progress", value: 487, percent: "38%", color: "#3b82f6" },
  { name: "Waiting on Customer", value: 238, percent: "19%", color: "#eab308" },
  { name: "Resolved", value: 250, percent: "19%", color: "#10b981" },
]

export function SupportTicketVolume() {
  return (
    <Card className="h-full border border-border/70 bg-card shadow-sm">
      <CardHeader className="flex flex-row items-center justify-between pb-4">
        <CardTitle className="text-lg font-bold">Support Ticket Volume</CardTitle>
        <Button variant="link" className="h-auto p-0 text-xs font-bold text-primary hover:no-underline">
          View all
        </Button>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="flex flex-col items-center gap-4">
          <div className="relative h-[140px] w-[140px]">
            <ResponsiveContainer width="100%" height="100%">
              <PieChart>
                <Pie
                  data={ticketData}
                  dataKey="value"
                  innerRadius={44}
                  outerRadius={68}
                  stroke="none"
                  paddingAngle={2}
                >
                  {ticketData.map((item) => (
                    <Cell key={item.name} fill={item.color} />
                  ))}
                </Pie>
              </PieChart>
            </ResponsiveContainer>
            <div className="pointer-events-none absolute inset-0 flex flex-col items-center justify-center">
              <span className="text-2xl font-black text-foreground">1,287</span>
              <span className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">Tickets</span>
            </div>
          </div>

          <div className="w-full space-y-2.5">
            {ticketData.map((item) => (
              <div key={item.name} className="grid grid-cols-[auto_1fr_auto] items-center gap-2 text-sm">
                <span className="h-2.5 w-2.5 rounded-full" style={{ backgroundColor: item.color }} />
                <span className="truncate font-medium text-muted-foreground">{item.name}</span>
                <span className="font-semibold text-foreground">
                  {item.value} ({item.percent})
                </span>
              </div>
            ))}
          </div>
        </div>

        <div className="flex items-center gap-2 border-t border-border/70 pt-3 text-xs text-muted-foreground">
          <Clock3 className="h-3.5 w-3.5" />
          Avg. first response: <span className="font-semibold text-foreground">1h 32m</span>
        </div>
      </CardContent>
    </Card>
  )
}
