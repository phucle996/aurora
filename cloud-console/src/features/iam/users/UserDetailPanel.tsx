import React, { useState, useEffect } from "react";
import { KeyRound, Power, Loader2, X } from "lucide-react";
import { toast } from "sonner";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { resetUserPassword } from "@/features/iam/users-api";
import { useUserSession } from "@/session/use-session";
import { useMutation } from "@tanstack/react-query";

import { OverviewTab } from "./OverviewTab";
import { RolesTab } from "./RolesTab";
import { DevicesTab } from "./DevicesTab";
import { MfaTab } from "./MfaTab";
import { AuthMethodsTab } from "./AuthMethodsTab";
import type { ExtendedUser } from "./UserTable";

// [COMMENT]: Sinh màu sắc avatar ngẫu nhiên đẹp mắt (nhất quán với page.tsx)
const getAvatarColors = (name: string) => {
  const hash = name.split("").reduce((acc, char) => acc + char.charCodeAt(0), 0);
  const colors = [
    "bg-blue-500/10 text-blue-600 border-blue-500/20 dark:text-blue-450 dark:border-blue-500/30",
    "bg-emerald-500/10 text-emerald-600 border-emerald-500/20 dark:text-emerald-450 dark:border-emerald-500/30",
    "bg-violet-500/10 text-violet-600 border-violet-500/20 dark:text-violet-450 dark:border-violet-500/30",
    "bg-amber-500/10 text-amber-600 border-amber-500/20 dark:text-amber-450 dark:border-amber-500/30",
    "bg-pink-500/10 text-pink-655 border-pink-500/20 dark:text-pink-400 dark:border-pink-500/30",
    "bg-indigo-500/10 text-indigo-600 border-indigo-500/20 dark:text-indigo-400 dark:border-indigo-500/30",
    "bg-cyan-500/10 text-cyan-600 border-cyan-500/20 dark:text-cyan-400 dark:border-cyan-500/30",
  ];
  return colors[hash % colors.length];
};

interface UserDetailPanelProps {
  selectedUser: ExtendedUser;
  onClose: () => void;
  updatingId: string | null;
  onUpdateStatus: (id: string, status: string, username: string) => void;
}

