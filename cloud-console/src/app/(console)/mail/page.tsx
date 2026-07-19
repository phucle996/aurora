"use client";

import React, { useState } from "react";
import {
  Mail,
  RefreshCw,
  Info,
  FileCode,
  Radio,
  History,
  Send,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import RouteGuard from "@/components/route-guard";

// [COMMENT]: Import 4 tabs bộ khung thiết kế trang Mail Cloud Console
import { OverviewTab } from "./components/OverviewTab";
import { TemplatesTab } from "./components/TemplatesTab";
import { ConsumersTab } from "./components/ConsumersTab";
import { DispatchHistoryTab } from "./components/DispatchHistoryTab";

function MailConsolePageContent() {
  // [COMMENT]: State quản lý tab đang kích hoạt trong trang Cloud Console Mail Service
  const [activeTab, setActiveTab] = useState<
    "Overview" | "Templates" | "Stream Consumers" | "Dispatch History"
  >("Overview");

  // [COMMENT]: Danh sách các tab theo yêu cầu đặc tả UI (4 tab chính)
  const tabs = [
    { id: "Overview", label: "Tổng quan", icon: Info },
    { id: "Templates", label: "Template", icon: FileCode },
    { id: "Stream Consumers", label: "Consumer (Redis / Kafka)", icon: Radio },
    { id: "Dispatch History", label: "Lịch sử gửi mail", icon: History },
  ] as const;

  const renderActiveTab = () => {
    switch (activeTab) {
      case "Overview":
        return <OverviewTab />;
      case "Templates":
        return <TemplatesTab />;
      case "Stream Consumers":
        return <ConsumersTab />;
      case "Dispatch History":
        return <DispatchHistoryTab />;
      default:
        return <OverviewTab />;
    }
  };

  return (
    <div className="flex flex-col gap-6 w-full relative pb-10 text-foreground min-h-[calc(100vh-110px)] items-stretch px-6">
      {/* [COMMENT]: 1. Header Section - Chuẩn Azure Portal / GitHub Hairline Style */}
      <div className="flex flex-col md:flex-row md:items-center justify-between gap-4 border-b border-border pb-5">
        <div className="flex items-center gap-3.5">
          <div className="h-9 w-9 flex items-center justify-center rounded-lg bg-blue-600/10 text-blue-500 border border-blue-500/20 shadow-xs">
            <Mail className="h-5 w-5" />
          </div>
          <div className="flex flex-col">
            <div className="flex items-center gap-2.5">
              <h1 className="text-xl font-bold text-foreground tracking-tight">
                Transactional Mail Service
              </h1>
              <span className="px-2 py-0.5 rounded text-[10px] font-mono font-bold bg-emerald-500/10 text-emerald-500 border border-emerald-500/20 uppercase">
                HA Engine Active
              </span>
            </div>
            <p className="text-[11px] text-muted-foreground mt-0.5 font-mono">
              Enterprise Cloud Native Transactional & System Mail Dispatch Engine (Dataplane + JMAP/SMTP)
            </p>
          </div>
        </div>

        <div className="flex items-center gap-2">
          <Button
            variant="outline"
            size="sm"
            className="font-semibold cursor-pointer text-xs h-9 gap-1.5"
            onClick={() => {
              // Action sync state / refetch metrics
            }}
          >
            <RefreshCw className="h-3.5 w-3.5" />
            <span>Sync Service State</span>
          </Button>

          <Button
            size="sm"
            className="font-semibold cursor-pointer text-xs h-9 gap-1.5 bg-blue-600 hover:bg-blue-700 text-white"
          >
            <Send className="h-3.5 w-3.5" />
            <span>Send Test Mail</span>
          </Button>
        </div>
      </div>

      {/* [COMMENT]: 2. Flat Hairline Tab Navigation Bar (Tuân thủ DESIGN.MD Divider First) */}
      <div className="flex border-b border-border text-[13px] font-bold text-muted-foreground select-none gap-2">
        {tabs.map((tab) => {
          const Icon = tab.icon;
          const isActive = activeTab === tab.id;
          return (
            <button
              key={tab.id}
              onClick={() => setActiveTab(tab.id as any)}
              className={cn(
                "pb-2.5 px-3.5 border-b-2 -mb-[2px] transition-all cursor-pointer font-bold flex items-center gap-2 outline-none",
                isActive
                  ? "border-blue-600 text-blue-600 dark:text-blue-400"
                  : "border-transparent hover:text-foreground hover:border-slate-300 dark:hover:border-slate-800"
              )}
            >
              <Icon className="h-4 w-4" />
              <span>{tab.label}</span>
            </button>
          );
        })}
      </div>

      {/* [COMMENT]: 3. Dynamic Active Tab Content Container */}
      <div className="animate-in fade-in slide-in-from-bottom-2 duration-200">
        {renderActiveTab()}
      </div>
    </div>
  );
}

// [COMMENT]: bọc RouteGuard để đảm bảo an toàn phân quyền truy cập tính năng Mail Console
export default function MailConsolePage() {
  return (
    <RouteGuard requiredKey="mail:mail" requiredAction="read">
      <MailConsolePageContent />
    </RouteGuard>
  );
}
