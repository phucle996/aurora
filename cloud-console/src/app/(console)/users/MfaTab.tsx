import React from "react";
import { Loader2, Fingerprint } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";
import { getUserMfaPlatform, type PlatformMfaStatus } from "@/lib/api/mfa";
import { useQuery } from "@tanstack/react-query";

interface MfaTabProps {
  selectedUser: any;
}

export function MfaTab({ selectedUser }: MfaTabProps) {
  // [COMMENT]: Sử dụng useQuery từ TanStack Query để quản lý thông tin MFA status.
  const {
    data: mfaData = null,
    isLoading: loadingMfa,
  } = useQuery<PlatformMfaStatus | null>({
    queryKey: ["userMfa", selectedUser?.id],
    queryFn: () => getUserMfaPlatform(selectedUser.id),
    enabled: !!selectedUser?.id,
  });

  if (loadingMfa) {
    return (
      <div className="flex flex-col items-center justify-center py-12 select-none text-muted-foreground gap-2">
        <Loader2 className="h-5 w-5 animate-spin text-blue-500" />
        <span className="text-[11px] font-semibold">Checking MFA configurations...</span>
      </div>
    );
  }

  const isMfaEnabled = mfaData ? mfaData.mfa_enabled : false;

  return (
    <div className="flex flex-col gap-3 text-xs select-none animate-in fade-in duration-200">
      <div>
        <span className="font-semibold text-foreground block mb-3 text-xs">MFA authentication</span>
        <div className="flex items-start gap-2.5 mb-4">
          <Fingerprint className="h-6 w-6 text-blue-500 mt-0.5" />
          <div className="flex-1">
            <span className="font-bold text-foreground block text-[11px] uppercase">Time-based One-time Password (TOTP)</span>
            <p className="text-[11px] text-foreground/80 mt-1 leading-normal font-semibold">
              Secures user authentication by requiring an additional token code from a mobile authenticator app.
            </p>
          </div>
        </div>

        <div className="flex items-center justify-between border-t border-border/40 pt-3">
          <div className="flex flex-col">
            <span className="text-muted-foreground font-semibold text-[10px]">MFA status</span>
            {isMfaEnabled && mfaData?.created_at && (
              <span className="text-[9px] text-muted-foreground/80 mt-0.5 font-semibold">
                Enabled: {new Date(mfaData.created_at).toLocaleString()}
              </span>
            )}
          </div>
          <Badge variant="outline" className={cn(
            "inline-flex items-center gap-1 px-2 py-0.5 rounded-md text-[10px] font-extrabold tracking-wider border h-5",
            isMfaEnabled
              ? "bg-emerald-500/10 text-emerald-655 dark:text-emerald-450 dark:border-emerald-500/30"
              : "bg-muted text-muted-foreground border-border"
          )}>
            {isMfaEnabled ? "ENABLED" : "DISABLED"}
          </Badge>
        </div>
      </div>
    </div>
  );
}
