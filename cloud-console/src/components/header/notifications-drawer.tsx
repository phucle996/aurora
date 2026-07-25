"use client";

import React, { useState, useEffect, useRef } from "react";
import { Bell, Loader2 } from "lucide-react";
import { cn } from "@/lib/utils";
import { useRealtime } from "@/context/RealtimeContext";
import {
  listTimelineActivities,
  listTimelineNotifications,
  markAllTimelineNotificationsRead,
  type TimelineActivity,
  type TimelineNotification,
} from "@/lib/api/timeline";

type JobNotificationPayload = {
  connect?: unknown;
  client?: unknown;
  subs?: unknown;
  ping?: unknown;
  operation?: string;
  title?: string;
  message?: string;
  status?: string;
  transaction_id?: string;
  notification_id?: string;
  created_at?: string;
};

interface NotificationItem {
  id: string;
  title: string;
  message: string;
  type: "success" | "error" | "info" | "warning" | "processing";
  time: string;
  read: boolean;
  createdAt?: string;
}

interface ToastItem {
  id: string;
  title: string;
  message: string;
  type: "success" | "error" | "info" | "warning" | "processing";
  time: string;
}

function normalizeType(value: string): NotificationItem["type"] {
  if (value === "success" || value === "error" || value === "processing") return value;
  return "info";
}

function formatTime(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "Unknown";
  return date.toLocaleString();
}

