import React, { useState, useEffect } from "react";
import {
  X,
  Edit,
  KeyRound,
  UserPlus,
  UserCheck,
  Power,
  Laptop,
  Lock,
  History,
  Fingerprint,
  Check,
  Globe,
  Shield,
  Trash2,
  Loader2
} from "lucide-react";
import { toast } from "sonner";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { getUserRolePlatform, getUserDevicesPlatform, type PlatformRoleItem } from "@/lib/api/session";
import { useUserSession } from "@/hooks/useUserSession";
import { getUserMfaPlatform, type PlatformMfaStatus } from "@/lib/api/mfa";

// [COMMENT]: Sinh màu sắc avatar ngẫu nhiên đẹp mắt (nhất quán với page.tsx)
const getAvatarColors = (name: string) => {
  const hash = name.split("").reduce((acc, char) => acc + char.charCodeAt(0), 0);
  const colors = [
    "bg-blue-500/10 text-blue-600 border-blue-500/20 dark:text-blue-450 dark:border-blue-500/30",
    "bg-emerald-500/10 text-emerald-600 border-emerald-500/20 dark:text-emerald-450 dark:border-emerald-500/30",
    "bg-violet-500/10 text-violet-600 border-violet-500/20 dark:text-violet-450 dark:border-violet-500/30",
    "bg-amber-500/10 text-amber-600 border-amber-500/20 dark:text-amber-450 dark:border-amber-500/30",
    "bg-pink-500/10 text-pink-650 border-pink-500/20 dark:text-pink-400 dark:border-pink-500/30",
    "bg-indigo-500/10 text-indigo-600 border-indigo-500/20 dark:text-indigo-400 dark:border-indigo-500/30",
    "bg-cyan-500/10 text-cyan-600 border-cyan-500/20 dark:text-cyan-400 dark:border-cyan-500/30",
  ];
  return colors[hash % colors.length];
};

interface UserDetailPanelProps {
  selectedUser: any; // extendedUser object
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
    const list = ["Overview"];
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
  const [roleData, setRoleData] = useState<PlatformRoleItem | null>(null);
  const [loadingRole, setLoadingRole] = useState(false);
  const [devicesData, setDevicesData] = useState<any[]>([]); // [COMMENT]: State lưu trữ danh sách thiết bị thực tế của user
  const [loadingDevices, setLoadingDevices] = useState(false); // [COMMENT]: Trạng thái loading khi fetch thiết bị
  const [mfaData, setMfaData] = useState<PlatformMfaStatus | null>(null); // [COMMENT]: Lưu trữ trạng thái MFA thực tế của user
  const [loadingMfa, setLoadingMfa] = useState(false); // [COMMENT]: Trạng thái loading khi kiểm tra MFA

  // [COMMENT]: Reset tab và dữ liệu role/devices khi người dùng chọn user khác
  useEffect(() => {
    setActiveDetailTab("Overview");
    setRoleData(null);
    setDevicesData([]);
    setMfaData(null);
  }, [selectedUser?.id]);

  // [COMMENT]: Thực hiện lazy load dữ liệu vai trò từ backend khi chuyển sang tab Roles
  useEffect(() => {
    if (activeDetailTab !== "Roles" || !selectedUser?.id) {
      return;
    }

    let active = true;
    const fetchRole = async () => {
      setLoadingRole(true);
      try {
        const role = await getUserRolePlatform(selectedUser.id);
        if (active) {
          setRoleData(role);
        }
      } catch (err) {
        console.error("Failed to fetch role", err);
        if (active) {
          setRoleData(null);
        }
      } finally {
        if (active) {
          setLoadingRole(false);
        }
      }
    };

    fetchRole();

    return () => {
      active = false;
    };
  }, [activeDetailTab, selectedUser?.id]);

