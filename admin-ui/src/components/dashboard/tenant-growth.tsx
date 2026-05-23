import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Button } from "@/components/ui/button"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { TrendingUp } from "lucide-react"

const growth = [
  { period: "Last 7 Days", active: "1,248", new: "36", churn: "0.9%", trend: "up" },
  { period: "Last 30 Days", active: "1,196", new: "142", churn: "1.1%", trend: "up" },
  { period: "Last 90 Days", active: "1,054", new: "369", churn: "1.0%", trend: "up" },
  { period: "Last 12 Months", active: "864", new: "612", churn: "0.8%", trend: "up" },
]

export function TenantGrowth() {
  return (
    <Card className="col-span-full xl:col-span-1 border border-border/70 bg-card shadow-sm">
      <CardHeader className="flex flex-row items-center justify-between pb-4">
        <CardTitle className="text-lg font-bold">Tenant Growth</CardTitle>
        <Button variant="link" className="h-auto p-0 text-xs font-bold text-primary hover:no-underline">View tenant analytics</Button>
      </CardHeader>
      <CardContent className="px-0">
        <Table>
          <TableHeader>
            <TableRow className="hover:bg-transparent border-border/50">
              <TableHead className="px-6 text-[10px] font-bold uppercase tracking-widest text-muted-foreground h-10">Period</TableHead>
              <TableHead className="text-center text-[10px] font-bold uppercase tracking-widest text-muted-foreground h-10">Active Tenants</TableHead>
              <TableHead className="text-center text-[10px] font-bold uppercase tracking-widest text-muted-foreground h-10">New Tenants</TableHead>
              <TableHead className="pr-6 text-right text-[10px] font-bold uppercase tracking-widest text-muted-foreground h-10">Churn Rate</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {growth.map((item, index) => (
              <TableRow key={index} className="group border-border/40 transition-colors hover:bg-muted/40">
                <TableCell className="px-6 py-4">
                  <span className="text-sm font-semibold text-foreground">{item.period}</span>
                </TableCell>
                <TableCell className="text-center py-4">
                  <span className="text-sm font-medium text-muted-foreground">{item.active}</span>
                </TableCell>
                <TableCell className="text-center py-4">
                  <div className="flex items-center justify-center gap-1.5">
                    <span className="text-sm font-bold text-foreground">{item.new}</span>
                    <TrendingUp className="h-3 w-3 text-emerald-500" />
                  </div>
                </TableCell>
                <TableCell className="pr-6 py-4 text-right">
                  <span className="text-sm font-medium text-muted-foreground">{item.churn}</span>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </CardContent>
    </Card>
  )
}
