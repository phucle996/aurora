"use client";

import React, { useState, useEffect } from "react";
import { useUserSession } from "@/hooks/useUserSession";
import SidebarConsole from "@/components/sidebar-console";
import HeaderConsole from "@/components/header-console";
import CommandPalette from "@/components/command-palette";
import {
  Activity,
  Cpu,
  Database,
  Layers,
  Plus,
  RefreshCw,
  ShieldAlert,
  Terminal,
  Server,
  Zap,
  Globe,
  Network
} from "lucide-react";
import { Button } from "@/components/ui/button";

// [COMMENT]: Khai báo interfaces cho dữ liệu mockup, tạo tính nhất quán cho các bảng dữ liệu Enterprise
interface ZoneData {
  name: string;
  code: string;
  region: string;
  status: "operational" | "maintenance" | "degraded";
  hosts: number;
  cpuUsed: number;
  cpuTotal: number;
}

interface VMData {
  name: string;
  status: "running" | "stopped" | "updating" | "error";
  ipAddress: string;
  zone: string;
  flavor: string;
  uptime: string;
}

// [COMMENT]: Giao diện Skeleton Loading cao cấp khi chờ tải phiên làm việc
function ConsoleSkeleton() {
  return (
    <div className="flex min-h-screen bg-[#F8FAFC] dark:bg-[#070A13]">
      {/* Sidebar Skeleton */}
      <div className="w-[272px] border-r border-slate-200 dark:border-slate-800/40 p-6 space-y-6 hidden md:block bg-white dark:bg-[#0B0F19]">
        <div className="flex items-center gap-3 animate-pulse">
          <div className="h-8 w-8 rounded-lg bg-slate-200 dark:bg-slate-800" />
          <div className="space-y-2">
            <div className="h-4 w-24 rounded bg-slate-200 dark:bg-slate-800" />
            <div className="h-3 w-16 rounded bg-slate-200 dark:bg-slate-800" />
          </div>
        </div>
        <div className="space-y-4 pt-8 animate-pulse">
          {Array.from({ length: 6 }).map((_, i) => (
            <div key={i} className="flex items-center gap-3 h-10 px-3 rounded-lg bg-slate-100/50 dark:bg-slate-900/30">
              <div className="h-4 w-4 rounded bg-slate-200 dark:bg-slate-800" />
              <div className="h-4 w-28 rounded bg-slate-200 dark:bg-slate-800" />
            </div>
          ))}
        </div>
      </div>

      {/* Main Panel Skeleton */}
      <div className="flex-1 flex flex-col min-h-screen">
        {/* Header Skeleton */}
        <div className="h-14 border-b border-slate-200 dark:border-slate-800/40 px-6 flex items-center justify-between bg-white dark:bg-[#0B0F19] animate-pulse">
          <div className="flex items-center gap-4">
            <div className="h-4 w-24 rounded bg-slate-200 dark:bg-slate-800" />
            <div className="h-4 w-32 rounded bg-slate-200 dark:bg-slate-800" />
          </div>
          <div className="flex items-center gap-4">
            <div className="h-8 w-8 rounded-full bg-slate-200 dark:bg-slate-800" />
            <div className="h-8 w-8 rounded bg-slate-200 dark:bg-slate-800" />
            <div className="h-8 w-16 rounded bg-slate-200 dark:bg-slate-800" />
          </div>
        </div>

        {/* Content Skeleton */}
        <main className="flex-1 p-6 md:p-8 max-w-[1400px] w-full mx-auto space-y-6 animate-pulse">
          <div className="rounded-xl border border-slate-200 dark:border-slate-800/40 bg-white dark:bg-[#0B0F19] p-6 space-y-4">
            <div className="h-6 w-48 rounded bg-slate-200 dark:bg-slate-800" />
            <div className="h-4 w-96 rounded bg-slate-200 dark:bg-slate-800" />
          </div>
          <div className="grid gap-6 md:grid-cols-3">
            {Array.from({ length: 3 }).map((_, i) => (
              <div key={i} className="rounded-xl border border-slate-200 dark:border-slate-800/40 bg-white dark:bg-[#0B0F19] p-5 space-y-4">
                <div className="h-4 w-24 rounded bg-slate-200 dark:bg-slate-800" />
                <div className="h-8 w-16 rounded bg-slate-200 dark:bg-slate-800" />
                <div className="h-3 w-32 rounded bg-slate-200 dark:bg-slate-800" />
              </div>
            ))}
          </div>
          <div className="rounded-xl border border-slate-200 dark:border-slate-800/40 bg-white dark:bg-[#0B0F19] p-6 space-y-4">
            <div className="h-5 w-32 rounded bg-slate-200 dark:bg-slate-800" />
            <div className="space-y-3">
              {Array.from({ length: 4 }).map((_, i) => (
                <div key={i} className="h-10 rounded bg-slate-100 dark:bg-slate-900/50" />
              ))}
            </div>
          </div>
        </main>
      </div>
    </div>
  );
}

