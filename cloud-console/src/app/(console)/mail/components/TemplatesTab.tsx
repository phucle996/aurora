"use client";

import React, { useState } from "react";
import {
  FileCode,
  Search,
  Plus,
  Filter,
  Eye,
  Edit,
  MoreVertical,
  RotateCcw,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";

// [COMMENT]: Interface đại diện cho thông tin mẫu email (Email Template)
export interface MailTemplateItem {
  id: string;
  name: string;
  subject: string;
  version: number;
  updatedAt: string;
  status: "Active" | "Draft" | "Archived";
  variables: string[];
}

// [COMMENT]: Tab 2 - TemplatesTab quản lý các mẫu email hệ thống & giao dịch (JMAP/HTML Templates)
export function TemplatesTab() {
  const [searchTerm, setSearchTerm] = useState("");
  const [statusFilter, setStatusFilter] = useState<string>("All");

  // [COMMENT]: Dữ liệu mẫu khung giao diện cho danh sách Email Templates
  const templates: MailTemplateItem[] = [
    {
      id: "tpl_account_verify_v2",
      name: "Account Verification Email",
      subject: "Xác thực tài khoản Aurora Cloud Platform",
      version: 2,
      updatedAt: "2026-07-18 14:30:00",
      status: "Active",
      variables: ["activation_link", "username", "expire_hours"],
    },
    {
      id: "tpl_password_reset_v1",
      name: "Password Reset Request",
      subject: "Yêu cầu đặt lại mật khẩu cho tài khoản {username}",
      version: 1,
      updatedAt: "2026-07-15 09:12:00",
      status: "Active",
      variables: ["reset_link", "username", "client_ip"],
    },
    {
      id: "tpl_billing_invoice_v3",
      name: "Monthly Billing Invoice Notification",
      subject: "Hóa đơn dịch vụ tháng {billing_month} - Aurora Cloud",
      version: 3,
      updatedAt: "2026-07-01 00:00:00",
      status: "Active",
      variables: ["invoice_id", "total_amount", "pdf_download_url"],
    },
    {
      id: "tpl_quota_alert_warning",
      name: "Resource Quota Warning",
      subject: "Cảnh báo vượt hạn ngạch tài nguyên [{workspace_name}]",
      version: 1,
      updatedAt: "2026-06-20 16:45:00",
      status: "Draft",
      variables: ["workspace_name", "usage_percent", "resource_type"],
    },
  ];

  const filteredTemplates = templates.filter((tpl) => {
    const matchSearch =
      tpl.name.toLowerCase().includes(searchTerm.toLowerCase()) ||
      tpl.id.toLowerCase().includes(searchTerm.toLowerCase()) ||
      tpl.subject.toLowerCase().includes(searchTerm.toLowerCase());
    const matchStatus = statusFilter === "All" || tpl.status === statusFilter;
    return matchSearch && matchStatus;
  });

  return (
    <div className="flex flex-col gap-4 text-foreground">
      {/* [COMMENT]: Khối 1 - Flat Integrated Toolbar (Thanh tìm kiếm & Nút tạo mới) */}
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-3 border border-border/80 rounded-xl p-3 bg-card">
        <div className="flex items-center gap-2 flex-1 max-w-md">
          <div className="relative w-full">
            <Search className="h-4 w-4 absolute left-3 top-1/2 -translate-y-1/2 text-muted-foreground" />
            <Input
              placeholder="Search template name, ID or subject..."
              value={searchTerm}
              onChange={(e) => setSearchTerm(e.target.value)}
              className="pl-9 h-9 text-xs bg-background rounded-md"
            />
          </div>
        </div>

        <div className="flex items-center gap-2">
          {/* Status Filter Selector */}
          <select
            value={statusFilter}
            onChange={(e) => setStatusFilter(e.target.value)}
            className="h-9 px-3 rounded-md border border-border bg-background text-xs font-semibold text-foreground outline-none cursor-pointer"
          >
            <option value="All">Status: All</option>
            <option value="Active">Status: Active</option>
            <option value="Draft">Status: Draft</option>
            <option value="Archived">Status: Archived</option>
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

          <Button size="sm" className="h-9 text-xs font-semibold gap-1.5 bg-blue-600 hover:bg-blue-700 text-white">
            <Plus className="h-4 w-4" />
            Create Template
          </Button>
        </div>
      </div>

      {/* [COMMENT]: Khối 2 - Table-First Email Template List Container */}
      <div className="border border-border/80 rounded-xl bg-card overflow-hidden">
        <div className="overflow-x-auto">
          <table className="w-full text-left text-xs border-collapse">
            <thead>
              <tr className="border-b border-border/60 bg-muted/30 text-muted-foreground font-semibold text-[11px] uppercase tracking-wider">
                <th className="py-2.5 px-4">Template ID / Name</th>
                <th className="py-2.5 px-4">Default Subject</th>
                <th className="py-2.5 px-4">Variables</th>
                <th className="py-2.5 px-4">Version</th>
                <th className="py-2.5 px-4">Status</th>
                <th className="py-2.5 px-4">Updated At</th>
                <th className="py-2.5 px-4 text-right">Actions</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border/40 font-normal">
              {filteredTemplates.length === 0 ? (
                <tr>
                  <td colSpan={7} className="py-8 text-center text-muted-foreground">
                    Không tìm thấy mẫu email phù hợp
                  </td>
                </tr>
              ) : (
                filteredTemplates.map((tpl) => (
                  <tr
                    key={tpl.id}
                    className="hover:bg-muted/10 transition-colors"
                  >
                    <td className="py-3 px-4">
                      <div className="flex flex-col">
                        <span className="font-semibold text-foreground text-sm">
                          {tpl.name}
                        </span>
                        <span className="text-[11px] font-mono text-muted-foreground select-all">
                          {tpl.id}
                        </span>
                      </div>
                    </td>
                    <td className="py-3 px-4 font-mono text-muted-foreground max-w-[240px] truncate">
                      {tpl.subject}
                    </td>
                    <td className="py-3 px-4">
                      <div className="flex flex-wrap gap-1 max-w-[200px]">
                        {tpl.variables.map((v) => (
                          <span
                            key={v}
                            className="text-[10px] font-mono px-1.5 py-0.5 rounded bg-muted text-muted-foreground border border-border/40"
                          >
                            {v}
                          </span>
                        ))}
                      </div>
                    </td>
                    <td className="py-3 px-4 font-mono text-xs">
                      v{tpl.version}
                    </td>
                    <td className="py-3 px-4">
                      <div className="flex items-center gap-1.5">
                        <span
                          className={`h-2 w-2 rounded-full ${
                            tpl.status === "Active"
                              ? "bg-emerald-500"
                              : tpl.status === "Draft"
                              ? "bg-amber-500"
                              : "bg-slate-400"
                          }`}
                        />
                        <span className="font-medium text-foreground">
                          {tpl.status}
                        </span>
                      </div>
                    </td>
                    <td className="py-3 px-4 font-mono text-[11px] text-muted-foreground">
                      {tpl.updatedAt}
                    </td>
                    <td className="py-3 px-4 text-right">
                      <div className="flex items-center justify-end gap-1">
                        <Button
                          variant="ghost"
                          size="sm"
                          className="h-7 w-7 p-0 text-muted-foreground hover:text-foreground"
                          title="Preview Template"
                        >
                          <Eye className="h-3.5 w-3.5" />
                        </Button>
                        <Button
                          variant="ghost"
                          size="sm"
                          className="h-7 w-7 p-0 text-muted-foreground hover:text-foreground"
                          title="Edit Template"
                        >
                          <Edit className="h-3.5 w-3.5" />
                        </Button>
                      </div>
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
