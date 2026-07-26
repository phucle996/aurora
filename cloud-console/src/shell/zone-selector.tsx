"use client";

import React, { useState, useRef, useEffect } from "react";
import { ChevronDown } from "lucide-react";
import { cn } from "@/lib/utils";
import { fetchZoneCatalog, switchZone, type ZoneCatalogItem } from "@/features/zones/api";
import { toast } from "sonner";
import { useQuery } from "@tanstack/react-query";
import { useUserSession } from "@/session/use-session";

function getCookie(name: string): string | null {
  if (typeof document === "undefined") return null;
  const value = `; ${document.cookie}`;
  const parts = value.split(`; ${name}=`);
  if (parts.length === 2) {
    try {
      return decodeURIComponent(parts.pop()?.split(";").shift() || "") || null;
    } catch {
      return null;
    }
  }
  return null;
}

export function ZoneSelector({ compact = false }: { compact?: boolean }) {
  const [zoneOpen, setZoneOpen] = useState(false);
  const [selectedZoneCode, setSelectedZoneCode] = useState(() => getCookie("zone_code") || "");
  const zoneRef = useRef<HTMLDivElement>(null);
  const { generation } = useUserSession();
  const { data: zones = [] } = useQuery<ZoneCatalogItem[]>({
    queryKey: ["console", generation ?? "anonymous", "zones", "catalog"],
    queryFn: ({ signal }) => fetchZoneCatalog({ signal }),
    staleTime: 15_000,
    refetchInterval: 30_000,
    refetchIntervalInBackground: false,
  });
  const activeZone = zones.find((zone) => zone.code.toLowerCase() === selectedZoneCode.toLowerCase()) ?? zones[0];

  useEffect(() => {
    const handleClickOutside = (e: MouseEvent) => {
      if (zoneRef.current && !zoneRef.current.contains(e.target as Node)) {
        setZoneOpen(false);
      }
    };
    document.addEventListener("mousedown", handleClickOutside);
    return () => document.removeEventListener("mousedown", handleClickOutside);
  }, []);

  return (
    <div ref={zoneRef} className="relative">
      <button
        onClick={() => setZoneOpen(!zoneOpen)}
        className={cn(
          "flex items-center gap-1.5 rounded-[4px] px-2.5 py-1.5 text-xs font-semibold text-slate-700 outline-none transition-colors hover:bg-slate-100 focus-visible:ring-2 focus-visible:ring-blue-500 dark:text-slate-300 dark:hover:bg-sidebar-console-hover",
          compact && "w-full justify-start text-slate-300 hover:bg-sidebar-console-hover",
        )}
      >
        <span className="text-slate-400 dark:text-slate-500 font-normal">Zone:</span>
        <span>{activeZone?.name || "Loading..."}</span>
        <ChevronDown className={cn("h-3 w-3 text-slate-400", compact && "ml-auto")} />
      </button>

      {zoneOpen && zones.length > 0 && (
        <div className="absolute left-0 top-[110%] z-50 w-48 rounded-[6px] border border-slate-200 bg-white py-1 shadow-sm dark:border-slate-800 dark:bg-slate-900">
          {zones.map((z) => (
            <button
              key={z.code}
              onClick={async () => {
                if (z.code === activeZone?.code) return;
                try {
                  await switchZone(z.code);
                  setSelectedZoneCode(z.code);
                  setZoneOpen(false);
                  toast.success(`Switched to zone: ${z.name}`);
                  window.location.reload();
                } catch (err: unknown) {
                  toast.error(err instanceof Error ? err.message : "Failed to switch active zone");
                }
              }}
              className={cn(
                "w-full text-left px-3 py-2 text-xs font-semibold text-slate-700 dark:text-slate-300",
                z.code === activeZone?.code
                  ? "text-blue-500 dark:text-blue-400 bg-slate-50 dark:bg-slate-800/40 cursor-default"
                  : "hover:bg-slate-50 dark:hover:bg-slate-800/50 cursor-pointer"
              )}
            >
              <div className="font-bold">{z.name}</div>
              <div className="text-[10px] text-slate-400 font-mono">{z.code}</div>
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