export default function ConsoleDashboard() {
  // [COMMENT]: Lấy dữ liệu phiên đăng nhập hiện tại từ User Session Provider
  const { loading, authenticated } = useUserSession();

  // [COMMENT]: State chia sẻ giữa Sidebar và Main Panel để điều khiển layout co giãn mượt mà
  const [isCollapsed, setIsCollapsed] = useState(false);
  const [activeId, setActiveId] = useState("overview");

  // [COMMENT]: State quản lý theme sáng/tối — đọc từ localStorage, mặc định "dark" nếu chưa có
  const [theme, setTheme] = useState<"light" | "dark">("dark");
  const [commandPaletteOpen, setCommandPaletteOpen] = useState(false);

  // [COMMENT]: Khôi phục theme đã lưu trong localStorage khi component mount lần đầu
  useEffect(() => {
    const saved = localStorage.getItem("theme");
    if (saved === "light" || saved === "dark") {
      setTheme(saved);
    }
  }, []);

  // [COMMENT]: Đồng bộ class 'dark' trên <html> và ghi theme xuống localStorage mỗi khi thay đổi
  useEffect(() => {
    const root = window.document.documentElement;
    if (theme === "dark") {
      root.classList.add("dark");
    } else {
      root.classList.remove("dark");
    }
    localStorage.setItem("theme", theme);
  }, [theme]);

  // [COMMENT]: Trả về Skeleton Loading nếu đang tải phiên đăng nhập hoặc chưa được xác thực
  if (loading || !authenticated) {
    return <ConsoleSkeleton />;
  }

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
          <div className="space-y-6">
            {/* [COMMENT]: Dashboard Header - Enterprise style */}
            <div className="flex flex-col gap-1">
              <h1 className="text-xl font-bold tracking-tight text-slate-800 dark:text-slate-100">System Overview</h1>
              <p className="text-sm text-slate-500 dark:text-slate-400">Real-time status and telemetry of Aurora Cloud global control plane.</p>
            </div>

            {/* [COMMENT]: Khối thống kê KPI Cards - Glassmorphism design */}
            <div className="grid grid-cols-1 md:grid-cols-4 gap-4">
              <div className="p-4 bg-white dark:bg-[#1E293B]/40 border border-slate-200 dark:border-slate-800 rounded-xl space-y-2 shadow-xs">
                <div className="flex justify-between items-center text-slate-500 dark:text-slate-400 text-xs font-medium">
                  <span>TOTAL COMPUTE</span>
                  <Cpu className="h-4 w-4 text-blue-500" />
                </div>
                <div className="flex items-baseline gap-2">
                  <span className="text-2xl font-bold text-slate-800 dark:text-slate-100">410 / 896</span>
                  <span className="text-xs text-slate-500 dark:text-slate-400">vCPUs</span>
                </div>
                <div className="w-full bg-slate-100 dark:bg-slate-800 h-1.5 rounded-full overflow-hidden">
                  <div className="bg-blue-500 h-full rounded-full" style={{ width: "45%" }} />
                </div>
              </div>

              <div className="p-4 bg-white dark:bg-[#1E293B]/40 border border-slate-200 dark:border-slate-800 rounded-xl space-y-2 shadow-xs">
                <div className="flex justify-between items-center text-slate-500 dark:text-slate-400 text-xs font-medium">
                  <span>STORAGE RAW CAPACITY</span>
                  <Database className="h-4 w-4 text-indigo-500" />
                </div>
                <div className="flex items-baseline gap-2">
                  <span className="text-2xl font-bold text-slate-800 dark:text-slate-100">1.4 / 2.8</span>
                  <span className="text-xs text-slate-500 dark:text-slate-400">PB</span>
                </div>
                <div className="w-full bg-slate-100 dark:bg-slate-800 h-1.5 rounded-full overflow-hidden">
                  <div className="bg-indigo-500 h-full rounded-full" style={{ width: "50%" }} />
                </div>
              </div>

              <div className="p-4 bg-white dark:bg-[#1E293B]/40 border border-slate-200 dark:border-slate-800 rounded-xl space-y-2 shadow-xs">
                <div className="flex justify-between items-center text-slate-500 dark:text-slate-400 text-xs font-medium">
                  <span>KUBERNETES PODS</span>
                  <Layers className="h-4 w-4 text-emerald-500" />
                </div>
                <div className="flex items-baseline gap-2">
                  <span className="text-2xl font-bold text-slate-800 dark:text-slate-100">1,842</span>
                  <span className="text-xs text-emerald-600 dark:text-emerald-400 font-semibold">Healthy</span>
                </div>
                <div className="text-[10px] text-slate-400 dark:text-slate-500">Across 12 cluster namespaces</div>
              </div>

              <div className="p-4 bg-white dark:bg-[#1E293B]/40 border border-slate-200 dark:border-slate-800 rounded-xl space-y-2 shadow-xs">
                <div className="flex justify-between items-center text-slate-500 dark:text-slate-400 text-xs font-medium">
                  <span>ACTIVE INCIDENTS</span>
                  <ShieldAlert className="h-4 w-4 text-amber-500 animate-pulse" />
                </div>
                <div className="flex items-baseline gap-2">
                  <span className="text-2xl font-bold text-amber-600 dark:text-amber-400">2</span>
                  <span className="text-xs text-slate-500 dark:text-slate-400">warnings</span>
                </div>
                <div className="text-[10px] text-slate-400 dark:text-slate-500">SLA unaffected. Operations online.</div>
              </div>
            </div>

            {/* [COMMENT]: Khối chi tiết danh sách tài nguyên và hạ tầng */}
            <div className="grid grid-cols-1 lg:grid-cols-3 gap-6">
              <div className="lg:col-span-2 p-5 bg-white dark:bg-[#111827]/60 border border-slate-200 dark:border-slate-800 rounded-xl space-y-4 shadow-xs">
                <div className="flex justify-between items-center">
                  <h2 className="text-sm font-semibold text-slate-800 dark:text-slate-200">Critical Workloads Status</h2>
                  <Button variant="outline" className="h-7 text-xs border-slate-200 dark:border-slate-800 text-slate-700 dark:text-slate-300 hover:bg-slate-50 dark:hover:bg-slate-800/40 gap-1.5 cursor-pointer">
                    <RefreshCw className="h-3 w-3" /> Sync Status
                  </Button>
                </div>

                <div className="overflow-x-auto">
                  <table className="w-full text-left border-collapse text-xs">
                    <thead>
                      <tr className="border-b border-slate-200 dark:border-slate-800 text-slate-500 dark:text-slate-400">
                        <th className="py-2.5 font-medium">Virtual Machine</th>
                        <th className="py-2.5 font-medium">Status</th>
                        <th className="py-2.5 font-medium">IP Address</th>
                        <th className="py-2.5 font-medium">Flavor Spec</th>
                        <th className="py-2.5 font-medium">Uptime</th>
                      </tr>
                    </thead>
                    <tbody className="divide-y divide-slate-100 dark:divide-slate-800/50">
                      {mockVMs.slice(0, 3).map((vm) => (
                        <tr key={vm.name} className="hover:bg-slate-50 dark:hover:bg-slate-800/20 text-slate-700 dark:text-slate-300">
                          <td className="py-3 font-semibold text-slate-800 dark:text-slate-100 flex items-center gap-2">
                            <span className="h-1.5 w-1.5 rounded-full bg-emerald-500" />
                            {vm.name}
                          </td>
                          <td className="py-3">
                            <span className="inline-flex items-center rounded-full bg-emerald-500/10 px-2 py-0.5 text-[10px] font-medium text-emerald-600 dark:text-emerald-400">
                              {vm.status}
                            </span>
                          </td>
                          <td className="py-3 font-mono text-slate-500 dark:text-slate-400">{vm.ipAddress}</td>
                          <td className="py-3 text-slate-500 dark:text-slate-400">{vm.flavor}</td>
                          <td className="py-3 text-slate-400 dark:text-slate-550">{vm.uptime}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </div>

              {/* [COMMENT]: Block logs và audit sự cố bên phải */}
              <div className="p-5 bg-white dark:bg-[#111827]/60 border border-slate-200 dark:border-slate-800 rounded-xl space-y-4 shadow-xs">
                <h2 className="text-sm font-semibold text-slate-800 dark:text-slate-200">Edge Connectivity Logs</h2>
                <div className="space-y-3 font-mono text-[11px] text-slate-600 dark:text-slate-400">
                  <div className="flex gap-2 text-blue-600 dark:text-blue-400">
                    <span>[12:35:10]</span>
                    <span>BGP Router peer established on vn-hn-1</span>
                  </div>
                  <div className="flex gap-2 text-emerald-600 dark:text-emerald-400">
                    <span>[12:34:02]</span>
                    <span>Kubelet reconciled workload replica set standard-2</span>
                  </div>
                  <div className="flex gap-2 text-amber-600 dark:text-amber-400">
                    <span>[12:30:15]</span>
                    <span>Replica latency spike (220ms) on vn-sg-2</span>
                  </div>
                  <div className="flex gap-2 text-slate-400 dark:text-slate-500">
                    <span>[12:28:45]</span>
                    <span>User security token refreshed for system-admin</span>
                  </div>
                </div>
              </div>
            </div>
          </div>
        );

      case "zones":
        return (
          <div className="space-y-6">
            <div className="flex justify-between items-start">
              <div className="space-y-1">
                <h1 className="text-xl font-bold tracking-tight text-slate-800 dark:text-slate-100">Availability Zones</h1>
                <p className="text-sm text-slate-500 dark:text-slate-400">Manage hypervisor placement, localization regions, and network zones.</p>
              </div>
              <Button className="bg-blue-600 hover:bg-blue-700 text-white text-xs gap-1 h-8 rounded-lg cursor-pointer">
                <Plus className="h-3.5 w-3.5" /> Create Zone
              </Button>
            </div>

            <div className="grid grid-cols-1 gap-4">
              {mockZones.map((zone) => (
                <div key={zone.code} className="p-5 bg-white dark:bg-[#111827]/60 border border-slate-200 dark:border-slate-800 rounded-xl flex flex-col md:flex-row md:items-center justify-between gap-4 shadow-xs">
                  <div className="space-y-1">
                    <div className="flex items-center gap-2">
                      <h3 className="font-bold text-slate-800 dark:text-slate-200">{zone.name}</h3>
                      <span className="text-xs bg-slate-100 dark:bg-slate-800 text-slate-600 dark:text-slate-400 px-2 py-0.5 rounded font-mono">{zone.code}</span>
                      <span className={`inline-flex items-center rounded-full px-2 py-0.5 text-[10px] font-medium ${zone.status === "operational" ? "bg-emerald-500/10 text-emerald-600 dark:text-emerald-400" : "bg-amber-500/10 text-amber-600 dark:text-amber-400"
                        }`}>
                        {zone.status}
                      </span>
                    </div>
                    <p className="text-xs text-slate-500">Region location: {zone.region}</p>
                  </div>

                  <div className="flex items-center gap-8">
                    <div className="text-right">
                      <div className="text-xs text-slate-400 dark:text-slate-500">PHYSICAL HOSTS</div>
                      <div className="text-base font-bold text-slate-700 dark:text-slate-300">{zone.hosts} units</div>
                    </div>
                    <div className="text-right">
                      <div className="text-xs text-slate-400 dark:text-slate-500">CPU UTILIZATION</div>
                      <div className="text-base font-bold text-slate-700 dark:text-slate-300">
                        {zone.cpuUsed} / {zone.cpuTotal} <span className="text-xs font-normal text-slate-400 dark:text-slate-500">vCPUs</span>
                      </div>
                    </div>
                  </div>
                </div>
              ))}
            </div>
          </div>
        );

      case "vms":
        return (
          <div className="space-y-6">
            <div className="flex justify-between items-start">
              <div className="space-y-1">
                <h1 className="text-xl font-bold tracking-tight text-slate-800 dark:text-slate-100">Virtual Machines</h1>
                <p className="text-sm text-slate-500 dark:text-slate-400">Provision, boot, shutdown and edit hypervisor tenant instances.</p>
              </div>
              <Button className="bg-blue-600 hover:bg-blue-700 text-white text-xs gap-1 h-8 rounded-lg cursor-pointer">
                <Plus className="h-3.5 w-3.5" /> Provision VM
              </Button>
            </div>

            <div className="p-5 bg-white dark:bg-[#111827]/60 border border-slate-200 dark:border-slate-800 rounded-xl shadow-xs">
              <div className="overflow-x-auto">
                <table className="w-full text-left border-collapse text-xs">
                  <thead>
                    <tr className="border-b border-slate-200 dark:border-slate-800 text-slate-500 dark:text-slate-400">
                      <th className="py-3 font-medium">Virtual Machine</th>
                      <th className="py-3 font-medium">Status</th>
                      <th className="py-3 font-medium">IP Address</th>
                      <th className="py-3 font-medium">Zone</th>
                      <th className="py-3 font-medium">Flavor Spec</th>
                      <th className="py-3 font-medium">Uptime</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-slate-100 dark:divide-slate-800/50">
                    {mockVMs.map((vm) => (
                      <tr key={vm.name} className="hover:bg-slate-55/40 dark:hover:bg-slate-800/20 text-slate-700 dark:text-slate-300">
                        <td className="py-3.5 font-semibold text-slate-800 dark:text-slate-100 flex items-center gap-2">
                          <span className={`h-1.5 w-1.5 rounded-full ${vm.status === "running" ? "bg-emerald-500" : vm.status === "updating" ? "bg-blue-500" : "bg-slate-500"
                            }`} />
                          {vm.name}
                        </td>
                        <td className="py-3.5">
                          <span className={`inline-flex items-center rounded-full px-2 py-0.5 text-[10px] font-medium ${vm.status === "running" ? "bg-emerald-500/10 text-emerald-600 dark:text-emerald-400" : vm.status === "updating" ? "bg-blue-500/10 text-blue-600 dark:text-blue-400" : "bg-slate-100 dark:bg-slate-800 text-slate-500 dark:text-slate-400"
                            }`}>
                            {vm.status}
                          </span>
                        </td>
                        <td className="py-3.5 font-mono text-slate-500 dark:text-slate-400">{vm.ipAddress}</td>
                        <td className="py-3.5 text-slate-500 dark:text-slate-400">{vm.zone}</td>
                        <td className="py-3.5 text-slate-500 dark:text-slate-400">{vm.flavor}</td>
                        <td className="py-3.5 text-slate-400 dark:text-slate-500">{vm.uptime}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          </div>
        );

      case "incidents":
        return (
          <div className="space-y-6">
            <div className="space-y-1">
              <h1 className="text-xl font-bold tracking-tight text-amber-600 dark:text-amber-400">Platform Incidents</h1>
              <p className="text-sm text-slate-500 dark:text-slate-400">Active incidents needing administrative triage or network routing patches.</p>
            </div>

            <div className="p-4 bg-amber-50 dark:bg-amber-500/10 border border-amber-200 dark:border-amber-500/20 rounded-xl space-y-2 text-xs shadow-xs">
              <div className="flex items-center gap-2 font-bold text-amber-700 dark:text-amber-400">
                <ShieldAlert className="h-4 w-4" />
                <span>CRITICAL INCIDENT: vn-sg-2 Hypervisor Host Link Failover</span>
              </div>
              <p className="text-amber-800 dark:text-amber-200">
                Physical switch port ge-0/0/12 experienced link flap. Auto-migrated 14 customer tenants to replica nodes.
              </p>
              <div className="text-slate-400 dark:text-slate-550 pt-1 text-[10px] font-mono">Incident ID: INC-8842-12. Elapsed: 24 mins.</div>
            </div>

            <div className="p-4 bg-blue-50 dark:bg-blue-500/10 border border-blue-200 dark:border-blue-500/20 rounded-xl space-y-2 text-xs shadow-xs">
              <div className="flex items-center gap-2 font-bold text-blue-700 dark:text-blue-400">
                <Zap className="h-4 w-4" />
                <span>WARNING: Redis replication queue backing up</span>
              </div>
              <p className="text-blue-800 dark:text-blue-200">
                Queue processing delay reached 12.4s. Backup cron execution currently suspended to reduce disk throughput.
              </p>
              <div className="text-slate-400 dark:text-slate-550 pt-1 text-[10px] font-mono">Incident ID: INC-1102-49. Elapsed: 1 hour 4 mins.</div>
            </div>
          </div>
        );

      default:
        // [COMMENT]: Trang demo chung cho các menu item chưa phát triển chi tiết
        return (
          <div className="flex flex-col items-center justify-center py-20 text-center space-y-4">
            <div className="flex h-12 w-12 items-center justify-center rounded-full bg-slate-100 dark:bg-slate-800/80 text-slate-400 dark:text-slate-500">
              <Terminal className="h-6 w-6" />
            </div>
            <div className="space-y-1">
              <h2 className="text-base font-semibold text-slate-800 dark:text-slate-200">Interactive Domain Panel</h2>
              <p className="text-xs text-slate-500 max-w-sm">
                Selected page <span className="font-semibold text-blue-550 dark:text-blue-400 font-mono">"{activeId}"</span> loaded dynamically. Implement the business logic for this subsystem under standard client components.
              </p>
            </div>
            <Button variant="outline" onClick={() => setActiveId("overview")} className="h-8 text-xs border-slate-200 dark:border-slate-800 text-slate-500 hover:text-slate-800 dark:hover:text-slate-200 cursor-pointer">
              Return to Overview
            </Button>
          </div>
        );
    }
  };

  return (
    <div className="min-h-screen bg-slate-50 text-slate-800 dark:bg-[#090D16] dark:text-slate-100 flex overflow-hidden transition-colors duration-250">
      {/* [COMMENT]: Sidebar console trung tâm */}
      <SidebarConsole
        isCollapsed={isCollapsed}
        setIsCollapsed={setIsCollapsed}
        activeId={activeId}
        setActiveId={setActiveId}
      />

      {/* [COMMENT]: Khối Main Layout Panel. 
          Khoảng cách lề trái co giãn đồng bộ theo trạng thái Sidebar (ml-[272px] hoặc ml-[60px]).
          Sử dụng transition-all cho trải nghiệm mượt mà. */}
      <div
        className="flex-1 min-w-0 transition-all duration-300 ease-in-out flex flex-col min-h-screen"
        style={{ marginLeft: isCollapsed ? "60px" : "272px" }}
      >
        {/* [COMMENT]: Component Header toàn cầu tích hợp context selector, platform health, notifications, theme toggler */}
        <HeaderConsole
          isCollapsed={isCollapsed}
          setIsCollapsed={setIsCollapsed}
          activeId={activeId}
          theme={theme}
          setTheme={setTheme}
          onOpenCommandPalette={() => setCommandPaletteOpen(true)}
        />

        {/* [COMMENT]: Nội dung chính của dashboard */}
        <main className="flex-1 p-6 md:p-8 max-w-[1400px] w-full mx-auto animate-in fade-in duration-200">
          {renderMainContent()}
        </main>

        {/* [COMMENT]: Footer chân trang - Appliance theme */}
        <footer className="border-t border-slate-200 dark:border-slate-800/40 py-4 px-6 text-center md:text-left md:flex md:justify-between text-[11px] text-slate-500 mt-auto bg-slate-100/50 dark:bg-[#070A11]/30">
          <span>© 2026 Aurora Cloud Services. All rights reserved.</span>
          <div className="flex justify-center gap-4 mt-2 md:mt-0">
            <a href="#" className="hover:text-slate-800 dark:hover:text-slate-400 transition-colors">Privacy Policy</a>
            <span>·</span>
            <a href="#" className="hover:text-slate-800 dark:hover:text-slate-400 transition-colors">Service Agreement</a>
            <span>·</span>
            <a href="#" className="hover:text-slate-800 dark:hover:text-slate-400 transition-colors">Kubernetes API Docs</a>
          </div>
        </footer>
      </div>

      {/* [COMMENT]: Khối Command Palette điều hướng nhanh kích hoạt qua Cmd+K hoặc click Search */}
      <CommandPalette
        isOpen={commandPaletteOpen}
        setIsOpen={setCommandPaletteOpen}
        setActivePage={setActiveId}
      />
    </div>
  );
}
