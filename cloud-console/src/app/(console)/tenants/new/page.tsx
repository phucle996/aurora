"use client";

import React, { useState } from "react";
import { useRouter } from "next/navigation";
import { toast } from "sonner";
import { ArrowLeft } from "lucide-react";

// [COMMENT]: Import API tạo tenant từ api module
import { createTenant, type CreateTenantPayload } from "@/features/tenants/api";

export default function CreateTenantPage() {
  const router = useRouter();

  // [COMMENT]: State lưu thông tin các trường nhập dữ liệu của Form tạo mới tổ chức
  const [orgName, setOrgName] = useState("");
  const [orgCode, setOrgCode] = useState("");
  const [orgDomain, setOrgDomain] = useState("");
  const [isSubmitting, setIsSubmitting] = useState(false);

  // [COMMENT]: Xử lý gửi Form tạo tổ chức mới lên server
  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();

    // [COMMENT]: Kiểm tra các trường dữ liệu bắt buộc đầu vào
    if (!orgName.trim()) {
      toast.error("Organization Display Name is required.");
      return;
    }
    if (!orgCode.trim()) {
      toast.error("Organization unique code is required.");
      return;
    }
    if (!orgDomain.trim()) {
      toast.error("Primary domain is required.");
      return;
    }

    // [COMMENT]: Kiểm tra định dạng Regex của code (chỉ chứa chữ thường, số, dấu gạch ngang, gạch dưới) theo đúng SoT
    const codeRegex = /^[a-z0-9-_]+$/;
    if (!codeRegex.test(orgCode)) {
      toast.error("Code format is invalid. Only lowercase letters, numbers, hyphens, and underscores are allowed.");
      return;
    }

    setIsSubmitting(true);

    try {
      const payload: CreateTenantPayload = {
        name: orgName.trim(),
        code: orgCode.trim(),
        primary_domain: orgDomain.trim(),
      };

      // [COMMENT]: Gọi API lưu trữ dữ liệu lên DB Control Plane
      await createTenant(payload);

      toast.success("Organization created successfully!");
      
      // [COMMENT]: Chuyển hướng người dùng quay lại trang danh sách tổ chức sau khi tạo thành công
      router.push("/tenants");
    } catch (err: unknown) {
      console.error("[Tenant] Failed to create tenant", err);
      const errMsg = err instanceof Error ? err.message : "Failed to create organization. Code might already exist.";
      toast.error(errMsg);
    } finally {
      setIsSubmitting(false);
    }
  };

  return (
    <div className="max-w-[640px] w-full mx-auto space-y-6">
      
      {/* Breadcrumb điều hướng quay lại trang danh sách */}
      <div className="flex items-center gap-2 select-none">
        <button
          onClick={() => router.push("/tenants")}
          className="text-xs font-semibold text-slate-400 hover:text-slate-650 dark:text-slate-500 dark:hover:text-slate-350 transition-all flex items-center gap-1 cursor-pointer"
        >
          <ArrowLeft className="h-3 w-3" />
          Organizations
        </button>
        <span className="text-xs text-slate-300 dark:text-slate-750">/</span>
        <span className="text-xs font-bold text-slate-700 dark:text-slate-300">New</span>
      </div>

      {/* Tiêu đề trang */}
      <div className="space-y-1 select-none">
        <h1 className="text-2xl font-bold tracking-tight text-slate-800 dark:text-slate-100">
          Create New Organization
        </h1>
        <p className="text-sm text-slate-500 dark:text-slate-400">
          Set up a logical organization identity domain for cloud workspace provisioning.
        </p>
      </div>

      {/* Form Card */}
      <div className="bg-white dark:bg-[#111827]/40 border border-slate-200 dark:border-slate-800 rounded-xl p-8 shadow-xs">
        <form onSubmit={handleSubmit} className="space-y-5">
          
          {/* Trường Display Name */}
          <div className="space-y-1.5">
            <label className="text-xs font-bold text-slate-700 dark:text-slate-350 block">
              Organization Display Name
            </label>
            <input
              type="text"
              required
              placeholder="e.g. Acme Corporation"
              value={orgName}
              onChange={(e) => {
                setOrgName(e.target.value);
                // [COMMENT]: Gợi ý sinh code viết thường tự động từ Display Name của user
                const slug = e.target.value
                  .toLowerCase()
                  .replace(/[^a-z0-9-_]/g, "-")
                  .replace(/-+/g, "-")
                  .replace(/^-|-$/g, "");
                setOrgCode(slug);
              }}
              disabled={isSubmitting}
              className="w-full h-10 px-3 rounded-lg border border-slate-300 dark:border-slate-800 bg-transparent text-xs text-slate-850 dark:text-slate-100 placeholder-slate-450 outline-none focus:border-blue-500 transition-all"
            />
          </div>

          {/* Trường Unique Code */}
          <div className="space-y-1.5">
            <label className="text-xs font-bold text-slate-700 dark:text-slate-350 block">
              Unique Code (URL path identifier)
            </label>
            <input
              type="text"
              required
              placeholder="e.g. acme-corp"
              value={orgCode}
              onChange={(e) => setOrgCode(e.target.value)}
              disabled={isSubmitting}
              className="w-full h-10 px-3 rounded-lg border border-slate-300 dark:border-slate-800 bg-transparent text-xs font-mono text-slate-850 dark:text-slate-200 placeholder-slate-450 outline-none focus:border-blue-500 transition-all"
            />
            <p className="text-[10px] text-slate-450 dark:text-slate-500">
              Must be unique. Format: lowercase letters, numbers, hyphens, and underscores only.
            </p>
          </div>

          {/* Trường Primary Domain */}
          <div className="space-y-1.5">
            <label className="text-xs font-bold text-slate-700 dark:text-slate-350 block">
              Primary Domain
            </label>
            <input
              type="text"
              required
              placeholder="e.g. acme.com"
              value={orgDomain}
              onChange={(e) => setOrgDomain(e.target.value)}
              disabled={isSubmitting}
              className="w-full h-10 px-3 rounded-lg border border-slate-300 dark:border-slate-800 bg-transparent text-xs text-slate-850 dark:text-slate-200 placeholder-slate-455 outline-none focus:border-blue-500 transition-all"
            />
          </div>

          {/* Action Buttons */}
          <div className="flex items-center justify-end gap-3 pt-4 border-t border-slate-100 dark:border-slate-855 mt-6">
            <button
              type="button"
              onClick={() => router.push("/tenants")}
              disabled={isSubmitting}
              className="h-9 px-4 border border-slate-200 dark:border-slate-800 hover:bg-slate-100 dark:hover:bg-slate-900 text-xs font-bold rounded-lg text-slate-750 dark:text-slate-350 cursor-pointer transition-all"
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={isSubmitting}
              className="h-9 px-5 bg-blue-600 hover:bg-blue-700 disabled:bg-blue-800 text-white text-xs font-bold rounded-lg cursor-pointer flex items-center gap-1.5 shadow-sm transition-all"
            >
              {isSubmitting ? (
                <span className="flex items-center gap-1.5">
                  <span className="h-3 w-3 animate-spin rounded-full border border-white/30 border-t-white" />
                  Creating...
                </span>
              ) : (
                "Create Organization"
              )}
            </button>
          </div>

        </form>
      </div>

    </div>
  );
}
