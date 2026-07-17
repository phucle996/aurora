import React, { useState, useEffect } from 'react';
import { useNavigate } from 'react-router-dom';
import { useAuthStore } from '../../lib/store/useAuthStore';
import { User, Eye, EyeOff, ShieldAlert, Coins } from 'lucide-react';

export default function LoginPage() {
  const navigate = useNavigate();
  const { login, isAuthenticated, isLoading, error, clearError } = useAuthStore();

  const [employeeCode, setEmployeeCode] = useState('');
  const [secretKey, setSecretKey] = useState('');
  const [showSecretKey, setShowSecretKey] = useState(false);
  const [rememberMe, setRememberMe] = useState(true);
  const [validationError, setValidationError] = useState<string | null>(null);

  // Nếu người dùng đã đăng nhập trước đó, chuyển hướng trực tiếp vào Dashboard
  useEffect(() => {
    if (isAuthenticated) {
      navigate('/', { replace: true });
    }
  }, [isAuthenticated, navigate]);

  // Xóa lỗi cũ khi người dùng thay đổi input
  useEffect(() => {
    if (error) clearError();
    if (validationError) setValidationError(null);
  }, [employeeCode, secretKey]);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();

    // Client-side validation
    const trimmedEmployeeCode = employeeCode.trim();
    if (!trimmedEmployeeCode) {
      setValidationError('Vui lòng nhập mã nhân viên');
      return;
    }
    if (!secretKey) {
      setValidationError('Vui lòng nhập khóa bí mật');
      return;
    }

    const success = await login(trimmedEmployeeCode, secretKey);
    if (success) {
      navigate('/', { replace: true });
    }
  };

  return (
    <div className="min-h-screen grid grid-cols-1 lg:grid-cols-2 select-none bg-white dark:bg-[#0b1329]">
      {/* Cột trái: Form Đăng nhập TailAdmin style */}
      <div className="flex flex-col justify-between p-8 sm:p-12 md:p-16 lg:p-20 bg-white dark:bg-[#0b1329] text-slate-800 dark:text-slate-100">

        {/* Form area */}
        <div className="w-full max-w-md mx-auto my-auto py-10">
          <div className="mb-8">
            <h1 className="text-3xl font-bold text-slate-900 dark:text-white tracking-tight">
              Sign In
            </h1>
            <p className="text-sm text-slate-500 dark:text-slate-400 mt-2 font-normal">
              Enter your employee code and secret key to sign in!
            </p>
          </div>

          {/* Thông báo lỗi nếu có */}
          {(error || validationError) && (
            <div className="mb-6 p-4 rounded-xl bg-red-50 dark:bg-red-950/40 border border-red-200 dark:border-red-800/50 flex items-start space-x-3 text-red-600 dark:text-red-400">
              <ShieldAlert className="h-5 w-5 shrink-0 mt-0.5" />
              <span className="text-sm leading-relaxed font-medium">
                {validationError || error}
              </span>
            </div>
          )}

          <form onSubmit={handleSubmit} className="space-y-6">
            {/* Field: Mã nhân viên */}
            <div className="space-y-2">
              <label className="block text-sm font-medium text-slate-700 dark:text-slate-300">
                Mã nhân viên <span className="text-red-500">*</span>
              </label>
              <div className="relative">
                <input
                  type="text"
                  value={employeeCode}
                  onChange={(e) => setEmployeeCode(e.target.value)}
                  placeholder="Nhập mã nhân viên"
                  disabled={isLoading}
                  className="w-full px-4 py-3.5 rounded-xl border border-slate-200 dark:border-slate-800 bg-slate-50 dark:bg-[#131d38] text-slate-900 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 text-sm focus:outline-none focus:ring-2 focus:ring-blue-600 focus:bg-white dark:focus:bg-[#131d38] transition-all disabled:opacity-50"
                />
                <User className="absolute right-4 top-1/2 -translate-y-1/2 h-5 w-5 text-slate-400 pointer-events-none" />
              </div>
            </div>

            {/* Field: Khóa bí mật */}
            <div className="space-y-2">
              <label className="block text-sm font-medium text-slate-700 dark:text-slate-300">
                Khóa bí mật <span className="text-red-500">*</span>
              </label>
              <div className="relative">
                <input
                  type={showSecretKey ? 'text' : 'password'}
                  value={secretKey}
                  onChange={(e) => setSecretKey(e.target.value)}
                  placeholder="Nhập khóa bí mật"
                  disabled={isLoading}
                  className="w-full px-4 py-3.5 pr-12 rounded-xl border border-slate-200 dark:border-slate-800 bg-slate-50 dark:bg-[#131d38] text-slate-900 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 text-sm focus:outline-none focus:ring-2 focus:ring-blue-600 focus:bg-white dark:focus:bg-[#131d38] transition-all disabled:opacity-50"
                />
                <button
                  type="button"
                  onClick={() => setShowSecretKey(!showSecretKey)}
                  disabled={isLoading}
                  className="absolute right-4 top-1/2 -translate-y-1/2 text-slate-400 hover:text-slate-600 dark:hover:text-slate-200 transition-colors"
                >
                  {showSecretKey ? <EyeOff className="h-5 w-5" /> : <Eye className="h-5 w-5" />}
                </button>
              </div>
            </div>

            {/* Row: Remember me */}
            <div className="flex items-center justify-between pt-1">
              <label className="flex items-center gap-2.5 cursor-pointer text-sm text-slate-600 dark:text-slate-400 font-medium">
                <input
                  type="checkbox"
                  checked={rememberMe}
                  onChange={(e) => setRememberMe(e.target.checked)}
                  className="w-4 h-4 rounded border-slate-300 text-blue-600 focus:ring-blue-500"
                />
                <span>Keep me logged in</span>
              </label>
            </div>

            {/* Submit Button */}
            <button
              type="submit"
              disabled={isLoading}
              className="w-full py-3.5 px-4 bg-blue-600 hover:bg-blue-700 text-white font-semibold text-sm rounded-xl shadow-md hover:shadow-lg active:scale-[0.99] transition-all flex items-center justify-center space-x-2 disabled:opacity-60 disabled:pointer-events-none"
            >
              {isLoading ? (
                <>
                  <svg className="animate-spin -ml-1 mr-3 h-4 w-4 text-white" fill="none" viewBox="0 0 24 24">
                    <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4"></circle>
                    <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"></path>
                  </svg>
                  <span>Đang đăng nhập...</span>
                </>
              ) : (
                <span>Sign in</span>
              )}
            </button>
          </form>
        </div>

        {/* Footer info */}
        <div className="text-xs text-slate-400 dark:text-slate-600 text-center sm:text-left">
          &copy; {new Date().getFullYear()} Aurora Cloud. Enterprise Auditing Console.
        </div>
      </div>

      {/* Cột phải: Brand Hero Panel (Deep Indigo / Navy Blue background) */}
      <div className="hidden lg:flex flex-col items-center justify-center p-12 bg-[#1c2434] relative overflow-hidden">
        {/* Background ambient pattern / grid lines */}
        <div className="absolute inset-0 bg-[linear-gradient(to_right,#ffffff0a_1px,transparent_1px),linear-gradient(to_bottom,#ffffff0a_1px,transparent_1px)] bg-[size:24px_24px]"></div>
        <div className="absolute top-1/2 left-1/2 -translate-x-1/2 -translate-y-1/2 w-[500px] h-[500px] bg-blue-600/10 rounded-full blur-[120px] pointer-events-none"></div>

        {/* Hero Content Box */}
        <div className="relative z-10 text-center max-w-md">
          <div className="inline-flex items-center justify-center gap-3 px-6 py-4 rounded-2xl bg-blue-600/20 border border-blue-500/30 backdrop-blur-md mb-6 shadow-xl">
            <div className="w-10 h-10 rounded-xl bg-blue-600 flex items-center justify-center text-white shadow-lg">
              <Coins size={22} />
            </div>
            <span className="text-3xl font-black text-white tracking-tight">
              Cost Console
            </span>
          </div>

          <h3 className="text-xl font-bold text-slate-100 mb-2">
            Enterprise Auditing & Cost Management
          </h3>
          <p className="text-sm text-slate-400 leading-relaxed font-normal">
            Hệ thống kiểm toán chi phí thương mại và theo dõi định mức tài nguyên giải pháp Cloud Native.
          </p>
        </div>
      </div>
    </div>
  );
}
