import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Button } from "@/components/ui/button"
import { Avatar, AvatarFallback } from "@/components/ui/avatar"

const actions = [
  { id: 1, user: "System Admin", action: "Updated pricing for Business plan", time: "1h ago", initial: "SA" },
  { id: 2, user: "System Admin", action: "Enabled Kubernetes in eu-central-1", time: "3h ago", initial: "SA" },
  { id: 3, user: "System Admin", action: "Resolved Incident INC-4587", time: "5h ago", initial: "SA" },
  { id: 4, user: "System Admin", action: "Added new region ap-southeast-3", time: "1d ago", initial: "SA" },
  { id: 5, user: "System Admin", action: "Updated storage policy rules", time: "2d ago", initial: "SA" },
]

export function AdminActions() {
  return (
    <Card className="col-span-full xl:col-span-1 border border-border/70 bg-card shadow-sm">
      <CardHeader className="flex flex-row items-center justify-between pb-4">
        <CardTitle className="text-lg font-bold">Recent Admin Actions</CardTitle>
        <Button variant="link" className="h-auto p-0 text-xs font-bold text-primary hover:no-underline">View all</Button>
      </CardHeader>
      <CardContent>
        <div className="space-y-5">
          {actions.map((action) => (
            <div key={action.id} className="flex items-start gap-4 group">
              <Avatar className="h-9 w-9 rounded-xl border border-border shadow-sm">
                <AvatarFallback className="bg-primary/5 text-primary text-xs font-bold">{action.initial}</AvatarFallback>
              </Avatar>
              <div className="flex-1 space-y-1 min-w-0">
                <div className="flex items-center justify-between gap-2">
                  <p className="text-sm font-bold text-foreground truncate group-hover:text-primary transition-colors">{action.action}</p>
                  <span className="text-xs font-medium text-muted-foreground shrink-0">{action.time}</span>
                </div>
                <p className="text-xs text-muted-foreground">{action.user}</p>
              </div>
            </div>
          ))}
        </div>
      </CardContent>
    </Card>
  )
}
