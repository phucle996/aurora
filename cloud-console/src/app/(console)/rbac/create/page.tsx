"use client";

import React, { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import {
  Shield,
  ArrowLeft,
  Loader2,
  Save,
  CheckSquare,
  Square,
  ChevronDown,
  ChevronRight,
  Upload,
  FileJson,
  Search,
  X,
  Info
} from "lucide-react";
import { toast } from "sonner";
import { listPermissions, createRole, type PermissionItem } from "@/lib/api/session";
import RouteGuard from "@/components/route-guard";

function CreateRoleContent() {
  const router = useRouter();
  const [loading, setLoading] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [permissions, setPermissions] = useState<PermissionItem[]>([]);

  // [COMMENT]: Khởi tạo state cho các trường thông tin của Role và tìm kiếm
  const [name, setName] = useState("");
  const [code, setCode] = useState("");
  const [description, setDescription] = useState("");
  const [roleLevel, setRoleLevel] = useState(8);
  const [selectedPerms, setSelectedPerms] = useState<string[]>([]);
  const [searchQuery, setSearchQuery] = useState("");

  // [COMMENT]: State quản lý trạng thái collapse/expand của Module và Object
  const [expandedModules, setExpandedModules] = useState<Record<string, boolean>>({});
  const [expandedObjects, setExpandedObjects] = useState<Record<string, boolean>>({});

  // [COMMENT]: State quản lý việc hiển thị panel Import JSON hoặc File
  const [showImport, setShowImport] = useState(false);
  const [importText, setImportText] = useState("");

  // [COMMENT]: Load danh sách permissions catalog từ Backend API
  useEffect(() => {
    let active = true;
    async function loadData() {
      setLoading(true);
      try {
        const data = await listPermissions();
        if (active) {
          setPermissions(data);

          // Mặc định expand tất cả các Module khi hiển thị lần đầu
          const initialModules: Record<string, boolean> = {};
          data.forEach((p) => {
            if (p.module) {
              initialModules[p.module] = true;
            }
          });
          setExpandedModules(initialModules);
        }
      } catch (err) {
        console.error(err);
        toast.error("Failed to load permissions catalog.");
      } finally {
        if (active) {
          setLoading(false);
        }
      }
    }
    void loadData();
    return () => {
      active = false;
    };
  }, []);

  // [COMMENT]: Xử lý nhóm và lọc permissions phẳng thành cấu trúc cây 3 bậc (Module -> Object -> Behaviors)
  const tree = React.useMemo(() => {
    const root: Record<string, {
      name: string;
      objects: Record<string, {
        name: string;
        behaviors: { id: string; behavior: string; description: string; fullName: string }[];
      }>;
    }> = {};

    permissions.forEach((perm) => {
      const fullName = `${perm.module}:${perm.object}:${perm.behavior}`;

      // Lọc theo từ khóa tìm kiếm (hỗ trợ tìm kiếm theo key hoặc mô tả)
      if (searchQuery.trim()) {
        const query = searchQuery.toLowerCase();
        const matchesQuery =
          fullName.toLowerCase().includes(query) ||
          perm.description?.toLowerCase().includes(query);
        if (!matchesQuery) return;
      }

      const mod = perm.module || "other";
      const obj = perm.object || "default";

      if (!root[mod]) {
        root[mod] = { name: mod, objects: {} };
      }
      if (!root[mod].objects[obj]) {
        root[mod].objects[obj] = { name: obj, behaviors: [] };
      }
      root[mod].objects[obj].behaviors.push({
        id: perm.id,
        behavior: perm.behavior,
        description: perm.description,
        fullName,
      });
    });

    return root;
  }, [permissions, searchQuery]);

  // [COMMENT]: Nhóm các quyền đã chọn phục vụ hiển thị Preview phân cấp ở Cột Phải
  const selectedTree = React.useMemo(() => {
    const root: Record<string, {
      name: string;
      objects: Record<string, {
        name: string;
        behaviors: string[];
      }>;
    }> = {};

    selectedPerms.forEach((id) => {
      const perm = permissions.find((p) => p.id === id);
      if (!perm) return;
      const mod = perm.module || "other";
      const obj = perm.object || "default";

      if (!root[mod]) {
        root[mod] = { name: mod, objects: {} };
      }
      if (!root[mod].objects[obj]) {
        root[mod].objects[obj] = { name: obj, behaviors: [] };
      }
      if (!root[mod].objects[obj].behaviors.includes(perm.behavior)) {
        root[mod].objects[obj].behaviors.push(perm.behavior);
      }
    });

    return root;
  }, [permissions, selectedPerms]);

  // [COMMENT]: Toggle chọn/bỏ chọn một quyền cụ thể
  const handleTogglePerm = (id: string) => {
    setSelectedPerms((prev) =>
      prev.includes(id) ? prev.filter((pId) => pId !== id) : [...prev, id]
    );
  };

  // [COMMENT]: Đọc nội dung file upload và gán vào khung nhập dữ liệu Import
  const handleFileUpload = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (!file) return;
    const reader = new FileReader();
    reader.onload = (event) => {
      const text = event.target?.result as string;
      setImportText(text);
    };
    reader.readAsText(file);
  };

  // [COMMENT]: Thực hiện phân tích cú pháp (JSON/Text) và ánh xạ sang mã quyền, hỗ trợ khớp wildcard (*)
  const handleImport = () => {
    if (!importText.trim()) {
      toast.error("Please paste JSON or input text first.");
      return;
    }

    let patterns: string[] = [];

    // Thử parse dạng JSON array
    try {
      const parsed = JSON.parse(importText);
      if (Array.isArray(parsed)) {
        patterns = parsed.map((p) => String(p));
      } else {
        patterns = [String(parsed)];
      }
    } catch {
      // Phân tách các chuỗi quyền theo dòng hoặc dấu phẩy
      patterns = importText
        .split(/[\n,]/)
        .map((p) => p.trim())
        .filter(Boolean);
    }

    if (patterns.length === 0) {
      toast.error("No permission patterns found to import.");
      return;
    }

    const escapeRegExp = (str: string) => {
      return str.replace(/[.+^${}()|[\]\\]/g, "\\$&");
    };

    // Khớp mẫu regex có chứa ký tự wildcard (*)
    const matchPattern = (pattern: string, permKey: string): boolean => {
      const cleanPat = pattern.trim().toLowerCase();
      const cleanKey = permKey.toLowerCase();
      if (cleanPat === cleanKey) return true;
      if (!cleanPat.includes("*")) return false;

      const regexStr = "^" + cleanPat.split("*").map(escapeRegExp).join(".*") + "$";
      try {
        return new RegExp(regexStr).test(cleanKey);
      } catch {
        return false;
      }
    };

    // Quét tìm tất cả các quyền thỏa mãn pattern
    const matchedIds: string[] = [];
    patterns.forEach((pattern) => {
      permissions.forEach((perm) => {
        const permKey = `${perm.module}:${perm.object}:${perm.behavior}`;
        if (matchPattern(pattern, permKey) || matchPattern(pattern, perm.id)) {
          if (!matchedIds.includes(perm.id)) {
            matchedIds.push(perm.id);
          }
        }
      });
    });

    if (matchedIds.length === 0) {
      toast.error("No matching permissions found in the catalog.");
      return;
    }

    // Cập nhật danh sách quyền được lựa chọn
    setSelectedPerms((prev) => {
      const newPerms = [...prev];
      matchedIds.forEach((id) => {
        if (!newPerms.includes(id)) {
          newPerms.push(id);
        }
      });
      return newPerms;
    });

    toast.success(`Successfully imported and matched ${matchedIds.length} permissions!`);
    setImportText("");
    setShowImport(false);
  };

  // [COMMENT]: Xử lý submit tạo mới Role thông qua API backend
  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!name.trim()) {
      toast.error("Role name is required.");
      return;
    }
    const cleanCode = code.trim().toLowerCase().replace(/[^a-z0-9_]/g, "_");
    if (!cleanCode) {
      toast.error("Role code is required (alphanumeric and underscores only).");
      return;
    }

    setSubmitting(true);
    try {
      await createRole({
        name: name.trim(),
        code: cleanCode,
        description: description.trim(),
        role_level: Number(roleLevel),
        scope: "platform", // Luôn gửi scope: platform theo nghiệp vụ console admin
        permission_ids: selectedPerms,
      });
      toast.success("Role created successfully.");
      router.push("/rbac");
    } catch (err) {
      console.error(err);
      toast.error(err instanceof Error ? err.message : "Failed to create role.");
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="space-y-6">
      {/* Header Area */}
      <div className="flex items-center gap-3 border-b border-slate-200 dark:border-slate-800 pb-5">
        <button
          onClick={() => router.push("/rbac")}
          className="flex items-center justify-center h-8 w-8 rounded-lg border border-slate-200 dark:border-slate-800 hover:bg-slate-50 dark:hover:bg-slate-800 text-slate-500 dark:text-slate-400 hover:text-slate-800 dark:hover:text-slate-200 transition-colors cursor-pointer"
        >
          <ArrowLeft className="h-4 w-4" />
        </button>
        <div>
          <h1 className="text-xl font-bold tracking-tight text-slate-900 dark:text-slate-100 flex items-center gap-2">
            <Shield className="h-5 w-5 text-blue-500" />
            <span>Create System Role</span>
          </h1>
          <p className="mt-1 text-xs font-semibold text-slate-500 dark:text-slate-400">
            Define a new security authority, set hierarchy level, and grant permissions.
          </p>
        </div>
      </div>

      <form onSubmit={(e) => void handleSubmit(e)}>
        <div className="grid grid-cols-1 lg:grid-cols-12 gap-6 items-start">

          {/* ======================================================== */}
          {/* CỘT TRÁI (2/3 chiều rộng = 8/12): Role Details & Grant Permissions */}
          {/* ======================================================== */}
          <div className="lg:col-span-8 space-y-6">

            {/* Form Fields Card (Role Details) */}
            <div className="bg-white dark:bg-slate-900/60 border border-slate-200 dark:border-slate-800 rounded-xl p-6 shadow-xs space-y-4">
              <h3 className="text-xs font-bold uppercase tracking-wider text-slate-850 dark:text-slate-200 border-b border-slate-150 dark:border-slate-800 pb-2">
                Role Details
              </h3>

              <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                {/* Role Name */}
                <div className="space-y-1.5">
                  <label className="text-xs font-bold uppercase tracking-wider text-slate-500 dark:text-slate-400">
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
                  <label className="text-xs font-bold uppercase tracking-wider text-slate-500 dark:text-slate-400">
                    Role Code / Key *
                  </label>
                  <input
                    type="text"
                    value={code}
                    onChange={(e) =>
                      setCode(e.target.value.toLowerCase().replace(/[^a-z0-9_]/g, "_"))
                    }
                    placeholder="e.g. storage_manager"
                    required
                    disabled={submitting}
                    className="w-full h-9 px-3 rounded-lg border border-slate-200 dark:border-slate-800 bg-transparent text-sm font-mono text-slate-900 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-600 focus:outline-hidden focus:border-blue-500 dark:focus:border-blue-500 transition-colors disabled:opacity-50"
                  />
                </div>
              </div>

              <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                {/* Role Level */}
                <div className="space-y-1.5">
                  <label className="text-xs font-bold uppercase tracking-wider text-slate-500 dark:text-slate-400">
                    Role Hierarchy Level (0 - 99) *
                  </label>
                  <input
                    type="number"
                    min={0}
                    max={99}
                    value={roleLevel}
                    onChange={(e) => setRoleLevel(Math.min(99, Math.max(0, Number(e.target.value))))}
                    required
                    disabled={submitting}
                    className="w-full h-9 px-3 rounded-lg border border-slate-200 dark:border-slate-800 bg-transparent text-sm text-slate-900 dark:text-slate-100 focus:outline-hidden focus:border-blue-500 dark:focus:border-blue-500 transition-colors disabled:opacity-50"
                  />
                </div>

                {/* Info Text */}
                <div className="flex items-end pb-1.5">
                  <span className="text-[10px] text-slate-400 dark:text-slate-600 font-semibold leading-tight">
                    Lower levels indicate higher privileges. Level 0 is Root, 1 is Admin. Lowercase, digits, and underscores only for Code Key.
                  </span>
                </div>
              </div>

              {/* Description */}
              <div className="space-y-1.5">
                <label className="text-xs font-bold uppercase tracking-wider text-slate-500 dark:text-slate-400">
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

            {/* Grant Permissions Card */}
            <div className="bg-white dark:bg-slate-900/60 border border-slate-200 dark:border-slate-800 rounded-xl p-6 shadow-xs space-y-5">

              {/* Header Actions */}
              <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 border-b border-slate-200 dark:border-slate-850 pb-4">
                <div className="space-y-1">
                  <h2 className="text-sm font-bold uppercase tracking-wider text-slate-800 dark:text-slate-200">
                    Grant Permissions
                  </h2>
                  <span className="text-[10px] font-semibold text-slate-400 dark:text-slate-500">
                    Search and configure authorization tags hierarchically
                  </span>
                </div>

                <div className="flex items-center gap-2">
                  <button
                    type="button"
                    onClick={() => setShowImport((prev) => !prev)}
                    className="flex items-center justify-center h-8 px-3 rounded-lg border border-slate-200 dark:border-slate-800 hover:bg-slate-50 dark:hover:bg-slate-855 text-xs font-semibold text-slate-600 dark:text-slate-300 transition-colors cursor-pointer"
                  >
                    <FileJson className="h-3.5 w-3.5 mr-1.5 text-blue-500" />
                    <span>Import JSON / File</span>
                  </button>
                </div>
              </div>

              {/* Import Collapsible Panel */}
              {showImport && (
                <div className="p-4 border border-dashed border-blue-500/25 dark:border-blue-500/35 bg-blue-500/[0.02] rounded-xl space-y-3.5">
                  <div className="flex items-center justify-between">
                    <span className="text-xs font-bold text-slate-800 dark:text-slate-200 flex items-center gap-1.5">
                      <Upload className="h-3.5 w-3.5 text-blue-500" />
                      <span>Upload Permission Matrix File / Paste JSON</span>
                    </span>
                    <button
                      type="button"
                      onClick={() => setShowImport(false)}
                      className="p-1 hover:bg-slate-100 dark:hover:bg-slate-800 rounded text-slate-400 hover:text-slate-600 cursor-pointer"
                    >
                      <X className="h-4 w-4" />
                    </button>
                  </div>

                  <div className="grid grid-cols-1 gap-3">
                    {/* Drag and Drop Zone */}
                    <div className="relative border border-dashed border-slate-200 dark:border-slate-800 hover:border-blue-500/50 dark:hover:border-blue-500/50 rounded-lg p-4 text-center cursor-pointer transition-colors group">
                      <input
                        type="file"
                        accept=".json,.txt"
                        onChange={handleFileUpload}
                        className="absolute inset-0 w-full h-full opacity-0 cursor-pointer"
                      />
                      <Upload className="h-5 w-5 mx-auto text-slate-400 group-hover:text-blue-500 transition-colors mb-1" />
                      <p className="text-[10px] font-bold text-slate-600 dark:text-slate-400">
                        Drag and drop a .json or .txt file here, or click to upload
                      </p>
                    </div>

                    {/* Textarea */}
                    <textarea
                      value={importText}
                      onChange={(e) => setImportText(e.target.value)}
                      placeholder='Paste JSON array e.g: ["iam:role:*", "storage:bucket:create"]&#10;Or list of items line-by-line...'
                      rows={4}
                      className="w-full p-2.5 rounded-lg border border-slate-200 dark:border-slate-800 bg-slate-950/20 text-xs font-mono text-slate-900 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-600 focus:outline-hidden focus:border-blue-500 transition-colors resize-none"
                    />
                  </div>

                  <div className="flex items-center gap-2 justify-end">
                    <span className="text-[9px] text-slate-400 dark:text-slate-500 font-semibold mr-auto flex items-center gap-1">
                      <Info className="h-3 w-3 text-blue-500" />
                      Supports wildcard * (e.g. iam:*:read)
                    </span>

                    <button
                      type="button"
                      onClick={() => setImportText("")}
                      className="h-7 px-3 rounded bg-slate-100 hover:bg-slate-200 dark:bg-slate-800 dark:hover:bg-slate-750 text-[10px] font-bold text-slate-600 dark:text-slate-350 transition-colors cursor-pointer"
                    >
                      Clear
                    </button>

                    <button
                      type="button"
                      onClick={handleImport}
                      className="h-7 px-3 rounded bg-blue-600 hover:bg-blue-700 text-[10px] font-bold text-white transition-colors cursor-pointer"
                    >
                      Apply Import
                    </button>
                  </div>
                </div>
              )}

              {/* Search input field */}
              <div className="relative">
                <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-slate-400 dark:text-slate-500" />
                <input
                  type="text"
                  value={searchQuery}
                  onChange={(e) => setSearchQuery(e.target.value)}
                  placeholder="Filter permissions by module, object, behavior, or description..."
                  className="w-full h-8 pl-9 pr-3 rounded-lg border border-slate-200 dark:border-slate-800 bg-transparent text-xs text-slate-900 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-600 focus:outline-hidden focus:border-blue-500 transition-colors"
                />
                {searchQuery && (
                  <button
                    type="button"
                    onClick={() => setSearchQuery("")}
                    className="absolute right-3 top-1/2 -translate-y-1/2 text-slate-400 hover:text-slate-600 p-0.5 rounded-full cursor-pointer"
                  >
                    <X className="h-3 w-3" />
                  </button>
                )}
              </div>

              {/* 3-Level Collapsible Tree rendering */}
              {loading ? (
                <div className="flex flex-col items-center justify-center py-20 text-slate-500">
                  <Loader2 className="h-6 w-6 animate-spin text-blue-500 mb-2.5" />
                  <span className="text-xs font-semibold uppercase tracking-wider animate-pulse">Loading Catalog...</span>
                </div>
              ) : Object.keys(tree).length === 0 ? (
                <div className="text-center py-12 text-xs font-semibold text-slate-500 select-none">
                  No matching permissions found in the database.
                </div>
              ) : (
                <div className="space-y-4">
                  {Object.values(tree).map((modNode) => {
                    const isModExpanded = !!expandedModules[modNode.name];

                    // Calculate module states
                    const moduleBehaviors: string[] = [];
                    Object.values(modNode.objects).forEach((o) => {
                      o.behaviors.forEach((b) => moduleBehaviors.push(b.id));
                    });
                    const moduleSelectedCount = moduleBehaviors.filter((id) => selectedPerms.includes(id)).length;
                    const isModuleAllSelected = moduleBehaviors.length > 0 && moduleSelectedCount === moduleBehaviors.length;
                    const isModuleSomeSelected = moduleSelectedCount > 0 && !isModuleAllSelected;

                    return (
                      <div
                        key={modNode.name}
                        className="border border-slate-200 dark:border-slate-800/80 rounded-lg overflow-hidden bg-slate-50/[0.15] dark:bg-slate-950/5"
                      >
                        {/* Module Row Header (Level 1) */}
                        <div className="flex items-center justify-between px-3 py-2 bg-slate-100/40 dark:bg-slate-900/35 border-b border-slate-200 dark:border-slate-850">
                          <div className="flex items-center gap-1.5">
                            <button
                              type="button"
                              onClick={() =>
                                setExpandedModules((prev) => ({ ...prev, [modNode.name]: !prev[modNode.name] }))
                              }
                              className="p-1 hover:bg-slate-200 dark:hover:bg-slate-800 rounded text-slate-500 dark:text-slate-400 cursor-pointer transition-colors"
                            >
                              {isModExpanded ? <ChevronDown className="h-4 w-4" /> : <ChevronRight className="h-4 w-4" />}
                            </button>

                            <button
                              type="button"
                              onClick={() => {
                                if (isModuleAllSelected) {
                                  setSelectedPerms((prev) => prev.filter((id) => !moduleBehaviors.includes(id)));
                                } else {
                                  setSelectedPerms((prev) => {
                                    const filtered = prev.filter((id) => !moduleBehaviors.includes(id));
                                    return [...filtered, ...moduleBehaviors];
                                  });
                                }
                              }}
                              className="flex items-center text-slate-500 dark:text-slate-400 cursor-pointer"
                            >
                              {isModuleAllSelected ? (
                                <CheckSquare className="h-4 w-4 text-blue-500 mr-1.5" />
                              ) : isModuleSomeSelected ? (
                                <div className="h-4 w-4 border border-blue-500 rounded bg-blue-500/20 mr-1.5 flex items-center justify-center">
                                  <div className="h-0.5 w-2 bg-blue-500" />
                                </div>
                              ) : (
                                <Square className="h-4 w-4 mr-1.5" />
                              )}
                            </button>

                            <span className="text-xs font-bold uppercase tracking-widest text-slate-855 dark:text-slate-200 font-mono">
                              Module: {modNode.name}
                            </span>
                            <span className="text-[10px] text-slate-400 dark:text-slate-500 font-bold ml-1">
                              ({moduleSelectedCount}/{moduleBehaviors.length})
                            </span>
                          </div>
                        </div>

                        {/* Module Objects list (Level 2) */}
                        {isModExpanded && (
                          <div className="p-3.5 space-y-3.5">
                            {Object.values(modNode.objects).map((objNode) => {
                              const uniqueKey = `${modNode.name}:${objNode.name}`;
                              const isObjExpanded = !!expandedObjects[uniqueKey];

                              const objBehaviors = objNode.behaviors.map((b) => b.id);
                              const objSelectedCount = objBehaviors.filter((id) => selectedPerms.includes(id)).length;
                              const isObjAllSelected = objBehaviors.length > 0 && objSelectedCount === objBehaviors.length;
                              const isObjSomeSelected = objSelectedCount > 0 && !isObjAllSelected;

                              return (
                                <div
                                  key={objNode.name}
                                  className="ml-1.5 border-l-2 border-slate-200 dark:border-slate-800 pl-3.5 space-y-2"
                                >
                                  {/* Object Header Row */}
                                  <div className="flex items-center gap-1.5 py-0.5">
                                    <button
                                      type="button"
                                      onClick={() =>
                                        setExpandedObjects((prev) => ({ ...prev, [uniqueKey]: !prev[uniqueKey] }))
                                      }
                                      className="p-0.5 hover:bg-slate-200 dark:hover:bg-slate-850 rounded text-slate-400 hover:text-slate-650 transition-colors cursor-pointer"
                                    >
                                      {isObjExpanded ? <ChevronDown className="h-3.5 w-3.5" /> : <ChevronRight className="h-3.5 w-3.5" />}
                                    </button>

                                    <button
                                      type="button"
                                      onClick={() => {
                                        if (isObjAllSelected) {
                                          setSelectedPerms((prev) => prev.filter((id) => !objBehaviors.includes(id)));
                                        } else {
                                          setSelectedPerms((prev) => {
                                            const filtered = prev.filter((id) => !objBehaviors.includes(id));
                                            return [...filtered, ...objBehaviors];
                                          });
                                        }
                                      }}
                                      className="flex items-center text-slate-500 dark:text-slate-400 cursor-pointer"
                                    >
                                      {isObjAllSelected ? (
                                        <CheckSquare className="h-3.5 w-3.5 text-blue-500 mr-1.5" />
                                      ) : isObjSomeSelected ? (
                                        <div className="h-3.5 w-3.5 border border-blue-500 rounded bg-blue-500/20 mr-1.5 flex items-center justify-center">
                                          <div className="h-0.5 w-1.5 bg-blue-500" />
                                        </div>
                                      ) : (
                                        <Square className="h-3.5 w-3.5 mr-1.5" />
                                      )}
                                    </button>

                                    <span className="text-xs font-bold text-slate-700 dark:text-slate-300 font-mono">
                                      {objNode.name}
                                    </span>
                                    <span className="text-[10px] text-slate-400 dark:text-slate-500 font-semibold">
                                      ({objSelectedCount}/{objBehaviors.length})
                                    </span>
                                  </div>

                                  {/* Behaviors grid checkboxes (Level 3) */}
                                  {isObjExpanded && (
                                    <div className="ml-5 grid grid-cols-1 sm:grid-cols-2 md:grid-cols-3 gap-2 pt-1 pb-1.5">
                                      {objNode.behaviors.map((b) => {
                                        const isChecked = selectedPerms.includes(b.id);
                                        return (
                                          <div
                                            key={b.id}
                                            onClick={() => handleTogglePerm(b.id)}
                                            className={`flex items-start gap-2 p-2 rounded-lg border transition-all cursor-pointer select-none group ${isChecked
                                                ? "bg-blue-500/10 border-blue-500/30 text-blue-200"
                                                : "bg-white dark:bg-slate-900/40 border-slate-200 dark:border-slate-800/80 hover:border-slate-355 dark:hover:border-slate-705"
                                              }`}
                                          >
                                            <div className="mt-0.5 text-slate-400 group-hover:text-blue-500 transition-colors">
                                              {isChecked ? (
                                                <CheckSquare className="h-3.5 w-3.5 text-blue-500" />
                                              ) : (
                                                <Square className="h-3.5 w-3.5" />
                                              )}
                                            </div>
                                            <div className="space-y-0.5 leading-none">
                                              <span className="text-[11px] font-bold font-mono text-slate-800 dark:text-slate-200 group-hover:text-blue-400 transition-colors">
                                                {b.behavior}
                                              </span>
                                              {b.description && (
                                                <p className="text-[9px] text-slate-400 dark:text-slate-500 font-medium leading-normal">
                                                  {b.description}
                                                </p>
                                              )}
                                            </div>
                                          </div>
                                        );
                                      })}
                                    </div>
                                  )}
                                </div>
                              );
                            })}
                          </div>
                        )}
                      </div>
                    );
                  })}
                </div>
              )}
            </div>

            {/* Submit Action Buttons */}
            <div className="flex items-center gap-3 border-t border-slate-200 dark:border-slate-800 pt-5 mt-6">
              <button
                type="submit"
                disabled={submitting}
                className="flex items-center justify-center h-9 px-4 rounded-lg bg-blue-600 hover:bg-blue-700 text-xs font-semibold text-white transition-colors cursor-pointer shadow-xs disabled:opacity-50"
              >
                {submitting ? (
                  <>
                    <Loader2 className="h-3.5 w-3.5 animate-spin mr-1.5" />
                    <span>Saving Role...</span>
                  </>
                ) : (
                  <>
                    <Save className="h-3.5 w-3.5 mr-1.5" />
                    <span>Save Role</span>
                  </>
                )}
              </button>

              <button
                type="button"
                onClick={() => router.push("/rbac")}
                disabled={submitting}
                className="flex items-center justify-center h-9 px-4 rounded-lg border border-slate-200 dark:border-slate-800 hover:bg-slate-50 dark:hover:bg-slate-800 text-xs font-semibold text-slate-600 dark:text-slate-300 transition-colors disabled:opacity-50 cursor-pointer"
              >
                Cancel
              </button>
            </div>

          </div>

          {/* ======================================================== */}
          {/* CỘT PHẢI (1/3 chiều rộng = 4/12): Selected Preview (Sticky) */}
          {/* ======================================================== */}
          <div className="lg:col-span-4 lg:sticky lg:top-6 space-y-6">

            {/* Selected Preview Card */}
            <div className="bg-white dark:bg-slate-900/60 border border-slate-200 dark:border-slate-800 rounded-xl p-5 shadow-xs space-y-4">
              <div className="border-b border-slate-200 dark:border-slate-800 pb-3 flex items-center justify-between">
                <h3 className="text-xs font-bold uppercase tracking-wider text-slate-850 dark:text-slate-200">
                  Selected Preview
                </h3>
                <span className="text-[10px] font-bold text-blue-500 bg-blue-500/10 px-2.5 py-0.5 rounded-full border border-blue-500/15">
                  {selectedPerms.length} Selected
                </span>
              </div>

              {selectedPerms.length === 0 ? (
                <div className="text-center py-8 text-xs text-slate-400 dark:text-slate-500 font-semibold leading-relaxed">
                  No permissions selected yet.<br />Toggles are available on the left tree.
                </div>
              ) : (
                <div className="space-y-3 max-h-120 overflow-y-auto pr-1">
                  {Object.values(selectedTree).map((modNode) => (
                    <div key={modNode.name} className="space-y-1.5">
                      <div className="text-xs font-bold font-mono text-blue-500 flex items-center gap-1.5">
                        <span className="h-1.5 w-1.5 rounded-full bg-blue-500 animate-pulse" />
                        <span>{modNode.name}</span>
                      </div>

                      <div className="pl-3.5 space-y-2 border-l border-slate-200 dark:border-slate-850 ml-0.5">
                        {Object.values(modNode.objects).map((objNode) => (
                          <div key={objNode.name} className="space-y-1">
                            <div className="text-[11px] font-semibold font-mono text-slate-700 dark:text-slate-400">
                              {objNode.name}
                            </div>
                            <div className="flex flex-wrap gap-1">
                              {objNode.behaviors.map((behavior) => (
                                <span
                                  key={behavior}
                                  className="text-[9px] font-bold font-mono px-1.5 py-0.5 rounded bg-slate-100 dark:bg-slate-800 text-slate-600 dark:text-slate-300 border border-slate-200 dark:border-slate-700/50"
                                >
                                  {behavior}
                                </span>
                              ))}
                            </div>
                          </div>
                        ))}
                      </div>
                    </div>
                  ))}
                </div>
              )}
            </div>

          </div>

        </div>
      </form>
    </div>
  );
}

export default function CreateRolePage() {
  return (
    <RouteGuard requiredKey="iam:role" requiredAction="create">
      <CreateRoleContent />
    </RouteGuard>
  );
}
