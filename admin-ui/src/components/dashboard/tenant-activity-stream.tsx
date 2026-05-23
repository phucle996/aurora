import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Button } from "@/components/ui/button"
import { Avatar, AvatarFallback } from "@/components/ui/avatar"

const activities = [
  { tenant: "Acme Corporation", event: "Scaled kubernetes cluster to 12 nodes", time: "5m ago", tag: "Infra" },
  { tenant: "Globex Inc.", event: "Created staging workspace in eu-central-1", time: "21m ago", tag: "Workspace" },
  { tenant: "Initech", event: "Enabled SSO policy enforcement", time: "42m ago", tag: "Security" },
  { tenant: "Umbrella Corp", event: "Upgraded to Enterprise annual plan", time: "1h ago", tag: "Billing" },
  { tenant: "Wayne Enterprises", event: "Rotated API keys for production apps", time: "2h ago", tag: "Access" },
]

export function TenantActivityStream() {
  return (
    <Card className="h-full border border-border/70 bg-card shadow-sm">
      <CardHeader className="flex flex-row items-center justify-between pb-4">
        <CardTitle className="text-lg font-bold">Tenant Activity Stream</CardTitle>
        <Button variant="link" className="h-auto p-0 text-xs font-bold text-primary hover:no-underline">
          View all
        </Button>
      </CardHeader>
      <CardContent className="space-y-4">
        {activities.map((item) => (
          <div key={`${item.tenant}-${item.time}`} className="flex items-start gap-3">
            <Avatar className="h-8 w-8 border border-border/70">
              <AvatarFallback className="bg-primary/10 text-[11px] font-bold text-primary">
                {item.tenant.slice(0, 2).toUpperCase()}
              </AvatarFallback>
            </Avatar>
            <div className="min-w-0 flex-1 space-y-1">
              <div className="flex items-center justify-between gap-2">
                <p className="truncate text-sm font-bold text-foreground">{item.tenant}</p>
                <span className="shrink-0 text-xs font-medium text-muted-foreground">{item.time}</span>
              </div>
              <p className="text-xs leading-relaxed text-muted-foreground">{item.event}</p>
            </div>
            <span className="rounded-md bg-accent px-2 py-0.5 text-[10px] font-bold uppercase tracking-wider text-accent-foreground">
              {item.tag}
            </span>
          </div>
        ))}
      </CardContent>
    </Card>
  )
}
