import { useState } from 'react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { Header, navigationItems } from './components/Header';
import PlanPage from './page/plan/page';
import DashboardPage from './page/dashboard/page';
import { Coins } from 'lucide-react';
import './App.css';

const queryClient = new QueryClient();

function CostDashboardShell() {
  const [activeTab, setActiveTab] = useState('dashboard');
  const [currency, setCurrency] = useState('VND');

  return (
    <div className="flex flex-col h-screen bg-slate-50 dark:bg-slate-950 font-sans text-slate-800 dark:text-slate-200 overflow-hidden">
      {/* Horizontal Top Navigation Header */}
      <Header
        activeTab={activeTab}
        setActiveTab={setActiveTab}
        currency={currency}
        setCurrency={setCurrency}
      />

      {/* Main Panel Content Area */}
      <main className="flex-1 flex flex-col overflow-hidden">
        <section className="flex-1 overflow-y-auto p-8">
          {activeTab === 'plans' ? (
            <PlanPage />
          ) : activeTab === 'dashboard' ? (
            <DashboardPage currency={currency} />
          ) : (
            <div className="p-12 text-center border border-slate-200 dark:border-slate-800 border-dashed rounded-xl bg-white dark:bg-slate-900 text-slate-400 text-xs">
              <Coins className="h-10 w-10 mx-auto text-slate-300 dark:text-slate-700 mb-3" />
              <h3 className="font-bold text-sm text-slate-700 dark:text-slate-300">Tính năng đang phát triển</h3>
              <p className="text-[11px] text-slate-400 max-w-xs mx-auto mt-1">
                Giao diện quản lý "{navigationItems.find(n => n.id === activeTab)?.name}" hiện đang được phát triển.
              </p>
            </div>
          )}
        </section>
      </main>
    </div>
  );
}

export default function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <CostDashboardShell />
    </QueryClientProvider>
  );
}
