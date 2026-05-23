import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Button } from "@/components/ui/button"
import { Database, Server, Globe, Cpu, Activity } from "lucide-react"

const services = [
  { name: "Compute", status: "Operational", uptime: "99.95%", incidents: 0, icon: Cpu },
  { name: "Storage", status: "Operational", uptime: "99.99%", incidents: 0, icon: Server },
  { name: "Kubernetes", status: "Operational", uptime: "99.94%", incidents: 1, icon: Activity },
  { name: "Database", status: "Operational", uptime: "99.96%", incidents: 0, icon: Database },
  { name: "Network", status: "Operational", uptime: "99.97%", incidents: 0, icon: Globe },
]

export function ServiceHealth() {
  return (
    <Card className="col-span-full xl:col-span-1 border border-border/70 bg-card shadow-sm">
      <CardHeader className="flex flex-row items-center justify-between pb-4">
        <CardTitle className="text-lg font-bold">Service Health / Uptime</CardTitle>
        <Button variant="link" className="h-auto p-0 text-xs font-bold text-primary hover:no-underline">View all services</Button>
      </CardHeader>
      <CardContent>
        <div className="space-y-4">
          <div className="grid grid-cols-4 text-[10px] font-bold uppercase tracking-widest text-muted-foreground pb-2">
            <div className="col-span-1">Service</div>
            <div className="text-center">Status</div>
            <div className="text-center">Uptime (30d)</div>
            <div className="text-right">Incidents</div>
          </div>
          <div className="space-y-4">
            {services.map((service) => {
              const Icon = service.icon
              return (
                <div key={service.name} className="grid grid-cols-4 items-center group">
                  <div className="flex items-center gap-3">
                    <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-muted text-muted-foreground transition-colors group-hover:bg-primary/10 group-hover:text-primary">
                      <Icon className="h-4 w-4" />
                    </div>
                    <span className="text-sm font-semibold text-foreground">{service.name}</span>
                  </div>
                  <div className="flex justify-center">
                    <div className="flex items-center gap-1.5 px-2 py-0.5 rounded-full bg-emerald-50 text-emerald-600">
                      <div className="h-1.5 w-1.5 rounded-full bg-emerald-500" />
                      <span className="text-[10px] font-bold uppercase tracking-wider">{service.status}</span>
                    </div>
                  </div>
                  <div className="text-center text-sm font-medium text-muted-foreground">
                    {service.uptime}
                  </div>
                  <div className="text-right text-sm font-bold text-foreground">
                    {service.incidents}
                  </div>
                </div>
              )
            })}
          </div>
        </div>
      </CardContent>
    </Card>
  )
}
