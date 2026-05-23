import { TrendingUp, TrendingDown, DollarSign, Users, Activity, PieChart, ShieldAlert, Cloud } from 'lucide-react'
import { Card, CardContent } from "@/components/ui/card"
import { cn } from "@/lib/utils"

interface StatCardProps {
  title: string
  value: string
  change: string
  trend: 'up' | 'down' | 'neutral'
  icon: React.ElementType
  iconColor: string
  iconBg: string
}

function StatCard({ title, value, change, trend, icon: Icon, iconColor, iconBg }: StatCardProps) {
  const isPositive = trend === 'up'
  const isNegative = trend === 'down'

  return (
    <Card className="border border-border/70 bg-card shadow-sm">
      <CardContent className="p-6">
        <div className="flex items-center justify-between">
          <div className={cn("flex h-12 w-12 items-center justify-center rounded-xl", iconBg)}>
            <Icon className={cn("h-6 w-6", iconColor)} />
          </div>
        </div>
        <div className="mt-4 space-y-1">
          <p className="text-sm font-medium text-muted-foreground">{title}</p>
          <div className="flex items-baseline gap-2">
            <h3 className="text-2xl font-bold tracking-tight text-foreground">{value}</h3>
          </div>
        </div>
        <div className="mt-4 flex items-center gap-2">
          <div className={cn(
            "flex items-center gap-0.5 rounded-full px-2 py-0.5 text-xs font-semibold",
            isPositive ? "bg-emerald-50 text-emerald-600" : isNegative ? "bg-rose-50 text-rose-600" : "bg-muted text-muted-foreground"
          )}>
            {isPositive ? <TrendingUp className="h-3 w-3" /> : isNegative ? <TrendingDown className="h-3 w-3" /> : null}
            {change}
          </div>
          <span className="text-xs text-muted-foreground whitespace-nowrap">vs last month</span>
        </div>
      </CardContent>
    </Card>
  )
}

export function StatsOverview() {
  return (
    <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-6">
      <StatCard
        title="Total Revenue"
        value="$12.48M"
        change="18.6%"
        trend="up"
        icon={DollarSign}
        iconColor="text-emerald-600"
        iconBg="bg-emerald-50"
      />
      <StatCard
        title="Monthly Recurring"
        value="$8.31M"
        change="16.4%"
        trend="up"
        icon={Activity}
        iconColor="text-blue-600"
        iconBg="bg-blue-50"
      />
      <StatCard
        title="Active Tenants"
        value="1,248"
        change="9.3%"
        trend="up"
        icon={Users}
        iconColor="text-purple-600"
        iconBg="bg-purple-50"
      />
      <StatCard
        title="Cloud Usage Spend"
        value="$5.67M"
        change="14.2%"
        trend="up"
        icon={Cloud}
        iconColor="text-sky-600"
        iconBg="bg-sky-50"
      />
      <StatCard
        title="Gross Profit Margin"
        value="62.7%"
        change="2.1pp"
        trend="up"
        icon={PieChart}
        iconColor="text-teal-600"
        iconBg="bg-teal-50"
      />
      <StatCard
        title="Open Incidents"
        value="23"
        change="+4"
        trend="down"
        icon={ShieldAlert}
        iconColor="text-rose-600"
        iconBg="bg-rose-50"
      />
    </div>
  )
}
