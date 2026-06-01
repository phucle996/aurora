import { useState } from 'react'
import {
  User,
  ShieldAlert,
  Key,
  Cpu,
  Server,
  Database,
  CheckCircle2,
  AlertTriangle,
  Zap,
  ArrowRight,
  ArrowDown,
  FileText,
  RefreshCw,
  Check,
  Lock,
  Activity,
  Workflow
} from 'lucide-react'

// ─── 1. SEQUENCE FLOW COMPONENT ───
export function VisualSequenceFlow() {
  const [activeStep, setActiveStep] = useState(1)

  const steps = [
    {
      id: 1,
      title: 'Client Request',
      desc: 'Yêu cầu gửi từ Client',
      detail: 'Request thô mang thông tin IP nguồn và đích đến API.',
      icon: User,
      color: 'from-blue-500 to-indigo-500',
      glow: 'shadow-blue-500/10'
    },
    {
      id: 2,
      title: 'Pre-Auth Shield',
      desc: 'Bộ lọc IP Biên mạng',
      detail: 'Kiểm tra IP Quota nguyên tử bằng Lua Script trên Redis Cluster. Chặn đứng DDoS ở biên mạng.',
      icon: ShieldAlert,
      color: 'from-indigo-500 to-purple-500',
      glow: 'shadow-indigo-500/10',
      connectsToRedis: true
    },
    {
      id: 3,
      title: 'JWT Auth Resolving',
      desc: 'Giải mã & Xác thực',
      detail: 'Xác minh chữ ký mã hóa JWT. Trích xuất User ID & Device ID cho các bước tiếp theo.',
      icon: Key,
      color: 'from-purple-500 to-pink-500',
      glow: 'shadow-purple-500/10'
    },
    {
      id: 4,
      title: 'Post-Auth Engine',
      desc: 'Match Quota Động',
      detail: 'Đối khớp dynamic route O(1) trong RAM, tính quota riêng theo cặp IP + Device ID hoặc User ID.',
      icon: Cpu,
      color: 'from-pink-500 to-rose-500',
      glow: 'shadow-pink-500/10',
      connectsToRedis: true
    },
    {
      id: 5,
      title: 'Backend Handler',
      desc: 'Xử lý Nghiệp vụ',
      detail: 'Yêu cầu an toàn đi vào tầng xử lý logic cốt lõi của hệ thống.',
      icon: Server,
      color: 'from-emerald-500 to-teal-500',
      glow: 'shadow-emerald-500/10'
    }
  ]

  return (
    <div className="space-y-6">
      {/* Container chính của luồng */}
      <div className="relative grid grid-cols-1 lg:grid-cols-5 gap-6 lg:gap-6 items-center bg-slate-50/50 dark:bg-slate-900/20 p-6 rounded-2xl border border-slate-200/80 dark:border-slate-800/80">
        
        {/* Bản đồ kết nối */}
        {steps.map((step, idx) => {
          const IconComponent = step.icon
          const isActive = activeStep === step.id

          return (
            <div key={step.id} className="relative flex flex-col items-center">
              {/* Card Node */}
              <button
                onClick={() => setActiveStep(activeStep === step.id ? null : step.id)}
                className={`relative w-full text-left p-5 rounded-xl border transition-all duration-300 ${
                  isActive
                    ? `bg-white dark:bg-slate-900 border-indigo-500 scale-105 shadow-xl ${step.glow}`
                    : 'bg-white/80 dark:bg-slate-900/60 border-slate-200 dark:border-slate-800 hover:border-slate-400 dark:hover:border-slate-700 hover:scale-102 shadow-sm'
                }`}
              >
                {/* Đầu icon nổi */}
                <div className={`absolute -top-3 -right-3 w-8 h-8 rounded-lg bg-gradient-to-br ${step.color} text-white flex items-center justify-center shadow-md`}>
                  <IconComponent className="w-4 h-4" />
                </div>

                <div className="pr-4">
                  <span className="text-[10px] uppercase font-bold tracking-wider text-slate-400 dark:text-slate-500">
                    Bước 0{step.id}
                  </span>
                  <h4 className="font-bold text-sm text-slate-800 dark:text-slate-200 mt-0.5">
                    {step.title}
                  </h4>
                  <p className="text-xs text-slate-500 dark:text-slate-400 mt-1">
                    {step.desc}
                  </p>
                </div>

                {/* Kết nối Redis Indicator */}
                {step.connectsToRedis && (
                  <div className="mt-2.5 inline-flex items-center gap-1 px-1.5 py-0.5 rounded bg-amber-50 dark:bg-amber-950/30 text-[9px] font-bold text-amber-600 dark:text-amber-400 border border-amber-200/40">
                    <Database className="w-2.5 h-2.5 animate-pulse" />
                    Redis Sync
                  </div>
                )}
              </button>

              {/* Mũi tên liên kết */}
              {idx < steps.length - 1 && (
                <>
                  {/* Mũi tên cho màn hình lớn (ngang) */}
                  <div className="hidden lg:block absolute top-1/2 -right-5 -translate-y-1/2 z-10 text-slate-300 dark:text-slate-700 animate-pulse">
                    <ArrowRight className="w-5 h-5" />
                  </div>
                  {/* Mũi tên cho màn hình nhỏ (dọc) */}
                  <div className="block lg:hidden my-2 text-slate-300 dark:text-slate-700">
                    <ArrowDown className="w-5 h-5" />
                  </div>
                </>
              )}
            </div>
          )
        })}
      </div>

      {/* Trung tâm lưu trữ Redis ở dưới dạng Node tách biệt tạo khối nổi 3D */}
      <div className="grid grid-cols-1 lg:grid-cols-3 gap-6 items-center bg-gradient-to-br from-indigo-50/40 via-purple-50/20 to-transparent dark:from-indigo-950/10 dark:via-purple-950/5 dark:to-transparent p-6 rounded-2xl border border-dashed border-indigo-200 dark:border-indigo-800/40">
        <div className="lg:col-span-2 flex items-start gap-4">
          <div className="flex-shrink-0 w-12 h-12 rounded-xl bg-gradient-to-br from-amber-500 to-orange-500 text-white flex items-center justify-center shadow-lg shadow-orange-500/20 animate-bounce">
            <Database className="w-6 h-6" />
          </div>
          <div>
            <h5 className="font-bold text-base text-slate-800 dark:text-slate-200">
              Trọng Tâm Dữ Liệu: Redis Cluster (High Availability)
            </h5>
            <p className="text-sm text-slate-500 dark:text-slate-400 mt-1 leading-relaxed">
              Cả Pre-Auth và Post-Auth đều liên kết thời gian thực thông qua Lua Script để đảm bảo nguyên tử, chống race condition và đồng bộ hóa phi trạng thái giữa hàng nghìn nodes.
            </p>
          </div>
        </div>
        <div className="lg:col-span-1 w-full">
          {activeStep ? (
            <div className="p-4 bg-white dark:bg-slate-900 border border-indigo-200 dark:border-indigo-800 rounded-xl text-sm animate-fadeIn shadow-md">
              <div className="font-bold text-indigo-600 dark:text-indigo-400 mb-1">
                💡 Chi tiết Bước {activeStep}:
              </div>
              <p className="text-slate-600 dark:text-slate-300 leading-relaxed">
                {steps.find((s) => s.id === activeStep).detail}
              </p>
            </div>
          ) : (
            <div className="p-4 bg-indigo-50/50 dark:bg-indigo-950/20 border border-dashed border-indigo-200 dark:border-indigo-800/40 rounded-xl text-sm text-indigo-600 dark:text-indigo-400 animate-pulse text-center">
              👉 Nhấp vào bất kỳ bước nào ở trên để xem chi tiết
            </div>
          )}
        </div>
      </div>
    </div>
  )
}

