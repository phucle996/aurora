import SystemArchitecture from '../components/home/SystemArchitecture.jsx'

export default function Home() {
  return (
    <div className="max-w-6xl mx-auto px-8 py-10 space-y-12">
      {/* Hero */}
      <header className="bg-gradient-to-br from-indigo-100 via-white to-pink-100 dark:from-indigo-900/40 dark:via-slate-900 dark:to-pink-900/30 border border-indigo-200 dark:border-indigo-800/50 rounded-2xl p-10">
        <div className="flex flex-wrap items-center gap-2 text-xs mb-5">
          <span className="px-2.5 py-1 bg-indigo-100 dark:bg-indigo-500/20 text-indigo-700 dark:text-indigo-300 rounded-full border border-indigo-300 dark:border-indigo-500/30">Platform</span>
          <span className="px-2.5 py-1 bg-emerald-100 dark:bg-emerald-500/20 text-emerald-700 dark:text-emerald-300 rounded-full border border-emerald-300 dark:border-emerald-500/30">Cloud-native</span>
          <span className="px-2.5 py-1 bg-slate-100 dark:bg-slate-800 text-slate-700 dark:text-slate-300 rounded-full border border-slate-300 dark:border-slate-700">Go • PostgreSQL • Redis</span>
        </div>
        <h1 className="text-4xl md:text-5xl font-bold mb-4 bg-gradient-to-r from-indigo-700 via-purple-700 to-pink-700 dark:from-indigo-300 dark:via-purple-300 dark:to-pink-300 bg-clip-text text-transparent">
          Aurora Platform
        </h1>
        <p className="text-lg md:text-xl text-slate-700 dark:text-slate-200 mb-3 max-w-3xl">
          Hệ điều phối hạ tầng theo mô hình <strong>controlplane / dataplane / agent</strong> —
          cung cấp authn/authz, IAM, topology, billing, task pipeline cho các tenant chạy trên
          nhiều runtime hỗn hợp.
        </p>
        <p className="text-sm text-slate-500 dark:text-slate-400 max-w-3xl">
          Mục tiêu: tách biệt rạch ròi quyền điều phối với workload thực thi, đảm bảo source of
          truth nằm ở PostgreSQL, và giữ Redis là kênh điều phối realtime an toàn cho HA.
        </p>
      </header>

      {/* Quick stats */}
      <section className="grid grid-cols-2 md:grid-cols-4 gap-3">
        {[
          { label: 'Source of Truth', value: 'PostgreSQL', icon: '🐘', accent: 'from-blue-500 to-cyan-500' },
          { label: 'Coordination', value: 'Redis Stream', icon: '⚡', accent: 'from-red-500 to-orange-500' },
          { label: 'Edge Proxy', value: 'Envoy', icon: '🚦', accent: 'from-purple-500 to-pink-500' },
          { label: 'Runtime', value: 'Go Service', icon: '🦫', accent: 'from-emerald-500 to-teal-500' },
        ].map((s) => (
          <div
            key={s.label}
            className={`bg-gradient-to-br ${s.accent} p-[1px] rounded-xl`}
          >
            <div className="bg-white dark:bg-slate-900 rounded-xl p-4 h-full">
              <div className="text-2xl mb-1">{s.icon}</div>
              <p className="text-[11px] uppercase tracking-wider text-slate-500">{s.label}</p>
              <p className="font-semibold text-slate-900 dark:text-slate-100">{s.value}</p>
            </div>
          </div>
        ))}
      </section>

      {/* Three planes */}
      <section>
        <h2 className="text-2xl font-bold mb-2 text-slate-900 dark:text-slate-100">
          Mô hình 3 mặt phẳng
        </h2>
        <p className="text-slate-600 dark:text-slate-400 mb-6">
          Aurora tách rõ ba lớp trách nhiệm — không lớp nào lấn sang lớp khác.
        </p>
        <div className="grid md:grid-cols-3 gap-4">
          {[
            {
              icon: '🧠',
              title: 'Controlplane',
              tag: 'Brain',
              gradient: 'from-indigo-500 to-purple-500',
              desc: 'Lớp điều phối trung tâm — nhận request, validate policy, ghi state chuẩn vào DB, phát lệnh xuống dataplane/agent.',
              points: [
                'HTTP / gRPC entrypoint',
                'IAM, billing, topology, task',
                'Source of truth: PostgreSQL',
                'Phát job qua Redis Stream',
              ],
            },
            {
              icon: '⚙️',
              title: 'Dataplane',
              tag: 'Workforce',
              gradient: 'from-blue-500 to-cyan-500',
              desc: 'Lớp xử lý runtime gần tài nguyên — tiêu thụ lệnh, thực hiện task, báo trạng thái ngược lại controlplane.',
              points: [
                'Consume Redis Stream',
                'Execute long-running workload',
                'Reconcile state với DB',
                'Không quyết định policy',
              ],
            },
            {
              icon: '🛰️',
              title: 'Agent',
              tag: 'Edge',
              gradient: 'from-emerald-500 to-teal-500',
              desc: 'Tiến trình edge gần host/node/VM — nhận lệnh đặc thù, thực hiện side effect cục bộ, gửi heartbeat & result.',
              points: [
                'Run on host/node/VM',
                'Side-effects cục bộ',
                'Heartbeat + result',
                'Không lưu state nghiệp vụ',
              ],
            },
          ].map((p) => (
            <div
              key={p.title}
              className={`bg-gradient-to-br ${p.gradient} p-[1px] rounded-xl`}
            >
              <div className="bg-white dark:bg-slate-900 rounded-xl p-6 h-full">
                <div className="flex items-center justify-between mb-3">
                  <div className="flex items-center gap-2">
                    <span className="text-2xl">{p.icon}</span>
                    <h3 className="text-lg font-bold text-slate-900 dark:text-slate-100">
                      {p.title}
                    </h3>
                  </div>
                  <span
                    className={`text-[10px] uppercase tracking-wider px-2 py-0.5 rounded-full bg-gradient-to-br ${p.gradient} text-white font-semibold`}
                  >
                    {p.tag}
                  </span>
                </div>
                <p className="text-sm text-slate-600 dark:text-slate-300 mb-3">{p.desc}</p>
                <ul className="space-y-1 text-xs text-slate-500 dark:text-slate-400">
                  {p.points.map((point) => (
                    <li key={point} className="flex items-start gap-1.5">
                      <span className="text-emerald-500">▸</span>
                      <span>{point}</span>
                    </li>
                  ))}
                </ul>
              </div>
            </div>
          ))}
        </div>
      </section>

      {/* System architecture */}
      <section>
        <h2 className="text-2xl font-bold mb-2 text-slate-900 dark:text-slate-100">
          System Architecture
        </h2>
        <p className="text-slate-600 dark:text-slate-400 mb-6">
          Luồng dữ liệu giữa client, edge proxy, controlplane cluster, dataplane workers,
          agents, và các infrastructure store.
        </p>
        <SystemArchitecture />
      </section>

      {/* Request flow */}
      <section>
        <h2 className="text-2xl font-bold mb-2 text-slate-900 dark:text-slate-100">
          Luồng request chính
        </h2>
        <p className="text-slate-600 dark:text-slate-400 mb-6">
          Mỗi request HTTP/gRPC đi qua các tầng theo thứ tự cố định để đảm bảo policy & audit.
        </p>
        <ol className="space-y-2">
          {[
            { step: 'Client', detail: 'Admin UI, Cloud UI, API client gọi HTTP/gRPC' },
            { step: 'Envoy edge', detail: 'TLS termination, vhost routing, mTLS giữa cluster' },
            { step: 'Middleware chain', detail: 'request ID → auth → access log → rate limit → origin/cookie guard' },
            { step: 'Handler', detail: 'Bind & validate request, gọi service' },
            { step: 'Service', detail: 'Business logic + policy decision, gọi repository nếu cần DB' },
            { step: 'Repository', detail: 'Nơi duy nhất phát SQL vào PostgreSQL' },
            { step: 'Async branch', detail: 'Service ghi state rồi enqueue job/event lên Redis Stream' },
            { step: 'Response', detail: 'Handler return — không rò internal error ra client' },
          ].map((row, i) => (
            <li
              key={row.step}
              className="flex items-start gap-4 p-4 bg-white dark:bg-slate-900/60 border border-slate-200 dark:border-slate-800 rounded-lg"
            >
              <span className="flex-shrink-0 w-7 h-7 rounded-full bg-gradient-to-br from-indigo-500 to-purple-500 text-white font-bold text-xs flex items-center justify-center">
                {i + 1}
              </span>
              <div className="flex-1">
                <p className="font-semibold text-slate-900 dark:text-slate-100">{row.step}</p>
                <p className="text-sm text-slate-600 dark:text-slate-400">{row.detail}</p>
              </div>
            </li>
          ))}
        </ol>
      </section>

      {/* Service modules */}
      <section>
        <h2 className="text-2xl font-bold mb-2 text-slate-900 dark:text-slate-100">
          Các module nghiệp vụ
        </h2>
        <p className="text-slate-600 dark:text-slate-400 mb-6">
          Controlplane chia theo module nghiệp vụ trong <code className="text-amber-600 dark:text-amber-400 bg-slate-100 dark:bg-slate-800 px-1.5 py-0.5 rounded text-sm">internal/</code>.
        </p>
        <div className="grid grid-cols-2 md:grid-cols-3 lg:grid-cols-4 gap-3">
          {[
            { icon: '🔐', name: 'IAM', desc: 'Auth, RBAC, MFA, device' },
            { icon: '🏢', name: 'Tenant', desc: 'Multi-tenant boundary' },
            { icon: '⚙️', name: 'Core', desc: 'Shared kernel types' },
            { icon: '📜', name: 'Policy Engine', desc: 'Rule evaluation' },
            { icon: '🛡️', name: 'Security', desc: 'Crypto, rate limit' },
            { icon: '👁️', name: 'Observability', desc: 'Tracing, metrics' },
            { icon: '📧', name: 'Mail', desc: 'SMTP, notifications' },
            { icon: '🖥️', name: 'Hypervisor', desc: 'VM lifecycle' },
          ].map((m) => (
            <div
              key={m.name}
              className="bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-lg p-4 hover:border-indigo-400 dark:hover:border-indigo-500 transition-colors"
            >
              <div className="text-2xl mb-2">{m.icon}</div>
              <p className="font-semibold text-slate-900 dark:text-slate-100">{m.name}</p>
              <p className="text-xs text-slate-500 dark:text-slate-400 mt-1">{m.desc}</p>
            </div>
          ))}
        </div>
      </section>

      {/* Design principles */}
      <section>
        <h2 className="text-2xl font-bold mb-2 text-slate-900 dark:text-slate-100">
          Nguyên tắc thiết kế
        </h2>
        <p className="text-slate-600 dark:text-slate-400 mb-6">
          Những ràng buộc kiến trúc giữ codebase lành mạnh khi scale.
        </p>
        <div className="grid md:grid-cols-2 gap-3">
          {[
            { title: 'Strict layers', desc: 'Handler → Service → Repository → DB. Không skip, không trộn. SQL chỉ được phép xuất hiện trong repository.' },
            { title: 'PostgreSQL as SoT', desc: 'Mọi business state bền vững nằm ở PostgreSQL. Redis chỉ phục vụ queue/cache/coordination/transient state.' },
            { title: 'Module-local errors', desc: 'Mỗi module có errorx riêng. Không leak internal error ra client; handler dịch về HTTP code chuẩn.' },
            { title: 'No raw secrets', desc: 'Không lưu raw secret/token/key trong DB. Plaintext chỉ tồn tại trong RAM tạm thời.' },
            { title: 'Replay-safe events', desc: 'Mất Redis tạm thời không hỏng state. PostgreSQL là nguồn replay/reconcile chuẩn.' },
            { title: 'Generic errors', desc: 'Authentication errors phải generic — không leak thông tin tồn tại / không tồn tại của user.' },
          ].map((p) => (
            <div
              key={p.title}
              className="bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-lg p-4"
            >
              <p className="font-semibold text-slate-900 dark:text-slate-100 mb-1">{p.title}</p>
              <p className="text-sm text-slate-600 dark:text-slate-400">{p.desc}</p>
            </div>
          ))}
        </div>
      </section>

      <footer className="border-t border-slate-200 dark:border-slate-800 pt-6 text-center text-sm text-slate-500">
        <p>Aurora Platform • runbook.aurora.local</p>
        <p className="mt-1">Cloud-native infrastructure orchestration</p>
      </footer>
    </div>
  )
}
