// ============================================================================
// 📂 COMPONENT: TopologySpec.jsx — System Topology Component Summary (C1)
// ============================================================================
// High-level component list — what each piece does, not how.
// Callsite: C1SystemContext.jsx → Section 2: System Topology
// ============================================================================

const COMPONENTS = [
  {
    color: '#7c3aed',
    icon: '/icons/envoy.svg',
    name: 'Envoy (Central)',
    role: 'Ingress gateway',
    functions: [
      'mTLS termination for Admin UI / Cloud UI / Runbook',
      'Routes all inbound HTTP/gRPC traffic into Controlplane',
      'No CP node exposes ports directly',
    ],
  },
  {
    color: '#2563eb',
    icon: null,
    emoji: '⚙️',
    name: 'Controlplane (CP)',
    role: 'Orchestrator & liveness authority',
    functions: [
      'Zone config management (keys, policies, assignments)',
      'Receives one-way gRPC heartbeat from each DP node (liveness fallback)',
      'Triggers salvage/rebalance when a DP node dies',
      'No direct connection to Zone Redis — isolation enforced',
    ],
  },
  {
    color: '#0891b2',
    icon: null,
    emoji: '🗄️',
    name: 'PgBouncer + PostgreSQL 17',
    role: 'Persistent data layer',
    functions: [
      'All CP DB writes go through PgBouncer (:6432)',
      'Stores zone definitions, IAM, audit logs',
      'Direct PostgreSQL access is debug-only',
    ],
  },
  {
    color: '#e11d48',
    icon: '/icons/redis.svg',
    name: 'Redis Core',
    role: 'CP session cache & lease bus',
    functions: [
      'CP-side session storage and distributed locks',
      'Zone nodes must NOT write here',
    ],
  },
  {
    color: '#d97706',
    icon: '/icons/redis.svg',
    name: 'Redis Job Cluster',
    role: 'Async task dispatch',
    functions: [
      'Cross-zone task queues partitioned by zone key',
      'Both CP and DP nodes produce/consume streams',
      'Single cluster, multiple zone-keyed streams',
    ],
  },
  {
    color: '#d97706',
    icon: '/icons/envoy.svg',
    name: 'Envoy — Zone (per zone)',
    role: 'Zone-local ingress proxy',
    functions: [
      'mTLS termination at zone edge',
      'Routes inbound traffic to DP node pool within same zone',
      'Each zone has its own independent Envoy instance',
      'Config synced from CP via xDS',
    ],
  },
  {
    color: '#7c3aed',
    icon: null,
    emoji: '🦀',
    name: 'Dataplane Node (Rust)',
    role: 'Zone edge daemon',
    functions: [
      'Registers itself to zone Redis on boot (SADD)',
      'Sends per-node liveness heartbeat every ~4s (SET EX 8)',
      'Pulls tasks from zone stream (XREADGROUP / XACK)',
      'Falls back to gRPC → CP when zone Redis is unreachable',
      'Acquires lease lock before salvaging dead peer tasks',
    ],
  },
  {
    color: '#e11d48',
    icon: '/icons/redis.svg',
    name: 'Redis — Zone (per zone)',
    role: 'Zone-local cache & stream cluster',
    functions: [
      'Stores active node registry (SADD dataplane:nodes:{zone})',
      'Holds per-node liveness TTL keys (EX 8s)',
      'Serves as Consumer Group stream for zone task dispatch',
      'Distributed lease lock for salvage coordination',
      'CP subscribes read-only — zone nodes are sole writers',
    ],
  },
]

// ── Render ────────────────────────────────────────────────────────────────────

export default function TopologySpec() {
  return (
    <div className="mt-5">
      <p className="text-[11px] font-mono text-slate-400 uppercase tracking-widest mb-3">Component Roles</p>
      <div className="grid md:grid-cols-2 gap-3">
        {COMPONENTS.map((c, i) => (
          <div
            key={i}
            className="flex gap-3 p-4 rounded-xl border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900/50 hover:border-slate-300 dark:hover:border-slate-700 transition-colors"
          >
            {/* Color accent */}
            <div className="flex-shrink-0 flex flex-col items-center gap-1.5 pt-0.5">
              <div
                className="w-1 rounded-full"
                style={{ backgroundColor: c.color, height: '100%', minHeight: '48px', opacity: 0.7 }}
              />
            </div>

            <div className="min-w-0">
              {/* Header */}
              <div className="flex items-center gap-2 mb-1.5">
                {c.icon ? (
                  <img src={c.icon} alt="" className="w-4 h-4 object-contain flex-shrink-0" />
                ) : (
                  <span className="text-sm leading-none">{c.emoji}</span>
                )}
                <span className="text-xs font-bold text-slate-800 dark:text-slate-100">{c.name}</span>
                <span
                  className="text-[9px] font-mono px-1.5 py-0.5 rounded-full border"
                  style={{ color: c.color, borderColor: c.color + '40', backgroundColor: c.color + '12' }}
                >
                  {c.role}
                </span>
              </div>

              {/* Function bullets */}
              <ul className="space-y-0.5">
                {c.functions.map((f, j) => (
                  <li key={j} className="flex items-start gap-1.5 text-[11px] text-slate-500 dark:text-slate-400">
                    <span className="mt-1 flex-shrink-0 w-1 h-1 rounded-full bg-slate-300 dark:bg-slate-600" />
                    {f}
                  </li>
                ))}
              </ul>
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}
