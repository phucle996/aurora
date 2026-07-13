"use client";

import React, { useState, useRef, useEffect } from "react";
import { ChevronDown } from "lucide-react";
import { cn } from "@/lib/utils";
import { fetchZoneCatalog, switchZone, type ZoneCatalogItem } from "@/lib/api/zone";
import { toast } from "sonner";

function getCookie(name: string): string | null {
  if (typeof document === "undefined") return null;
  const value = `; ${document.cookie}`;
  const parts = value.split(`; ${name}=`);
  if (parts.length === 2) return parts.pop()?.split(";").shift() || null;
  return null;
}

export function ZoneSelector() {
  const [zoneOpen, setZoneOpen] = useState(false);
  const [activeZone, setActiveZone] = useState("");
  const [zones, setZones] = useState<ZoneCatalogItem[]>([]);
  const zoneRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const handleClickOutside = (e: MouseEvent) => {
      if (zoneRef.current && !zoneRef.current.contains(e.target as Node)) {
        setZoneOpen(false);
      }
    };
    document.addEventListener("mousedown", handleClickOutside);
    return () => document.removeEventListener("mousedown", handleClickOutside);
  }, []);

  useEffect(() => {
    let active = true;
    fetchZoneCatalog()
      .then((data) => {
        if (active && data) {
          setZones(data);
          if (data.length > 0) {
            const currentZoneCode = getCookie("zone_code") || data[0].code;
            const matchedZone = data.find(
              (z) => z.code.toLowerCase() === currentZoneCode.toLowerCase()
            );
            setActiveZone(matchedZone ? matchedZone.name : data[0].name);
          }
        }
      })
      .catch((err) => {
        console.error("[Header] Failed to fetch active zones list:", err);
      });
    return () => {
      active = false;
    };
  }, []);

  return (
    <div ref={zoneRef} className="relative">
      <button
        onClick={() => setZoneOpen(!zoneOpen)}
        className="flex items-center gap-1.5 text-xs font-semibold px-2.5 py-1.5 rounded-lg hover:bg-slate-100 dark:hover:bg-sidebar-console-hover text-slate-700 dark:text-slate-300 cursor-pointer transition-colors"
      >
        <span className="text-slate-400 dark:text-slate-500 font-normal">Zone:</span>
        <span>{activeZone || "Loading..."}</span>
        <ChevronDown className="h-3 w-3 text-slate-400" />
      </button>

      {zoneOpen && zones.length > 0 && (
        <div className="absolute top-[110%] left-0 w-48 bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-lg shadow-xl py-1 z-50 animate-in fade-in slide-in-from-top-1 duration-100">
          {zones.map((z) => (
            <button
              key={z.code}
              onClick={async () => {
                if (z.name === activeZone) return;
                try {
                  await switchZone(z.code);
                  setActiveZone(z.name);
                  setZoneOpen(false);
                  toast.success(`Switched to zone: ${z.name}`);
                  window.location.reload();
                } catch (err: any) {
                  toast.error(err.message || "Failed to switch active zone");
                }
              }}
              className={cn(
                "w-full text-left px-3 py-2 text-xs font-semibold text-slate-700 dark:text-slate-300",
                z.name === activeZone
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
