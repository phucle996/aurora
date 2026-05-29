const STATES = [
  {
    id: 'no-session',
    label: 'NO_SESSION',
    color: 'border-slate-300 dark:border-slate-600 bg-slate-100 dark:bg-slate-800/50 text-slate-700 dark:text-slate-300',
  },
  {
    id: 'active',
    label: 'ACTIVE',
    color: 'border-emerald-400 dark:border-emerald-500/50 bg-emerald-50 dark:bg-emerald-500/10 text-emerald-700 dark:text-emerald-300',
    note: 'TTL 15m • Verify 3 fragments + JTI not blacklisted',
  },
  {
    id: 'grace',
    label: 'GRACE',
    color: 'border-amber-400 dark:border-amber-500/50 bg-amber-50 dark:bg-amber-500/10 text-amber-700 dark:text-amber-300',
    note: 'TTL 10s • Old session valid for in-flight requests',
  },
]

const TRANSITIONS = [
  { from: 'NO_SESSION', to: 'ACTIVE', label: 'Login (API key + MFA + device pk)' },
  { from: 'ACTIVE', to: 'ACTIVE', label: 'Request — verify 3 fragments' },
  { from: 'ACTIVE', to: 'GRACE', label: 'Refresh — generate new tokens' },
  { from: 'GRACE', to: 'ACTIVE', label: 'New tokens received' },
  { from: 'ACTIVE', to: 'NO_SESSION', label: 'Logout — delete session + blacklist JTI' },
  { from: 'ACTIVE', to: 'NO_SESSION', label: 'Inactivity — 15m timeout' },
]

export default function StateDiagram() {
  return (
    <div className="bg-slate-50 dark:bg-slate-900/60 border border-slate-200 dark:border-slate-800 rounded-xl p-5 my-4">
      <div className="grid md:grid-cols-3 gap-3">
        {STATES.map((s) => (
          <div key={s.id} className={`border-2 rounded-lg p-4 ${s.color}`}>
            <p className="font-mono font-bold text-sm">{s.label}</p>
            {s.note && (
              <p className="text-xs text-slate-500 dark:text-slate-400 mt-2">{s.note}</p>
            )}
          </div>
        ))}
      </div>

      <div className="mt-5">
        <p className="text-[11px] uppercase tracking-wider text-slate-500 mb-2">
          Transitions
        </p>
        <ul className="space-y-1.5 text-sm text-slate-700 dark:text-slate-300">
          {TRANSITIONS.map((t, i) => (
            <li key={i} className="flex items-start gap-2">
              <span className="font-mono text-xs px-1.5 py-0.5 rounded bg-slate-200 dark:bg-slate-800 text-slate-700 dark:text-slate-300">
                {t.from}
              </span>
              <span className="text-slate-400 dark:text-slate-500">→</span>
              <span className="font-mono text-xs px-1.5 py-0.5 rounded bg-slate-200 dark:bg-slate-800 text-slate-700 dark:text-slate-300">
                {t.to}
              </span>
              <span className="text-slate-600 dark:text-slate-400">{t.label}</span>
            </li>
          ))}
        </ul>
      </div>
    </div>
  )
}