export function UserDetailPanel({
  selectedUser,
  onClose,
  updatingId,
  onUpdateStatus
}: UserDetailPanelProps) {
  const { checkPermission } = useUserSession();

  // [COMMENT]: Sinh danh sách tab động dựa theo quyền hạn thực tế của actor
  const tabs = React.useMemo(() => {
    const list = ["Overview", "Sign-in"];
    if (checkPermission("iam:role", "read")) {
      list.push("Roles");
    }
    if (checkPermission("iam:device", "read")) {
      list.push("Devices");
    }
    list.push("MFA");
    return list;
  }, [checkPermission]);

  const [activeDetailTab, setActiveDetailTab] = useState("Overview");
  const [showConfirm, setShowConfirm] = useState<"enable" | "disable" | null>(null); // [COMMENT]: Confirm inline status update state
  const [showResetForm, setShowResetForm] = useState(false); // [COMMENT]: Show inline reset password form state
  const [newPassword, setNewPassword] = useState(""); // [COMMENT]: Input text for new password

  // [COMMENT]: Reset tab và dữ liệu khi đổi user mục tiêu
  useEffect(() => {
    let active = true;
    queueMicrotask(() => {
      if (!active) return;
      setActiveDetailTab("Overview");
      setShowConfirm(null);
      setShowResetForm(false);
      setNewPassword("");
    });
    return () => { active = false; };
  }, [selectedUser?.id]);

  const renderDetailTab = () => {
    switch (activeDetailTab) {
      case "Overview":
        return <OverviewTab selectedUser={selectedUser} />;
      case "Sign-in":
        return <AuthMethodsTab selectedUser={selectedUser} />;
      case "Roles":
        return <RolesTab selectedUser={selectedUser} getAvatarColors={getAvatarColors} />;
      case "Devices":
        return <DevicesTab selectedUser={selectedUser} />;
      case "MFA":
        return <MfaTab selectedUser={selectedUser} />;
      default:
        return null;
    }
  };

  // [COMMENT]: Mutation reset mật khẩu người dùng
  const resetPasswordMutation = useMutation<void, Error, string>({
    mutationFn: (password) => resetUserPassword(selectedUser.id, password),
    onSuccess: () => {
      toast.success(`Password for @${selectedUser.username} has been reset successfully`);
      setShowResetForm(false);
      setNewPassword("");
    },
    onError: (err: unknown) => {
      toast.error(err instanceof Error ? err.message : "Failed to reset password");
    },
  });

  const handleResetPassword = () => {
    if (!newPassword) return;
    resetPasswordMutation.mutate(newPassword);
  };

  return (
    <div className="w-full lg:w-[33%] bg-transparent text-card-foreground pl-6 lg:border-l border-border/60 flex flex-col gap-4.5 animate-in slide-in-from-right duration-300 ease-in-out">
      {/* Header info */}
      <div className="flex items-center gap-3 pr-8 select-none">
        <div className={cn(
          "h-10 w-10 flex items-center justify-center rounded-full text-sm font-bold border",
          getAvatarColors(selectedUser.id)
        )}>
          {selectedUser.ext.fullname
            .split(" ")
            .map((n: string) => n.charAt(0))
            .slice(0, 2)
            .join("")
            .toUpperCase() || "U"}
        </div>
        <div className="flex flex-col">
          <div className="flex items-center gap-1.5">
            <span className="font-bold text-sm text-foreground">{selectedUser.ext.fullname}</span>
            <Badge variant="outline" className={cn(
              "capitalize font-bold text-[9px] px-1.5 py-0.2 h-4.5 border",
              selectedUser.status === "active"
                ? "bg-emerald-500/10 text-emerald-600 border-emerald-500/20 dark:text-emerald-450 dark:border-emerald-500/30"
                : selectedUser.status === "pending-active"
                  ? "bg-amber-500/10 text-amber-600 border-amber-500/20 dark:text-amber-400 dark:border-amber-500/30"
                  : "bg-red-500/10 text-red-655 border-red-500/20 dark:text-red-450 dark:border-red-500/30"
            )}>
              {selectedUser.status === "pending-active" ? "Pending" : selectedUser.status}
            </Badge>
          </div>
        </div>
        <button
          type="button"
          onClick={onClose}
          aria-label="Close user details"
          className="ml-auto inline-flex h-7 w-7 items-center justify-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground"
        >
          <X className="h-3.5 w-3.5" />
        </button>
      </div>

      {/* Navigation Tabs */}
      <div className="flex overflow-x-auto border-b border-border text-[11px] font-bold text-muted-foreground select-none">
        {tabs.map((tab) => (
          <button
            key={tab}
            onClick={() => setActiveDetailTab(tab)}
            className={cn(
              "shrink-0 pb-2 px-2.5 border-b-2 -mb-[2px] transition-all cursor-pointer font-bold",
              activeDetailTab === tab
                ? "border-blue-600 text-blue-600 dark:text-blue-450"
                : "border-transparent hover:text-foreground"
            )}
          >
            {tab}
          </button>
        ))}
      </div>

      {/* Active Action Buttons */}
      <div className="flex flex-wrap items-center gap-2 select-none">
        <Button
          variant="outline"
          size="sm"
          onClick={() => { setShowResetForm(true); setShowConfirm(null); }}
          className="text-foreground font-bold hover:border-border transition-colors cursor-pointer flex items-center gap-1.5"
        >
          <KeyRound className="h-3.5 w-3.5 text-muted-foreground" />
          <span>Reset Password</span>
        </Button>
        {checkPermission("iam:users", "manage") && (
          selectedUser.status === "disabled" || selectedUser.status === "suspended" ? (
            <Button
              variant="outline"
              size="sm"
              onClick={() => { setShowConfirm("enable"); setShowResetForm(false); }}
              disabled={updatingId !== null}
              className="border-emerald-200 dark:border-emerald-900/50 text-emerald-600 dark:text-emerald-450 font-bold hover:bg-emerald-50 dark:hover:bg-emerald-950/20 transition-colors cursor-pointer flex items-center gap-1.5 disabled:opacity-50"
            >
              <Power className="h-3.5 w-3.5" />
              <span>{updatingId === selectedUser.id ? "Enabling..." : "Enable User"}</span>
            </Button>
          ) : (
            <Button
              variant="outline"
              size="sm"
              onClick={() => { setShowConfirm("disable"); setShowResetForm(false); }}
              disabled={updatingId !== null}
              className="border-red-200 dark:border-red-900/50 text-red-655 dark:text-red-400 font-bold hover:bg-red-50 dark:hover:bg-red-950/20 transition-colors cursor-pointer flex items-center gap-1.5 disabled:opacity-50"
            >
              <Power className="h-3.5 w-3.5" />
              <span>{updatingId === selectedUser.id ? "Disabling..." : "Disable User"}</span>
            </Button>
          )
        )}
      </div>

      {/* Confirmation Box (Inline) */}
      {showConfirm && (
        <div className={cn(
          "rounded-lg p-3.5 text-xs flex flex-col gap-2 select-none animate-in fade-in slide-in-from-top-2 duration-200 border",
          showConfirm === "enable"
            ? "bg-emerald-500/5 border-emerald-500/20 dark:border-emerald-500/10 text-emerald-800 dark:text-emerald-300"
            : "bg-red-500/5 border-red-500/20 dark:border-red-500/10 text-red-800 dark:text-red-300"
        )}>
          <span className="font-semibold text-foreground">
            Do you want to {showConfirm} this user?
          </span>
          <div className="flex items-center gap-2 mt-1">
            <Button
              size="xs"
              variant="default"
              disabled={updatingId !== null}
              onClick={async () => {
                const targetStatus = showConfirm === "enable" ? "active" : "disabled";
                onUpdateStatus(selectedUser.id, targetStatus, selectedUser.username);
                setShowConfirm(null);
              }}
              className={cn(
                "!text-white font-bold cursor-pointer h-7 px-2.5",
                showConfirm === "enable" ? "!bg-emerald-600 hover:!bg-emerald-700" : "!bg-red-600 hover:!bg-red-700"
              )}
            >
              Confirm
            </Button>
            <Button
              size="xs"
              variant="outline"
              disabled={updatingId !== null}
              onClick={() => setShowConfirm(null)}
              className="font-bold text-foreground cursor-pointer h-7 px-2.5 border-border hover:bg-muted"
            >
              Cancel
            </Button>
          </div>
        </div>
      )}

      {/* Reset Password Form (Inline) */}
      {showResetForm && (
        <div className="rounded-lg p-3.5 text-xs flex flex-col gap-2.5 select-none animate-in fade-in slide-in-from-top-2 duration-200 border border-blue-500/20 dark:border-blue-500/10 bg-blue-500/5">
          <span className="font-semibold text-foreground">
            Set new password for @{selectedUser.username}
          </span>
          <div className="flex gap-2">
            <input
              type="password"
              placeholder="Enter new password"
              value={newPassword}
              onChange={(e) => setNewPassword(e.target.value)}
              className="bg-background text-foreground border border-border rounded px-2.5 py-1 text-xs flex-1 outline-none focus:border-blue-500 min-w-0"
            />
          </div>
          <div className="flex items-center gap-2 mt-1">
            <Button
              size="xs"
              variant="default"
              disabled={updatingId !== null || resetPasswordMutation.isPending || !newPassword.trim()}
              onClick={handleResetPassword}
              className="!bg-blue-600 hover:!bg-blue-700 !text-white font-bold cursor-pointer h-7 px-2.5 flex items-center gap-1"
            >
              {resetPasswordMutation.isPending && <Loader2 className="h-3 w-3 animate-spin text-white" />}
              <span>Confirm</span>
            </Button>
            <Button
              size="xs"
              variant="outline"
              disabled={updatingId !== null}
              onClick={() => {
                setShowResetForm(false);
                setNewPassword("");
              }}
              className="font-bold text-foreground cursor-pointer h-7 px-2.5 border-border hover:bg-muted"
            >
              Cancel
            </Button>
          </div>
        </div>
      )}

      {/* Tab view rendering (NO MORE SCROLL OR MAX-HEIGHT) */}
      <div className="flex flex-col gap-4.5">
        {renderDetailTab()}
      </div>
    </div>
  );
}
