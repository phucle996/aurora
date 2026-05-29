// Visual system architecture: 5 horizontal layers, gradient borders, no external SVG lib.
//   Client tier  →  Edge tier  →  Controlplane tier  →  Coordination tier  →  Runtime tier  →  Storage tier

const Layer = ({ icon, name, role, children, accent, gradient }) => (
  <div className={`relative bg-gradient-to-br ${gradient} p-[1px] rounded-2xl`}>
    <div className="bg-white dark:bg-slate-900 rounded-2xl p-5">
      <div className="flex items-center gap-3 mb-4 pb-3 border-b border-slate-200 dark:border-slate-800">
        <span className="text-2xl">{icon}</span>
        <div>
          <p className="font-bold text-slate-900 dark:text-slate-100">{name}</p>
          <p className={`text-xs ${accent}`}>{role}</p>
        </div>
      </div>
      <div className="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-4 gap-2">
        {children}
      </div>
    </div>
  </div>
)

const Box = ({ icon, label, sub }) => (
  <div className="bg-slate-50 dark:bg-slate-800/60 border border-slate-200 dark:border-slate-700 rounded-lg px-3 py-2 text-center hover:border-slate-400 dark:hover:border-slate-500 transition-colors">
    <div className="text-lg leading-none mb-1">{icon}</div>
    <div className="font-semibold text-xs text-slate-900 dark:text-slate-100">{label}</div>
    {sub && (
      <div className="text-[10px] text-slate-500 dark:text-slate-400 mt-0.5 font-mono">
        {sub}
      </div>
    )}
  </div>
)

const Arrow = ({ label }) => (
  <div className="flex flex-col items-center py-1">
    <svg className="w-5 h-5 text-slate-400 dark:text-slate-600" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="2">
      <path strokeLinecap="round" strokeLinejoin="round" d="M19 14l-7 7m0 0l-7-7m7 7V3" />
    </svg>
    {label && (
      <span className="text-[10px] text-slate-500 dark:text-slate-400 font-mono">{label}</span>
    )}
  </div>
)

export default function SystemArchitecture() {
  return (
    <div className="space-y-2">
      {/* Client tier */}
      <Layer
        icon="👤"
        name="Client Tier"
        role="UI surfaces & external integrations"
        gradient="from-slate-300 to-slate-400 dark:from-slate-700 dark:to-slate-600"
        accent="text-slate-500 dark:text-slate-400"
      >
        <Box icon="🛠" label="Admin UI" sub="adminui.aurora.local" />
        <Box icon="☁️" label="Cloud UI" sub="cloud.aurora.local" />
        <Box icon="📚" label="Runbook" sub="runbook.aurora.local" />
        <Box icon="🤖" label="API Client" sub="HTTP / gRPC" />
      </Layer>

      <Arrow label="HTTPS / mTLS" />

      {/* Edge tier */}
      <Layer
        icon="🚦"
        name="Edge / Ingress"
        role="TLS termination, routing, security headers"
        gradient="from-purple-500 to-pink-500"
        accent="text-purple-600 dark:text-purple-400"
      >
        <Box icon="⚡" label="Envoy" sub="vhost routing" />
        <Box icon="🔒" label="TLS" sub="mkcert / prod CA" />
        <Box icon="🛡" label="WAF" sub="security headers" />
        <Box icon="🔁" label="HMR" sub="dev WebSocket" />
      </Layer>

      <Arrow label="HTTP / gRPC + JWT cookies" />

      {/* Controlplane tier */}
      <Layer
        icon="🧠"
        name="Controlplane Cluster"
        role="3 instances behind Envoy LB"
        gradient="from-indigo-500 to-purple-500"
        accent="text-indigo-600 dark:text-indigo-400"
      >
        <Box icon="🟦" label="cp-1" sub=":8080" />
        <Box icon="🟦" label="cp-2" sub=":8080" />
        <Box icon="🟦" label="cp-3" sub=":8080" />
        <Box icon="🔐" label="IAM" sub="auth & RBAC" />
        <Box icon="🏢" label="Tenant" sub="multi-tenant" />
        <Box icon="📜" label="Policy" sub="rule engine" />
        <Box icon="📧" label="Mail" sub="SMTP" />
        <Box icon="🖥" label="Hypervisor" sub="VM lifecycle" />
      </Layer>

      <Arrow label="enqueue jobs / events" />

      {/* Coordination tier */}
      <Layer
        icon="⚡"
        name="Coordination Layer"
        role="Realtime queue, distributed locks, transient state"
        gradient="from-red-500 to-orange-500"
        accent="text-red-600 dark:text-red-400"
      >
        <Box icon="🎯" label="Redis Stream" sub="job pipeline" />
        <Box icon="🔒" label="Distributed Lock" sub="recovery codes" />
        <Box icon="💾" label="Session Cache" sub="iam:admin:session:*" />
        <Box icon="🚫" label="JTI Blacklist" sub="iam:blacklist:*" />
      </Layer>

      <Arrow label="consume → execute → report" />

      {/* Runtime tier */}
      <Layer
        icon="⚙️"
        name="Runtime Tier"
        role="Workload execution & edge agents"
        gradient="from-emerald-500 to-teal-500"
        accent="text-emerald-600 dark:text-emerald-400"
      >
        <Box icon="🏗" label="Dataplane" sub="long-running" />
        <Box icon="🛰" label="Agent" sub="host / VM" />
        <Box icon="📊" label="Worker" sub="task pipeline" />
        <Box icon="🔄" label="Reconciler" sub="state sync" />
      </Layer>

      <Arrow label="persist business state" />

      {/* Storage tier */}
      <Layer
        icon="💽"
        name="Storage Tier"
        role="Source of truth & observability stack"
        gradient="from-blue-500 to-cyan-500"
        accent="text-blue-600 dark:text-blue-400"
      >
        <Box icon="🐘" label="PostgreSQL" sub="SoT" />
        <Box icon="📈" label="Prometheus" sub="metrics" />
        <Box icon="🔭" label="Tempo" sub="traces" />
        <Box icon="📜" label="Loki" sub="logs" />
        <Box icon="📊" label="Grafana" sub="dashboards" />
        <Box icon="📡" label="OTel" sub="collector" />
      </Layer>

      <p className="text-xs text-slate-500 dark:text-slate-400 mt-4 text-center italic">
        PostgreSQL là source of truth · Redis là kênh điều phối realtime · Envoy là biên duy
        nhất tiếp xúc client
      </p>
    </div>
  )
}
