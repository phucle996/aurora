import { LogOut, Moon, Sun, TrendingUp } from "lucide-react";
import { useNavigate } from "react-router-dom";

import { useAuthStore } from "../lib/store/useAuthStore";

interface HeaderProps {
  ownerKind: "personal" | "tenant";
  darkMode: boolean;
  onToggleTheme: () => void;
}

export function Header({ ownerKind, darkMode, onToggleTheme }: HeaderProps) {
  const navigate = useNavigate();
  const { user, logout } = useAuthStore();

  return (
    <header className="z-40 flex min-h-14 shrink-0 items-center gap-3 border-b border-slate-200 bg-white px-4 py-3 text-xs select-none dark:border-slate-800/80 dark:bg-slate-950 sm:px-6">
      <button
        type="button"
        className="flex shrink-0 items-center gap-2.5 text-left"
        onClick={() => navigate("/")}
      >
          <span className="rounded-[4px] bg-blue-600 p-1.5 text-white">
          <TrendingUp size={16} />
        </span>
        <span>
          <span className="block text-[15px] leading-none font-semibold tracking-tight text-slate-900 dark:text-white">Aurora Cost</span>
          <span className="mt-0.5 block text-[10px] font-medium tracking-wider text-blue-400/90 uppercase">
            Billing control center
          </span>
        </span>
      </button>

      <div className="ml-auto flex shrink-0 items-center gap-2 sm:gap-3">
        <span className="hidden rounded-[4px] border border-slate-200 bg-slate-50 px-2.5 py-1 text-[10px] font-medium text-slate-500 dark:border-slate-800 dark:bg-slate-900 dark:text-slate-400 sm:block">
          {ownerKind === "personal" ? "Personal account" : "Tenant account"}
        </span>
        <div className="flex items-center gap-2">
          <span className="flex h-8 w-8 items-center justify-center rounded-[4px] border border-blue-200 bg-blue-50 text-xs font-bold text-blue-600 dark:border-blue-900/30 dark:bg-blue-950/40 dark:text-blue-400">
            {user ? user.username.substring(0, 2).toUpperCase() : "AU"}
          </span>
          <span className="hidden xl:block">
            <span className="block text-[11px] leading-none font-semibold text-slate-800 dark:text-slate-200">
              {user?.username ?? "Aurora User"}
            </span>
            <span className="mt-0.5 block text-[9px] text-slate-500">IAM Billing Session</span>
          </span>
        </div>
        <button type="button" onClick={onToggleTheme} className="rounded-[4px] border border-slate-200 p-2 text-slate-500 hover:bg-slate-50 dark:border-slate-800 dark:text-slate-400 dark:hover:bg-slate-900" title={darkMode ? "Use light mode" : "Use dark mode"}>
          {darkMode ? <Sun size={14} /> : <Moon size={14} />}
        </button>
        <button
          type="button"
          onClick={async () => {
            await logout();
            navigate("/");
          }}
          className="rounded-[4px] border border-slate-200 p-2 text-slate-500 transition-colors hover:bg-red-50 hover:text-red-600 dark:border-slate-800 dark:text-slate-400 dark:hover:bg-red-950/20 dark:hover:text-red-400"
          title="Đăng xuất"
        >
          <LogOut size={14} />
        </button>
      </div>
    </header>
  );
}
