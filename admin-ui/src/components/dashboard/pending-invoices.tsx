import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Button } from "@/components/ui/button"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"

const pending = [
  { tenant: "Stark Industries", invoices: 3, amount: "$48,210.00", overdue: "7 days" },
  { tenant: "Wayne Enterprises", invoices: 2, amount: "$32,450.00", overdue: "5 days" },
  { tenant: "Cyberdyne Systems", invoices: 2, amount: "$21,780.00", overdue: "3 days" },
  { tenant: "Hooli LLC", invoices: 1, amount: "$12,490.00", overdue: "2 days" },
  { tenant: "Oscorp", invoices: 1, amount: "$9,220.00", overdue: "1 day" },
]

export function PendingInvoices() {
  return (
    <Card className="h-full border border-border/70 bg-card shadow-sm">
      <CardHeader className="flex flex-row items-center justify-between pb-4">
        <CardTitle className="text-lg font-bold">Pending Invoices</CardTitle>
        <Button variant="link" className="h-auto p-0 text-xs font-bold text-primary hover:no-underline">
          View all
        </Button>
      </CardHeader>
      <CardContent className="px-0">
        <Table>
          <TableHeader>
            <TableRow className="hover:bg-transparent">
              <TableHead className="px-5 text-[10px] font-bold uppercase tracking-widest text-muted-foreground">Tenant</TableHead>
              <TableHead className="text-center text-[10px] font-bold uppercase tracking-widest text-muted-foreground">Invoices</TableHead>
              <TableHead className="text-right text-[10px] font-bold uppercase tracking-widest text-muted-foreground">Amount</TableHead>
              <TableHead className="pr-5 text-right text-[10px] font-bold uppercase tracking-widest text-muted-foreground">Overdue</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {pending.map((row) => (
              <TableRow key={row.tenant}>
                <TableCell className="px-5 py-3 text-sm font-semibold text-foreground">{row.tenant}</TableCell>
                <TableCell className="py-3 text-center text-sm font-medium text-muted-foreground">{row.invoices}</TableCell>
                <TableCell className="py-3 text-right text-sm font-bold text-foreground">{row.amount}</TableCell>
                <TableCell className="pr-5 py-3 text-right text-xs font-medium text-muted-foreground">{row.overdue}</TableCell>
              </TableRow>
            ))}
            <TableRow className="bg-muted/30 hover:bg-muted/30">
              <TableCell className="px-5 py-3 text-sm font-bold text-foreground">Total</TableCell>
              <TableCell className="py-3 text-center text-sm font-bold text-foreground">9</TableCell>
              <TableCell className="py-3 text-right text-sm font-black text-foreground">$124,150.00</TableCell>
              <TableCell className="pr-5 py-3" />
            </TableRow>
          </TableBody>
        </Table>
      </CardContent>
    </Card>
  )
}
