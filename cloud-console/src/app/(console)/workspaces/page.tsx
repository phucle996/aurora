"use client";

import React, { useEffect, useState, useCallback, useMemo } from "react";
import {
  LayoutGrid,
  Plus,
  Loader2,
  FolderOpen,
  Search,
  X,
  Trash2,
  Copy,
  Check,
  ArrowUpDown,
  RefreshCw,
} from "lucide-react";
import { toast } from "sonner";
import {
  listWorkspaces,
  createWorkspace,
  deleteWorkspace,
  type WorkspaceItem,
} from "@/lib/api/workspace";
import { useUserSession } from "@/hooks/useUserSession";
import { useWorkspace } from "@/context/WorkspaceContext";
import { cn } from "@/lib/utils";

// [COMMENT]: Cột sort được hỗ trợ
type SortKey = "name" | "code" | "created_at";
type SortDir = "asc" | "desc";

// [COMMENT]: Component copy-to-clipboard nhỏ gọn cho code badge
function CopyBadge({ value }: { value: string }) {
  const [copied, setCopied] = useState(false);
  const handleCopy = (e: React.MouseEvent) => {
    e.stopPropagation();
    navigator.clipboard.writeText(value).catch(() => { });
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  };
  return (
    <button
      onClick={handleCopy}
      className="inline-flex items-center gap-1 px-2 py-0.5 rounded-md bg-muted/60 border border-border text-[11px] font-mono text-muted-foreground hover:text-foreground hover:bg-muted transition-colors cursor-pointer group"
      title="Copy code"
    >
      <span>{value}</span>
      {copied ? (
        <Check className="h-2.5 w-2.5 text-emerald-500 shrink-0" />
      ) : (
        <Copy className="h-2.5 w-2.5 shrink-0 opacity-40 group-hover:opacity-100 transition-opacity" />
      )}
    </button>
  );
}

