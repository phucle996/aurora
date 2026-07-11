"use client";

import React from "react";
import { Calendar } from "lucide-react";

// [COMMENT]: Định dạng ngày tháng hiển thị thông minh
function formatDate(dateStr: string): string {
  try {
    const d = new Date(dateStr);
    if (isNaN(d.getTime())) return dateStr;
    const months = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
    const now = new Date();
    if (d.toDateString() === now.toDateString()) {
      return "Today";
    }
    const yesterday = new Date();
    yesterday.setDate(now.getDate() - 1);
    if (d.toDateString() === yesterday.toDateString()) {
      return "Yesterday";
    }
    return `${months[d.getMonth()]} ${d.getDate().toString().padStart(2, "0")}`;
  } catch {
    return dateStr;
  }
}

interface AuditStatsCardProps {
  scope: string;
  assignmentsCount: number;
  selectedPermsCount: number;
  createdAt: string;
  updatedAt: string;
}

export default function AuditStatsCard({
  scope,
  assignmentsCount,
  selectedPermsCount,
  createdAt,
  updatedAt
}: AuditStatsCardProps) {
  return (
    <div className="bg-white dark:bg-slate-900/60 border border-slate-200 dark:border-slate-800 rounded-xl p-5 shadow-xs space-y-4">
      <div className="border-b border-slate-200 dark:border-slate-800 pb-3 flex items-center justify-between">
        <h3 className="text-xs font-bold uppercase tracking-wider text-slate-855 dark:text-slate-200">
          Role Audit Status
        </h3>
        <span className="inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-medium bg-slate-100 dark:bg-slate-800/80 text-slate-600 dark:text-slate-400 border border-slate-200 dark:border-slate-800/60 capitalize">
          {scope} Scope
        </span>
      </div>

      <div className="space-y-3.5 text-xs text-slate-600 dark:text-slate-400">
        <div className="flex items-center justify-between">
          <span>Current Assignments</span>
          <strong className="text-slate-850 dark:text-slate-200 font-mono">
            {assignmentsCount === 1 ? "1 user" : `${assignmentsCount} users`}
          </strong>
        </div>

        <div className="flex items-center justify-between">
          <span>Permissions Granted</span>
          <strong className="text-slate-850 dark:text-slate-200 font-mono">
            {selectedPermsCount} rules
          </strong>
        </div>

        {createdAt && (
          <div className="flex items-center justify-between">
            <span className="flex items-center gap-1">
              <Calendar className="h-3 w-3 text-slate-455" />
              <span>Created At</span>
            </span>
            <strong className="text-slate-850 dark:text-slate-200 font-mono text-[10.5px]">
              {formatDate(createdAt)}
            </strong>
          </div>
        )}

        {updatedAt && (
          <div className="flex items-center justify-between border-t border-slate-105 dark:border-slate-800/60 pt-3">
            <span className="flex items-center gap-1">
              <Calendar className="h-3 w-3 text-slate-455" />
              <span>Last Updated</span>
            </span>
            <strong className="text-slate-850 dark:text-slate-200 font-mono text-[10.5px]">
              {formatDate(updatedAt)}
            </strong>
          </div>
        )}
      </div>
    </div>
  );
}
