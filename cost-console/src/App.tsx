import { useState, useEffect } from 'react';
import { BrowserRouter, Routes, Route, Navigate, useLocation } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { Header, navigationItems } from './components/Header';
import PlanPage from './page/plan/page';
import DashboardPage from './page/dashboard/page';
import LoginPage from './page/login/page';
import { useAuthStore } from './lib/store/useAuthStore';
import { Coins } from 'lucide-react';
import './App.css';

// Khởi tạo React Query client cho toàn bộ ứng dụng
const queryClient = new QueryClient();

// Component hiển thị fallback cho các menu tính năng chưa phát triển
function FeatureInDevelopment() {
  const location = useLocation(); // Lấy thông tin route hiện tại
  const currentItem = navigationItems.find(n => location.pathname.startsWith(n.path) && n.path !== '/');

  return (
    <div className="p-12 text-center border border-slate-200 dark:border-slate-800 border-dashed rounded-xl bg-white dark:bg-slate-900 text-slate-400 text-xs">
      <Coins className="h-10 w-10 mx-auto text-slate-300 dark:text-slate-700 mb-3" />
      <h3 className="font-bold text-sm text-slate-700 dark:text-slate-300">Tính năng đang phát triển</h3>
      <p className="text-[11px] text-slate-400 max-w-xs mx-auto mt-1">
        Giao diện quản lý "{currentItem?.name || 'Tính năng'}" hiện đang được phát triển.
      </p>
    </div>
  );
}

// Shell khung chính của ứng dụng Cost Console dành cho User đã đăng nhập
function CostDashboardShell() {
  // State quản lý loại tiền tệ chính (VND / USD)
  const [currency, setCurrency] = useState('VND');

  return (
    <div className="flex flex-col h-screen bg-slate-50 dark:bg-slate-950 font-sans text-slate-800 dark:text-slate-200 overflow-hidden">
      {/* Thanh Navigation Header phía trên */}
      <Header
        currency={currency}
        setCurrency={setCurrency}
      />

      {/* Khu vực nội dung hiển thị chính (Main Content Area) */}
      <main className="flex-1 flex flex-col overflow-hidden">
        <section className="flex-1 overflow-y-auto p-8">
          <Routes>
            {/* Route chính /plan cho trang Quản lý Gói Cước & Giá */}
            <Route path="/plan" element={<PlanPage />} />
            {/* Alias redirect từ /plans về /plan để nhất quán */}
            <Route path="/plans" element={<Navigate to="/plan" replace />} />

            {/* Route trang chủ / và /dashboard */}
            <Route path="/" element={<DashboardPage currency={currency} />} />
            <Route path="/dashboard" element={<DashboardPage currency={currency} />} />

            {/* Catch-all route cho các menu khác chưa dựng UI chi tiết */}
            <Route path="*" element={<FeatureInDevelopment />} />
          </Routes>
        </section>
      </main>
    </div>
  );
}

// AppContent quản lý trạng thái tải phiên (session) và bảo vệ route
function AppContent() {
  const { isAuthenticated, checkSession, isLoading } = useAuthStore();

  // Kiểm tra session hiện có tại cookies khi ứng dụng khởi chạy
  useEffect(() => {
    checkSession();
  }, [checkSession]);

  // Hiển thị màn hình chờ khi đang check session và chưa có trạng thái authenticate
  if (isLoading && !isAuthenticated) {
    return (
      <div className="min-h-screen flex items-center justify-center bg-[#020617] text-slate-300 select-none">
        <div className="flex flex-col items-center space-y-4">
          <svg className="animate-spin h-8 w-8 text-blue-500" fill="none" viewBox="0 0 24 24">
            <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4"></circle>
            <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"></path>
          </svg>
          <span className="text-xs font-semibold tracking-wider text-slate-500 uppercase">Đang phục hồi phiên làm việc...</span>
        </div>
      </div>
    );
  }

  return (
    <Routes>
      {/* Route Đăng nhập công khai */}
      <Route path="/login" element={<LoginPage />} />

      {/* Bảo vệ các route nghiệp vụ khác bằng Auth Guard */}
      <Route
        path="/*"
        element={
          isAuthenticated ? (
            <CostDashboardShell />
          ) : (
            <Navigate to="/login" replace />
          )
        }
      />
    </Routes>
  );
}

// Export App component chính bọc QueryClientProvider và BrowserRouter
export default function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <AppContent />
      </BrowserRouter>
    </QueryClientProvider>
  );
}
