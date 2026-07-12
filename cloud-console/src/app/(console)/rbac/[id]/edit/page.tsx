"use client";

import React, { useEffect, useState, useMemo } from "react";
import { useRouter, useParams } from "next/navigation";
import { Shield, ArrowLeft, Loader2, Save } from "lucide-react";
import { toast } from "sonner";
import { listPermissions, getRoleDetails, updateRole, type PermissionItem } from "@/lib/api/rbac";
import RouteGuard from "@/components/route-guard";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";

// Sub-components import
import RoleDetailsCard from "./components/RoleDetailsCard";
import GrantPermissionsCard from "./components/GrantPermissionsCard";
import AuditStatsCard from "./components/AuditStatsCard";
import SelectedPreviewCard from "./components/SelectedPreviewCard";

function EditRoleContent() {
  const router = useRouter();
  const { id } = useParams() as { id: string };

  // [COMMENT]: State cho các trường thông tin của Role và audit metadata
  const [name, setName] = useState("");
  const [code, setCode] = useState("");
  const [description, setDescription] = useState("");
  const [roleLevel, setRoleLevel] = useState(8);
  const [scope, setScope] = useState("platform");
  const [assignmentsCount, setAssignmentsCount] = useState(0);
  const [createdAt, setCreatedAt] = useState("");
  const [updatedAt, setUpdatedAt] = useState("");

  const [selectedPerms, setSelectedPerms] = useState<string[]>([]);
  const [originalPerms, setOriginalPerms] = useState<string[]>([]);
  const [searchQuery, setSearchQuery] = useState("");

  // [COMMENT]: State quản lý trạng thái collapse/expand của Module và Object
  const [expandedModules, setExpandedModules] = useState<Record<string, boolean>>({});
  const [expandedObjects, setExpandedObjects] = useState<Record<string, boolean>>({});

  // [COMMENT]: State quản lý việc hiển thị panel Import JSON hoặc File
  const [showImport, setShowImport] = useState(false);
  const [importText, setImportText] = useState("");

  // [COMMENT]: Sử dụng useQuery từ TanStack Query để tải permissions catalog
  const {
    data: permissions = [],
    isLoading: loadingPerms,
  } = useQuery<PermissionItem[]>({
    queryKey: ["permissions"],
    queryFn: () => listPermissions(),
  });

  // [COMMENT]: Sử dụng useQuery từ TanStack Query để tải chi tiết Role
  const {
    data: roleData = null,
    isLoading: loadingRole,
  } = useQuery({
    queryKey: ["role", id],
    queryFn: async () => {
      try {
        return await getRoleDetails(id);
      } catch (err: any) {
        toast.error(err.message || "Failed to load role details.");
        router.push("/rbac");
        return null;
      }
    },
    enabled: !!id,
  });

  const loading = loadingPerms || loadingRole;

  // Cập nhật thông tin vai trò khi roleData đã tải xong
  useEffect(() => {
    if (roleData) {
      setName(roleData.name);
      setCode(roleData.code);
      setDescription(roleData.description || "");
      setRoleLevel(roleData.role_level);
      setScope(roleData.scope);
      setAssignmentsCount(roleData.assignments_count || 0);
      setCreatedAt(roleData.created_at || "");
      setUpdatedAt(roleData.updated_at || "");
      const flatIds = (roleData.permissions || []).map((p) => p.id);
      setSelectedPerms(flatIds);
      setOriginalPerms(flatIds);
    }
  }, [roleData]);

  // Mặc định expand tất cả các Module khi hiển thị lần đầu sau khi permissions load xong
  useEffect(() => {
    if (permissions.length > 0) {
      const initialModules: Record<string, boolean> = {};
      permissions.forEach((p) => {
        if (p.module) {
          initialModules[p.module] = true;
        }
      });
      setExpandedModules(initialModules);
    }
  }, [permissions]);

  // [COMMENT]: Xử lý nhóm và lọc permissions phẳng thành cấu trúc cây 3 bậc (Module -> Object -> Behaviors)
  const tree = useMemo(() => {
    const root: Record<string, {
      name: string;
      objects: Record<string, {
        name: string;
        behaviors: { id: string; behavior: string; description: string; fullName: string }[];
      }>;
    }> = {};

    permissions.forEach((perm) => {
      const fullName = `${perm.module}:${perm.object}:${perm.behavior}`;

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

    try {
      const parsed = JSON.parse(importText);
      if (Array.isArray(parsed)) {
        patterns = parsed.map((p) => String(p));
      } else {
        patterns = [String(parsed)];
      }
    } catch {
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

  const queryClient = useQueryClient();

  // [COMMENT]: Mutation gửi API cập nhật Role và invalidate cache liên quan
  const updateRoleMutation = useMutation<void, Error, any>({
    mutationFn: (variables) => updateRole(id, variables),
    onSuccess: () => {
      toast.success("Role updated successfully.");
      queryClient.invalidateQueries({ queryKey: ["role", id] });
      queryClient.invalidateQueries({ queryKey: ["roles"] });
      router.push(`/rbac/${id}`);
    },
    onError: (err) => {
      console.error(err);
      toast.error(err instanceof Error ? err.message : "Failed to update role.");
    },
  });

  const submitting = updateRoleMutation.isPending;

  // [COMMENT]: Xử lý submit lưu thay đổi thông tin Role thông qua API backend
  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!name.trim()) {
      toast.error("Role name is required.");
      return;
    }

    updateRoleMutation.mutate({
      name: name.trim(),
      description: description.trim(),
      permission_ids: selectedPerms,
    });
  };

  if (loading) {
    return (
      <div className="flex flex-col items-center justify-center py-40 text-slate-500">
        <Loader2 className="h-8 w-8 animate-spin text-blue-500 mb-3" />
        <span className="text-xs font-semibold uppercase tracking-wider animate-pulse">Loading editor...</span>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      {/* Header Area */}
      <div className="flex items-center justify-between border-b border-slate-200 dark:border-slate-800 pb-5">
        <div className="flex items-center gap-3">
          <button
            type="button"
            onClick={() => router.push(`/rbac/${id}`)}
            className="flex items-center justify-center h-8 w-8 rounded-lg border border-slate-200 dark:border-slate-800 hover:bg-slate-55 dark:hover:bg-slate-800 text-slate-500 dark:text-slate-400 hover:text-slate-800 dark:hover:text-slate-200 transition-colors cursor-pointer"
          >
            <ArrowLeft className="h-4 w-4" />
          </button>
          <div>
            <h1 className="text-xl font-bold tracking-tight text-slate-900 dark:text-slate-100 flex items-center gap-2">
              <Shield className="h-5 w-5 text-blue-500" />
              <span>Edit System Role</span>
            </h1>
            <p className="mt-1 text-xs font-semibold text-slate-500 dark:text-slate-400">
              Modify security authorities and permissions matrix.
            </p>
          </div>
        </div>

        {/* Action buttons */}
        <div className="flex items-center gap-2">
          <button
            type="button"
            onClick={() => router.push(`/rbac/${id}`)}
            disabled={submitting}
            className="flex items-center justify-center h-8 px-3.5 rounded-lg border border-slate-200 dark:border-slate-800 hover:bg-slate-50 dark:hover:bg-slate-800 text-xs font-semibold text-slate-655 dark:text-slate-350 transition-colors disabled:opacity-50 cursor-pointer"
          >
            Cancel
          </button>
          <button
            onClick={(e) => void handleSubmit(e)}
            disabled={submitting}
            className="flex items-center justify-center h-8 px-4 rounded-lg bg-blue-600 hover:bg-blue-700 text-xs font-semibold text-white transition-colors cursor-pointer shadow-xs disabled:opacity-50 gap-1.5"
          >
            {submitting ? (
              <>
                <Loader2 className="h-3.5 w-3.5 animate-spin" />
                <span>Saving...</span>
              </>
            ) : (
              <>
                <Save className="h-3.5 w-3.5" />
                <span>Save Changes</span>
              </>
            )}
          </button>
        </div>
      </div>

      <form onSubmit={(e) => void handleSubmit(e)}>
        <div className="grid grid-cols-1 lg:grid-cols-12 gap-6 items-start">
          {/* CỘT TRÁI: Role Details & Grant Permissions */}
          <div className="lg:col-span-8 space-y-6">
            <RoleDetailsCard
              name={name}
              setName={setName}
              code={code}
              description={description}
              setDescription={setDescription}
              roleLevel={roleLevel}
              submitting={submitting}
            />

            <GrantPermissionsCard
              permissions={permissions}
              selectedPerms={selectedPerms}
              setSelectedPerms={setSelectedPerms}
              searchQuery={searchQuery}
              setSearchQuery={setSearchQuery}
              expandedModules={expandedModules}
              setExpandedModules={setExpandedModules}
              expandedObjects={expandedObjects}
              setExpandedObjects={setExpandedObjects}
              showImport={showImport}
              setShowImport={setShowImport}
              importText={importText}
              setImportText={setImportText}
              handleFileUpload={handleFileUpload}
              handleImport={handleImport}
              handleTogglePerm={handleTogglePerm}
              tree={tree}
            />

            {/* Bottom Action Buttons */}
            <div className="flex items-center gap-3 border-t border-slate-200 dark:border-slate-800 pt-5 mt-6">
              <button
                onClick={(e) => void handleSubmit(e)}
                type="submit"
                disabled={submitting}
                className="flex items-center justify-center h-9 px-4 rounded-lg bg-blue-600 hover:bg-blue-700 text-xs font-semibold text-white transition-colors cursor-pointer shadow-xs disabled:opacity-50"
              >
                {submitting ? (
                  <>
                    <Loader2 className="h-3.5 w-3.5 animate-spin mr-1.5" />
                    <span>Saving Changes...</span>
                  </>
                ) : (
                  <>
                    <Save className="h-3.5 w-3.5 mr-1.5" />
                    <span>Save Changes</span>
                  </>
                )}
              </button>

              <button
                type="button"
                onClick={() => router.push(`/rbac/${id}`)}
                disabled={submitting}
                className="flex items-center justify-center h-9 px-4 rounded-lg border border-slate-200 dark:border-slate-800 hover:bg-slate-55 dark:hover:bg-slate-800 text-xs font-semibold text-slate-655 dark:text-slate-300 transition-colors disabled:opacity-50 cursor-pointer"
              >
                Cancel
              </button>
            </div>
          </div>

          {/* CỘT PHẢI: Stats & Selected Preview */}
          <div className="lg:col-span-4 lg:sticky lg:top-6 space-y-6">
            <AuditStatsCard
              scope={scope}
              assignmentsCount={assignmentsCount}
              selectedPermsCount={selectedPerms.length}
              createdAt={createdAt}
              updatedAt={updatedAt}
            />

            <SelectedPreviewCard
              permissions={permissions}
              originalPerms={originalPerms}
              selectedPerms={selectedPerms}
            />
          </div>
        </div>
      </form>
    </div>
  );
}

export default function EditRolePage() {
  return (
    <RouteGuard requiredKey="iam:role" requiredAction="write">
      <EditRoleContent />
    </RouteGuard>
  );
}
