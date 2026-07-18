import React, { useState, useEffect } from 'react';
import { useNavigate } from 'react-router-dom';
import { useAuthStore } from '../../lib/store/useAuthStore';
import { User, Eye, EyeOff, Coins, Terminal, CheckCircle2, Layers, Globe, BarChart3, ShieldCheck, ArrowDown } from 'lucide-react';
import { toast } from 'sonner';

export default function LoginPage() {
  const navigate = useNavigate();
  const { login, isAuthenticated, isLoading, error, clearError } = useAuthStore();

  const [employeeCode, setEmployeeCode] = useState('');
  const [secretKey, setSecretKey] = useState('');
  const [showSecretKey, setShowSecretKey] = useState(false);
  const [mounted, setMounted] = useState(false);

  useEffect(() => {
    // Kích hoạt fade-in animation khi component mount
    setMounted(true);
  }, []);

  useEffect(() => {
    if (isAuthenticated) navigate('/', { replace: true });
  }, [isAuthenticated, navigate]);

  useEffect(() => {
    if (error) {
      toast.error(error);
      clearError();
    }
  }, [error, clearError]);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    const trimmedCode = employeeCode.trim();
    if (!trimmedCode) { toast.warning('Vui lòng nhập mã nhân viên.'); return; }
    if (!secretKey) { toast.warning('Vui lòng nhập khóa bí mật.'); return; }
    const success = await login(trimmedCode, secretKey);
    if (success) navigate('/', { replace: true });
  };

  return (
    <div className="min-h-screen bg-[#080d14] text-[#e6edf3] flex items-center justify-center select-none overflow-hidden relative">

      {/* ── Radial glow ──────────────────────────────────────────── */}
      <div
        className="absolute inset-0 pointer-events-none"
        style={{
          background: 'radial-gradient(ellipse 80% 60% at 50% 50%, rgba(30,58,138,0.08) 0%, transparent 70%)',
        }}
      />

      {/* ── Grid dot pattern ────────────────────────────────────── */}
      <div
        className="absolute inset-0 pointer-events-none opacity-[0.03]"
        style={{
          backgroundImage:
            'linear-gradient(#58a6ff 1px, transparent 1px), linear-gradient(90deg, #58a6ff 1px, transparent 1px)',
          backgroundSize: '40px 40px',
        }}
      />

      {/* ── Content ─────────────────────────────────────────────── */}
      <div
        className="relative z-10 w-full max-w-5xl px-8 flex flex-col lg:flex-row items-stretch gap-0"
        style={{ opacity: mounted ? 1 : 0, transition: 'opacity 240ms ease' }}
      >

          {/* ── LEFT: Login Form ──────────────────────────────────────── */}
          <div className="w-full lg:w-[400px] flex-shrink-0 pr-0 lg:pr-14 flex flex-col justify-center">

            {/* Sub-label */}
            <div className="flex items-center gap-2 mb-4">
              <Terminal size={12} className="text-slate-600" />
              <span className="text-[11px] font-semibold text-slate-600 uppercase tracking-[0.12em]">
                Xác thực phiên làm việc
              </span>
            </div>

            {/* Title */}
            <h1 className="text-[32px] font-bold text-[#e6edf3] leading-tight tracking-tight mb-2">
              Đăng nhập<br />hệ thống
            </h1>
            <p className="text-[13px] text-slate-500 leading-relaxed mb-8">
              Nhập mã nhân viên và khóa bí mật để truy cập<br />Aurora Cost Console.
            </p>

            {/* ── Hairline divider ── */}
            <div className="border-t border-[#1c2333] mb-7" />

            {/* Form */}
            <form onSubmit={handleSubmit} className="flex flex-col gap-5" noValidate>

              {/* Mã nhân viên */}
              <div className="flex flex-col gap-1.5">
                <label htmlFor="login-employee-code" className="text-[11px] font-semibold text-slate-500 uppercase tracking-[0.1em]">
                  Mã nhân viên
                </label>
                <div className="relative group">
                  <input
                    id="login-employee-code"
                    type="text"
                    value={employeeCode}
                    onChange={(e) => setEmployeeCode(e.target.value)}
                    placeholder="VD: EMP-00123"
                    disabled={isLoading}
                    autoComplete="username"
                    autoFocus
                    className="
                      w-full h-9 px-3 pr-9 rounded-[5px]
                      border border-[#21262d]
                      bg-[#0d1117]
                      text-[13px] text-[#e6edf3] placeholder:text-[#3d444d]
                      shadow-[inset_0_1px_2px_rgba(0,0,0,0.3)]
                      hover:border-[#30363d]
                      focus:outline-none focus:border-[#388bfd] focus:ring-1 focus:ring-[#388bfd]/30
                      disabled:opacity-40 disabled:cursor-not-allowed
                      transition-all duration-150
                    "
                  />
                  <User size={13} className="absolute right-3 top-1/2 -translate-y-1/2 text-[#3d444d] pointer-events-none" />
                </div>
              </div>

              {/* Khóa bí mật */}
              <div className="flex flex-col gap-1.5">
                <label htmlFor="login-secret-key" className="text-[11px] font-semibold text-slate-500 uppercase tracking-[0.1em]">
                  Khóa bí mật
                </label>
                <div className="relative">
                  <input
                    id="login-secret-key"
                    type={showSecretKey ? 'text' : 'password'}
                    value={secretKey}
                    onChange={(e) => setSecretKey(e.target.value)}
                    placeholder="••••••••••••••••"
                    disabled={isLoading}
                    autoComplete="current-password"
                    className="
                      w-full h-9 px-3 pr-9 rounded-[5px]
                      border border-[#21262d]
                      bg-[#0d1117]
                      text-[13px] text-[#e6edf3] placeholder:text-[#3d444d]
                      shadow-[inset_0_1px_2px_rgba(0,0,0,0.3)]
                      hover:border-[#30363d]
                      focus:outline-none focus:border-[#388bfd] focus:ring-1 focus:ring-[#388bfd]/30
                      disabled:opacity-40 disabled:cursor-not-allowed
                      transition-all duration-150
                    "
                  />
                  <button
                    type="button"
                    tabIndex={-1}
                    onClick={() => setShowSecretKey(!showSecretKey)}
                    disabled={isLoading}
                    className="absolute right-3 top-1/2 -translate-y-1/2 text-[#3d444d] hover:text-slate-400 transition-colors duration-150 disabled:opacity-40"
                    aria-label={showSecretKey ? 'Ẩn khóa bí mật' : 'Hiện khóa bí mật'}
                  >
                    {showSecretKey ? <EyeOff size={13} /> : <Eye size={13} />}
                  </button>
                </div>
              </div>

              {/* Button */}
              <button
                id="login-submit-btn"
                type="submit"
                disabled={isLoading}
                className="
                  mt-1 h-9 w-full rounded-[5px]
                  bg-blue-600 hover:brightness-110
                  text-[13px] font-semibold text-white
                  flex items-center justify-center gap-2
                  shadow-sm hover:shadow-blue-900/30
                  disabled:opacity-40 disabled:cursor-not-allowed
                  active:scale-[0.99] active:shadow-none
                  transition-all duration-[140ms]
                  focus:outline-none focus:ring-2 focus:ring-[#388bfd]/50
                "
              >
                {isLoading ? (
                  <>
                    <svg className="animate-spin h-3.5 w-3.5 text-white/70" fill="none" viewBox="0 0 24 24">
                      <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
                      <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z" />
                    </svg>
                    <span>Đang xác thực...</span>
                  </>
                ) : (
                  <span>Đăng nhập</span>
                )}
              </button>
            </form>
          </div>

          {/* ── Vertical divider (lg+) ────────────────────────────────── */}
          <div className="hidden lg:flex flex-col items-center mx-0 px-0">
            <div className="w-px flex-1" style={{
              background: 'linear-gradient(to bottom, transparent 0%, #21262d 20%, #21262d 80%, transparent 100%)',
            }} />
          </div>

          {/* ── RIGHT: Info Panel ─────────────────────────────────────── */}
          <div className="flex-1 pl-0 lg:pl-14 flex flex-col justify-center py-2">

            {/* Product header */}
            <div className="flex items-center gap-3 mb-6">
              <div className="w-10 h-10 rounded-lg bg-[#0d1117] border border-[#21262d] flex items-center justify-center shadow-sm">
                <Coins size={18} className="text-blue-400" />
              </div>
              <div>
                <div className="text-[14px] font-semibold text-[#e6edf3]">Aurora Cost Console</div>
                <div className="text-[11px] text-slate-600 font-mono">Enterprise Auditing &amp; Cost Management</div>
              </div>
            </div>

            <h2 className="text-[22px] font-bold text-[#e6edf3] leading-snug mb-1.5">
              Kiểm toán chi phí &<br />quản lý tài nguyên Cloud
            </h2>
            <p className="text-[13px] text-slate-500 leading-relaxed mb-6">
              Hệ thống theo dõi định mức, kiểm toán giao dịch và quản lý gói cước cho hạ tầng Cloud Native đa vùng.
            </p>

            {/* ── Hairline ── */}
            <div className="border-t border-[#1c2333] mb-5" />

            {/* Capabilities */}
            <div className="text-[10px] font-semibold text-slate-600 uppercase tracking-[0.12em] mb-3">Capabilities</div>
            <div className="grid grid-cols-2 gap-x-6 gap-y-2.5 mb-6">
              {[
                { icon: BarChart3, label: 'Realtime Metering', sub: 'Per-resource usage' },
                { icon: Globe, label: 'Multi-Zone HA', sub: 'Zone-aware aggregation' },
                { icon: ShieldCheck, label: 'Audit Trail', sub: 'Immutable via ClickHouse' },
                { icon: Layers, label: 'Quota Enforcement', sub: 'Plan-based limits' },
                { icon: CheckCircle2, label: 'Billing Engine', sub: 'Automated invoicing' },
                { icon: Terminal, label: 'Internal API', sub: 'gRPC + REST' },
              ].map(({ icon: Icon, label, sub }) => (
                <div key={label} className="flex items-start gap-2.5">
                  <Icon size={13} className="text-blue-500 mt-0.5 flex-shrink-0" />
                  <div>
                    <div className="text-[12px] font-medium text-[#c9d1d9]">{label}</div>
                    <div className="text-[10px] text-slate-600 font-mono">{sub}</div>
                  </div>
                </div>
              ))}
            </div>

            {/* ── Hairline ── */}
            <div className="border-t border-[#1c2333] mb-5" />

            {/* Architecture flow */}
            <div className="text-[10px] font-semibold text-slate-600 uppercase tracking-[0.12em] mb-4">Architecture</div>
            <div className="flex flex-col gap-0">
              {[
                { label: 'Control Plane', color: 'text-slate-400' },
                { label: 'Metering Engine', color: 'text-blue-400' },
                { label: 'Billing Engine', color: 'text-blue-400' },
                { label: 'Invoice &amp; Report', color: 'text-blue-400' },
              ].map((item, idx, arr) => (
                <div key={item.label} className="flex flex-col items-start">
                  <div className={`text-[11px] font-medium ${item.color} font-mono`} dangerouslySetInnerHTML={{ __html: item.label }} />
                  {idx < arr.length - 1 && (
                    <div className="flex items-center ml-1.5 my-0.5 text-slate-700">
                      <ArrowDown size={10} />
                    </div>
                  )}
                </div>
              ))}
            </div>
          </div>
        </div>
      </div>
  );
}
