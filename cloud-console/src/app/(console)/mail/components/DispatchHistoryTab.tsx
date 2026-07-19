"use client";

import React, { useState } from "react";
import {
  History,
  Search,
  RotateCcw,
  CheckCircle2,
  XCircle,
  Clock,
  AlertCircle,
  ExternalLink,
  Filter,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";

// [COMMENT]: Interface định nghĩa cho bản ghi lịch sử gửi email (Mail Dispatch Log Item)
export interface MailDispatchRecord {
  jobId: string;
  recipient: string;
  senderProfileId: string;
  senderVersion: number;
  templateId: string;
  subject: string;
  status: "Delivered" | "Failed" | "Pending" | "Retrying";
  latencyMs: number;
  dispatchedAt: string;
  errorMessage?: string;
}

// [COMMENT]: Tab 4 - DispatchHistoryTab truy vấn & kiểm vết lịch sử gửi email giao dịch (Audit Log)
export function DispatchHistoryTab() {
  const [searchTerm, setSearchTerm] = useState("");
  const [statusFilter, setStatusFilter] = useState<string>("All");

  // [COMMENT]: Dữ liệu mẫu khung lịch sử gửi mail phục vụ giao diện Cloud Console
  const records: MailDispatchRecord[] = [
    {
      jobId: "job_01j4m8k9a0b1c2d3e4f5",
      recipient: "phucle@example.com",
      senderProfileId: "profile_system_noreply",
      senderVersion: 1,
      templateId: "tpl_account_verify_v2",
      subject: "Xác thực tài khoản Aurora Cloud Platform",
      status: "Delivered",
      latencyMs: 128,
      dispatchedAt: "2026-07-19 21:14:02",
    },
    {
      jobId: "job_01j4m8k9a0b1c2d3e4f6",
      recipient: "admin@dev.domain.io",
      senderProfileId: "profile_billing_dept",
      senderVersion: 2,
      templateId: "tpl_billing_invoice_v3",
      subject: "Hóa đơn dịch vụ tháng 07/2026 - Aurora Cloud",
      status: "Delivered",
      latencyMs: 156,
      dispatchedAt: "2026-07-19 21:10:15",
    },
    {
      jobId: "job_01j4m8k9a0b1c2d3e4f7",
      recipient: "user_invalid_domain@badmail.test",
      senderProfileId: "profile_system_noreply",
      senderVersion: 1,
      templateId: "tpl_password_reset_v1",
      subject: "Yêu cầu đặt lại mật khẩu cho tài khoản user_test",
      status: "Failed",
      latencyMs: 3500,
      dispatchedAt: "2026-07-19 20:55:40",
      errorMessage: "550 5.1.1 Recipient address rejected: User unknown in local recipient table",
    },
    {
      jobId: "job_01j4m8k9a0b1c2d3e4f8",
      recipient: "ops_alerts@company.org",
      senderProfileId: "profile_alerts_bot",
      senderVersion: 1,
      templateId: "tpl_quota_alert_warning",
      subject: "Cảnh báo vượt hạn ngạch tài nguyên [Production-AZ1]",
      status: "Retrying",
      latencyMs: 420,
      dispatchedAt: "2026-07-19 20:48:10",
      errorMessage: "Connection timeout to upstream SMTP relay node. Retrying backoff 2/3...",
    },
  ];

  const filteredRecords = records.filter((r) => {
    const matchSearch =
      r.jobId.toLowerCase().includes(searchTerm.toLowerCase()) ||
      r.recipient.toLowerCase().includes(searchTerm.toLowerCase()) ||
      r.templateId.toLowerCase().includes(searchTerm.toLowerCase()) ||
      r.subject.toLowerCase().includes(searchTerm.toLowerCase());
    const matchStatus = statusFilter === "All" || r.status === statusFilter;
    return matchSearch && matchStatus;
  });

  return (
    <div className="flex flex-col gap-4 text-foreground">
      {/* [COMMENT]: Khối 1 - Integrated Toolbar (Tìm kiếm & Bộ lọc trạng thái) */}
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-3 border border-border/80 rounded-xl p-3 bg-card">
        <div className="flex items-center gap-2 flex-1 max-w-md">
          <div className="relative w-full">
            <Search className="h-4 w-4 absolute left-3 top-1/2 -translate-y-1/2 text-muted-foreground" />
            <Input
              placeholder="Search Job ID, Recipient, Template ID or Subject..."
              value={searchTerm}
              onChange={(e) => setSearchTerm(e.target.value)}
              className="pl-9 h-9 text-xs bg-background rounded-md"
            />
          </div>
        </div>

        <div className="flex items-center gap-2">
          <select
            value={statusFilter}
            onChange={(e) => setStatusFilter(e.target.value)}
            className="h-9 px-3 rounded-md border border-border bg-background text-xs font-semibold text-foreground outline-none cursor-pointer"
          >
            <option value="All">Status: All</option>
            <option value="Delivered">Status: Delivered</option>
            <option value="Failed">Status: Failed</option>
            <option value="Pending">Status: Pending</option>
            <option value="Retrying">Status: Retrying</option>
          </select>

          {searchTerm || statusFilter !== "All" ? (
            <Button
              variant="ghost"
              size="sm"
              onClick={() => {
                setSearchTerm("");
                setStatusFilter("All");
              }}
              className="h-9 text-xs text-blue-500 hover:text-blue-600 font-semibold gap-1.5"
            >
              <RotateCcw className="h-3.5 w-3.5" />
              Reset
            </Button>
          ) : null}
        </div>
      </div>

      {/* [COMMENT]: Khối 2 - Table-First Dispatch History Log */}
      <div className="border border-border/80 rounded-xl bg-card overflow-hidden">
        <div className="overflow-x-auto">
          <table className="w-full text-left text-xs border-collapse">
            <thead>
              <tr className="border-b border-border/60 bg-muted/30 text-muted-foreground font-semibold text-[11px] uppercase tracking-wider">
                <th className="py-2.5 px-4">Job ID / Recipient</th>
                <th className="py-2.5 px-4">Template / Subject</th>
                <th className="py-2.5 px-4">Sender Profile</th>
                <th className="py-2.5 px-4">Latency</th>
                <th className="py-2.5 px-4">Status</th>
                <th className="py-2.5 px-4">Dispatched At</th>
                <th className="py-2.5 px-4 text-right">Details</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border/40 font-normal">
              {filteredRecords.length === 0 ? (
                <tr>
                  <td colSpan={7} className="py-8 text-center text-muted-foreground">
                    Không tìm thấy bản ghi gửi mail phù hợp
                  </td>
                </tr>
              ) : (
                filteredRecords.map((r) => (
                  <tr key={r.jobId} className="hover:bg-muted/10 transition-colors">
                    <td className="py-3 px-4">
                      <div className="flex flex-col">
                        <span className="font-semibold text-foreground text-sm select-all">
                          {r.recipient}
                        </span>
                        <span className="text-[11px] font-mono text-muted-foreground select-all">
                          {r.jobId}
                        </span>
                      </div>
                    </td>
                    <td className="py-3 px-4">
                      <div className="flex flex-col max-w-[260px]">
                        <span className="font-mono text-xs font-semibold text-blue-500 truncate">
                          {r.templateId}
                        </span>
                        <span className="text-[11px] text-muted-foreground truncate">
                          {r.subject}
                        </span>
                      </div>
                    </td>
                    <td className="py-3 px-4 font-mono text-xs text-muted-foreground">
                      {r.senderProfileId} <span className="text-[10px]">v{r.senderVersion}</span>
                    </td>
                    <td className="py-3 px-4 font-mono text-xs text-foreground">
                      {r.latencyMs} ms
                    </td>
                    <td className="py-3 px-4">
                      <div className="flex flex-col gap-1">
                        <div className="flex items-center gap-1.5">
                          {r.status === "Delivered" && (
                            <CheckCircle2 className="h-3.5 w-3.5 text-emerald-500" />
                          )}
                          {r.status === "Failed" && (
                            <XCircle className="h-3.5 w-3.5 text-red-500" />
                          )}
                          {r.status === "Retrying" && (
                            <AlertCircle className="h-3.5 w-3.5 text-amber-500 animate-pulse" />
                          )}
                          <span
                            className={`font-semibold ${
                              r.status === "Delivered"
                                ? "text-emerald-500"
                                : r.status === "Failed"
                                ? "text-red-500"
                                : "text-amber-500"
                            }`}
                          >
                            {r.status}
                          </span>
                        </div>
                        {r.errorMessage && (
                          <span className="text-[10px] text-red-400 font-mono max-w-[200px] truncate" title={r.errorMessage}>
                            {r.errorMessage}
                          </span>
                        )}
                      </div>
                    </td>
                    <td className="py-3 px-4 font-mono text-[11px] text-muted-foreground">
                      {r.dispatchedAt}
                    </td>
                    <td className="py-3 px-4 text-right">
                      <Button
                        variant="ghost"
                        size="sm"
                        className="h-7 w-7 p-0 text-muted-foreground hover:text-foreground"
                        title="View Dispatch Payload & Audit Trace"
                      >
                        <ExternalLink className="h-3.5 w-3.5" />
                      </Button>
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  );
}
