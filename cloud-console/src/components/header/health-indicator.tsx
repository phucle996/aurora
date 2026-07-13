"use client";

import React, { useState, useRef, useEffect } from "react";
import { ShieldCheck } from "lucide-react";

export function HealthIndicator() {
  const [healthOpen, setHealthOpen] = useState(false);
  const healthRef = useRef<HTMLDivElement>(null);

  const microserviceHealth = [
    { name: "Control API", status: "Healthy" },
    { name: "Block Storage Service", status: "Healthy" },
    { name: "SDN Network Router", status: "Healthy" },
    { name: "Hypervisor Controller", status: "Healthy" }
  ];

  useEffect(() => {
    const handleClickOutside = (e: MouseEvent) => {
      if (healthRef.current && !healthRef.current.contains(e.target as Node)) {
        setHealthOpen(false);
      }
    };
    document.addEventListener("mousedown", handleClickOutside);
    return () => document.removeEventListener("mousedown", handleClickOutside);
  }, []);

  return (
    <div ref={healthRef} className="relative">
      <button
        onClick={() => setHealthOpen(!healthOpen)}
        className="flex items-center gap-1.5 px-2.5 py-1.5 rounded-lg hover:bg-slate-100 dark:hover:bg-sidebar-console-hover text-xs font-semibold text-emerald-600 dark:text-emerald-400 cursor-pointer transition-colors"
      >
        <span className="h-1.5 w-1.5 rounded-full bg-emerald-500 animate-pulse" />
        <span className="hidden sm:inline">Healthy</span>
      </button>

      {healthOpen && (
        <div className="absolute top-[110%] right-0 w-60 bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-lg shadow-xl p-3.5 z-50 animate-in fade-in slide-in-from-top-1 duration-100">
          <div className="flex items-center gap-1.5 text-xs font-bold text-slate-800 dark:text-slate-200 mb-2 pb-1.5 border-b border-slate-100 dark:border-slate-800">
            <ShieldCheck className="h-4 w-4 text-emerald-500" />
            <span>All Core Services Operational</span>
          </div>
          <div className="space-y-2">
            {microserviceHealth.map((svc) => (
              <div key={svc.name} className="flex justify-between items-center text-[11px]">
                <span className="text-slate-600 dark:text-slate-400">{svc.name}</span>
                <span className="font-semibold text-emerald-500">{svc.status}</span>
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}
