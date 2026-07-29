"use client";

import { useMemo } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Laptop, Loader2, LogOut, RefreshCw, Smartphone } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import {
  getMyDevices,
  logoutOtherDevices,
  revokeMyDevice,
} from "@/features/iam/devices-api";
import { useConsoleQueryScope } from "@/shared/query/scope";

export function DeviceSettingsScreen() {
  const scope = useConsoleQueryScope();
  const queryClient = useQueryClient();
  const queryKey = useMemo(() => [...scope, "settings", "devices"] as const, [scope]);

  const devicesQuery = useQuery({
    queryKey,
    queryFn: ({ signal }) => getMyDevices(signal),
    staleTime: 10_000,
  });

  const revokeMutation = useMutation({
    mutationFn: revokeMyDevice,
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey });
      toast.success("Device access revoked");
    },
    onError: (error) => toast.error(error instanceof Error ? error.message : "Device access could not be revoked"),
  });

  const othersMutation = useMutation({
    mutationFn: logoutOtherDevices,
    onSuccess: async (count) => {
      await queryClient.invalidateQueries({ queryKey });
      toast.success(`${count} other session${count === 1 ? "" : "s"} revoked`);
    },
    onError: (error) => toast.error(error instanceof Error ? error.message : "Other devices could not be signed out"),
  });

  if (devicesQuery.isLoading) {
    return <div className="rounded-[8px] border border-border p-12 text-center text-xs text-muted-foreground">Loading devices…</div>;
  }
  if (devicesQuery.isError || !devicesQuery.data) {
    return (
      <div className="rounded-[8px] border border-border p-10 text-center">
        <p className="text-sm font-semibold text-red-500">Devices could not be loaded</p>
        <Button className="mt-4" size="sm" variant="outline" onClick={() => void devicesQuery.refetch()}>
          Try again
        </Button>
      </div>
    );
  }

  return (
    <div className="space-y-5">
      <section className="flex flex-col gap-4 rounded-[8px] border border-border bg-card p-4 sm:flex-row sm:items-center sm:justify-between sm:p-5">
        <div>
          <h2 className="text-sm font-semibold">Signed-in devices</h2>
          <p className="mt-1 text-xs text-muted-foreground">
            Revoke sessions you no longer recognize. The current browser cannot revoke itself from this page.
          </p>
        </div>
        <div className="flex gap-2">
          <Button variant="outline" size="sm" disabled={devicesQuery.isRefetching} onClick={() => void devicesQuery.refetch()}>
            <RefreshCw className={devicesQuery.isRefetching ? "animate-spin" : undefined} />
            Refresh
          </Button>
          <Button
            variant="outline"
            size="sm"
            disabled={othersMutation.isPending || devicesQuery.data.items.every((device) => device.is_current || device.status === "revoked")}
            onClick={() => othersMutation.mutate()}
          >
            {othersMutation.isPending ? <Loader2 className="animate-spin" /> : <LogOut />}
            Sign out others
          </Button>
        </div>
      </section>

      <div className="overflow-hidden rounded-[8px] border border-border bg-card">
        {devicesQuery.data.items.length === 0 ? (
          <div className="p-12 text-center text-xs text-muted-foreground">No device records are available.</div>
        ) : (
          devicesQuery.data.items.map((device, index) => {
            const current = device.is_current;
            const mobile = /mobile|android|iphone/i.test(device.last_seen_user_agent ?? "");
            return (
              <div
                key={device.id}
                className={`flex flex-col gap-4 p-4 sm:flex-row sm:items-center sm:p-5 ${index > 0 ? "border-t border-border" : ""}`}
              >
                <div className="flex min-w-0 flex-1 items-start gap-3">
                  <div className="rounded-[6px] border border-border bg-muted/30 p-2 text-muted-foreground">
                    {mobile ? <Smartphone className="h-5 w-5" /> : <Laptop className="h-5 w-5" />}
                  </div>
                  <div className="min-w-0">
                    <div className="flex flex-wrap items-center gap-2">
                      <h3 className="text-sm font-semibold">{device.device_name || "Unknown device"}</h3>
                      {current && <span className="rounded-full bg-blue-500/10 px-2 py-0.5 text-[10px] font-bold text-blue-600">Current</span>}
                      <span className={device.status === "online" ? "rounded-full bg-emerald-500/10 px-2 py-0.5 text-[10px] font-bold text-emerald-600" : "rounded-full bg-muted px-2 py-0.5 text-[10px] font-bold text-muted-foreground"}>
                        {device.status}
                      </span>
                    </div>
                    <p className="mt-1 truncate text-xs text-muted-foreground">{device.last_seen_user_agent || "User agent unavailable"}</p>
                    <p className="mt-1 text-[11px] text-muted-foreground">
                      {device.last_seen_ip || "IP unavailable"}
                      {device.last_seen_at ? ` · Last seen ${new Date(device.last_seen_at).toLocaleString()}` : ""}
                    </p>
                  </div>
                </div>
                <Button
                  variant="outline"
                  disabled={current || device.status === "revoked" || revokeMutation.isPending}
                  onClick={() => revokeMutation.mutate(device.id)}
                >
                  {revokeMutation.isPending && revokeMutation.variables === device.id ? <Loader2 className="animate-spin" /> : <LogOut />}
                  Revoke
                </Button>
              </div>
            );
          })
        )}
      </div>
    </div>
  );
}
