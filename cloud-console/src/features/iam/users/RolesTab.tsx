import React, { useState, useEffect } from "react";
import { Loader2, Lock, UserCheck } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { toast } from "sonner";
import { cn } from "@/lib/utils";
import { getUserRole, listRoles, assignUserRole, type PlatformRoleItem } from "@/features/rbac/api";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useConsoleQueryScope } from "@/shared/query/scope";
import type { ExtendedUser } from "./UserTable";

interface RolesTabProps {
  selectedUser: ExtendedUser;
  getAvatarColors: (name: string) => string;
}

export function RolesTab({ selectedUser, getAvatarColors }: RolesTabProps) {
  const queryClient = useQueryClient();
  const scope = useConsoleQueryScope();
  const [showAssignForm, setShowAssignForm] = useState(false);
  const [selectedRoleID, setSelectedRoleID] = useState("");

  // [COMMENT]: Reset tab local state khi đổi user
  useEffect(() => {
    let active = true;
    queueMicrotask(() => {
      if (!active) return;
      setShowAssignForm(false);
      setSelectedRoleID("");
    });
    return () => { active = false; };
  }, [selectedUser?.id]);

  // [COMMENT]: Sử dụng useQuery để quản lý thông tin role của user được chọn.
  // retry: false giúp tránh việc gọi lại API thừa thãi khi user bị chặn quyền (Insufficient level)
  const {
    data: roleData = null,
    isLoading: loadingUserRole,
    error: userRoleError,
  } = useQuery<PlatformRoleItem | null>({
    queryKey: [...scope, "iam", "user-role", selectedUser?.id],
    queryFn: () => getUserRole(selectedUser.id),
    enabled: !!selectedUser?.id,
    retry: false,
  });

  // [COMMENT]: Sử dụng useQuery để lấy danh sách tất cả các roles có sẵn trong hệ thống
  const {
    data: rolesList = [],
    isLoading: loadingRolesList,
  } = useQuery<PlatformRoleItem[]>({
    queryKey: [...scope, "rbac", "roles"],
    queryFn: () => listRoles(),
    enabled: !!selectedUser?.id,
  });

  const loadingRole = loadingUserRole || loadingRolesList;

  // [COMMENT]: Mutation gán role mới cho User, tự động re-fetch query thông tin userRole sau khi gán thành công
  const assignRoleMutation = useMutation<void, Error, string>({
    mutationFn: (roleID) => assignUserRole(selectedUser.id, roleID),
    onSuccess: () => {
      toast.success(`Role has been assigned to @${selectedUser.username} successfully`);
      setShowAssignForm(false);
      setSelectedRoleID("");
      queryClient.invalidateQueries({ queryKey: [...scope, "iam", "user-role", selectedUser.id] });
    },
    onError: (err: unknown) => {
      toast.error(err instanceof Error ? err.message : "Failed to assign role");
    },
  });

  const assigningRole = assignRoleMutation.isPending;

  const handleAssignRole = () => {
    if (!selectedRoleID) {
      toast.error("Please select a role to assign");
      return;
    }
    assignRoleMutation.mutate(selectedRoleID);
  };

  if (loadingRole) {
    return (
      <div className="flex flex-col items-center justify-center py-12 select-none text-muted-foreground gap-2">
        <Loader2 className="h-5 w-5 animate-spin text-blue-500" />
        <span className="text-[11px] font-semibold">Loading role allocation...</span>
      </div>
    );
  }

  // [COMMENT]: Nếu xảy ra lỗi phân quyền hoặc không thể fetch roleData -> hiển thị banner Access restricted
  if (!roleData || !!userRoleError) {
    return (
      <div className="flex flex-col items-center justify-center py-10 select-none text-muted-foreground/80 gap-1.5 text-center border border-border/40 bg-muted/5 rounded-lg p-4">
        <Lock className="h-5 w-5 text-red-500/60" />
        <span className="text-[11px] font-semibold text-foreground">Access restricted</span>
        <p className="text-[10px] max-w-[220px] leading-normal font-semibold text-muted-foreground mt-0.5">
          Insufficient level hierarchy to view. This account possesses a higher administrative rank than your current credentials.
        </p>
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-4 text-xs select-none animate-in fade-in duration-200">
      <div>
        <span className="font-semibold text-foreground block mb-2 text-xs">Platform role</span>
        <Badge variant="outline" className={cn(
          "inline-flex items-center px-2 py-0.5 rounded-md text-[10px] font-extrabold tracking-wider mb-3 border h-5",
          getAvatarColors(roleData.name)
        )}>
          {roleData.name}
        </Badge>

        {roleData.description && (
          <div className="flex flex-col gap-1.5 py-2 border-b border-border/30">
            <span className="text-[11px] font-semibold text-muted-foreground">Description</span>
            <p className="text-[11px] text-foreground/85 leading-relaxed font-medium">
              {roleData.description}
            </p>
          </div>
        )}

        <div className="flex flex-col gap-2 pt-3">
          <span className="font-semibold text-foreground text-[11px] block mb-1">Role specifications</span>
          <div className="flex flex-col gap-2">
            <div className="flex justify-between items-center border-b border-border/30 py-2">
              <span className="text-[11px] font-semibold text-muted-foreground">Role level</span>
              <span className="font-semibold text-foreground">Level {roleData.role_level}</span>
            </div>
            <div className="flex justify-between items-center border-b border-border/30 py-2">
              <span className="text-[11px] font-semibold text-muted-foreground">Scope</span>
              <span className="font-semibold text-foreground capitalize">{roleData.scope}</span>
            </div>
            <div className="flex justify-between items-center border-b border-border/30 py-2">
              <span className="text-[11px] font-semibold text-muted-foreground">System code</span>
              <code className="font-mono text-[11px] text-foreground font-semibold bg-muted/30 dark:bg-muted/10 px-1.5 py-0.5 rounded border border-border/40">{roleData.code}</code>
            </div>
          </div>
        </div>

        <div className="mt-5 border-t border-border/40 pt-4">
          {showAssignForm ? (
            <div className="p-3 bg-blue-500/5 dark:bg-blue-500/2 rounded-lg flex flex-col gap-3.5 border border-blue-500/20 select-none animate-in slide-in-from-top-2 duration-200">
              <div className="flex flex-col gap-1.5">
                <span className="font-bold text-foreground text-[11px] block">Select Platform Role</span>
                <select
                  value={selectedRoleID}
                  onChange={(e) => setSelectedRoleID(e.target.value)}
                  className="w-full h-8 px-2 rounded-lg border border-border bg-card text-xs text-foreground font-medium focus:outline-hidden cursor-pointer"
                >
                  <option value="">-- Choose Role --</option>
                  {rolesList
                    .filter((r) => r.id !== roleData.id)
                    .map((r) => (
                      <option key={r.id} value={r.id}>
                        {r.name} (Level {r.role_level})
                      </option>
                    ))}
                </select>
              </div>

              <div className="text-[10px] leading-relaxed text-amber-600 dark:text-amber-400 font-semibold border-l-2 border-amber-500 pl-2">
                Assigning a new role will overwrite this user&apos;s current platform role assignment. This change will instantly invalidate their active session permissions across platform services.
              </div>

              <div className="flex gap-2 justify-end">
                <Button
                  variant="ghost"
                  size="xs"
                  onClick={() => {
                    setShowAssignForm(false);
                    setSelectedRoleID("");
                  }}
                  className="font-bold text-muted-foreground hover:text-foreground h-7 px-2.5 text-[10px]"
                >
                  Cancel
                </Button>
                <Button
                  size="xs"
                  disabled={!selectedRoleID || assigningRole}
                  onClick={handleAssignRole}
                  className="!bg-blue-600 hover:!bg-blue-700 !text-white font-bold h-7 px-3 text-[10px] flex items-center gap-1"
                >
                  {assigningRole && <Loader2 className="h-3 w-3 animate-spin text-white" />}
                  <span>Confirm Assign</span>
                </Button>
              </div>
            </div>
          ) : (
            <Button
              variant="outline"
              size="sm"
              onClick={() => setShowAssignForm(true)}
              className="w-full font-bold text-foreground cursor-pointer flex items-center justify-center gap-1.5 transition-colors"
            >
              <UserCheck className="h-3.5 w-3.5 text-muted-foreground" />
              <span>Assign Role</span>
            </Button>
          )}
        </div>
      </div>
    </div>
  );
}
