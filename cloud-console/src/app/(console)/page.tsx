"use client";

import React, { useMemo } from "react";
import {
  Compass,
  Globe,
  Cpu,
  HardDrive,
  Network,
  GitMerge,
  Layers,
  FolderGit,
  Copy,
  Building2,
  LayoutGrid,
  Server,
  Lock,
  UserCheck,
  History,
  Activity,
  AlertCircle,
  Clock,
  Users,
  Settings,
  ChevronLeft,
  ChevronRight,
  ShieldAlert,
  CircleDot
} from "lucide-react";
import { useConsoleLayout } from "./layout";

interface ZoneData {
  name: string;
  code: string;
  region: string;
  status: "operational" | "degraded" | "outage";
  hosts: number;
  cpuUsed: number;
  cpuTotal: number;
}

interface VMData {
  name: string;
  status: "running" | "updating" | "stopped";
  ipAddress: string;
  zone: string;
  flavor: string;
  uptime: string;
}

export default function ConsoleDashboard() {
  // [COMMENT]: Lấy trạng thái activeId từ Context Layout chung
  const { activeId } = useConsoleLayout();

  // [COMMENT]: Mock data chi tiết cho các domain hạ tầng vật lý và mạng
  const mockZones: ZoneData[] = [
    { name: "Vietnam Hanoi Zone 1", code: "vn-hn-1", region: "Hanoi, VN", status: "operational", hosts: 24, cpuUsed: 148, cpuTotal: 256 },
    { name: "Vietnam Danang Zone 1", code: "vn-dn-1", region: "Danang, VN", status: "operational", hosts: 12, cpuUsed: 42, cpuTotal: 128 },
    { name: "Vietnam Saigon Zone 2", code: "vn-sg-2", region: "Saigon, VN", status: "degraded", hosts: 36, cpuUsed: 220, cpuTotal: 512 }
  ];

  const mockVMs: VMData[] = [
    { name: "k8s-control-plane-01", status: "running", ipAddress: "10.240.10.12", zone: "vn-hn-1", flavor: "8 vCPU / 16 GB RAM", uptime: "14d 6h" },
    { name: "postgres-primary-db", status: "running", ipAddress: "10.240.20.4", zone: "vn-hn-1", flavor: "16 vCPU / 32 GB RAM", uptime: "42d 12h" },
    { name: "nginx-ingress-router", status: "running", ipAddress: "10.240.10.2", zone: "vn-sg-2", flavor: "4 vCPU / 8 GB RAM", uptime: "5d 1h" },
    { name: "redis-cache-cluster-01", status: "updating", ipAddress: "10.240.30.15", zone: "vn-dn-1", flavor: "4 vCPU / 8 GB RAM", uptime: "2h 15m" },
    { name: "legacy-audit-worker", status: "stopped", ipAddress: "10.240.40.89", zone: "vn-sg-2", flavor: "2 vCPU / 4 GB RAM", uptime: "Offline" }
  ];

  // [COMMENT]: Hàm render giao diện chính dựa theo Item ID được kích hoạt trong Sidebar
  const renderMainContent = () => {
    switch (activeId) {
      case "overview":
        return (
          <div className="space-y-5">
            {/* [COMMENT]: Dashboard Header - Enterprise style, gọn gàng */}
            <div className="flex flex-col gap-0.5">
              <h1 className="text-xl font-bold tracking-tight text-slate-800 dark:text-slate-100">System Overview</h1>
              <p className="text-xs text-slate-500 dark:text-slate-400">Real-time status and telemetry of Aurora Cloud global control plane.</p>
            </div>

            {/* [COMMENT]: Khối thống kê KPI Cards - Sử dụng nền bg-card, bo góc 4px (rounded-[4px]) và loại bỏ hoàn toàn shadow */}
            <div className="grid grid-cols-1 md:grid-cols-4 gap-4">
              <div className="p-4 bg-card border border-border rounded-[4px] space-y-2">
                <div className="flex justify-between items-center text-slate-550 dark:text-slate-450 text-xs font-medium">
                  <span>TOTAL COMPUTE</span>
                  <Cpu className="h-4 w-4 text-blue-500 animate-pulse" />
                </div>
                <div className="space-y-0.5">
                  <div className="text-xl font-bold tracking-tight">410 vCPUs</div>
                  <div className="text-[10px] text-slate-500 dark:text-slate-400">Across 3 active regions</div>
                </div>
              </div>

              <div className="p-4 bg-card border border-border rounded-[4px] space-y-2">
                <div className="flex justify-between items-center text-slate-550 dark:text-slate-450 text-xs font-medium">
                  <span>MEMORY USED</span>
                  <HardDrive className="h-4 w-4 text-indigo-500" />
                </div>
                <div className="space-y-1">
                  <div className="text-xl font-bold tracking-tight">812 GB / 1.2 TB</div>
                  <div className="h-1.5 w-full bg-slate-100 dark:bg-slate-900 rounded-[4px] overflow-hidden mt-1">
                    <div className="h-full bg-indigo-500 rounded-[4px]" style={{ width: "67%" }} />
                  </div>
                </div>
              </div>

              <div className="p-4 bg-card border border-border rounded-[4px] space-y-2">
                <div className="flex justify-between items-center text-slate-550 dark:text-slate-450 text-xs font-medium">
                  <span>VIRTUAL WORKLOADS</span>
                  <Server className="h-4 w-4 text-emerald-500" />
                </div>
                <div className="space-y-0.5">
                  <div className="text-xl font-bold tracking-tight">184 Running</div>
                  <div className="text-[10px] text-emerald-600 dark:text-emerald-400 font-semibold">✔ All hypervisors stable</div>
                </div>
              </div>

              <div className="p-4 bg-card border border-border rounded-[4px] space-y-2">
                <div className="flex justify-between items-center text-slate-550 dark:text-slate-450 text-xs font-medium">
                  <span>GLOBAL NETWORKS</span>
                  <Network className="h-4 w-4 text-sky-500" />
                </div>
                <div className="space-y-0.5">
                  <div className="text-xl font-bold tracking-tight">16 Active VPCs</div>
                  <div className="text-[10px] text-slate-500 dark:text-slate-400">Cross-region SD-WAN online</div>
                </div>
              </div>
            </div>

            {/* [COMMENT]: Khối hiển thị chi tiết Zones & VM Workloads */}
            <div className="grid grid-cols-1 lg:grid-cols-3 gap-4">
              
              {/* Cột trái: Danh sách các Zone hạ tầng, sử dụng nền bg-card, bo góc 4px */}
              <div className="lg:col-span-1 bg-card border border-border rounded-[4px] p-4 space-y-4">
                <div className="flex items-center justify-between">
                  <span className="text-xs font-bold text-slate-800 dark:text-slate-200 uppercase tracking-wider">Physical Zones</span>
                  <span className="flex h-2 w-2 rounded-full bg-emerald-500" />
                </div>
                <div className="space-y-2.5">
                  {mockZones.map((zone) => (
                    <div key={zone.code} className="p-3 border border-border bg-slate-50/40 dark:bg-slate-900/10 rounded-[4px] space-y-2">
                      <div className="flex justify-between items-start">
                        <div>
                          <div className="text-xs font-bold text-slate-800 dark:text-slate-100">{zone.name}</div>
                          <div className="text-[10px] text-slate-500 dark:text-slate-500">{zone.region}</div>
                        </div>
                        {/* [COMMENT]: Sử dụng huy hiệu trạng thái bo góc 4px và thêm icon/ký hiệu hỗ trợ tiếp cận WCAG */}
                        <span className={`px-1.5 py-0.5 rounded-[4px] text-[9px] font-bold capitalize flex items-center gap-1 ${
                          zone.status === "operational" 
                            ? "bg-emerald-500/10 text-emerald-600 dark:text-emerald-400" 
                            : "bg-amber-500/10 text-amber-500"
                        }`}>
                          {zone.status === "operational" ? "✔" : "⚠"} {zone.status}
                        </span>
                      </div>
                      <div className="grid grid-cols-2 gap-2 text-[10px] text-slate-500 dark:text-slate-400 border-t border-border/60 pt-2">
                        <div>Hosts: <strong className="text-slate-700 dark:text-slate-300">{zone.hosts}</strong></div>
                        <div>CPU: <strong className="text-slate-700 dark:text-slate-300">{zone.cpuUsed}/{zone.cpuTotal} Cores</strong></div>
                      </div>
                    </div>
                  ))}
                </div>
              </div>

              {/* Cột phải: Virtual Workloads, sử dụng nền bg-card, bo góc 4px */}
              <div className="lg:col-span-2 bg-card border border-border rounded-[4px] p-4 space-y-4">
                <div className="flex justify-between items-center">
                  <span className="text-xs font-bold text-slate-800 dark:text-slate-200 uppercase tracking-wider">Virtual Workloads</span>
                  <button className="text-[10px] font-bold text-blue-500 hover:underline cursor-pointer">View all instances</button>
                </div>
                <div className="overflow-x-auto">
                  <table className="w-full text-left border-collapse">
                    <thead>
                      <tr className="border-b border-border text-[9px] text-slate-500 dark:text-slate-400 font-bold uppercase">
                        <th className="py-2 w-1/3">Name</th>
                        <th className="py-2">Status</th>
                        <th className="py-2">IP Address</th>
                        <th className="py-2">Zone</th>
                        <th className="py-2">Flavor</th>
                        <th className="py-2 text-right">Uptime</th>
                      </tr>
                    </thead>
                    <tbody className="divide-y divide-border/40 text-xs">
                      {mockVMs.map((vm) => (
                        <tr key={vm.name} className="hover:bg-slate-50/50 dark:hover:bg-slate-900/5 transition-all">
                          <td className="py-2.5 font-semibold text-slate-800 dark:text-slate-200">{vm.name}</td>
                          <td className="py-2.5">
                            {/* [COMMENT]: Biểu tượng trạng thái kết hợp dấu hiệu text và icon tròn để đạt tiêu chuẩn WCAG */}
                            <span className={`inline-flex items-center gap-1 text-[10px] font-bold capitalize ${
                              vm.status === "running" 
                                ? "text-emerald-500" 
                                : vm.status === "updating"
                                ? "text-amber-500"
                                : "text-slate-400"
                            }`}>
                              <span className={`h-1.5 w-1.5 rounded-full ${
                                vm.status === "running" ? "bg-emerald-500" : vm.status === "updating" ? "bg-amber-500 animate-pulse" : "bg-slate-400"
                              }`} />
                              {vm.status}
                            </span>
                          </td>
                          <td className="py-2.5 font-mono text-[9.5px] text-slate-600 dark:text-slate-350">{vm.ipAddress}</td>
                          <td className="py-2.5 font-mono text-[9.5px] text-slate-500">{vm.zone}</td>
                          <td className="py-2.5 text-[10px] text-slate-600 dark:text-slate-400">{vm.flavor}</td>
                          <td className="py-2.5 text-[10px] text-slate-500 text-right">{vm.uptime}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </div>

            </div>
          </div>
        );

      default:
        // [COMMENT]: Trang demo chung cho các menu item chưa phát triển chi tiết, bo góc 4px, không shadow
        return (
          <div className="py-12 bg-card border border-border rounded-[4px] text-center max-w-xl mx-auto space-y-4 select-none">
            <div className="mx-auto flex h-10 w-10 items-center justify-center rounded-[4px] bg-blue-50 dark:bg-blue-950/30 text-blue-600 dark:text-blue-400">
              <Lock className="h-5 w-5" />
            </div>
            <div className="space-y-1.5 px-6">
              <h2 className="text-sm font-semibold text-slate-800 dark:text-slate-200 capitalize">
                {activeId.replace("-", " ")} Section
              </h2>
              <p className="text-xs text-slate-500 dark:text-slate-450">
                This console tab is part of the placeholder settings. The full logical implementation can be customized under client routing workflows.
              </p>
            </div>
          </div>
        );
    }
  };

  return (
    <div className="space-y-5 flex flex-col min-h-[calc(100vh-64px)]">
      <div className="flex-1">
        {renderMainContent()}
      </div>

      {/* [COMMENT]: Footer chân trang - Sử dụng border-border */}
      <footer className="border-t border-border py-3 text-center md:text-left md:flex md:justify-between text-[10px] text-slate-500 bg-transparent shrink-0 mt-6 select-none">
        <span>© 2026 Aurora Cloud Services. All rights reserved.</span>
        <div className="flex justify-center gap-4 mt-2 md:mt-0">
          <a href="#" className="hover:text-slate-850 dark:hover:text-slate-350 transition-colors">Privacy Policy</a>
          <span>·</span>
          <a href="#" className="hover:text-slate-850 dark:hover:text-slate-350 transition-colors">Service Agreement</a>
          <span>·</span>
          <a href="#" className="hover:text-slate-850 dark:hover:text-slate-350 transition-colors">Kubernetes API Docs</a>
        </div>
      </footer>
    </div>
  );
}