  // [COMMENT]: Thực hiện lazy load danh sách thiết bị thực tế khi chuyển sang tab Devices
  useEffect(() => {
    if (activeDetailTab !== "Devices" || !selectedUser?.id) {
      return;
    }

    let active = true;
    const fetchDevices = async () => {
      setLoadingDevices(true);
      try {
        const res = await getUserDevicesPlatform(selectedUser.id);
        if (active && res) {
          setDevicesData(res.items);
        }
      } catch (err) {
        console.error("Failed to fetch devices", err);
        if (active) {
          setDevicesData([]);
        }
      } finally {
        if (active) {
          setLoadingDevices(false);
        }
      }
    };

    fetchDevices();

    return () => {
      active = false;
    };
  }, [activeDetailTab, selectedUser?.id]);

  // [COMMENT]: Thực hiện lazy load dữ liệu MFA thực tế từ backend khi chuyển sang tab MFA
  useEffect(() => {
    if (activeDetailTab !== "MFA" || !selectedUser?.id) {
      return;
    }

    let active = true;
    const fetchMfa = async () => {
      setLoadingMfa(true);
      try {
        const res = await getUserMfaPlatform(selectedUser.id);
        if (active) {
          setMfaData(res);
        }
      } catch (err) {
        console.error("Failed to fetch mfa status", err);
        if (active) {
          setMfaData(null);
        }
      } finally {
        if (active) {
          setLoadingMfa(false);
        }
      }
    };

    fetchMfa();

    return () => {
      active = false;
    };
  }, [activeDetailTab, selectedUser?.id]);

