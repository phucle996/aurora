import { Receipt, Plus, Download } from "lucide-react";
import { WalletOnboarding } from "../onboarding/WalletOnboarding";

interface DashboardPageProps {
  currency: string;
  admin: boolean;
  personal: boolean;
}

export default function DashboardPage({ currency, admin, personal }: DashboardPageProps) {
  if (!admin) {
    return <WalletOnboarding personal={personal} />;
  }

  return (
    <div className="space-y-8 text-xs select-none">
      {/* Title block */}
      <div className="flex justify-between items-center select-none">
        <div>
          <h2 className="text-xl font-bold text-slate-900 dark:text-white">Dashboard</h2>
          <p className="text-xs text-slate-500 font-medium">Đối soát và quản lý tài chính thời gian thực</p>
        </div>
        <button className="flex items-center gap-2 bg-blue-600 hover:bg-blue-700 text-white font-bold text-xs px-4 py-2 rounded-lg shadow-sm transition-colors cursor-pointer">
          <Download size={14} />
          Xuất báo cáo đối soát
        </button>
      </div>

      {/* Cards Grid */}
      <div className="grid grid-cols-1 md:grid-cols-3 gap-6">
        <div className="bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 p-6 rounded-lg flex flex-col justify-between">
          <div className="flex justify-between items-start">
            <span className="text-xs font-semibold text-slate-500">Doanh Thu Tháng Này</span>
            <span className="bg-emerald-50 dark:bg-emerald-950/40 text-emerald-600 dark:text-emerald-400 text-[10px] font-bold px-2 py-0.5 rounded-full">+12.4%</span>
          </div>
          <div className="mt-4">
            <h3 className="text-2xl font-bold text-slate-900 dark:text-white">
              {currency === 'VND' ? '124,500,000 ₫' : '$5,187.50'}
            </h3>
            <p className="text-[10px] text-slate-400 font-medium mt-1">So với tháng trước</p>
          </div>
        </div>

        <div className="bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 p-6 rounded-lg flex flex-col justify-between">
          <div className="flex justify-between items-start">
            <span className="text-xs font-semibold text-slate-500">Chi Phí Hệ Thống Thực Tế</span>
            <span className="bg-rose-50 dark:bg-rose-950/40 text-rose-600 dark:text-rose-400 text-[10px] font-bold px-2 py-0.5 rounded-full">+8.1%</span>
          </div>
          <div className="mt-4">
            <h3 className="text-2xl font-bold text-slate-900 dark:text-white">
              {currency === 'VND' ? '45,210,000 ₫' : '$1,883.75'}
            </h3>
            <p className="text-[10px] text-slate-400 font-medium mt-1">Lượng điện, băng thông & khấu hao</p>
          </div>
        </div>

        <div className="bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 p-6 rounded-lg flex flex-col justify-between">
          <div className="flex justify-between items-start">
            <span className="text-xs font-semibold text-slate-500">Tỷ Lệ Lợi Nhuận Gộp</span>
            <span className="bg-blue-50 dark:bg-blue-950/40 text-blue-600 dark:text-blue-400 text-[10px] font-bold px-2 py-0.5 rounded-full">Ổn định</span>
          </div>
          <div className="mt-4">
            <h3 className="text-2xl font-bold text-slate-900 dark:text-white">63.68%</h3>
            <p className="text-[10px] text-slate-400 font-medium mt-1">Biên lợi nhuận gộp hệ thống</p>
          </div>
        </div>
      </div>

      {/* Table Placeholder */}
      <div className="bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-lg overflow-hidden">
        <div className="px-6 py-5 border-b border-slate-200 dark:border-slate-800 flex justify-between items-center">
          <div>
            <h4 className="font-bold text-sm text-slate-900 dark:text-white">Danh sách hóa đơn phát sinh gần đây</h4>
            <p className="text-[10px] text-slate-500 font-medium mt-0.5">Dữ liệu hóa đơn trực tiếp từ dịch vụ cost-manager</p>
          </div>
          <button className="flex items-center gap-1 text-xs font-bold text-blue-600 dark:text-blue-400 cursor-pointer">
            <Plus size={14} />
            Tạo hóa đơn thủ công
          </button>
        </div>

        <div className="p-6 text-center space-y-4">
          <div className="w-12 h-12 rounded-full bg-slate-50 dark:bg-slate-800/40 text-slate-400 flex items-center justify-center mx-auto">
            <Receipt size={22} />
          </div>
          <div>
            <h5 className="font-bold text-xs text-slate-800 dark:text-slate-200">Chưa có giao dịch nào được tải</h5>
            <p className="text-[10px] text-slate-500 max-w-sm mx-auto mt-1">Đang chờ API Gateway kết nối dịch vụ cost-manager để truy vấn hóa đơn thực tế.</p>
          </div>
        </div>
      </div>
    </div>
  );
}
