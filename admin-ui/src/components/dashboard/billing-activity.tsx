import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Button } from "@/components/ui/button"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"

const activities = [
  { description: "Invoice Payment", tenant: "Acme Corporation", amount: "$24,850.00", time: "2h ago" },
  { description: "Subscription Upgrade", tenant: "Globex Inc.", amount: "$7,200.00", time: "4h ago" },
  { description: "Invoice Payment", tenant: "Initech", amount: "$3,150.00", time: "6h ago" },
  { description: "Usage Charge (May)", tenant: "Umbrella Corp", amount: "$19,560.10", time: "9h ago" },
  { description: "Subscription Upgrade", tenant: "Soylent Corp", amount: "$4,600.00", time: "12h ago" },
]

export function BillingActivity() {
  return (
    <Card className="col-span-full xl:col-span-1 border border-border/70 bg-card shadow-sm">
      <CardHeader className="flex flex-row items-center justify-between pb-4">
        <CardTitle className="text-lg font-bold">Recent Billing Activity</CardTitle>
        <Button variant="link" className="h-auto p-0 text-xs font-bold text-primary hover:no-underline">View all billing activity</Button>
      </CardHeader>
      <CardContent className="px-0">
        <Table>
          <TableHeader>
            <TableRow className="hover:bg-transparent border-border/50">
              <TableHead className="px-6 text-[10px] font-bold uppercase tracking-widest text-muted-foreground h-10">Description</TableHead>
              <TableHead className="text-[10px] font-bold uppercase tracking-widest text-muted-foreground h-10">Tenant</TableHead>
              <TableHead className="text-[10px] font-bold uppercase tracking-widest text-muted-foreground h-10">Amount</TableHead>
              <TableHead className="pr-6 text-right text-[10px] font-bold uppercase tracking-widest text-muted-foreground h-10">Time</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {activities.map((item, index) => (
              <TableRow key={index} className="group border-border/40 transition-colors hover:bg-muted/40">
                <TableCell className="px-6 py-4">
                  <span className="text-sm font-semibold text-foreground">{item.description}</span>
                </TableCell>
                <TableCell className="py-4">
                  <span className="text-sm font-medium text-muted-foreground">{item.tenant}</span>
                </TableCell>
                <TableCell className="py-4">
                  <span className="text-sm font-bold text-foreground">{item.amount}</span>
                </TableCell>
                <TableCell className="pr-6 py-4 text-right">
                  <span className="text-xs font-medium text-muted-foreground">{item.time}</span>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </CardContent>
    </Card>
  )
}
