import {
  TrendingUp,
  Bell,
  LayoutDashboard,
  Coins,
  Receipt,
  CreditCard,
  History
} from "lucide-react";
import { useNavigate, useLocation } from "react-router-dom";
import { cn } from "../lib/utils";

// Interface định nghĩa cho từng item trên thanh điều hướng Header
export interface NavigationItem {
  id: string;
  name: string;
  path: string; // Đường dẫn tương ứng với route của trang (ví dụ /plan, /)
  icon: React.ComponentType<{ size?: number; className?: string }>;
}

interface HeaderProps {
  currency: string;
  setCurrency: (currency: string) => void;
}

// Danh sách các mục menu trên Navigation Header
export const navigationItems: NavigationItem[] = [
  { id: 'dashboard', name: 'Dashboard', path: '/', icon: LayoutDashboard },
  { id: 'plans', name: 'Gói Cước & Giá', path: '/plan', icon: Coins },
  { id: 'invoices', name: 'Hóa Đơn Kế Toán', path: '/invoices', icon: Receipt },
  { id: 'gateways', name: 'Cổng Nạp Tiền', path: '/gateways', icon: CreditCard },
  { id: 'history', name: 'Lịch Sử Giao Dịch', path: '/history', icon: History },
];

export function Header({ currency, setCurrency }: HeaderProps) {
  // Sử dụng hook của react-router-dom để điều hướng đường dẫn URL
  const navigate = useNavigate();
  const location = useLocation();

  // Kiểm tra đường dẫn hiện tại để highlight tab tương ứng
  const currentPath = location.pathname;

  return (
    <header className="h-16 bg-white dark:bg-slate-900 border-b border-slate-200 dark:border-slate-800/80 flex items-center justify-between px-8 select-none shrink-0 w-full z-40 text-xs">
      {/* Left: Logo & Top Navigation */}
      <div className="flex items-center gap-8">
        {/* Logo ứng dụng Aurora Cost */}
        <div 
          className="flex items-center gap-2.5 cursor-pointer"
          onClick={() => navigate('/')} // Bấm vào logo chuyển về trang chủ /
        >
          <div className="bg-blue-600 text-white p-1.5 rounded-[4px] shrink-0">
            <TrendingUp size={16} />
          </div>
          <div>
            <h1 className="font-extrabold text-sm leading-none text-slate-900 dark:text-white tracking-tight">Aurora Cost</h1>
            <span className="text-[9px] font-bold text-slate-400 uppercase tracking-wider block mt-0.5">Management Plane</span>
          </div>
        </div>

        {/* Top Navbar Menu điều hướng các Route */}
        <nav className="flex items-center gap-1">
          {navigationItems.map((item) => {
            const Icon = item.icon;
            // Xác định xem mục menu này có đang active dựa trên URL path hiện tại không
            const isActive = item.path === '/' 
              ? (currentPath === '/' || currentPath === '/dashboard') 
              : currentPath.startsWith(item.path);

            return (
              <button
                key={item.id}
                onClick={() => navigate(item.path)} // Điều hướng đến path được định nghĩa
                className={cn(
                  "flex items-center px-3.5 py-2 rounded font-bold transition-colors cursor-pointer text-[12px]",
                  isActive
                    ? "bg-slate-100 dark:bg-slate-800 text-blue-600 dark:text-blue-400"
                    : "text-slate-500 dark:text-slate-400 hover:bg-slate-50 dark:hover:bg-slate-800/40 hover:text-slate-800 dark:hover:text-slate-200"
                )}
              >
                <Icon size={14} className="mr-1.5 shrink-0" />
                <span>{item.name}</span>
              </button>
            );
          })}
        </nav>
      </div>

      {/* Right: Currency, Notifications & User Info */}
      <div className="flex items-center gap-5">
        {/* Currency Switcher */}
        <div className="flex border border-slate-200 dark:border-slate-800 rounded p-0.5 bg-slate-50 dark:bg-slate-800/40 shrink-0">
          <button
            onClick={() => setCurrency('VND')}
            className={cn(
              "px-2.5 py-1 text-[11px] font-bold rounded transition-all cursor-pointer",
              currency === 'VND'
                ? "bg-white dark:bg-slate-700 shadow-sm text-blue-600 dark:text-blue-400"
                : "text-slate-400"
            )}
          >
            VND
          </button>
          <button
            onClick={() => setCurrency('USD')}
            className={cn(
              "px-2.5 py-1 text-[11px] font-bold rounded transition-all cursor-pointer",
              currency === 'USD'
                ? "bg-white dark:bg-slate-700 shadow-sm text-blue-600 dark:text-blue-400"
                : "text-slate-400"
            )}
          >
            USD
          </button>
        </div>

        {/* Notifications */}
        <button className="p-2 border border-slate-200 dark:border-slate-800 rounded hover:bg-slate-50 dark:hover:bg-slate-800/40 text-slate-400 hover:text-slate-600 dark:hover:text-slate-200 relative cursor-pointer outline-none">
          <Bell size={16} />
          <span className="absolute top-1.5 right-1.5 w-1.5 h-1.5 bg-rose-500 rounded-full"></span>
        </button>

        {/* Divider */}
        <div className="h-5 w-[1px] bg-slate-200 dark:bg-slate-800 shrink-0"></div>

        {/* Accountant Profile */}
        <div className="flex items-center gap-2.5">
          <div className="w-8 h-8 rounded bg-blue-50 dark:bg-blue-950/40 border border-blue-100/50 dark:border-blue-900/30 text-blue-600 dark:text-blue-400 flex items-center justify-center font-bold text-xs">
            KT
          </div>
          <div className="hidden md:block">
            <p className="text-[11px] font-extrabold text-slate-800 dark:text-slate-200 leading-none">Kế toán trưởng</p>
            <p className="text-[9px] text-slate-400 font-medium mt-0.5">finance@aurora.cloud</p>
          </div>
        </div>
      </div>
    </header>
  );
}
