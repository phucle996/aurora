"use client";

import React, { useState } from "react";
import {
  Database,
  Radio,
  RefreshCw,
  Search,
  Activity,
  Layers,
  HardDrive,
  Cpu,
  CheckCircle2,
  AlertTriangle,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";

// [COMMENT]: Interface định nghĩa cho mỗi Stream Broker Consumer Instance (Redis Stream, Kafka Topic, NATS, v.v.)
export interface StreamConsumerItem {
  id: string;
  brokerType: "Redis Stream" | "Apache Kafka" | "NATS JetStream" | "RabbitMQ";
  topicOrStream: string;
  consumerGroup: string;
  activeConsumers: number;
  lagOrPending: number;
  throughputPerSec: number;
  status: "Healthy" | "Degraded" | "Idle" | "Error";
  lastPolled: string;
}

// [COMMENT]: Tab 3 - ConsumersTab quản lý và giám sát các Stream Broker / Message Consumers
// Phụ trách nhận mail payload từ Controlplane / Job-Orchestrator để chuyển xuống Dataplane
export function ConsumersTab() {
  const [searchTerm, setSearchTerm] = useState("");
  const [brokerFilter, setBrokerFilter] = useState<string>("All");

  // [COMMENT]: Dữ liệu mẫu khung giao diện cho các Stream Broker Consumers
  const consumerList: StreamConsumerItem[] = [
    {
      id: "consumer_redis_mail_dispatch_v1",
      brokerType: "Redis Stream",
      topicOrStream: "stream:mail:dispatch:v1",
      consumerGroup: "dataplane_mail_workers",
      activeConsumers: 12,
      lagOrPending: 14,
      throughputPerSec: 185,
      status: "Healthy",
      lastPolled: "2 giây trước",
    },
    {
      id: "consumer_redis_mail_results_v1",
      brokerType: "Redis Stream",
      topicOrStream: "stream:mail:results:v1",
      consumerGroup: "job_orchestrator_results_processor",
      activeConsumers: 4,
      lagOrPending: 0,
      throughputPerSec: 180,
      status: "Healthy",
      lastPolled: "1 giây trước",
    },
    {
      id: "consumer_kafka_transactional_mails",
      brokerType: "Apache Kafka",
      topicOrStream: "aurora.mail.transactional.v1",
      consumerGroup: "cg-mail-dataplane-cluster",
      activeConsumers: 8,
      lagOrPending: 3,
      throughputPerSec: 420,
      status: "Healthy",
      lastPolled: "5 giây trước",
    },
    {
      id: "consumer_nats_bulk_notifications",
      brokerType: "NATS JetStream",
      topicOrStream: "EVENTS.mail.bulk.dispatch",
      consumerGroup: "bulk-mail-workers",
      activeConsumers: 0,
      lagOrPending: 0,
      throughputPerSec: 0,
      status: "Idle",
      lastPolled: "1 phút trước",
    },
  ];

  const filteredConsumers = consumerList.filter((c) => {
    const matchSearch =
      c.topicOrStream.toLowerCase().includes(searchTerm.toLowerCase()) ||
      c.consumerGroup.toLowerCase().includes(searchTerm.toLowerCase()) ||
      c.id.toLowerCase().includes(searchTerm.toLowerCase());
    const matchBroker = brokerFilter === "All" || c.brokerType === brokerFilter;
    return matchSearch && matchBroker;
  });

  return (
    <div className="flex flex-col gap-5 text-foreground">
      {/* [COMMENT]: Khối 1 - Broker Cluster Overview Header Summary */}
      <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
        <div className="p-4 rounded-xl border border-border/80 bg-card flex items-center justify-between">
          <div className="flex items-center gap-3">
            <div className="h-9 w-9 rounded-lg bg-emerald-500/10 text-emerald-500 flex items-center justify-center border border-emerald-500/20">
              <Radio className="h-5 w-5 animate-pulse" />
            </div>
            <div>
              <span className="text-[11px] font-semibold text-muted-foreground uppercase tracking-wider block">
                Active Stream Brokers
              </span>
              <span className="text-xl font-bold text-foreground font-mono">
                3 Connected
              </span>
            </div>
          </div>
          <Badge variant="outline" className="bg-emerald-500/10 text-emerald-500 border-emerald-500/20 text-[10px]">
            HA Ready
          </Badge>
        </div>

        <div className="p-4 rounded-xl border border-border/80 bg-card flex items-center justify-between">
          <div className="flex items-center gap-3">
            <div className="h-9 w-9 rounded-lg bg-blue-500/10 text-blue-500 flex items-center justify-center border border-blue-500/20">
              <Activity className="h-5 w-5" />
            </div>
            <div>
              <span className="text-[11px] font-semibold text-muted-foreground uppercase tracking-wider block">
                Total Ingestion Rate
              </span>
              <span className="text-xl font-bold text-foreground font-mono">
                785 msgs/sec
              </span>
            </div>
          </div>
          <span className="text-xs text-muted-foreground font-mono">Realtime</span>
        </div>

        <div className="p-4 rounded-xl border border-border/80 bg-card flex items-center justify-between">
          <div className="flex items-center gap-3">
            <div className="h-9 w-9 rounded-lg bg-amber-500/10 text-amber-500 flex items-center justify-center border border-amber-500/20">
              <Layers className="h-5 w-5" />
            </div>
            <div>
              <span className="text-[11px] font-semibold text-muted-foreground uppercase tracking-wider block">
                Consumer Stream Lag
              </span>
              <span className="text-xl font-bold text-foreground font-mono">
                17 pending
              </span>
            </div>
          </div>
          <Badge variant="outline" className="bg-emerald-500/10 text-emerald-500 border-emerald-500/20 text-[10px]">
            Optimal
          </Badge>
        </div>
      </div>

      {/* [COMMENT]: Khối 2 - Toolbar Lọc Broker Consumer */}
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-3 border border-border/80 rounded-xl p-3 bg-card">
        <div className="flex items-center gap-2 flex-1 max-w-md">
          <div className="relative w-full">
            <Search className="h-4 w-4 absolute left-3 top-1/2 -translate-y-1/2 text-muted-foreground" />
            <Input
              placeholder="Filter by stream, topic or consumer group..."
              value={searchTerm}
              onChange={(e) => setSearchTerm(e.target.value)}
              className="pl-9 h-9 text-xs bg-background rounded-md"
            />
          </div>
        </div>

        <div className="flex items-center gap-2">
          <select
            value={brokerFilter}
            onChange={(e) => setBrokerFilter(e.target.value)}
            className="h-9 px-3 rounded-md border border-border bg-background text-xs font-semibold text-foreground outline-none cursor-pointer"
          >
            <option value="All">Broker: All Types</option>
            <option value="Redis Stream">Broker: Redis Stream</option>
            <option value="Apache Kafka">Broker: Apache Kafka</option>
            <option value="NATS JetStream">Broker: NATS JetStream</option>
          </select>

          <Button
            variant="outline"
            size="sm"
            className="h-9 text-xs font-semibold gap-1.5"
          >
            <RefreshCw className="h-3.5 w-3.5" />
            Refresh Stream Lag
          </Button>
        </div>
      </div>

      {/* [COMMENT]: Khối 3 - Stream Consumer Table View */}
      <div className="border border-border/80 rounded-xl bg-card overflow-hidden">
        <div className="overflow-x-auto">
          <table className="w-full text-left text-xs border-collapse">
            <thead>
              <tr className="border-b border-border/60 bg-muted/30 text-muted-foreground font-semibold text-[11px] uppercase tracking-wider">
                <th className="py-2.5 px-4">Broker Type</th>
                <th className="py-2.5 px-4">Stream / Topic Name</th>
                <th className="py-2.5 px-4">Consumer Group</th>
                <th className="py-2.5 px-4">Active Workers</th>
                <th className="py-2.5 px-4">Stream Lag</th>
                <th className="py-2.5 px-4">Throughput</th>
                <th className="py-2.5 px-4">Status</th>
                <th className="py-2.5 px-4">Last Polled</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border/40 font-normal">
              {filteredConsumers.map((c) => (
                <tr key={c.id} className="hover:bg-muted/10 transition-colors">
                  <td className="py-3 px-4 font-semibold text-foreground">
                    <span className="px-2 py-0.5 rounded bg-muted border border-border/50 text-[11px] font-mono">
                      {c.brokerType}
                    </span>
                  </td>
                  <td className="py-3 px-4 font-mono font-semibold text-blue-500 select-all">
                    {c.topicOrStream}
                  </td>
                  <td className="py-3 px-4 font-mono text-muted-foreground">
                    {c.consumerGroup}
                  </td>
                  <td className="py-3 px-4 font-mono text-xs font-semibold text-foreground">
                    {c.activeConsumers} threads
                  </td>
                  <td className="py-3 px-4 font-mono text-xs">
                    <span
                      className={`font-semibold ${
                        c.lagOrPending > 50
                          ? "text-amber-500"
                          : "text-emerald-500"
                      }`}
                    >
                      {c.lagOrPending} msgs
                    </span>
                  </td>
                  <td className="py-3 px-4 font-mono text-xs text-foreground">
                    {c.throughputPerSec} /s
                  </td>
                  <td className="py-3 px-4">
                    <div className="flex items-center gap-1.5">
                      <span
                        className={`h-2 w-2 rounded-full ${
                          c.status === "Healthy"
                            ? "bg-emerald-500"
                            : c.status === "Idle"
                            ? "bg-slate-400"
                            : "bg-amber-500"
                        }`}
                      />
                      <span className="font-medium text-foreground">
                        {c.status}
                      </span>
                    </div>
                  </td>
                  <td className="py-3 px-4 font-mono text-[11px] text-muted-foreground">
                    {c.lastPolled}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  );
}
