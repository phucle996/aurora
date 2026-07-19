"use client";

import React from "react";
import {
  Mail,
  CheckCircle2,
  AlertTriangle,
  Clock,
  Server,
  Zap,
  Activity,
  Layers,
  ArrowUpRight,
} from "lucide-react";
import { Badge } from "@/components/ui/badge";

// [COMMENT]: Tab 1 - OverviewTab hiển thị tổng quan hạ tầng Cloud-Native Mail Service
// Bao gồm metrics vận hành HA, trạng thái broker (Redis/Kafka) và lưu lượng gửi nhận.
export function OverviewTab() {
  // [COMMENT]: Dữ liệu mẫu khung giao diện cho hệ thống Mail Service Cloud-Native
  const metrics = [
    {
      title: "Mail Dispatched (24h)",
      value: "128,450",
      change: "+12.4%",
      isPositive: true,
      icon: Mail,
      desc: "Tổng số mail giao dịch đã xử lý trong ngày",
    },
    {
      title: "Delivery Success Rate",
      value: "99.85%",
      change: "+0.02%",
      isPositive: true,
      icon: CheckCircle2,
      desc: "Tỷ lệ chuyển thư thành công tới JMAP/SMTP recipient",
    },
    {
      title: "Avg Delivery Latency",
      value: "142 ms",
      change: "-8 ms",
      isPositive: true,
      icon: Clock,
      desc: "Thời gian xử lý trung bình từ Queue đến Dataplane worker",
    },
    {
      title: "Stream Queue Backlog",
      value: "14 jobs",
      change: "Stable",
      isPositive: true,
      icon: Layers,
      desc: "Số lượng job đang chờ phân phối trong Redis Stream",
    },
  ];

  const clusterNodes = [
    {
      name: "mail-dataplane-worker-az1",
      zone: "ap-southeast-1a",
      status: "Active",
      cpu: "18%",
      ram: "420 MB",
      concurrency: "64 worker threads",
    },
    {
      name: "mail-dataplane-worker-az2",
      zone: "ap-southeast-1b",
      status: "Active",
      cpu: "22%",
      ram: "480 MB",
      concurrency: "64 worker threads",
    },
    {
      name: "mail-dataplane-worker-az3",
      zone: "ap-southeast-1c",
      status: "Active",
      cpu: "15%",
      ram: "390 MB",
      concurrency: "64 worker threads",
    },
  ];

  return (
    <div className="flex flex-col gap-6 text-foreground">
      {/* [COMMENT]: Khối 1 - Key Operational Metrics Cards */}
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
        {metrics.map((m) => {
          const Icon = m.icon;
          return (
            <div
              key={m.title}
              className="p-4 rounded-xl border border-border bg-card transition-colors flex flex-col justify-between gap-3"
            >
              <div className="flex items-center justify-between">
                <span className="text-[12px] font-semibold text-muted-foreground">
                  {m.title}
                </span>
                <div className="h-7 w-7 rounded-lg bg-blue-500/10 text-blue-500 flex items-center justify-center border border-blue-500/20">
                  <Icon className="h-4 w-4" />
                </div>
              </div>

              <div>
                <div className="text-2xl font-bold tracking-tight text-foreground">
                  {m.value}
                </div>
                <div className="flex items-center gap-1.5 mt-1">
                  <span className="text-[11px] font-semibold text-emerald-500 flex items-center gap-0.5">
                    <ArrowUpRight className="h-3 w-3" />
                    {m.change}
                  </span>
                  <span className="text-[11px] text-muted-foreground truncate">
                    vs last 24h
                  </span>
                </div>
              </div>

              <div className="text-[11px] text-muted-foreground/80 border-t border-border/40 pt-2 font-mono">
                {m.desc}
              </div>
            </div>
          );
        })}
      </div>

      {/* [COMMENT]: Khối 2 - HA Dataplane Worker Cluster Status */}
      <div className="border border-border/80 rounded-xl bg-card overflow-hidden">
        <div className="p-4 border-b border-border/60 flex items-center justify-between bg-muted/20">
          <div className="flex items-center gap-2.5">
            <Server className="h-4 w-4 text-blue-500" />
            <div>
              <h3 className="text-sm font-semibold text-foreground">
                Dataplane Worker Pool Status (HA Mode)
              </h3>
              <p className="text-[11px] text-muted-foreground">
                Danh sách các node worker thực thi gửi mail phân bổ đa Availability Zone
              </p>
            </div>
          </div>
          <Badge variant="outline" className="bg-emerald-500/10 text-emerald-500 border-emerald-500/20 text-[11px] font-semibold">
            <span className="h-1.5 w-1.5 rounded-full bg-emerald-500 mr-1.5 animate-pulse" />
            3/3 Nodes Healthy
          </Badge>
        </div>

        <div className="divide-y divide-border/40">
          {clusterNodes.map((node) => (
            <div
              key={node.name}
              className="p-3.5 px-4 flex flex-col sm:flex-row sm:items-center justify-between gap-3 hover:bg-muted/10 transition-colors text-xs"
            >
              <div className="flex items-center gap-3">
                <div className="h-2 w-2 rounded-full bg-emerald-500 shrink-0" />
                <div className="flex flex-col">
                  <span className="font-semibold text-foreground font-mono">
                    {node.name}
                  </span>
                  <span className="text-[11px] text-muted-foreground">
                    Zone: {node.zone}
                  </span>
                </div>
              </div>

              <div className="flex items-center gap-6 font-mono text-[12px] text-muted-foreground">
                <div>
                  <span>CPU: </span>
                  <span className="text-foreground font-semibold">{node.cpu}</span>
                </div>
                <div>
                  <span>RAM: </span>
                  <span className="text-foreground font-semibold">{node.ram}</span>
                </div>
                <div>
                  <span>Threads: </span>
                  <span className="text-foreground font-semibold">{node.concurrency}</span>
                </div>
              </div>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}