export function NotificationsDrawer() {
  const [notifOpen, setNotifOpen] = useState(false);
  const [view, setView] = useState<"notifications" | "activity">("notifications");
  const [notifications, setNotifications] = useState<NotificationItem[]>([]);
  const [activities, setActivities] = useState<TimelineActivity[]>([]);
  const [activeToasts, setActiveToasts] = useState<ToastItem[]>([]);
  const notifRef = useRef<HTMLDivElement>(null);

  const { subscribeToStream } = useRealtime();

  const hydrateNotifications = async () => {
    try {
      const page = await listTimelineNotifications();
      setNotifications((current) => {
        const live = new Map(current.map((item) => [item.id, item]));
        return page.items.map((item: TimelineNotification) => {
          const existing = live.get(item.notification_id);
          return {
            id: item.notification_id,
            title: item.title,
            message: item.message,
            type: normalizeType(item.severity),
            time: formatTime(item.created_at),
            read: existing?.read ?? Boolean(item.read_at),
            createdAt: item.created_at,
          };
        });
      });
    } catch (error) {
      // History is diagnostic/user convenience; realtime remains independently
      // usable when Scylla is temporarily unavailable.
      console.warn("Unable to load notification history", error);
    }
  };

  useEffect(() => {
    const handleClickOutside = (e: MouseEvent) => {
      if (notifRef.current && !notifRef.current.contains(e.target as Node)) {
        setNotifOpen(false);
      }
    };
    document.addEventListener("mousedown", handleClickOutside);
    return () => document.removeEventListener("mousedown", handleClickOutside);
  }, []);

  useEffect(() => {
    // [COMMENT]: Lắng nghe sự kiện thông báo local từ ObjectsTab khi bắt đầu upload
    const handleLocalNotificationAdd = (e: Event) => {
      const customEvent = e as CustomEvent;
      const { id, title, message, type } = customEvent.detail;
      
      const newNotif: NotificationItem = {
        id,
        title,
        message,
        type,
        time: "Just now",
        read: false,
      };

      setNotifications((prev) => {
        if (prev.some((n) => n.id === id)) return prev;
        return [newNotif, ...prev].slice(0, 10);
      });
    };

    // [COMMENT]: Lắng nghe sự kiện cập nhật trạng thái upload từ ObjectsTab (thành công/thất bại)
    const handleLocalNotificationUpdate = (e: Event) => {
      const customEvent = e as CustomEvent;
      const { id, status, error } = customEvent.detail;

      setNotifications((prev) =>
        prev.map((n) => {
          if (n.id === id) {
            return {
              ...n,
              title: status === "SUCCESS" ? "Tải lên hoàn tất" : "Tải lên thất bại",
              message: status === "SUCCESS" 
                ? `Tệp ${n.message.split(" ")[1] || "tin"} đã được tải lên thành công.` 
                : `Lỗi tải lên: ${error || "Không rõ nguyên nhân"}`,
              type: status === "SUCCESS" ? "success" : "error",
            };
          }
          return n;
        })
      );
    };

    window.addEventListener("local-notification:add", handleLocalNotificationAdd);
    window.addEventListener("local-notification:update", handleLocalNotificationUpdate);

    // [COMMENT]: Lắng nghe WebSocket kết quả của các Job không silent từ Centrifugo
    const unsubscribe = subscribeToStream("notification", "job.notification", (payload: JobNotificationPayload) => {
      if (payload && payload.operation !== "storage.object.sts") {
        console.log("🔔 Realtime notification received in drawer component:", payload);
      } else if (payload) {
        console.log("🔔 Sensitive realtime notification received in drawer component (redacted)");
      }
      if (!payload) return;

      if (payload.connect || payload.client || payload.subs || payload.ping) {
        return;
      }
      if (!payload.title && !payload.message) {
        return;
      }

      // Các operation này được xử lý silent ở component tương ứng
      const SILENT_OPERATIONS = new Set([
        "storage.object.presign",
      ]);
      if (payload.operation && SILENT_OPERATIONS.has(payload.operation)) {
        return;
      }

      const typeMap: Record<string, "success" | "error" | "info" | "processing"> = {
        "SUCCESS": "success",
        "FAILED": "error",
        "PROCESSING": "processing",
      };

      const statusType = typeMap[payload.status ?? ""] || "info";
      const notifId = payload.notification_id || payload.transaction_id;
      if (!notifId) return;
      const notifTitle = payload.title || "System Event";
      const notifMsg = payload.message || "";

      setNotifications((prev) => {
        const exists = prev.some((n) => n.id === notifId);
        if (exists) {
          // [COMMENT]: Ghi đè thông báo có cùng transaction_id để tránh spam nhiều dòng
          return prev.map((n) =>
            n.id === notifId
            ? {
                  ...n,
                  title: notifTitle,
                  message: notifMsg,
                  type: statusType,
                  time: "Just now",
                  createdAt: payload.created_at ?? n.createdAt,
                }
              : n,
          );
        }
        // Thêm mới lên đầu danh sách nếu chưa có
        const newNotif: NotificationItem = {
          id: notifId,
          title: notifTitle,
          message: notifMsg,
          type: statusType,
          time: "Just now",
          read: false,
          createdAt: payload.created_at,
        };
        return [newNotif, ...prev].slice(0, 10);
      });
    });

    return () => {
      window.removeEventListener("local-notification:add", handleLocalNotificationAdd);
      window.removeEventListener("local-notification:update", handleLocalNotificationUpdate);
      unsubscribe();
    };
  }, [subscribeToStream]);

  return (
    <>
      {/* Notifications Button */}
      <div ref={notifRef} className="relative">
        <button
          onClick={() => {
            setNotifOpen(!notifOpen);
            if (!notifOpen) {
              void hydrateNotifications();
              void listTimelineActivities()
                .then((page) => setActivities(page.items))
                .catch((error) => console.warn("Unable to load activity history", error));
              setNotifications((prev) => prev.map((n) => ({ ...n, read: true })));
              void markAllTimelineNotificationsRead().catch((error) =>
                console.warn("Unable to mark notification history read", error),
              );
            }
          }}
          className="p-2 rounded-lg hover:bg-slate-100 dark:hover:bg-sidebar-console-hover text-slate-500 dark:text-slate-400 hover:text-slate-900 dark:hover:text-white cursor-pointer transition-colors relative"
          title="Notifications"
        >
          <Bell className="h-4 w-4" />
          {notifications.some((n) => !n.read) && (
            <span className="absolute top-1 right-1 flex h-2 w-2">
              <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-blue-400 opacity-75" />
              <span className="relative inline-flex rounded-full h-2 w-2 bg-blue-500" />
            </span>
          )}
        </button>
      </div>

      {/* Azure-like Toaster container */}
      <div className="fixed top-16 right-6 z-50 flex flex-col gap-3 w-80 max-w-sm pointer-events-none select-text">
        {activeToasts.map((toast) => (
          <div
            key={toast.id}
            className={cn(
              "pointer-events-auto w-full bg-white dark:bg-slate-900 border rounded-none shadow-xl p-4 flex flex-col gap-1 relative overflow-hidden transition-all duration-300 animate-in slide-in-from-right-5 fade-in duration-200",
              toast.type === "success" && "border-l-4 border-l-emerald-500 border-slate-200 dark:border-slate-800",
              toast.type === "error" && "border-l-4 border-l-rose-500 border-slate-200 dark:border-slate-800",
              toast.type === "processing" && "border-l-4 border-l-blue-500 border-slate-200 dark:border-slate-800",
              toast.type === "info" && "border-l-4 border-l-blue-400 border-slate-200 dark:border-slate-800",
            )}
          >
            <button
              onClick={() => setActiveToasts((prev) => prev.filter((t) => t.id !== toast.id))}
              className="absolute top-2 right-2 p-1 text-slate-400 hover:text-slate-600 dark:hover:text-slate-200 rounded-sm cursor-pointer"
            >
              <span className="text-[14px] font-bold">×</span>
            </button>

            <div className="flex items-center gap-2">
              {toast.type === "processing" && (
                <Loader2 className="h-3.5 w-3.5 animate-spin text-blue-500 shrink-0" />
              )}
              <h4 className="text-xs font-bold text-slate-800 dark:text-slate-100 uppercase tracking-wide">
                {toast.title}
              </h4>
            </div>

            <p className="text-xs text-slate-600 dark:text-slate-300 leading-snug pr-4">
              {toast.message}
            </p>

            <div className="flex justify-between items-center text-[9px] text-slate-400 font-mono mt-1 pt-1 border-t border-slate-50 dark:border-slate-800/40">
              <span>SYSTEM EVENT</span>
              <span>{toast.time}</span>
            </div>
          </div>
        ))}
      </div>

      {/* Slide-out Drawer overlay */}
      {notifOpen && (
        <>
          <div
            className="fixed inset-0 bg-black/40 backdrop-blur-xs z-50 animate-in fade-in duration-200"
            onClick={() => setNotifOpen(false)}
          />

          <div
            className="fixed top-0 right-0 h-full w-80 sm:w-96 bg-white dark:bg-slate-900 shadow-2xl border-l border-slate-200 dark:border-slate-800 z-50 flex flex-col transition-all duration-300 ease-in-out animate-in slide-in-from-right duration-250"
          >
            <div className="flex items-center justify-between px-4 py-4 border-b border-slate-100 dark:border-slate-800 shrink-0">
              <div className="flex items-center gap-2">
                <Bell className="h-4 w-4 text-slate-700 dark:text-slate-300" />
                <h3 className="text-sm font-bold text-slate-800 dark:text-slate-200">
                  {view === "notifications" ? "Notifications" : "Activity history"}
                </h3>
              </div>
              <button
                onClick={() => setNotifOpen(false)}
                className="p-1 rounded-md hover:bg-slate-100 dark:hover:bg-slate-800 text-slate-400 hover:text-slate-600 dark:hover:text-slate-200 cursor-pointer transition-colors"
              >
                <span className="text-[18px] font-bold leading-none">&times;</span>
              </button>
            </div>

            <div className="flex gap-1 px-4 py-2 border-b border-slate-100 dark:border-slate-800">
              <button
                className={cn(
                  "flex-1 py-1.5 text-xs font-semibold",
                  view === "notifications"
                    ? "bg-slate-900 text-white dark:bg-white dark:text-slate-900"
                    : "text-slate-500 hover:bg-slate-100 dark:hover:bg-slate-800",
                )}
                onClick={() => setView("notifications")}
              >
                Notifications
              </button>
              <button
                className={cn(
                  "flex-1 py-1.5 text-xs font-semibold",
                  view === "activity"
                    ? "bg-slate-900 text-white dark:bg-white dark:text-slate-900"
                    : "text-slate-500 hover:bg-slate-100 dark:hover:bg-slate-800",
                )}
                onClick={() => setView("activity")}
              >
                Activity
              </button>
            </div>

            <div className="flex-1 overflow-y-auto px-4 py-4 space-y-4 select-text">
              {view === "activity" ? (
                activities.length === 0 ? (
                  <div className="flex flex-col items-center justify-center h-full text-slate-400 dark:text-slate-500 gap-2">
                    <Bell className="h-8 w-8 stroke-1 text-slate-300 dark:text-slate-700" />
                    <span className="text-xs">No activity yet</span>
                  </div>
                ) : (
                  activities.map((activity) => (
                    <div
                      key={activity.event_id}
                      className="p-3 border border-slate-100 dark:border-slate-800/80 bg-slate-50/50 dark:bg-slate-900/40 relative flex flex-col gap-1"
                    >
                      <div className="flex items-start justify-between gap-2">
                        <p className="text-xs font-bold text-slate-700 dark:text-slate-200 leading-tight">
                          {activity.title}
                        </p>
                        <span className="text-[9px] font-bold px-1.5 py-0.5 rounded uppercase tracking-wide bg-slate-100 text-slate-600 dark:bg-slate-800 dark:text-slate-400">
                          {activity.category}
                        </span>
                      </div>
                      <p className="text-xs text-slate-500 dark:text-slate-400 leading-relaxed">
                        {activity.summary}
                      </p>
                      <div className="flex justify-between items-center text-[9px] text-slate-400 font-mono mt-1 pt-1 border-t border-slate-100/50 dark:border-slate-800/40">
                        <span>{activity.action}</span>
                        <span>{formatTime(activity.occurred_at)}</span>
                      </div>
                    </div>
                  ))
                )
              ) : notifications.length === 0 ? (
                <div className="flex flex-col items-center justify-center h-full text-slate-400 dark:text-slate-500 gap-2">
                  <Bell className="h-8 w-8 stroke-1 text-slate-300 dark:text-slate-700" />
                  <span className="text-xs">No notifications yet</span>
                </div>
              ) : (
                notifications.map((notif) => (
                  <div
                    key={notif.id}
                    className="p-3 border border-slate-100 dark:border-slate-800/80 bg-slate-50/50 dark:bg-slate-900/40 relative flex flex-col gap-1 rounded-none hover:border-slate-200 dark:hover:border-slate-750 transition-colors"
                  >
                    <div className="flex items-start justify-between gap-2">
                      <p className="text-xs font-bold text-slate-700 dark:text-slate-200 leading-tight">
                        {notif.title}
                      </p>
                      <span
                        className={cn(
                          "text-[9px] font-bold px-1.5 py-0.5 rounded shrink-0 uppercase tracking-wide",
                          notif.type === "success" && "bg-emerald-50 text-emerald-600 dark:bg-emerald-950/30 dark:text-emerald-400",
                          notif.type === "error" && "bg-rose-50 text-rose-600 dark:bg-rose-950/30 dark:text-rose-400",
                          notif.type === "processing" && "bg-blue-50 text-blue-600 dark:bg-blue-950/30 dark:text-blue-400",
                          notif.type === "info" && "bg-slate-100 text-slate-600 dark:bg-slate-800 dark:text-slate-400",
                        )}
                      >
                        {notif.type}
                      </span>
                    </div>
                    <p className="text-xs text-slate-500 dark:text-slate-400 leading-relaxed">
                      {notif.message}
                    </p>
                    <div className="flex justify-between items-center text-[9px] text-slate-400 font-mono mt-1 pt-1 border-t border-slate-100/50 dark:border-slate-800/40">
                      <span>SYSTEM EVENT</span>
                      <span>{notif.time}</span>
                    </div>
                  </div>
                ))
              )}
            </div>

            {view === "notifications" && notifications.length > 0 && (
              <div className="p-3 border-t border-slate-100 dark:border-slate-800 bg-slate-50/50 dark:bg-slate-900/30 shrink-0">
                <button
                  onClick={() => {
                    setNotifications((prev) => prev.map((item) => ({ ...item, read: true })));
                    void markAllTimelineNotificationsRead().catch((error) =>
                      console.warn("Unable to mark notification history read", error),
                    );
                  }}
                  className="w-full py-2 text-center text-xs font-bold text-slate-500 hover:text-slate-700 dark:text-slate-400 dark:hover:text-slate-200 border border-slate-200 dark:border-slate-800 hover:border-slate-300 dark:hover:border-slate-700 bg-white dark:bg-slate-900 transition-colors cursor-pointer"
                >
                  Mark all as read
                </button>
              </div>
            )}
          </div>
        </>
      )}
    </>
  );
}
