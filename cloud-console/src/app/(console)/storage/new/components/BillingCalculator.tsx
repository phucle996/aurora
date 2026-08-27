"use client";

import React, { useEffect, useMemo, useState } from "react";
import { DollarSign, Info, Loader2 } from "lucide-react";
import { useQuery } from "@tanstack/react-query";

import { useConsoleQueryScope } from "@/shared/query/scope";
import { getStorageEstimate, type StorageEstimate } from "@/features/billing/api";
import { formatMicroUnits } from "@/features/billing/money";

interface BillingCalculatorProps {
  quotaGB: number;
}

const BYTES_PER_GB = 1_000_000_000;

function quotaBytes(quotaGB: number): string | null {
  if (!Number.isSafeInteger(quotaGB) || quotaGB <= 0) return null;
  const bytes = quotaGB * BYTES_PER_GB;
  return Number.isSafeInteger(bytes) ? String(bytes) : null;
}

export function BillingCalculator({ quotaGB }: BillingCalculatorProps) {
  const scope = useConsoleQueryScope();
  const requestedBytes = useMemo(() => quotaBytes(quotaGB), [quotaGB]);
  const [debouncedBytes, setDebouncedBytes] = useState<string | null>(requestedBytes);

  // Debounce typing so a large quota edit does not create one Cost request per keystroke.
  useEffect(() => {
    const timer = window.setTimeout(() => setDebouncedBytes(requestedBytes), 250);
    return () => window.clearTimeout(timer);
  }, [requestedBytes]);

  const estimateQuery = useQuery<StorageEstimate>({
    queryKey: [...scope, "billing", "storage-estimate", debouncedBytes ?? "invalid"],
    queryFn: ({ signal }) => getStorageEstimate(debouncedBytes as string, signal),
    enabled: debouncedBytes !== null,
    staleTime: 30_000,
    retry: false,
  });

  const estimate = estimateQuery.data;
  const amount = estimate ? formatMicroUnits(estimate.hourly_estimate_micro_units, estimate.currency) : null;
  const unavailable = estimateQuery.isError;
  const stale = estimateQuery.isError && Boolean(estimate);

  return (
    <div className="space-y-6 select-none self-start">
      <div className="bg-card text-card-foreground border border-border rounded-lg shadow-xs p-6 space-y-4">
        <h3 className="text-xs font-bold uppercase tracking-wider text-foreground border-b border-border pb-2 flex items-center gap-1.5">
          <DollarSign className="h-4 w-4 text-emerald-500" />
          <span>Billing Estimate</span>
        </h3>

        <div className="flex flex-col items-center justify-center py-4 bg-emerald-500/5 dark:bg-emerald-500/2 rounded-lg border border-emerald-500/10">
          <span className="text-[10px] font-bold text-emerald-600 dark:text-emerald-400 uppercase tracking-widest">
            Estimated Storage Cost
          </span>
          <div className="flex items-baseline mt-1.5 text-foreground">
            {estimateQuery.isPending ? (
              <Loader2 className="h-7 w-7 animate-spin text-muted-foreground" aria-label="Loading estimate" />
            ) : (
              <span className={`text-3xl font-extrabold font-mono ${stale ? "text-amber-600 dark:text-amber-400" : ""}`}>
                {amount ?? (unavailable ? "Unavailable" : "—")}
              </span>
            )}
            <span className="text-xs font-bold text-muted-foreground ml-1">/ hour</span>
          </div>
          <span className="text-[9px] text-muted-foreground/80 mt-1 font-semibold text-center">
            {estimate
              ? `Cost Manager schedule · ${estimate.pricing_schedule_code} · hourly PAYG`
              : `Based on ${quotaGB} GB allocated storage`}
          </span>
        </div>

        <div className="space-y-3 pt-2 text-xs">
          <span className="font-bold text-foreground text-[10px] uppercase tracking-wider text-muted-foreground block">
            Effective Pricing
          </span>
          <div className="flex flex-col gap-2">
            <div className="flex justify-between items-center border-b border-border/30 pb-2 gap-3">
              <span className="text-[11px] font-semibold text-muted-foreground">Pricing schedule</span>
              <span className="font-semibold text-foreground font-mono text-right">
                {estimate ? `${estimate.pricing_schedule_code} · v${estimate.pricing_version}` : "—"}
              </span>
            </div>
            <div className="flex justify-between items-center border-b border-border/30 pb-2 gap-3">
              <span className="text-[11px] font-semibold text-muted-foreground">Estimate source</span>
              <span className="font-semibold text-emerald-600 dark:text-emerald-400 font-mono">Cost Manager API</span>
            </div>
            <div className="flex justify-between items-center pb-1 gap-3">
              <span className="text-[11px] font-semibold text-muted-foreground">Capacity</span>
              <span className="font-semibold text-foreground font-mono">{quotaGB} GB</span>
            </div>
          </div>
        </div>
      </div>

      <div className="bg-card text-card-foreground border border-border rounded-lg shadow-xs p-6 space-y-3.5">
        <h4 className="text-[10px] font-bold uppercase tracking-wider text-foreground flex items-center gap-1.5">
          <Info className="h-4 w-4 text-blue-500" />
          <span>Estimate scope</span>
        </h4>
        <p className="text-[11px] text-muted-foreground leading-relaxed font-semibold">
          This is a read-only estimate from the active Cost pricing snapshot. Final charges use metered usage and the
          immutable pricing version pinned by the billing run.
        </p>
        <div className="p-3 bg-muted/40 rounded-lg border border-border/60 text-[10px] leading-relaxed text-muted-foreground font-semibold">
          Egress, request operations and other usage dimensions are billed separately when metering data is available.
        </div>
      </div>
    </div>
  );
}
