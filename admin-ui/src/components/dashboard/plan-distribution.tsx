import { Pie, PieChart, ResponsiveContainer, Cell, Tooltip } from "recharts"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Button } from "@/components/ui/button"

const data = [
  { name: "Enterprise", value: 42.3, count: 528, color: "#2563EB" },
  { name: "Business", value: 31.8, count: 397, color: "#3B82F6" },
  { name: "Standard", value: 17.6, count: 219, color: "#60A5FA" },
  { name: "Developer", value: 6.0, count: 75, color: "#93C5FD" },
  { name: "Free Tier", value: 2.3, count: 29, color: "#BFDBFE" },
]

export function PlanDistribution() {
  return (
    <Card className="col-span-full lg:col-span-1 border border-border/70 bg-card shadow-sm">
      <CardHeader className="flex flex-row items-center justify-between pb-2">
        <CardTitle className="text-lg font-bold">Subscription Plan Distribution</CardTitle>
      </CardHeader>
      <CardContent className="px-6">
        <div className="flex flex-col sm:flex-row items-center justify-between gap-8 py-4">
          <div className="relative h-[220px] w-[220px] shrink-0">
            <ResponsiveContainer width="100%" height="100%">
              <PieChart>
                <Pie
                  data={data}
                  cx="50%"
                  cy="50%"
                  innerRadius={65}
                  outerRadius={95}
                  paddingAngle={5}
                  dataKey="value"
                >
                  {data.map((entry, index) => (
                    <Cell key={`cell-${index}`} fill={entry.color} />
                  ))}
                </Pie>
                <Tooltip />
              </PieChart>
            </ResponsiveContainer>
            <div className="absolute inset-0 flex flex-col items-center justify-center">
              <span className="text-3xl font-bold text-foreground">1,248</span>
              <span className="text-xs text-muted-foreground uppercase tracking-wider font-semibold">Tenants</span>
            </div>
          </div>

          <div className="flex-1 w-full space-y-3">
            {data.map((item) => (
              <div key={item.name} className="flex items-center justify-between group">
                <div className="flex items-center gap-2.5">
                  <div className="h-2.5 w-2.5 rounded-full" style={{ backgroundColor: item.color }} />
                  <span className="text-sm font-medium text-muted-foreground group-hover:text-foreground transition-colors">{item.name}</span>
                </div>
                <div className="flex items-center gap-2">
                  <span className="text-sm font-bold text-foreground">{item.value}%</span>
                  <span className="text-xs text-muted-foreground">({item.count})</span>
                </div>
              </div>
            ))}
            <div className="pt-4">
              <Button variant="link" className="h-auto p-0 text-xs font-bold text-primary hover:no-underline">
                View all plans
              </Button>
            </div>
          </div>
        </div>
      </CardContent>
    </Card>
  )
}
