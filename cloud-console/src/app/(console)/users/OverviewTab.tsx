import React from "react";
import { Check } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";

interface OverviewTabProps {
  selectedUser: any;
}

export function OverviewTab({ selectedUser }: OverviewTabProps) {
  return (
    <div className="flex flex-col gap-5 text-xs select-none">
      <div className="flex flex-col gap-3">
        <span className="font-semibold text-foreground text-xs block mb-1">Basic information</span>

        <div className="flex flex-col gap-2">
          <div className="flex justify-between items-center border-b border-border/30 py-2 gap-4">
            <span className="text-[11px] font-semibold text-muted-foreground shrink-0">User ID</span>
            <span className="font-mono text-[11px] text-foreground select-all font-semibold whitespace-nowrap text-right min-w-0">{selectedUser.id}</span>
          </div>

          <div className="flex justify-between items-center border-b border-border/30 py-2 gap-4">
            <span className="text-[11px] font-semibold text-muted-foreground shrink-0">Email</span>
            <div className="flex items-center gap-1.5 min-w-0 justify-end">
              <span className="font-semibold text-foreground truncate select-all">{selectedUser.email}</span>
              <Badge variant="outline" className="bg-emerald-500/10 text-emerald-600 border-emerald-500/20 dark:text-emerald-400 dark:border-emerald-500/30 font-extrabold uppercase tracking-wider text-[9px] h-4 shrink-0">
                <Check className="h-2.5 w-2.5 font-bold" />
                Verified
              </Badge>
            </div>
          </div>
          <div className="flex justify-between items-center border-b border-border/30 py-2">
            <span className="text-[11px] font-semibold text-muted-foreground">Full name</span>
            <span className="font-semibold text-foreground">{selectedUser.ext.fullname}</span>
          </div>
          <div className="flex justify-between items-center border-b border-border/30 py-2">
            <span className="text-[11px] font-semibold text-muted-foreground">Status</span>
            <span className={cn(
              "capitalize font-bold text-[10px]",
              selectedUser.status === "active"
                ? "text-emerald-600 dark:text-emerald-450"
                : selectedUser.status === "pending-active"
                  ? "text-amber-600 dark:text-amber-450"
                  : "text-red-655 dark:text-red-400"
            )}>
              {selectedUser.status === "pending-active" ? "pending" : selectedUser.status}
            </span>
          </div>

          <div className="flex flex-col pt-2">
            <span className="text-[11px] font-semibold text-muted-foreground mb-1">Bio</span>
            <p className="text-[11px] text-foreground/80 leading-normal font-medium whitespace-pre-wrap">
              {selectedUser.bio || "No description provided."}
            </p>
          </div>
        </div>
      </div>
    </div>
  );
}