  const renderDetailTab = () => {
    switch (activeDetailTab) {
      case "Overview":
        return (
          <div className="flex flex-col gap-5 text-xs select-none">
            {/* Basic Information */}
            <div className="pb-5 border-b border-border/60 flex flex-col gap-3">
              <span className="font-semibold text-foreground text-xs block mb-1">Basic information</span>

              <div className="flex flex-col gap-2">
                <div className="flex justify-between items-start border-b border-border/30 py-2">
                  <span className="text-[11px] font-semibold text-muted-foreground">User ID</span>
                  <span className="font-mono text-[11px] text-foreground break-all select-all font-semibold max-w-[200px] text-right">{selectedUser.id}</span>
                </div>

                <div className="flex justify-between items-center border-b border-border/30 py-2">
                  <span className="text-[11px] font-semibold text-muted-foreground">Email</span>
                  <div className="flex items-center gap-1.5">
                    <span className="font-semibold text-foreground">{selectedUser.email}</span>
                    <Badge variant="outline" className="bg-emerald-500/10 text-emerald-600 border-emerald-500/20 dark:text-emerald-400 dark:border-emerald-500/30 font-extrabold uppercase tracking-wider text-[9px] h-4">
                      <Check className="h-2.5 w-2.5 font-bold" />
                      Verified
                    </Badge>
                  </div>
                </div>
                <div className="flex justify-between items-center border-b border-border/30 py-2">
                  <span className="text-[11px] font-semibold text-muted-foreground">Full name</span>
                  <span className="font-semibold text-foreground">{selectedUser.ext.fullname}</span>
                </div>
                <div className="flex justify-between items-center border-b border-border/30 py-2">
                  <span className="text-[11px] font-semibold text-muted-foreground">Status</span>
                  <Badge variant="outline" className={cn(
                    "capitalize font-bold text-[9px] h-5 border",
                    selectedUser.status === "active"
                      ? "bg-emerald-500/10 text-emerald-600 border-emerald-500/20 dark:text-emerald-400 dark:border-emerald-500/30"
                      : selectedUser.status === "pending-active"
                        ? "bg-amber-500/10 text-amber-600 border-amber-500/20 dark:text-amber-400 dark:border-amber-500/30"
                        : "bg-red-500/10 text-red-650 border-red-500/20 dark:text-red-450 dark:border-red-500/30"
                  )}>
                    {selectedUser.status === "pending-active" ? "pending" : selectedUser.status}
                  </Badge>
                </div>

                <div className="flex flex-col pt-2">
                  <span className="text-[11px] font-semibold text-muted-foreground mb-1">Notes</span>
                  <p className="text-[11px] text-foreground/80 leading-normal font-medium">
                    Platform administrator. Desired state monitor.
                  </p>
                </div>
              </div>
            </div>

            {/* Security Summary */}
            <div className="flex flex-col gap-3 pt-1">
              <span className="font-semibold text-foreground text-xs block mb-1">Security summary</span>

              <div className="flex items-center justify-between border-b border-border/30 py-2">
                <div className="flex items-center gap-2.5">
                  <Fingerprint className="h-4.5 w-4.5 text-blue-500 dark:text-blue-400" />
                  <div className="flex flex-col">
                    <span className="font-bold text-foreground text-[11px]">MFA</span>
                    <span className="text-[10px] text-muted-foreground font-medium">
                      {selectedUser.ext.mfaEnabled ? "Enabled (TOTP)" : "Disabled"}
                    </span>
                  </div>
                </div>
                <Button
                  variant="outline"
                  size="xs"
                  onClick={() => toast.info("Redirecting to MFA control panel...")}
                  className="font-bold text-foreground cursor-pointer"
                >
                  Manage
                </Button>
              </div>

              <div className="flex items-center justify-between border-b border-border/30 py-2">
                <div className="flex items-center gap-2.5">
                  <KeyRound className="h-4.5 w-4.5 text-indigo-500 dark:text-indigo-400" />
                  <div className="flex flex-col">
                    <span className="font-bold text-foreground text-[11px]">Password</span>
                    <span className="text-[10px] text-muted-foreground font-medium">Last changed 12 days ago</span>
                  </div>
                </div>
                <Button
                  variant="outline"
                  size="xs"
                  onClick={() => toast.info("Initializing password lifecycle update...")}
                  className="font-bold text-foreground cursor-pointer"
                >
                  Reset
                </Button>
              </div>

              <div className="flex items-center justify-between py-1">
                <div className="flex items-center gap-2.5">
                  <History className="h-4.5 w-4.5 text-amber-500 dark:text-amber-400" />
                  <div className="flex flex-col">
                    <span className="font-bold text-foreground text-[11px]">Active Sessions</span>
                    <span className="text-[10px] text-muted-foreground font-medium">
                      {selectedUser.ext.devicesCount > 0 ? `${selectedUser.ext.devicesCount} active sessions` : "No active sessions"}
                    </span>
                  </div>
                </div>
                <Button
                  variant="outline"
                  size="xs"
                  onClick={() => toast.info("Opening active sessions inspector...")}
                  className="font-bold text-foreground cursor-pointer"
                >
                  View
                </Button>
              </div>
            </div>
          </div>
        );
      case "Roles":
        if (loadingRole) {
          return (
            <div className="flex flex-col items-center justify-center py-12 select-none text-muted-foreground gap-2">
              <Loader2 className="h-5 w-5 animate-spin text-blue-500" />
              <span className="text-[11px] font-semibold">Loading role allocation...</span>
            </div>
          );
        }

        if (!roleData) {
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
              {/* [COMMENT]: Sử dụng helper getAvatarColors sinh màu tự động, đảm bảo công bằng cho mọi Role */}
              <Badge variant="outline" className={cn(
                "inline-flex items-center px-2 py-0.5 rounded-md text-[10px] font-extrabold tracking-wider mb-3 border h-5",
                getAvatarColors(roleData.name)
              )}>
                {roleData.name}
              </Badge>

              {/* [COMMENT]: Hiển thị mô tả vai trò dạng phẳng với hairline nhẹ */}
              {roleData.description && (
                <div className="flex flex-col gap-1.5 py-2 border-b border-border/30">
                  <span className="text-[11px] font-semibold text-muted-foreground">Description</span>
                  <p className="text-[11px] text-foreground/85 leading-relaxed font-medium">
                    {roleData.description}
                  </p>
                </div>
              )}

              {/* [COMMENT]: Render động cấu hình vai trò dạng danh sách phẳng Azure-like */}
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

              {/* [COMMENT]: Nút gán vai trò nằm trực tiếp trong ngữ cảnh quản trị tab Roles */}
              <div className="mt-5 border-t border-border/40 pt-4">
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => toast.info("Role allocation manager opening")}
                  className="w-full font-bold text-foreground cursor-pointer flex items-center justify-center gap-1.5 transition-colors"
                >
                  <UserCheck className="h-3.5 w-3.5 text-muted-foreground" />
                  <span>Assign Role</span>
                </Button>
              </div>
            </div>
          </div>
        );
      case "Devices":
        if (loadingDevices) {
          return (
            <div className="flex items-center justify-center py-10 select-none">
              <Loader2 className="h-5 w-5 animate-spin text-muted-foreground/60" />
            </div>
          );
        }

        return (
          <div className="flex flex-col gap-3 text-xs select-none animate-in fade-in duration-200">
            <div>
              <span className="font-semibold text-foreground block mb-3 text-xs">Registered devices ({devicesData.length})</span>
              {devicesData.length === 0 ? (
                <div className="py-6 text-center text-muted-foreground font-bold">No devices registered.</div>
              ) : (
                <div className="space-y-3">
                  {devicesData.map((item, idx) => {
                    const dev = item.device || {};
                    const isOnline = item.is_online || false;
                    const lastSeen = item.last_seen_at || null;
                    const ip = item.last_seen_ip || dev.LastSeenIP || "Unknown IP";
                    const ua = item.last_seen_user_agent || dev.LastSeenUserAgent || "Unknown UA";
                    const deviceName = dev.DeviceName || dev.device_name || "Unknown Device";
                    const status = dev.Status || dev.status || "Unknown";

                    return (
                      <div key={idx} className="flex items-center justify-between border-b border-border/30 pb-2.5 last:border-0 last:pb-0">
                        <div className="flex items-center gap-2">
                          <Laptop className="h-4.5 w-4.5 text-muted-foreground" />
                          <div className="flex flex-col">
                            <span className="font-bold text-foreground">
                              {deviceName}
                            </span>
                            <span className="text-[10px] text-muted-foreground font-medium max-w-[200px] truncate block" title={ua}>
                              IP: {ip} • {ua}
                            </span>
                            {lastSeen && (
                              <span className="text-[9px] text-muted-foreground/60 font-semibold mt-0.5">
                                Last Active: {new Date(lastSeen).toLocaleString()}
                              </span>
                            )}
                          </div>
                        </div>
                        <div className="flex items-center gap-1.5">
                          {status === "revoked" ? (
                            <Badge variant="outline" className="bg-red-500/10 text-red-600 border-red-500/20 dark:text-red-400 dark:border-red-500/30 font-extrabold uppercase tracking-wider text-[8px] h-4">
                              Revoked
                            </Badge>
                          ) : (
                            <Badge variant="outline" className={cn(
                              "font-extrabold uppercase tracking-wider text-[8px] h-4 border",
                              isOnline
                                ? "bg-emerald-500/10 text-emerald-600 border-emerald-500/20 dark:text-emerald-400 dark:border-emerald-500/30"
                                : "bg-slate-500/10 text-slate-500 border-slate-500/20 dark:text-slate-400 dark:border-slate-500/30"
                            )}>
                              {isOnline ? "Online" : "Offline"}
                            </Badge>
                          )}
                        </div>
                      </div>
                    );
                  })}
                </div>
              )}
            </div>
          </div>
        );
      case "MFA":
        if (loadingMfa) {
          return (
            <div className="flex flex-col items-center justify-center py-12 select-none text-muted-foreground gap-2">
              <Loader2 className="h-5 w-5 animate-spin text-blue-500" />
              <span className="text-[11px] font-semibold">Checking MFA configurations...</span>
            </div>
          );
        }

        const isMfaEnabled = mfaData ? mfaData.mfa_enabled : false;

        return (
          <div className="flex flex-col gap-3 text-xs select-none">
            <div>
              <span className="font-semibold text-foreground block mb-3 text-xs">MFA authentication</span>
              <div className="flex items-start gap-2.5 mb-4">
                <Fingerprint className="h-6 w-6 text-blue-500 mt-0.5" />
                <div className="flex-1">
                  <span className="font-bold text-foreground block text-[11px] uppercase">Time-based One-time Password (TOTP)</span>
                  <p className="text-[11px] text-foreground/80 mt-1 leading-normal font-semibold">
                    Secures user authentication by requiring an additional token code from a mobile authenticator app.
                  </p>
                </div>
              </div>

              <div className="flex items-center justify-between border-t border-border/40 pt-3">
                <div className="flex flex-col">
                  <span className="text-muted-foreground font-semibold text-[10px]">MFA status</span>
                  {isMfaEnabled && mfaData?.created_at && (
                    <span className="text-[9px] text-muted-foreground/80 mt-0.5 font-semibold">
                      Enabled: {new Date(mfaData.created_at).toLocaleString()}
                    </span>
                  )}
                </div>
                <Badge variant="outline" className={cn(
                  "inline-flex items-center gap-1 px-2 py-0.5 rounded-md text-[10px] font-extrabold tracking-wider border h-5",
                  isMfaEnabled
                    ? "bg-emerald-500/10 text-emerald-650 dark:text-emerald-450 dark:border-emerald-500/30"
                    : "bg-muted text-muted-foreground border-border"
                )}>
                  {isMfaEnabled ? "ENABLED" : "DISABLED"}
                </Badge>
              </div>
            </div>
          </div>
        );
      default:
        return null;
    }
  };

  return (
    <div className="w-full lg:w-[33%] bg-transparent text-card-foreground pl-6 lg:border-l border-border/60 flex flex-col gap-4.5 animate-in slide-in-from-right duration-300 ease-in-out">
      {/* Header info */}
      <div className="flex items-center gap-3 pr-8 select-none">
        <div className={cn(
          "h-10 w-10 flex items-center justify-center rounded-full text-sm font-bold border",
          getAvatarColors(selectedUser.ext.fullname)
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
                ? "bg-emerald-500/10 text-emerald-600 border-emerald-500/20 dark:text-emerald-400 dark:border-emerald-500/30"
                : selectedUser.status === "pending-active"
                  ? "bg-amber-500/10 text-amber-600 border-amber-500/20 dark:text-amber-400 dark:border-amber-500/30"
                  : "bg-red-500/10 text-red-655 border-red-500/20 dark:text-red-450 dark:border-red-500/30"
            )}>
              {selectedUser.status === "pending-active" ? "Pending" : selectedUser.status}
            </Badge>
          </div>
        </div>
        <Button
          variant="ghost"
          size="icon-sm"
          onClick={onClose}
          className="absolute right-4 top-4 border border-border text-muted-foreground hover:text-foreground cursor-pointer transition-colors"
        >
          <X className="h-3.5 w-3.5" />
        </Button>
      </div>

      {/* Navigation Tabs */}
      <div className="flex border-b border-border text-[11px] font-bold text-muted-foreground select-none">
        {tabs.map((tab) => (
          <button
            key={tab}
            onClick={() => setActiveDetailTab(tab)}
            className={cn(
              "pb-2 px-2.5 border-b-2 -mb-[2px] transition-all cursor-pointer font-bold",
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
          onClick={() => toast.info(`Initializing password reset request for @${selectedUser.username}`)}
          className="text-foreground font-bold hover:border-border transition-colors cursor-pointer flex items-center gap-1.5"
        >
          <KeyRound className="h-3.5 w-3.5 text-muted-foreground" />
          <span>Reset Password</span>
        </Button>
        {checkPermission("iam:users", "delete") && !selectedUser.ext.isMe && (
          selectedUser.status === "disabled" || selectedUser.status === "suspended" ? (
            <Button
              variant="outline"
              size="sm"
              onClick={() => onUpdateStatus(selectedUser.id, "active", selectedUser.username)}
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
              onClick={() => onUpdateStatus(selectedUser.id, "disabled", selectedUser.username)}
              disabled={updatingId !== null}
              className="border-red-200 dark:border-red-900/50 text-red-650 dark:text-red-400 font-bold hover:bg-red-50 dark:hover:bg-red-950/20 transition-colors cursor-pointer flex items-center gap-1.5 disabled:opacity-50"
            >
              <Power className="h-3.5 w-3.5" />
              <span>{updatingId === selectedUser.id ? "Disabling..." : "Disable User"}</span>
            </Button>
          )
        )}
      </div>

      {/* Tab view rendering (NO MORE SCROLL OR MAX-HEIGHT) */}
      <div className="flex flex-col gap-4.5">
        {renderDetailTab()}
      </div>
    </div>
  );
}
