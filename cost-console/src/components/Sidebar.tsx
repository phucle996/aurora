import { useLocation, useNavigate } from "react-router-dom";

import { useAuthStore } from "../lib/store/useAuthStore";
import { cn } from "../lib/utils";
import { navigationItems } from "../navigation";

export function Sidebar() {
  const location = useLocation();
  const navigate = useNavigate();
  const checkPermission = useAuthStore((state) => state.checkPermission);
  const visibleNavigation = navigationItems.filter((item) => {
    if (item.anyPermission) {
      return item.anyPermission.some(({ key, action }) => checkPermission(key, action));
    }
    return !item.permission || checkPermission(item.permission.key, item.permission.action);
  });

  return (
    <>
      <aside className="hidden w-60 shrink-0 border-r border-slate-200 bg-white px-3 py-5 dark:border-slate-800/80 dark:bg-slate-950 lg:flex lg:flex-col">
        <p className="px-3 text-[10px] font-bold tracking-[0.18em] text-slate-400 dark:text-slate-600 uppercase">Workspace</p>
        <nav className="mt-3 space-y-1" aria-label="Cost Console">
          {visibleNavigation.map((item) => {
            const Icon = item.icon;
            const active = item.path === "/"
              ? location.pathname === "/" || location.pathname === "/dashboard"
              : location.pathname.startsWith(item.path);
            return <button type="button" key={item.id} onClick={() => navigate(item.path)} className={cn("flex w-full items-center gap-3 rounded-[4px] px-3 py-2.5 text-left text-xs font-semibold transition-colors", active ? "bg-blue-50 text-blue-700 shadow-[inset_2px_0_0_0_rgb(37_99_235)] dark:bg-blue-500/10 dark:text-blue-300 dark:shadow-[inset_2px_0_0_0_rgb(96_165_250)]" : "text-slate-500 hover:bg-slate-50 hover:text-slate-900 dark:text-slate-400 dark:hover:bg-slate-900 dark:hover:text-slate-200")}><Icon size={16} aria-hidden="true" />{item.name}</button>;
          })}
        </nav>
        <p className="mt-auto px-3 text-[10px] leading-relaxed text-slate-400 dark:text-slate-600">Amounts shown here are authoritative Billing projections.</p>
      </aside>
      <nav className="fixed right-3 bottom-3 left-3 z-40 flex justify-around rounded-xl border border-slate-700/80 bg-slate-900/95 p-1 shadow-2xl backdrop-blur lg:hidden" aria-label="Cost Console mobile navigation">
        {visibleNavigation.map((item) => {
          const Icon = item.icon;
          const active = item.path === "/" ? location.pathname === "/" || location.pathname === "/dashboard" : location.pathname.startsWith(item.path);
          return <button type="button" key={item.id} onClick={() => navigate(item.path)} className={cn("flex min-w-0 flex-1 flex-col items-center gap-1 rounded-lg px-2 py-2 text-[10px] font-semibold", active ? "bg-blue-500/15 text-blue-300" : "text-slate-500")}><Icon size={16} aria-hidden="true" /><span className="truncate">{item.name}</span></button>;
        })}
      </nav>
    </>
  );
}
