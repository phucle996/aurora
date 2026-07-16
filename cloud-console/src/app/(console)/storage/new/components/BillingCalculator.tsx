import React from "react";
import { DollarSign, Info } from "lucide-react";

interface BillingCalculatorProps {
  quotaGB: number;
}

export function BillingCalculator({ quotaGB }: BillingCalculatorProps) {
  // [COMMENT]: Tính toán chi phí động dựa trên GB nhập vào ($0.015 / GB / tháng)
  const estimatedCost = (quotaGB * 0.015).toFixed(2);

  return (
    <div className="space-y-6 select-none self-start">
      {/* Box tính tiền động (Dynamic Billing Calculator Card) */}
      <div className="bg-card text-card-foreground border border-border rounded-lg shadow-xs p-6 space-y-4">
        <h3 className="text-xs font-bold uppercase tracking-wider text-foreground border-b border-border pb-2 flex items-center gap-1.5">
          <DollarSign className="h-4 w-4 text-emerald-500" />
          <span>Billing Calculator</span>
        </h3>

        <div className="flex flex-col items-center justify-center py-4 bg-emerald-500/5 dark:bg-emerald-500/2 rounded-lg border border-emerald-500/10">
          <span className="text-[10px] font-bold text-emerald-600 dark:text-emerald-400 uppercase tracking-widest">
            Estimated Cost
          </span>
          <div className="flex items-baseline mt-1.5 text-foreground">
            <span className="text-3xl font-extrabold font-mono">{estimatedCost}</span>
            <span className="text-xs font-bold text-muted-foreground ml-1">/ month</span>
          </div>
          <span className="text-[9px] text-muted-foreground/80 mt-1 font-semibold">
            Based on {quotaGB} GB allocated storage
          </span>
        </div>

        {/* Chi tiết bảng giá */}
        <div className="space-y-3 pt-2 text-xs">
          <span className="font-bold text-foreground text-[10px] uppercase tracking-wider text-muted-foreground block">
            Object Storage Rates
          </span>

          <div className="flex flex-col gap-2">
            <div className="flex justify-between items-center border-b border-border/30 pb-2">
              <span className="text-[11px] font-semibold text-muted-foreground">Standard Storage</span>
              <span className="font-semibold text-foreground font-mono">$0.015 / GB / month</span>
            </div>
            <div className="flex justify-between items-center border-b border-border/30 pb-2">
              <span className="text-[11px] font-semibold text-muted-foreground">Inbound Data (Ingress)</span>
              <span className="font-bold text-emerald-500 font-mono">Free</span>
            </div>
            <div className="flex justify-between items-center border-b border-border/30 pb-2">
              <span className="text-[11px] font-semibold text-muted-foreground">Outbound Data (Egress)</span>
              <span className="font-semibold text-foreground font-mono">$0.05 / GB</span>
            </div>
            <div className="flex justify-between items-center border-b border-border/30 pb-2">
              <span className="text-[11px] font-semibold text-muted-foreground">Write Requests (PUT/COPY)</span>
              <span className="font-semibold text-foreground font-mono">$0.005 / 10k reqs</span>
            </div>
            <div className="flex justify-between items-center pb-1">
              <span className="text-[11px] font-semibold text-muted-foreground">Read Requests (GET/SELECT)</span>
              <span className="font-semibold text-foreground font-mono">$0.004 / 10k reqs</span>
            </div>
          </div>
        </div>
      </div>

      {/* Card So sánh chi phí / Lợi ích */}
      <div className="bg-card text-card-foreground border border-border rounded-lg shadow-xs p-6 space-y-3.5">
        <h4 className="text-[10px] font-bold uppercase tracking-wider text-foreground flex items-center gap-1.5">
          <Info className="h-4 w-4 text-blue-500" />
          <span>Why Aurora Storage?</span>
        </h4>
        <p className="text-[11px] text-muted-foreground leading-relaxed font-semibold">
          Our storage charges are calculated pro-rata hourly based on storage volume limit. We do not charge for local internal transfers.
        </p>
        <div className="p-3 bg-muted/40 rounded-lg border border-border/60 text-[10px] leading-relaxed text-muted-foreground font-semibold">
          Compared to standard cloud storage providers ($0.023/GB), Aurora Object Storage saves you up to **35%** on your cloud storage bill.
        </div>
      </div>
    </div>
  );
}
