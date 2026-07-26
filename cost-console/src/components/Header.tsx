import { Bell, LogOut, TrendingUp } from "lucide-react";
import { useLocation, useNavigate } from "react-router-dom";

import { useAuthStore } from "../lib/store/useAuthStore";
import { cn } from "../lib/utils";
import { navigationItems } from "../navigation";

interface HeaderProps {
  currency: string;
  setCurrency: (currency: string) => void;
}

export function Header({ currency, setCurrency }: HeaderProps) {
  const navigate = useNavigate();
  const location = useLocation();
  const { user, logout, checkPermission } = useAuthStore();
  const visibleNavigation = navigationItems.filter((item) => {
    if (item.anyPermission) {
      return item.anyPermission.some(({ key, action }) => checkPermission(key, action));
    }
    return !item.permission || checkPermission(item.permission.key, item.permission.action);
  });

  return (
    <header className="z-40 flex min-h-16 w-full shrink-0 flex-wrap items-center gap-3 border-b border-slate-800/80 bg-slate-900 px-4 py-3 text-xs select-none lg:flex-nowrap lg:px-8">
      <button
        type="button"
        className="order-1 flex shrink-0 items-center gap-2.5 text-left"
        onClick={() => navigate("/")}
      >
        <span className="rounded bg-blue-600 p-1.5 text-white">
          <TrendingUp size={16} />
        </span>
        <span>
          <span className="block text-sm leading-none font-extrabold tracking-tight text-white">Aurora Cost</span>
          <span className="mt-0.5 block text-[9px] font-bold tracking-wider text-slate-400 uppercase">
            Management Plane
          </span>
        </span>
      </button>

      <nav className="order-3 flex w-full items-center gap-1 overflow-x-auto pb-0.5 lg:order-2 lg:w-auto lg:flex-1 lg:pl-5">
        {visibleNavigation.map((item) => {
          const Icon = item.icon;
          const active = item.path === "/"
            ? location.pathname === "/" || location.pathname === "/dashboard"
            : location.pathname.startsWith(item.path);
          return (
            <button
              type="button"
              key={item.id}
              onClick={() => navigate(item.path)}
              className={cn(
                "flex shrink-0 items-center rounded px-3 py-2 text-[11px] font-bold transition-colors",
                active
                  ? "bg-slate-800 text-blue-400"
                  : "text-slate-400 hover:bg-slate-800/50 hover:text-slate-200",
              )}
            >
              <Icon size={14} className="mr-1.5 shrink-0" />
              {item.name}
            </button>
          );
        })}
      </nav>

      <div className="order-2 ml-auto flex shrink-0 items-center gap-2 sm:gap-3 lg:order-3">
        <div className="hidden rounded border border-slate-800 bg-slate-800/40 p-0.5 sm:flex">
          {["VND", "USD"].map((option) => (
            <button
              type="button"
              key={option}
              onClick={() => setCurrency(option)}
              className={cn(
                "rounded px-2.5 py-1 text-[10px] font-bold",
                currency === option ? "bg-slate-700 text-blue-400 shadow-sm" : "text-slate-500",
              )}
            >
              {option}
            </button>
          ))}
        </div>
        <button
          type="button"
          aria-label="Notifications"
          className="relative hidden rounded border border-slate-800 p-2 text-slate-400 hover:bg-slate-800/40 hover:text-slate-200 sm:block"
        >
          <Bell size={16} />
          <span className="absolute top-1.5 right-1.5 h-1.5 w-1.5 rounded-full bg-rose-500" />
        </button>
        <div className="flex items-center gap-2">
          <span className="flex h-8 w-8 items-center justify-center rounded border border-blue-900/30 bg-blue-950/40 text-xs font-bold text-blue-400">
            {user ? user.username.substring(0, 2).toUpperCase() : "AU"}
          </span>
          <span className="hidden xl:block">
            <span className="block text-[11px] leading-none font-extrabold text-slate-200">
              {user?.username ?? "Aurora User"}
            </span>
            <span className="mt-0.5 block text-[9px] text-slate-500">IAM Billing Session</span>
          </span>
        </div>
        <button
          type="button"
          onClick={async () => {
            await logout();
            navigate("/");
          }}
          className="rounded border border-slate-800 p-2 text-slate-400 transition-colors hover:bg-red-950/20 hover:text-red-400"
          title="Đăng xuất"
        >
          <LogOut size={14} />
        </button>
      </div>
    </header>
  );
}