// ─── 2. STATE MACHINE COMPONENT ───
export function VisualStateMachine() {
  const [activeState, setActiveState] = useState('allow')

  const states = [
    {
      id: 'allow',
      title: '🟢 1. ALLOW STATE (Trong Hạn Ngạch)',
      desc: 'Hạn ngạch: Cấu hình động qua Policy Engine',
      detail: 'Mọi request nằm dưới hạn ngạch do Policy Engine biên dịch đều được cho qua tức thời. Khi các hình phạt cách ly/block hết thời gian TTL, client tự động quay trở lại ALLOW.',
      color: 'border-emerald-200 dark:border-emerald-900 bg-emerald-50/20 dark:bg-emerald-950/10 text-emerald-600 dark:text-emerald-400',
      glow: 'shadow-emerald-500/10',
      icon: CheckCircle2
    },
    {
      id: 'throttle',
      title: '🟡 2. THROTTLE STATE (Phạt 2s)',
      desc: 'Phạt delay 2 giây & lưu marker vi phạm vào cửa sổ 10 phút',
      detail: 'Khi vượt quá capacity, trả về HTTP 429 và phạt delay 2 giây. Hết 2 giây delay, client tự động quay trở lại ALLOW (nếu không vướng cách ly/block).',
      color: 'border-amber-200 dark:border-amber-900 bg-amber-50/20 dark:bg-amber-950/10 text-amber-600 dark:text-amber-400',
      glow: 'shadow-amber-500/10',
      icon: AlertTriangle
    },
    {
      id: 'blocked',
      title: '🔴 3. ISOLATION & BLOCK STATE (Cách Ly RAM)',
      desc: 'Vi phạm dưới 3 lần: cách ly 60s | Từ 3 lần trở lên: block 15 phút',
      detail: 'Node cách ly trực tiếp trên Local Cache (RAM). Vi phạm dưới 3 lần: phạt cách ly (Isolation) 60 giây. Vi phạm >= 3 lần: nâng mức phạt lên Block 15 phút. Hết TTL hình phạt -> Tự động phục hồi về ALLOW.',
      color: 'border-rose-200 dark:border-rose-900 bg-rose-50/20 dark:bg-rose-950/10 text-rose-600 dark:text-rose-400',
      glow: 'shadow-rose-500/10',
      icon: Zap
    }
  ]

  return (
    <div className="space-y-6">
      {/* 3 cards nối tiếp */}
      <div className="grid grid-cols-1 md:grid-cols-3 gap-6">
        {states.map((state) => {
          const IconComponent = state.icon
          const isSelected = activeState === state.id

          return (
            <div
              key={state.id}
              onClick={() => setActiveState(activeState === state.id ? null : state.id)}
              className={`relative p-5 rounded-2xl border-2 transition-all duration-300 cursor-pointer ${state.color} ${
                isSelected ? `scale-105 shadow-xl ${state.glow}` : 'shadow-sm hover:scale-102 hover:border-current/40'
              }`}
            >
              <div className="flex items-center gap-3 mb-3">
                <div className={`p-2 rounded-xl bg-white dark:bg-slate-900 shadow-sm`}>
                  <IconComponent className="w-5 h-5" />
                </div>
                <h4 className="font-extrabold text-sm tracking-wide">
                  {state.title}
                </h4>
              </div>
              <p className="font-bold text-xs text-slate-700 dark:text-slate-300">
                {state.desc}
              </p>
              <p className="text-xs text-slate-500 dark:text-slate-400 mt-2 leading-relaxed">
                {state.detail}
              </p>
            </div>
          )
        })}
      </div>

      {/* SVG Connecting Flow Diagram for 3D state transition */}
      <div className="relative hidden md:block bg-slate-950/90 border border-slate-800 p-6 rounded-2xl shadow-inner text-slate-300">
        <p className="text-[10px] uppercase font-extrabold tracking-wider text-slate-500 mb-4">
          Sơ đồ chuyển trạng thái tương tác vật lý (Interactive State Transition - Click để chọn trạng thái)
        </p>
        
        <svg viewBox="0 0 800 200" className="w-full h-auto overflow-visible select-none">
          <defs>
            <marker id="arrow-yellow" viewBox="0 0 10 10" refX="6" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse">
              <path d="M 0 1 L 10 5 L 0 9 z" fill="#f59e0b" />
            </marker>
            <marker id="arrow-red" viewBox="0 0 10 10" refX="6" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse">
              <path d="M 0 1 L 10 5 L 0 9 z" fill="#f43f5e" />
            </marker>
            <marker id="arrow-green" viewBox="0 0 10 10" refX="6" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse">
              <path d="M 0 1 L 10 5 L 0 9 z" fill="#10b981" />
            </marker>
            <filter id="glow-emerald" x="-20%" y="-20%" width="140%" height="140%">
              <feGaussianBlur in="SourceGraphic" stdDeviation="6" />
            </filter>
            <filter id="glow-amber" x="-20%" y="-20%" width="140%" height="140%">
              <feGaussianBlur in="SourceGraphic" stdDeviation="6" />
            </filter>
            <filter id="glow-rose" x="-20%" y="-20%" width="140%" height="140%">
              <feGaussianBlur in="SourceGraphic" stdDeviation="6" />
            </filter>
          </defs>

          {/* ALLOW -> THROTTLE (Forward Path) */}
          <path
            d="M 160 50 L 350 50"
            stroke={activeState === 'throttle' ? '#f59e0b' : '#334155'}
            strokeWidth={activeState === 'throttle' ? '3' : '2'}
            fill="none"
            markerEnd="url(#arrow-yellow)"
            className="transition-all duration-300"
          />
          <text x="255" y="38" textAnchor="middle" className="text-[10px] font-extrabold fill-slate-400">
            Vượt Quota
          </text>

          {/* THROTTLE -> BLOCK (Forward Path) */}
          <path
            d="M 440 50 L 635 50"
            stroke={activeState === 'blocked' ? '#f43f5e' : '#334155'}
            strokeWidth={activeState === 'blocked' ? '3' : '2'}
            fill="none"
            markerEnd="url(#arrow-red)"
            className="transition-all duration-300"
          />
          <text x="535" y="38" textAnchor="middle" className="text-[10px] font-extrabold fill-slate-400">
            Tái diễn ≥ 3 lần / 10 phút
          </text>

          {/* THROTTLE -> ALLOW (Loop Back - Decay) */}
          <path
            d="M 385 75 Q 260 120 135 75"
            stroke="#10b981"
            strokeWidth="1.5"
            strokeDasharray="4 4"
            fill="none"
            markerEnd="url(#arrow-green)"
            className={`transition-all duration-300 ${activeState === 'allow' ? 'opacity-100 stroke-[2.5px]' : 'opacity-70'}`}
          />
          <text x="260" y="115" textAnchor="middle" className="text-[9px] font-extrabold fill-emerald-400">
            Hết phạt 2s → Tự động về ALLOW
          </text>

          {/* BLOCK -> ALLOW (Loop Back - Decay) */}
          <path
            d="M 680 95 Q 400 185 120 95"
            stroke="#10b981"
            strokeWidth="1.5"
            strokeDasharray="4 4"
            fill="none"
            markerEnd="url(#arrow-green)"
            className={`transition-all duration-300 ${activeState === 'allow' ? 'opacity-100 stroke-[2.5px]' : 'opacity-70'}`}
          />
          <text x="400" y="165" textAnchor="middle" className="text-[9px] font-extrabold fill-emerald-400">
            Hết cách ly 60s / Block 15m → Tự động về ALLOW
          </text>

          {/* Node 1: ALLOW */}
          <g
            className="cursor-pointer group"
            onClick={() => setActiveState('allow')}
          >
            <circle
              cx="120"
              cy="60"
              r="35"
              fill="#064e3b"
              stroke="#10b981"
              strokeWidth={activeState === 'allow' ? '4' : '2'}
              filter={activeState === 'allow' ? 'url(#glow-emerald)' : ''}
              className="transition-all duration-300 group-hover:stroke-emerald-400"
            />
            <text x="120" y="55" textAnchor="middle" className="text-[11px] font-extrabold fill-emerald-300">
              ALLOW
            </text>
            <text x="120" y="72" textAnchor="middle" className="text-[9px] font-bold fill-emerald-400">
              (Thực thi)
            </text>
          </g>

          {/* Node 2: THROTTLE */}
          <g
            className="cursor-pointer group"
            onClick={() => setActiveState('throttle')}
          >
            <circle
              cx="400"
              cy="60"
              r="35"
              fill="#78350f"
              stroke="#f59e0b"
              strokeWidth={activeState === 'throttle' ? '4' : '2'}
              filter={activeState === 'throttle' ? 'url(#glow-amber)' : ''}
              className="transition-all duration-300 group-hover:stroke-amber-400"
            />
            <text x="400" y="55" textAnchor="middle" className="text-[11px] font-extrabold fill-amber-300">
              THROTTLE
            </text>
            <text x="400" y="72" textAnchor="middle" className="text-[9px] font-bold fill-amber-400">
              (Phạt 2s)
            </text>
          </g>

          {/* Node 3: BLOCK */}
          <g
            className="cursor-pointer group"
            onClick={() => setActiveState('blocked')}
          >
            <circle
              cx="680"
              cy="60"
              r="35"
              fill="#4c0519"
              stroke="#f43f5e"
              strokeWidth={activeState === 'blocked' ? '4' : '2'}
              filter={activeState === 'blocked' ? 'url(#glow-rose)' : ''}
              className="transition-all duration-300 group-hover:stroke-rose-400"
            />
            <text x="680" y="55" textAnchor="middle" className="text-[11px] font-extrabold fill-rose-300">
              ISOLATED
            </text>
            <text x="680" y="72" textAnchor="middle" className="text-[9px] font-bold fill-rose-400">
              (RAM Cache)
            </text>
          </g>
        </svg>
      </div>
    </div>
  )
}

