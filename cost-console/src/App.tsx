import { useEffect, useState } from 'react';
import { BrowserRouter, Navigate, Routes, Route } from 'react-router-dom';
import { QueryClientProvider } from '@tanstack/react-query';
import { Header } from './components/Header';
import { RouteGuard } from './components/RouteGuard';
import { Sidebar } from './components/Sidebar';
import PricingSchedulesPage from './page/pricing-schedules/page';
import DashboardPage from './page/dashboard/page';
import { ReferralCampaigns } from './page/referrals/ReferralCampaigns';
import { queryClient } from './lib/queryClient';
import { useAuthStore } from './lib/store/useAuthStore';
import { Toaster } from 'sonner';
import './App.css';

function CostDashboardShell() {
  const { renderContext, isLoading, error } = useAuthStore();
  const [darkMode, setDarkMode] = useState(() => localStorage.getItem('cost.theme') !== 'light');

  useEffect(() => {
    document.documentElement.classList.toggle('dark', darkMode);
    localStorage.setItem('cost.theme', darkMode ? 'dark' : 'light');
  }, [darkMode]);

  if (!renderContext) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-slate-950 p-6 text-slate-300">
        <div className="max-w-md rounded-lg border border-slate-800 bg-slate-900 p-8 text-center">
          <h1 className="text-lg font-semibold text-white">
            {isLoading ? 'Restoring Cost Console session…' : 'Cost Console context unavailable'}
          </h1>
          <p className="mt-2 text-sm text-slate-400">
            {error || 'IAM did not return a valid billing render context.'}
          </p>
        </div>
      </div>
    );
  }

  return (
    <div className="cost-app flex h-screen flex-col overflow-hidden bg-slate-50 font-sans text-slate-900 dark:bg-slate-950 dark:text-slate-200">
      <Header ownerKind={renderContext.kind} darkMode={darkMode} onToggleTheme={() => setDarkMode((value) => !value)} />
      <div className="flex min-h-0 flex-1">
        <Sidebar />
        <main className="min-w-0 flex-1 overflow-y-auto bg-slate-50 dark:bg-slate-950">
          <section className="mx-auto w-full max-w-[1600px] p-4 pb-24 sm:p-6 sm:pb-24 lg:p-8">
            <Routes>
              {/* Controlled PAYG pricing schedule catalog. Legacy plans/tiers are removed. */}
              <Route
                path="/pricing-schedules"
                element={
                  <RouteGuard
                    requiredKey="billing:pricing_schedule"
                    requiredAction="read"
                  >
                    <PricingSchedulesPage />
                  </RouteGuard>
                }
              />

              <Route path="/" element={<DashboardPage personal={renderContext.kind === "personal"} />} />
              <Route path="/dashboard" element={<DashboardPage personal={renderContext.kind === "personal"} />} />
              <Route
                path="/referrals"
                element={
                  <RouteGuard requiredKey="billing:credit" requiredAction="adjust">
                    <ReferralCampaigns />
                  </RouteGuard>
                }
              />

              <Route path="*" element={<Navigate to="/" replace />} />
            </Routes>
          </section>
        </main>
      </div>
    </div>
  );
}

// AppContent quản lý trạng thái tải phiên (session) và bảo vệ route
function AppContent() {
  const { isAuthenticated, checkSession, isLoading, error } = useAuthStore();

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
      {/* [COMMENT]: Cost Console không có credential form; session chỉ đến từ IAM one-time handoff. */}
      <Route
        path="/*"
        element={
          isAuthenticated ? (
            <CostDashboardShell />
          ) : (
            <div className="min-h-screen flex items-center justify-center bg-[#020617] text-slate-300 p-6">
              <div className="max-w-md rounded-lg border border-slate-800 bg-slate-900 p-8 text-center">
                <h1 className="text-lg font-semibold text-white">IAM session required</h1>
                <p className="mt-2 text-sm text-slate-400">
                  Connect this Cost Console origin to your current Aurora IAM session.
                </p>
                {error && <p className="mt-3 text-xs text-rose-400">{error}</p>}
                <a
                  href="/auth/start"
                  className="mt-6 inline-flex rounded bg-blue-600 px-4 py-2 text-sm font-semibold text-white"
                >
                  Continue with Aurora IAM
                </a>
              </div>
            </div>
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
        <Toaster position="bottom-right" richColors closeButton toastOptions={{ className: 'cost-toast' }} />
      </BrowserRouter>
    </QueryClientProvider>
  );
}
