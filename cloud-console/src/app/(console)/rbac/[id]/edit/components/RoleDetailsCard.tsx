"use client";

import React, { useState } from "react";
import { Copy, Check } from "lucide-react";

// [COMMENT]: Component CopyBadge hỗ trợ copy mã vai trò
function CopyBadge({ value }: { value: string }) {
  const [copied, setCopied] = useState(false);
  const handleCopy = (e: React.MouseEvent) => {
    e.preventDefault();
    e.stopPropagation();
    navigator.clipboard.writeText(value);
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  };
  return (
    <button
      onClick={handleCopy}
      type="button"
      className="inline-flex items-center gap-1.5 px-2 py-0.5 rounded bg-slate-50 hover:bg-slate-100 dark:bg-slate-900/40 dark:hover:bg-slate-800 text-slate-655 dark:text-slate-400 border border-slate-200/60 dark:border-slate-800/80 text-[10px] font-mono transition-colors group select-all cursor-pointer"
    >
      <span>{value}</span>
      {copied ? (
        <Check className="h-3 w-3 text-emerald-500" />
      ) : (
        <Copy className="h-3 w-3 text-slate-400 group-hover:text-slate-600 dark:group-hover:text-slate-355 transition-colors" />
      )}
    </button>
  );
}

interface RoleDetailsCardProps {
  name: string;
  setName: (v: string) => void;
  code: string;
  description: string;
  setDescription: (v: string) => void;
  roleLevel: number;
  submitting: boolean;
}

export default function RoleDetailsCard({
  name,
  setName,
  code,
  description,
  setDescription,
  roleLevel,
  submitting
}: RoleDetailsCardProps) {
  return (
    <div className="bg-white dark:bg-slate-900/60 border border-slate-200 dark:border-slate-800 rounded-xl p-6 shadow-xs space-y-4">
      <h3 className="text-xs font-bold uppercase tracking-wider text-slate-850 dark:text-slate-200 border-b border-slate-150 dark:border-slate-800 pb-2">
        Role Details
      </h3>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        {/* Role Name */}
        <div className="space-y-1.5">
          <label className="text-xs font-bold uppercase tracking-wider text-slate-505 dark:text-slate-400">
            Role Name *
          </label>
          <input
            type="text"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="e.g. Storage Manager"
            required
            disabled={submitting}
            className="w-full h-9 px-3 rounded-lg border border-slate-200 dark:border-slate-800 bg-transparent text-sm text-slate-900 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-600 focus:outline-hidden focus:border-blue-500 dark:focus:border-blue-500 transition-colors disabled:opacity-50"
          />
        </div>

        {/* Role Code / Key */}
        <div className="space-y-1.5">
          <label className="text-xs font-bold uppercase tracking-wider text-slate-505 dark:text-slate-400 block mb-1">
            Role Code / Key
          </label>
          <div className="h-9 flex items-center">
            <CopyBadge value={code} />
          </div>
        </div>
      </div>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        {/* Role Level */}
        <div className="space-y-1.5">
          <label className="text-xs font-bold uppercase tracking-wider text-slate-505 dark:text-slate-400">
            Role Hierarchy Level
          </label>
          <div className="h-9 flex items-center">
            <span
              style={{ backgroundColor: "#2b2112", color: "#F5B642" }}
              className="inline-flex items-center px-2.5 py-0.5 rounded text-[10px] font-bold font-mono tracking-wider border border-[#F5B642]/10 select-all"
            >
              Level {roleLevel}
            </span>
          </div>
        </div>

        {/* Info Text */}
        <div className="flex items-end pb-1.5">
          <span className="text-[10px] text-slate-405 dark:text-slate-600 font-semibold leading-tight">
            Lower levels indicate higher privileges. Level 0 is Root, 1 is Admin. Scope, Code Key, and Hierarchy Level are immutable to ensure system security and taxonomy stability.
          </span>
        </div>
      </div>

      {/* Description */}
      <div className="space-y-1.5">
        <label className="text-xs font-bold uppercase tracking-wider text-slate-505 dark:text-slate-405">
          Description
        </label>
        <textarea
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          placeholder="Provide a brief summary of this role's responsibilities..."
          rows={2}
          disabled={submitting}
          className="w-full px-3 py-2 rounded-lg border border-slate-200 dark:border-slate-800 bg-transparent text-sm text-slate-900 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-600 focus:outline-hidden focus:border-blue-500 dark:focus:border-blue-500 transition-colors disabled:opacity-50 resize-none"
        />
      </div>
    </div>
  );
}