// ─── 3. HOT-RELOAD FLOW COMPONENT ───
export function VisualHotReloadFlow() {
  const [hoveredIdx, setHoveredIdx] = useState(null)

  const nodes = [
    {
      title: 'SRE YAML Config',
      desc: 'Thay đổi chính sách trên YAML',
      icon: FileText,
      color: 'bg-blue-500 shadow-blue-500/20'
    },
    {
      title: 'Policy Engine',
      desc: 'Tải và phân tách cấu trúc',
      icon: RefreshCw,
      color: 'bg-indigo-500 shadow-indigo-500/20'
    },
    {
      title: 'Compile & Validate',
      desc: 'Kiểm thử logic & Map hóa',
      icon: Check,
      color: 'bg-purple-500 shadow-purple-500/20'
    },
    {
      title: 'Trigger Hooks',
      desc: 'Kích hoạt callback nội bộ',
      icon: Workflow,
      color: 'bg-pink-500 shadow-pink-500/20'
    },
    {
      title: 'Atomic Swap',
      desc: 'Hoán đổi nguyên tử (Mutex)',
      icon: Lock,
      color: 'bg-rose-500 shadow-rose-500/20'
    },
    {
      title: 'Live Middleware',
      desc: 'Áp dụng tức thì cho Requests',
      icon: Activity,
      color: 'bg-emerald-500 shadow-emerald-500/20'
    }
  ]

  return (
    <div className="bg-slate-950 dark:bg-slate-900/60 rounded-2xl p-6 border border-slate-800 shadow-xl overflow-hidden">
      <div className="relative flex flex-col md:flex-row justify-between items-center gap-6 md:gap-2">
        {nodes.map((node, idx) => {
          const Icon = node.icon
          const isHovered = hoveredIdx === idx

          return (
            <div
              key={idx}
              onMouseEnter={() => setHoveredIdx(idx)}
              onMouseLeave={() => setHoveredIdx(null)}
              className="flex-1 w-full md:w-auto relative flex flex-col items-center text-center cursor-default"
            >
              {/* Icon nổi với hiệu ứng bóng mờ đẹp mắt */}
              <div
                className={`w-12 h-12 rounded-xl text-white flex items-center justify-center transition-all duration-300 ${node.color} ${
                  isHovered ? 'scale-120 rotate-6 shadow-2xl' : 'scale-100 shadow-md'
                }`}
              >
                <Icon className="w-5 h-5" />
              </div>

              {/* Nhãn văn bản */}
              <h5 className="font-bold text-xs text-slate-200 mt-3 select-none">
                {node.title}
              </h5>
              <p className="text-[10px] text-slate-500 dark:text-slate-400 mt-1 max-w-[120px] select-none">
                {node.desc}
              </p>

              {/* Đường liên kết ngang */}
              {idx < nodes.length - 1 && (
                <>
                  <div className="hidden md:block absolute top-6 -right-[40%] w-[80%] h-0.5 bg-gradient-to-r from-slate-700 to-slate-800 -z-0">
                    {isHovered && (
                      <div className="absolute top-1/2 -translate-y-1/2 w-2 h-2 rounded-full bg-indigo-400 animate-ping" />
                    )}
                  </div>
                  <div className="block md:hidden my-2 text-slate-700">
                    <ArrowDown className="w-4 h-4 animate-bounce" />
                  </div>
                </>
              )}
            </div>
          )
        })}
      </div>

      {/* Box chú thích chi tiết khi hover */}
      <div className="mt-6 border-t border-slate-800/80 pt-4 flex items-center justify-center text-center">
        {hoveredIdx !== null ? (
          <p className="text-xs text-indigo-300 animate-fadeIn">
            💡 <strong>Quy trình bảo mật:</strong> Trong suốt pipeline này, chính sách cũ (LKG - Last Known Good) luôn duy trì để xử lý requests của khách hàng. Chỉ khi <strong>Atomic Swap</strong> hoàn thành tuyệt đối an toàn qua RWMutex, cấu hình mới mới đi vào hoạt động.
          </p>
        ) : (
          <p className="text-xs text-slate-500 italic select-none">
            Rê chuột vào từng bước của pipeline để xem chi tiết luồng xử lý hot-reload không gián đoạn
          </p>
        )}
      </div>
    </div>
  )
}
