const navGroups = [
  {
    title: 'Authentication',
    items: [
      { id: 'auth-token-model', label: 'Token Model & Lifecycle', icon: '🔐' },
    ],
  },
  {
    title: 'General',
    items: [
      { id: 'home', label: 'Home', icon: '🏠' },
    ],
  },
]

export default function Sidebar({ open, currentPage, onNavigate }) {
  return (
    <aside
      className={`${open ? 'w-64' : 'w-0'} bg-slate-900 border-r border-slate-800 transition-all duration-300 overflow-hidden flex flex-col flex-shrink-0`}
    >
      <div className="p-6 border-b border-slate-800">
        <h2 className="text-lg font-bold bg-gradient-to-r from-indigo-400 to-pink-400 bg-clip-text text-transparent">
          🚀 Aurora Runbook
        </h2>
        <p className="text-xs text-slate-400 mt-1">Operational Knowledge Base</p>
      </div>

      <nav className="flex-1 overflow-y-auto p-4 space-y-6">
        {navGroups.map((group) => (
          <div key={group.title}>
            <p className="px-3 text-[11px] font-semibold uppercase tracking-wider text-slate-500 mb-2">
              {group.title}
            </p>
            <div className="space-y-1">
              {group.items.map((item) => {
                const active = item.id === currentPage
                return (
                  <button
                    key={item.id}
                    onClick={() => onNavigate(item.id)}
                    className={`w-full text-left flex items-center gap-2 px-3 py-2 rounded-lg text-sm transition-colors ${
                      active
                        ? 'bg-indigo-500/15 text-indigo-300 border border-indigo-500/30'
                        : 'text-slate-300 hover:bg-slate-800 hover:text-slate-100 border border-transparent'
                    }`}
                  >
                    <span>{item.icon}</span>
                    <span>{item.label}</span>
                  </button>
                )
              })}
            </div>
          </div>
        ))}
      </nav>

      <div className="p-4 border-t border-slate-800 text-xs text-slate-500">
        <p>v2.2 • runbook.aurora.local</p>
      </div>
    </aside>
  )
}
