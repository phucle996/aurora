"use client";

import React, { useState, useEffect, useMemo, useRef } from "react";
import { Bell } from "lucide-react";
import { cn } from "@/lib/utils";
import {
  listNotifications,
  listUserActivities,
  markAllNotificationsRead,
  type TimelineActivity,
} from "@/features/notifications/api";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useConsoleQueryScope } from "@/shared/query/scope";
import { formatTime, notificationItem, type NotificationItem } from "@/features/notifications/model";
import { useNotificationRealtime } from "@/features/notifications/realtime";

export function NotificationsDrawer() {
  const [notifOpen, setNotifOpen] = useState(false);
  const [view, setView] = useState<"notifications" | "activity">("notifications");
  const notifRef = useRef<HTMLDivElement>(null);
  const queryClient = useQueryClient();
  const scope = useConsoleQueryScope();
  const notificationKey = useMemo(() => [...scope, "notifications", "history"] as const, [scope]);
  const activityKey = useMemo(() => [...scope, "activity", "history"] as const, [scope]);
  useNotificationRealtime(notificationKey);

  const notificationsQuery = useQuery<NotificationItem[]>({
    queryKey: notificationKey,
    queryFn: async ({ signal }) => (await listNotifications(undefined, signal)).items.map(notificationItem),
    enabled: notifOpen,
  });
  const activitiesQuery = useQuery<TimelineActivity[]>({
    queryKey: activityKey,
    queryFn: async ({ signal }) => (await listUserActivities(undefined, signal)).items,
    enabled: notifOpen && view === "activity",
  });
  const notifications = notificationsQuery.data ?? [];
  const activities = activitiesQuery.data ?? [];

  const markAllRead = () => {
    const current = queryClient.getQueryData<NotificationItem[]>(notificationKey);
    if (current) {
      queryClient.setQueryData<NotificationItem[]>(notificationKey,
        current.map((item) => ({ ...item, read: true })),
      );
    }
    void markAllNotificationsRead().catch(() =>
      queryClient.invalidateQueries({ queryKey: notificationKey }),
    );
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

  return (
    <>
      {/* Notifications Button */}
      <div ref={notifRef} className="relative">
        <button
          onClick={() => {
            setNotifOpen(!notifOpen);
            if (!notifOpen) {
              markAllRead();
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
                    markAllRead();
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