export default function MyWorkspacesPage() {
  const { profile, checkPermission } = useUserSession();
  const {
    activeWorkspaceID,
    addWorkspaceToCatalog,
    removeWorkspaceFromCatalog,
  } = useWorkspace();

  const [workspaces, setWorkspaces] = useState<WorkspaceItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);

  // [COMMENT]: Kiểm quyền tạo / xóa Workspace
  const canCreate = useMemo(() => checkPermission("hierarchy:workspace", "create"), [checkPermission]);
  const canDelete = useMemo(() => checkPermission("hierarchy:workspace", "delete"), [checkPermission]);

  // [COMMENT]: Toolbar state — search, sort
  const [search, setSearch] = useState("");
  const [sortKey, setSortKey] = useState<SortKey>("created_at");
  const [sortDir, setSortDir] = useState<SortDir>("desc");

  // [COMMENT]: Modal tạo workspace
  const [isModalOpen, setIsModalOpen] = useState(false);
  const [createLoading, setCreateLoading] = useState(false);
  const [wsName, setWsName] = useState("");
  const [wsCode, setWsCode] = useState("");
  const [wsDescription, setWsDescription] = useState("");

  // [COMMENT]: Modal xóa workspace
  const [isDeleteModalOpen, setIsDeleteModalOpen] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<WorkspaceItem | null>(null);
  const [deleteConfirmCode, setDeleteConfirmCode] = useState("");
  const [deleteLoading, setDeleteLoading] = useState(false);

  // [COMMENT]: Lấy danh sách Workspace thuộc personal context
  const loadWorkspaces = useCallback(
    async (isRefresh = false) => {
      if (!profile?.user_id) return;
      if (isRefresh) setRefreshing(true);
      else setLoading(true);
      try {
        const list = await listWorkspaces();
        setWorkspaces(list);
        if (isRefresh) toast.success("Workspace list synchronized.");
      } catch (err) {
        toast.error(err instanceof Error ? err.message : "Failed to load workspaces.");
      } finally {
        setLoading(false);
        setRefreshing(false);
      }
    },
    [profile]
  );

  useEffect(() => {
    if (profile?.user_id) void loadWorkspaces();
  }, [profile, loadWorkspaces]);

  // [COMMENT]: Auto-generate workspace code slug từ tên
  const handleNameChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const val = e.target.value;
    setWsName(val);
    setWsCode(
      val
        .toLowerCase()
        .replace(/[^a-z0-9]+/g, "-")
        .replace(/(^-|-$)/g, "")
    );
  };

  // [COMMENT]: Xử lý submit tạo mới Workspace
  const handleCreateWorkspace = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!profile?.user_id) return;
    if (!wsName.trim() || !wsCode.trim()) {
      toast.error("Please enter a valid workspace name and code.");
      return;
    }
    setCreateLoading(true);
    try {
      const newWs = await createWorkspace({
        name: wsName.trim(),
        code: wsCode.trim(),
        description: wsDescription.trim(),
      });
      toast.success("Workspace created successfully.");
      setWsName("");
      setWsCode("");
      setWsDescription("");
      setIsModalOpen(false);
      // [COMMENT]: Zero-Request merge — không reload API, cập nhật local state + global catalog
      if (newWs) {
        setWorkspaces((prev) => [...prev, newWs]);
        addWorkspaceToCatalog({ id: newWs.id, code: newWs.code, name: newWs.name });
      }
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "Failed to create workspace.");
    } finally {
      setCreateLoading(false);
    }
  };

  // [COMMENT]: Mở modal xác nhận xóa
  const handleOpenDeleteModal = (ws: WorkspaceItem, e: React.MouseEvent) => {
    e.stopPropagation();
    setDeleteTarget(ws);
    setDeleteConfirmCode("");
    setIsDeleteModalOpen(true);
  };

  // [COMMENT]: Xử lý xóa Workspace sau khi người dùng nhập đúng code/slug để xác nhận
  const handleDeleteWorkspace = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!deleteTarget) return;
    if (deleteConfirmCode !== deleteTarget.code) {
      toast.error("Workspace code does not match.");
      return;
    }

    setDeleteLoading(true);
    try {
      await deleteWorkspace(deleteTarget.id);
      toast.success(`Workspace "${deleteTarget.name}" deleted.`);
      setWorkspaces((prev) => prev.filter((item) => item.id !== deleteTarget.id));
      removeWorkspaceFromCatalog(deleteTarget.id);
      setIsDeleteModalOpen(false);
      setDeleteTarget(null);
    } catch (err) {
      const msg = err instanceof Error ? err.message : "Failed to delete workspace.";
      if (msg.includes("cannot delete the last remaining workspace")) {
        toast.error("Deletion rejected: At least one workspace must exist.");
      } else if (msg.includes("active resources exist")) {
        toast.error("Deletion rejected: Please remove all resources first.");
      } else {
        toast.error(msg);
      }
    } finally {
      setDeleteLoading(false);
    }
  };

  // [COMMENT]: Toggle sort — nếu click cùng cột thì đảo chiều, khác cột thì set mới
  const handleSort = (key: SortKey) => {
    if (sortKey === key) setSortDir((d) => (d === "asc" ? "desc" : "asc"));
    else {
      setSortKey(key);
      setSortDir("asc");
    }
  };

  // [COMMENT]: Filtered + sorted workspaces
  const displayedWorkspaces = useMemo(() => {
    let result = workspaces.filter((ws) => {
      const q = search.toLowerCase();
      return (
        ws.name.toLowerCase().includes(q) ||
        ws.code.toLowerCase().includes(q) ||
        (ws.description ?? "").toLowerCase().includes(q)
      );
    });
    result = [...result].sort((a, b) => {
      const va = (a[sortKey] ?? "") as string;
      const vb = (b[sortKey] ?? "") as string;
      if (va < vb) return sortDir === "asc" ? -1 : 1;
      if (va > vb) return sortDir === "asc" ? 1 : -1;
      return 0;
    });
    return result;
  }, [workspaces, search, sortKey, sortDir]);

  // [COMMENT]: Header sort icon helper component
  const SortIcon = ({ col }: { col: SortKey }) => (
    <ArrowUpDown
      className={cn(
        "h-3 w-3 ml-1 shrink-0 transition-colors",
        sortKey === col ? "text-foreground" : "text-muted-foreground/40"
      )}
    />
  );

  // [COMMENT]: Tính màu icon workspace dựa trên hash của id
  const getIconColor = (id: string) => {
    const hash = id.split("").reduce((acc, c) => acc + c.charCodeAt(0), 0);
    const colors = [
      "bg-blue-500/10 text-blue-500 border-blue-500/20",
      "bg-violet-500/10 text-violet-500 border-violet-500/20",
      "bg-emerald-500/10 text-emerald-500 border-emerald-500/20",
      "bg-amber-500/10 text-amber-500 border-amber-500/20",
      "bg-pink-500/10 text-pink-500 border-pink-500/20",
      "bg-indigo-500/10 text-indigo-500 border-indigo-500/20",
      "bg-cyan-500/10 text-cyan-500 border-cyan-500/20",
    ];
    return colors[hash % colors.length];
  };

  return (
    <div className="space-y-5">
      {/* ======================================================== */}
      {/* 1. Page Header                                            */}
      {/* ======================================================== */}
      <div className="flex flex-col sm:flex-row sm:items-start sm:justify-between gap-4 border-b border-border pb-5">
        <div>
          <h1 className="text-xl font-bold tracking-tight text-foreground flex items-center gap-2">
            <LayoutGrid className="h-5 w-5 text-blue-500" />
            <span>My Workspaces</span>
          </h1>
          <p className="mt-1 text-xs font-medium text-muted-foreground">
            Provision and manage isolated workspaces. Each workspace contains its own resources and settings.
          </p>
        </div>
        <div className="flex items-center gap-2 shrink-0">
          {/* Refresh */}
          <button
            onClick={() => void loadWorkspaces(true)}
            disabled={refreshing}
            className="flex items-center justify-center h-8 w-8 rounded-lg border border-border hover:bg-muted/50 text-muted-foreground hover:text-foreground transition-colors cursor-pointer disabled:opacity-50"
            title="Refresh"
          >
            <RefreshCw className={cn("h-3.5 w-3.5", refreshing && "animate-spin")} />
          </button>
          {/* Create */}
          {canCreate && (
            <button
              onClick={() => setIsModalOpen(true)}
              className="flex items-center justify-center h-8 px-3.5 rounded-lg bg-blue-600 hover:bg-blue-700 text-xs font-semibold text-white transition-colors cursor-pointer shadow-sm gap-1.5"
            >
              <Plus className="h-3.5 w-3.5" />
              <span>Create Workspace</span>
            </button>
          )}
        </div>
      </div>

      {/* ======================================================== */}
      {/* 2. Toolbar — Search                                       */}
      {/* ======================================================== */}
      <div className="flex items-center gap-3">
        <div className="relative w-64">
          <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-muted-foreground/60 pointer-events-none" />
          <input
            type="text"
            placeholder="Search workspace..."
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="w-full h-8 pl-8 pr-3 text-xs bg-background border border-border rounded-lg focus:outline-none focus:border-blue-500 text-foreground placeholder:text-muted-foreground/50 transition-colors"
          />
          {search && (
            <button
              onClick={() => setSearch("")}
              className="absolute right-2.5 top-1/2 -translate-y-1/2 text-muted-foreground/60 hover:text-muted-foreground cursor-pointer"
            >
              <X className="h-3 w-3" />
            </button>
          )}
        </div>
        <span className="text-xs text-muted-foreground select-none">
          {displayedWorkspaces.length} workspace{displayedWorkspaces.length !== 1 ? "s" : ""}
        </span>
      </div>

      {/* ======================================================== */}
      {/* 3. Resource Table                                         */}
      {/* ======================================================== */}
      <div className="rounded-xl border border-border overflow-hidden">
        {loading && workspaces.length === 0 ? (
          // Skeleton rows
          <div>
            <div className="flex items-center gap-6 px-6 py-3 bg-muted/20 border-b border-border">
              {["w-32", "w-20", "w-24"].map((w, i) => (
                <div key={i} className={cn("h-3 rounded bg-muted animate-pulse", w)} />
              ))}
            </div>
            {Array.from({ length: 4 }).map((_, i) => (
              <div
                key={i}
                className="flex items-center gap-6 px-6 py-4 border-b border-border last:border-0 animate-pulse"
              >
                <div className="flex items-center gap-3 flex-1">
                  <div className="h-8 w-8 rounded-lg bg-muted shrink-0" />
                  <div className="space-y-1.5">
                    <div className="h-3 w-36 bg-muted rounded" />
                    <div className="h-2.5 w-24 bg-muted/60 rounded" />
                  </div>
                </div>
                <div className="h-5 w-24 bg-muted rounded-md" />
                <div className="h-3 w-20 bg-muted rounded" />
              </div>
            ))}
          </div>
        ) : displayedWorkspaces.length === 0 ? (
          <div className="flex flex-col items-center justify-center py-20 text-center select-none">
            <FolderOpen className="h-9 w-9 text-muted-foreground/40 mb-3" />
            <p className="text-sm font-semibold text-foreground">
              {search ? "No workspaces match your search" : "No workspaces found"}
            </p>
            <p className="text-xs text-muted-foreground mt-1 max-w-xs leading-relaxed">
              {search
                ? "Try different keywords or clear the search."
                : 'Click "Create Workspace" to provision your first isolated environment.'}
            </p>
          </div>
        ) : (
          <table className="w-full text-left border-collapse">
            <thead>
              <tr className="border-b border-border bg-muted/20 text-[10px] font-extrabold uppercase tracking-wider text-muted-foreground select-none">
                <th
                  className="px-6 py-3 cursor-pointer hover:text-foreground transition-colors"
                  onClick={() => handleSort("name")}
                >
                  <span className="inline-flex items-center">
                    Name <SortIcon col="name" />
                  </span>
                </th>
                <th
                  className="px-6 py-3 cursor-pointer hover:text-foreground transition-colors"
                  onClick={() => handleSort("code")}
                >
                  <span className="inline-flex items-center">
                    Code <SortIcon col="code" />
                  </span>
                </th>
                <th
                  className="px-6 py-3 cursor-pointer hover:text-foreground transition-colors"
                  onClick={() => handleSort("created_at")}
                >
                  <span className="inline-flex items-center">
                    Created <SortIcon col="created_at" />
                  </span>
                </th>
                {canDelete && (
                  <th className="px-6 py-3 text-right">Actions</th>
                )}
              </tr>
            </thead>

            <tbody className="divide-y divide-border text-[13px]">
              {displayedWorkspaces.map((ws) => {
                const isActive = ws.id === activeWorkspaceID;
                const iconColor = getIconColor(ws.id);

                return (
                  <tr
                    key={ws.id}
                    className={cn(
                      "transition-colors select-none group",
                      isActive && "bg-blue-500/5"
                    )}
                  >
                    {/* Name + description */}
                    <td className="px-6 py-4">
                      <div className="flex items-center gap-3">
                        <div
                          className={cn(
                            "h-8 w-8 flex items-center justify-center rounded-lg border shrink-0",
                            iconColor
                          )}
                        >
                          <LayoutGrid className="h-3.5 w-3.5" />
                        </div>
                        <div className="flex flex-col min-w-0">
                          <div className="flex items-center gap-2">
                            <span className="font-semibold text-foreground truncate">
                              {ws.name}
                            </span>
                            {/* [COMMENT]: Current badge chỉ hiện khi là active workspace */}
                            {isActive && (
                              <span className="inline-flex items-center px-1.5 py-0.5 rounded-md text-[9px] font-bold uppercase tracking-wider bg-blue-500/10 text-blue-500 border border-blue-500/20 shrink-0">
                                Current
                              </span>
                            )}
                          </div>
                          {ws.description ? (
                            <span className="text-[11px] text-muted-foreground truncate mt-0.5">
                              {ws.description}
                            </span>
                          ) : (
                            <span className="text-[11px] text-muted-foreground/40 mt-0.5 italic">
                              No description
                            </span>
                          )}
                        </div>
                      </div>
                    </td>

                    {/* Code badge with copy */}
                    <td className="px-6 py-4">
                      <CopyBadge value={ws.code} />
                    </td>

                    {/* Created At */}
                    <td className="px-6 py-4">
                      <div className="flex flex-col">
                        <span className="text-xs font-medium text-foreground">
                          {new Date(ws.created_at).toLocaleDateString("en-GB", {
                            day: "2-digit",
                            month: "short",
                            year: "numeric",
                          })}
                        </span>
                        <span className="text-[10px] text-muted-foreground font-mono mt-0.5">
                          {new Date(ws.created_at).toLocaleTimeString("en-GB", {
                            hour: "2-digit",
                            minute: "2-digit",
                            timeZoneName: "short",
                          })}
                        </span>
                      </div>
                    </td>

                    {/* Delete action */}
                    {canDelete && (
                      <td className="px-6 py-4 text-right">
                        <button
                          onClick={(e) => handleOpenDeleteModal(ws, e)}
                          className="inline-flex items-center gap-1.5 px-2.5 h-7 rounded-lg border border-transparent text-[11px] font-semibold text-muted-foreground hover:text-red-500 hover:border-red-500/30 hover:bg-red-500/5 transition-all cursor-pointer opacity-0 group-hover:opacity-100"
                          title="Delete workspace"
                        >
                          <Trash2 className="h-3.5 w-3.5" />
                          <span>Delete</span>
                        </button>
                      </td>
                    )}
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}
      </div>

      {/* ======================================================== */}
      {/* 4. Create Workspace Modal                                 */}
      {/* ======================================================== */}
      {isModalOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center">
          {/* Backdrop */}
          <div
            className="absolute inset-0 bg-black/50 backdrop-blur-sm"
            onClick={() => {
              if (!createLoading) setIsModalOpen(false);
            }}
          />

          {/* Dialog */}
          <div className="relative w-full max-w-md p-6 bg-background border border-border rounded-xl shadow-2xl z-10 animate-in fade-in zoom-in-95 duration-150">
            <button
              onClick={() => setIsModalOpen(false)}
              disabled={createLoading}
              className="absolute top-4 right-4 p-1 rounded-lg text-muted-foreground hover:text-foreground transition-colors disabled:opacity-50 cursor-pointer"
            >
              <X className="h-4 w-4" />
            </button>

            <div className="mb-5">
              <h3 className="text-base font-bold text-foreground flex items-center gap-2">
                <LayoutGrid className="h-4 w-4 text-blue-500" />
                <span>Create Workspace</span>
              </h3>
              <p className="text-xs text-muted-foreground mt-1">
                Initialize a personal isolated workspace environment.
              </p>
            </div>

            <form onSubmit={(e) => void handleCreateWorkspace(e)} className="space-y-4">
              <div className="space-y-1.5">
                <label className="text-[11px] font-bold uppercase tracking-wider text-muted-foreground">
                  Workspace Name
                </label>
                <input
                  type="text"
                  required
                  placeholder="e.g. Development Env"
                  value={wsName}
                  onChange={handleNameChange}
                  disabled={createLoading}
                  className="w-full px-3 py-2 text-xs bg-muted/30 border border-border rounded-lg focus:outline-none focus:border-blue-500 text-foreground placeholder:text-muted-foreground/50 transition-colors disabled:opacity-50"
                />
              </div>

              <div className="space-y-1.5">
                <label className="text-[11px] font-bold uppercase tracking-wider text-muted-foreground">
                  Workspace Code
                </label>
                <input
                  type="text"
                  required
                  placeholder="e.g. dev-env"
                  value={wsCode}
                  onChange={(e) =>
                    setWsCode(e.target.value.toLowerCase().replace(/[^a-z0-9-]+/g, ""))
                  }
                  disabled={createLoading}
                  className="w-full px-3 py-2 text-xs bg-muted/30 border border-border rounded-lg focus:outline-none focus:border-blue-500 text-foreground font-mono placeholder:text-muted-foreground/50 transition-colors disabled:opacity-50"
                />
                <span className="text-[10px] text-muted-foreground">
                  Letters, numbers, and hyphens only.
                </span>
              </div>

              <div className="space-y-1.5">
                <label className="text-[11px] font-bold uppercase tracking-wider text-muted-foreground">
                  Description{" "}
                  <span className="font-normal normal-case">(optional)</span>
                </label>
                <textarea
                  placeholder="e.g. Workspace for core product development"
                  value={wsDescription}
                  onChange={(e) => setWsDescription(e.target.value)}
                  disabled={createLoading}
                  rows={2}
                  className="w-full px-3 py-2 text-xs bg-muted/30 border border-border rounded-lg focus:outline-none focus:border-blue-500 text-foreground placeholder:text-muted-foreground/50 transition-colors disabled:opacity-50 resize-none"
                />
              </div>

              <div className="flex justify-end items-center gap-2 pt-2">
                <button
                  type="button"
                  onClick={() => setIsModalOpen(false)}
                  disabled={createLoading}
                  className="flex items-center justify-center h-8 px-4 rounded-lg border border-border hover:bg-muted/50 text-xs font-semibold text-muted-foreground transition-colors disabled:opacity-50 cursor-pointer"
                >
                  Cancel
                </button>
                <button
                  type="submit"
                  disabled={createLoading}
                  className="flex items-center justify-center h-8 px-4 rounded-lg bg-blue-600 hover:bg-blue-700 text-xs font-semibold text-white transition-colors disabled:opacity-50 cursor-pointer gap-1.5"
                >
                  {createLoading ? (
                    <>
                      <Loader2 className="h-3.5 w-3.5 animate-spin" />
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

      {/* ======================================================== */}
      {/* 5. Delete Workspace Confirmation Modal                   */}
      {/* ======================================================== */}
      {isDeleteModalOpen && deleteTarget && (
        <div className="fixed inset-0 z-50 flex items-center justify-center">
          {/* Backdrop */}
          <div
            className="absolute inset-0 bg-black/50 backdrop-blur-sm"
            onClick={() => {
              if (!deleteLoading) {
                setIsDeleteModalOpen(false);
                setDeleteTarget(null);
              }
            }}
          />

          {/* Dialog */}
          <div className="relative w-full max-w-md p-6 bg-background border border-border rounded-xl shadow-2xl z-10 animate-in fade-in zoom-in-95 duration-150">
            <button
              onClick={() => {
                setIsDeleteModalOpen(false);
                setDeleteTarget(null);
              }}
              disabled={deleteLoading}
              className="absolute top-4 right-4 p-1 rounded-lg text-muted-foreground hover:text-foreground transition-colors disabled:opacity-50 cursor-pointer"
            >
              <X className="h-4 w-4" />
            </button>

            <div className="mb-4">
              <h3 className="text-base font-bold text-red-500 flex items-center gap-2">
                <Trash2 className="h-4 w-4" />
                <span>Delete Workspace</span>
              </h3>
              <p className="text-xs text-muted-foreground mt-1">
                This action is permanent and cannot be undone. All resources inside this workspace will be deleted.
              </p>
            </div>

            <div className="p-3 bg-red-500/5 border border-red-500/10 rounded-lg text-xs text-red-400 mb-4 leading-normal">
              Warning: You are about to delete workspace <strong>{deleteTarget.name}</strong>.
            </div>

            <form onSubmit={(e) => void handleDeleteWorkspace(e)} className="space-y-4">
              <div className="space-y-1.5">
                <label className="text-[11px] font-bold uppercase tracking-wider text-muted-foreground">
                  Confirm Workspace Code
                </label>
                <p className="text-[11px] text-muted-foreground">
                  Please type <span className="font-mono font-bold text-foreground bg-muted px-1.5 py-0.5 rounded border border-border">{deleteTarget.code}</span> to confirm.
                </p>
                <input
                  type="text"
                  required
                  placeholder={deleteTarget.code}
                  value={deleteConfirmCode}
                  onChange={(e) => setDeleteConfirmCode(e.target.value)}
                  disabled={deleteLoading}
                  className="w-full px-3 py-2 text-xs bg-muted/30 border border-border rounded-lg focus:outline-none focus:border-red-500 text-foreground font-mono placeholder:text-muted-foreground/30 transition-colors disabled:opacity-50"
                  autoComplete="off"
                />
              </div>

              <div className="flex justify-end items-center gap-2 pt-2">
                <button
                  type="button"
                  onClick={() => {
                    setIsDeleteModalOpen(false);
                    setDeleteTarget(null);
                  }}
                  disabled={deleteLoading}
                  className="flex items-center justify-center h-8 px-4 rounded-lg border border-border hover:bg-muted/50 text-xs font-semibold text-muted-foreground transition-colors disabled:opacity-50 cursor-pointer"
                >
                  Cancel
                </button>
                <button
                  type="submit"
                  disabled={deleteLoading || deleteConfirmCode !== deleteTarget.code}
                  className="flex items-center justify-center h-8 px-4 rounded-lg bg-red-600 hover:bg-red-750 text-xs font-semibold text-white transition-colors disabled:opacity-50 disabled:cursor-not-allowed disabled:bg-red-600/50 cursor-pointer gap-1.5"
                >
                  {deleteLoading ? (
                    <>
                      <Loader2 className="h-3.5 w-3.5 animate-spin" />
                      <span>Deleting...</span>
                    </>
                  ) : (
                    <span>Delete Workspace</span>
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

