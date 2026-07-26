import React from "react";
import { Loader2, Laptop } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";
import { getUserDevices } from "@/features/iam/devices-api";
import { useQuery } from "@tanstack/react-query";
import { useConsoleQueryScope } from "@/shared/query/scope";
import type { ExtendedUser } from "./UserTable";

type DeviceRecord = Record<string, unknown>;
type DeviceItem = DeviceRecord & {
  device?: DeviceRecord;
  is_online?: boolean;
  last_seen_at?: string | null;
  last_seen_ip?: string | null;
  last_seen_user_agent?: string | null;
};

interface DevicesTabProps {
  selectedUser: ExtendedUser;
}

export function DevicesTab({ selectedUser }: DevicesTabProps) {
  const scope = useConsoleQueryScope();
  // [COMMENT]: Sử dụng useQuery từ TanStack Query để quản lý danh sách thiết bị.
  const {
    data: devicesData = [],
    isLoading: loadingDevices,
  } = useQuery<DeviceItem[]>({
    queryKey: [...scope, "iam", "user-devices", selectedUser?.id],
    queryFn: async () => {
      const res = await getUserDevices(selectedUser.id);
      return res?.items || [];
    },
    enabled: !!selectedUser?.id,
  });

  if (loadingDevices) {
    return (
      <div className="flex items-center justify-center py-10 select-none">
        <Loader2 className="h-5 w-5 animate-spin text-muted-foreground/60" />
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-3 text-xs select-none animate-in fade-in duration-200">
      <div>
        <span className="font-semibold text-foreground block mb-3 text-xs">Registered devices ({devicesData.length})</span>
        {devicesData.length === 0 ? (
          <div className="py-6 text-center text-muted-foreground font-bold">No devices registered.</div>
        ) : (
          <div className="space-y-3">
            {devicesData.map((item, idx) => {
              const dev = item.device || {};
              const isOnline = item.is_online === true;
              const lastSeen = item.last_seen_at || null;
              const ip = item.last_seen_ip || String(dev.LastSeenIP || "Unknown IP");
              const ua = item.last_seen_user_agent || String(dev.LastSeenUserAgent || "Unknown UA");
              const deviceName = String(dev.DeviceName || dev.device_name || "Unknown Device");
              const status = String(dev.Status || dev.status || "Unknown");

              return (
                <div key={idx} className="flex items-center justify-between border-b border-border/30 pb-2.5 last:border-0 last:pb-0">
                  <div className="flex items-center gap-2">
                    <Laptop className="h-4.5 w-4.5 text-muted-foreground" />
                    <div className="flex flex-col">
                      <span className="font-bold text-foreground">
                        {deviceName}
                      </span>
                      <span className="text-[10px] text-muted-foreground font-medium max-w-[200px] truncate block" title={ua}>
                        IP: {ip} • {ua}
                      </span>
                      {lastSeen && (
                        <span className="text-[9px] text-muted-foreground/60 font-semibold mt-0.5">
                          Last Active: {new Date(lastSeen).toLocaleString()}
                        </span>
                      )}
                    </div>
                  </div>
                  <div className="flex items-center gap-1.5">
                    {status === "revoked" ? (
                      <Badge variant="outline" className="bg-red-500/10 text-red-600 border-red-500/20 dark:text-red-400 dark:border-red-500/30 font-extrabold uppercase tracking-wider text-[8px] h-4">
                        Revoked
                      </Badge>
                    ) : (
                      <Badge variant="outline" className={cn(
                        "font-extrabold uppercase tracking-wider text-[8px] h-4 border",
                        isOnline
                          ? "bg-emerald-500/10 text-emerald-600 border-emerald-500/20 dark:text-emerald-400 dark:border-emerald-500/30"
                          : "bg-slate-500/10 text-slate-500 border-slate-500/20 dark:text-slate-400 dark:border-slate-500/30"
                      )}>
                        {isOnline ? "Online" : "Offline"}
                      </Badge>
                    )}
                  </div>
                </div>
              );
            })}
          </div>
        )}
      </div>
    </div>
  );
}
