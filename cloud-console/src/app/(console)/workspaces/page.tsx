"use client";

import React, { useEffect, useState, useCallback, useMemo } from "react";
import {
  LayoutGrid,
  Plus,
  Loader2,
  FolderOpen,
  Activity,
  Calendar,
  X,
  CheckCircle2,
  Trash2
} from "lucide-react";
import { toast } from "sonner";
import { listWorkspaces, createWorkspace, deleteWorkspace, type WorkspaceItem } from "@/lib/api/workspace";
import { useUserSession } from "@/hooks/useUserSession";
import { useWorkspace } from "@/context/WorkspaceContext";
import { cn } from "@/lib/utils";

export default function MyWorkspacesPage() {
  const { profile, checkPermission } = useUserSession();
  const { activeWorkspaceID, selectWorkspace, addWorkspaceToCatalog, removeWorkspaceFromCatalog } = useWorkspace();

  const [workspaces, setWorkspaces] = useState<WorkspaceItem[]>([]);
  const [loading, setLoading] = useState(true);

  // [COMMENT]: Kiểm quyền tạo Workspace
  const canCreate = useMemo(() => {
    return checkPermission("*:*:hierarchy:workspace", "create");
  }, [checkPermission]);

  // [COMMENT]: Kiểm quyền xóa Workspace
  const canDelete = useMemo(() => {
    return checkPermission("*:*:hierarchy:workspace", "delete");
  }, [checkPermission]);

  // Modal State
  const [isModalOpen, setIsModalOpen] = useState(false);
  const [createLoading, setCreateLoading] = useState(false);
  const [wsName, setWsName] = useState("");
  const [wsCode, setWsCode] = useState("");
  const [wsDescription, setWsDescription] = useState("");

  // [COMMENT]: Lấy danh sách Workspace thuộc personal context
  const loadWorkspaces = useCallback(async (showToast = false) => {
    const userID = profile?.user_id;
    if (!userID) return;

    setLoading(true);
    try {
      const list = await listWorkspaces();
      setWorkspaces(list);
      if (showToast) {
        toast.success("Workspace list synchronized.");
      }
    } catch (err) {
      console.error(err);
      toast.error(err instanceof Error ? err.message : "Failed to load workspaces.");
    } finally {
      setLoading(false);
    }
  }, [profile]);

  useEffect(() => {
    if (profile?.user_id) {
      void loadWorkspaces();
    }
  }, [profile, loadWorkspaces]);

  // [COMMENT]: Auto-generate workspace code slug
  const handleNameChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const val = e.target.value;
    setWsName(val);
    const slug = val
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, "-")
      .replace(/(^-|-$)/g, "");
    setWsCode(slug);
  };

  // [COMMENT]: Xử lý submit tạo mới Workspace
  const handleCreateWorkspace = async (e: React.FormEvent) => {
    e.preventDefault();
    const userID = profile?.user_id;
    if (!userID) return;

    if (!wsName.trim() || !wsCode.trim()) {
      toast.error("Please enter a valid workspace name and code.");
      return;
    }

    setCreateLoading(true);
    try {
      const newWs = await createWorkspace({
        name: wsName.trim(),
        code: wsCode.trim(),
        description: wsDescription.trim()
      });

      toast.success("Workspace created successfully.");

      // Clear form
      setWsName("");
      setWsCode("");
      setWsDescription("");
      setIsModalOpen(false);

      // [COMMENT]: Đồng bộ dữ liệu Zero-Request & Zero-Reload
      // Merge workspace mới trực tiếp vào local state và global catalog dropdown mà không gọi lại bất kỳ API GET nào
      if (newWs) {
        setWorkspaces((prev) => [...prev, newWs]);
        addWorkspaceToCatalog({
          id: newWs.id,
          code: newWs.code,
          name: newWs.name,
        });
      }
    } catch (err) {
      console.error(err);
      toast.error(err instanceof Error ? err.message : "Failed to create workspace.");
    } finally {
      setCreateLoading(false);
    }
  };

  // [COMMENT]: Xử lý xóa Workspace
  const handleDeleteWorkspace = async (ws: WorkspaceItem) => {
    if (!window.confirm(`Are you sure you want to delete workspace "${ws.name}"?`)) {
      return;
    }

    try {
      await deleteWorkspace(ws.id);
      toast.success("Workspace deleted successfully.");

      // Dọn dẹp cục bộ trên client
      setWorkspaces((prev) => prev.filter((item) => item.id !== ws.id));
      removeWorkspaceFromCatalog(ws.id);
    } catch (err) {
      console.error(err);
      const errMsg = err instanceof Error ? err.message : "Failed to delete workspace.";
      if (errMsg.includes("cannot delete the last remaining workspace")) {
        toast.error("Deletion rejected: You must maintain at least one workspace.");
      } else if (errMsg.includes("active resources exist")) {
        toast.error("Deletion rejected: Please delete all resources in this workspace first.");
      } else {
        toast.error(errMsg);
      }
    }
  };

  return (
    <div className="space-y-6">
      {/* ========================================== */}
      {/* 1. Header Area */}
      {/* ========================================== */}
      <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-4 border-b border-slate-200 dark:border-slate-800 pb-5">
        <div>
          <h1 className="text-xl font-bold tracking-tight text-slate-900 dark:text-slate-100 flex items-center gap-2">
            <LayoutGrid className="h-5 w-5 text-blue-500" />
            <span>My Workspaces</span>
          </h1>
          <p className="mt-1.5 text-xs font-semibold text-slate-500 dark:text-slate-400">
            Provision, manage isolated workspaces and check resources deployment in the current active Zone.
          </p>
        </div>

        <div className="flex items-center gap-2">
          {canCreate && (
            <button
              onClick={() => setIsModalOpen(true)}
              className="flex items-center justify-center h-8 px-3.5 rounded-lg bg-blue-600 hover:bg-blue-700 text-xs font-semibold text-white transition-colors cursor-pointer shadow-xs"
            >
              <Plus className="h-3.5 w-3.5 mr-1.5" />
              <span>Create Workspace</span>
            </button>
          )}
        </div>
      </div>

      {/* ========================================== */}
      {/* 2. Workspace Cards Grid */}
      {/* ========================================== */}
      {loading && workspaces.length === 0 ? (
        <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
          {Array.from({ length: 3 }).map((_, idx) => (
            <div
              key={idx}
              className="p-5 border border-slate-200 dark:border-slate-800/80 bg-white dark:bg-slate-900/60 rounded-xl space-y-4 animate-pulse"
            >
              <div className="flex justify-between items-center">
                <div className="h-4.5 w-24 bg-slate-100 dark:bg-slate-800 rounded-md" />
                <div className="h-4.5 w-16 bg-slate-100 dark:bg-slate-800 rounded-md" />
              </div>
              <div className="space-y-2">
                <div className="h-6 w-40 bg-slate-100 dark:bg-slate-800 rounded-md" />
                <div className="h-3 w-48 bg-slate-100 dark:bg-slate-800 rounded-md" />
              </div>
              <div className="h-8 bg-slate-50 dark:bg-slate-900/40 rounded-md" />
            </div>
          ))}
        </div>
      ) : workspaces.length === 0 ? (
        <div className="flex flex-col items-center justify-center py-20 px-6 bg-white dark:bg-slate-900/60 border border-slate-200 dark:border-slate-800 rounded-xl select-none text-center">
          <FolderOpen className="h-10 w-10 text-slate-350 dark:text-slate-700 mb-3" />
          <p className="text-sm font-bold text-slate-700 dark:text-slate-300">No workspaces found</p>
          <p className="text-xs text-slate-400 mt-1 max-w-sm leading-normal">
            You don't have any workspaces created in this zone. Click the button above to launch your first workspace environment.
          </p>
        </div>
      ) : (
        <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
          {workspaces.map((ws) => {
            const isActive = ws.id === activeWorkspaceID;
            return (
              <div
                key={ws.id}
                className={cn(
                  "relative p-5 border bg-white dark:bg-slate-900/60 rounded-xl transition-all duration-200 group flex flex-col justify-between overflow-hidden",
                  isActive
                    ? "border-blue-500 shadow-md ring-1 ring-blue-500/10"
                    : "border-slate-200 dark:border-slate-800/80 hover:border-slate-300 dark:hover:border-slate-700 hover:-translate-y-0.5 shadow-xs"
                )}
              >
                {/* Scope Indicator */}
                <div className="flex justify-between items-center mb-3">
                  <span className="flex items-center gap-1.5 text-[10px] font-bold uppercase tracking-wider text-slate-400">
                    <Activity className="h-3.5 w-3.5 text-slate-400 shrink-0" />
                    <span>Personal</span>
                  </span>

                  {canDelete && (
                    <button
                      onClick={(e) => {
                        e.stopPropagation();
                        void handleDeleteWorkspace(ws);
                      }}
                      className="p-1.5 rounded-lg text-slate-450 hover:text-red-500 hover:bg-red-50 dark:hover:bg-red-950/20 transition-colors cursor-pointer"
                      title="Delete Workspace"
                    >
                      <Trash2 className="h-3.5 w-3.5" />
                    </button>
                  )}
                </div>

                {/* Workspace Name & Slug */}
                <div className="space-y-1">
                  <h2 className="text-base font-bold text-slate-800 dark:text-slate-100 group-hover:text-blue-500 transition-colors">
                    {ws.name}
                  </h2>
                  <p className="text-xs font-mono text-slate-400 dark:text-slate-500">
                    slug: {ws.code}
                  </p>
                  {ws.description && (
                    <p className="text-[11px] text-slate-505 dark:text-slate-400 line-clamp-2 mt-1.5 leading-normal font-medium">
                      {ws.description}
                    </p>
                  )}
                </div>

                {/* Details list */}
                <div className="mt-4 pt-3.5 border-t border-slate-100 dark:border-slate-800/80 space-y-2 text-[11px] text-slate-500 dark:text-slate-400">
                  <div className="flex justify-between">
                    <span className="flex items-center gap-1">
                      <Calendar className="h-3 w-3 text-slate-400" />
                      <span>Created At</span>
                    </span>
                    <span className="font-medium text-slate-600 dark:text-slate-400">
                      {new Date(ws.created_at).toLocaleDateString()}
                    </span>
                  </div>
                </div>

                {/* Quick Activation Bar */}
                <div className="mt-5 pt-1">
                  {isActive ? (
                    <div className="flex items-center justify-center w-full h-8 rounded-lg bg-blue-500/10 text-blue-600 dark:text-blue-400 text-xs font-bold gap-1 border border-blue-500/15">
                      <CheckCircle2 className="h-3.5 w-3.5" />
                      <span>Currently Selected</span>
                    </div>
                  ) : (
                    <button
                      onClick={() => {
                        selectWorkspace(ws.id);
                        toast.success(`Active workspace switched to: ${ws.name}`);
                      }}
                      className="w-full flex items-center justify-center h-8 rounded-lg border border-slate-200 dark:border-slate-800 hover:border-slate-300 dark:hover:border-slate-700 hover:bg-slate-50 dark:hover:bg-slate-900/60 text-xs font-semibold text-slate-600 dark:text-slate-300 hover:text-slate-950 dark:hover:text-white transition-colors cursor-pointer"
                    >
                      Select Workspace
                    </button>
                  )}
                </div>
              </div>
            );
          })}
        </div>
      )}

      {/* ========================================== */}
      {/* 3. Create Workspace Modal (Overlay + Container) */}
      {/* ========================================== */}
      {isModalOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center">
          {/* Backdrop Overlay */}
          <div
            className="absolute inset-0 bg-slate-900/60 dark:bg-slate-950/80 backdrop-blur-xs transition-opacity"
            onClick={() => {
              if (!createLoading) setIsModalOpen(false);
            }}
          />

          {/* Dialog Container */}
          <div className="relative w-full max-w-md p-6 bg-white dark:bg-slate-950 border border-slate-200 dark:border-slate-800/80 rounded-xl shadow-2xl z-50 animate-in fade-in zoom-in-95 duration-150">
            {/* Close Button */}
            <button
              onClick={() => setIsModalOpen(false)}
              disabled={createLoading}
              className="absolute top-4 right-4 p-1 rounded-lg text-slate-400 hover:text-slate-600 dark:hover:text-slate-200 transition-colors disabled:opacity-50 cursor-pointer"
            >
              <X className="h-4 w-4" />
            </button>

            {/* Modal Title */}
            <div className="mb-5">
              <h3 className="text-base font-bold text-slate-900 dark:text-slate-100 flex items-center gap-2">
                <LayoutGrid className="h-4.5 w-4.5 text-blue-500" />
                <span>Create Workspace</span>
              </h3>
              <p className="text-xs text-slate-500 mt-1">
                Initialize a personal isolated workspace.
              </p>
            </div>

            {/* Form */}
            <form onSubmit={handleCreateWorkspace} className="space-y-4">
              <div className="space-y-1.5">
                <label className="text-[11px] font-bold uppercase tracking-wider text-slate-400">
                  Workspace Name
                </label>
                <input
                  type="text"
                  required
                  placeholder="e.g. Development Env"
                  value={wsName}
                  onChange={handleNameChange}
                  disabled={createLoading}
                  className="w-full px-3 py-1.5 text-xs bg-slate-50 dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-lg focus:outline-hidden focus:border-blue-500 text-slate-800 dark:text-slate-150 transition-colors disabled:opacity-50"
                />
              </div>

              <div className="space-y-1.5">
                <label className="text-[11px] font-bold uppercase tracking-wider text-slate-400">
                  Workspace Slug/Code
                </label>
                <input
                  type="text"
                  required
                  placeholder="e.g. dev-env"
                  value={wsCode}
                  onChange={(e) => setWsCode(e.target.value.toLowerCase().replace(/[^a-z0-9-]+/g, ""))}
                  disabled={createLoading}
                  className="w-full px-3 py-1.5 text-xs bg-slate-50 dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-lg focus:outline-hidden focus:border-blue-500 text-slate-850 dark:text-slate-200 font-mono transition-colors disabled:opacity-50"
                />
                <span className="text-[10px] text-slate-400 dark:text-slate-500">
                  Letters, numbers, and dashes only.
                </span>
              </div>

              <div className="space-y-1.5">
                <label className="text-[11px] font-bold uppercase tracking-wider text-slate-400">
                  Description
                </label>
                <textarea
                  placeholder="e.g. Workspace for core product development"
                  value={wsDescription}
                  onChange={(e) => setWsDescription(e.target.value)}
                  disabled={createLoading}
                  rows={3}
                  className="w-full px-3 py-1.5 text-xs bg-slate-50 dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-lg focus:outline-hidden focus:border-blue-500 text-slate-800 dark:text-slate-150 transition-colors disabled:opacity-50 resize-none"
                />
              </div>


              {/* Actions Footer */}
              <div className="flex justify-end items-center gap-2 pt-3 ">
                <button
                  type="button"
                  onClick={() => setIsModalOpen(false)}
                  disabled={createLoading}
                  className="flex items-center justify-center h-8 px-4 rounded-lg border border-slate-200 dark:border-slate-800 hover:bg-slate-50 dark:hover:bg-slate-900 text-xs font-semibold text-slate-500 dark:text-slate-400 transition-colors disabled:opacity-50 cursor-pointer"
                >
                  Cancel
                </button>
                <button
                  type="submit"
                  disabled={createLoading}
                  className="flex items-center justify-center h-8 px-4 rounded-lg bg-blue-600 hover:bg-blue-700 text-xs font-semibold text-white transition-colors disabled:opacity-50 cursor-pointer"
                >
                  {createLoading ? (
                    <>
                      <Loader2 className="h-3.5 w-3.5 animate-spin mr-1.5" />
                      <span>Creating...</span>
                    </>
                  ) : (
                    <span>Create</span>
                  )}
                </button>
              </div>
            </form>
          </div>
        </div>
      )}
    </div>
  );
}
