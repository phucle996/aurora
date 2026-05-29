const PAGE_TITLES = {
  'auth-token-model': {
    title: 'Admin Auth Token Model & Lifecycle',
    subtitle: 'Aurora Admin Authentication Specification',
    badge: 'v2.2 • Production Ready',
  },
  home: {
    title: 'Aurora Runbook',
    subtitle: 'Operational Knowledge Base',
    badge: 'Dev',
  },
}

export default function Header({ onMenuClick, currentPage }) {
  const meta = PAGE_TITLES[currentPage] || PAGE_TITLES.home
  return (
    <header className="bg-slate-900 border-b border-slate-800 px-6 py-4 flex items-center justify-between">
      <div className="flex items-center gap-4">
        <button
          onClick={onMenuClick}
          className="p-2 hover:bg-slate-800 rounded-lg transition-colors"
          aria-label="Toggle sidebar"
        >
          ☰
        </button>
        <div>
          <h1 className="text-xl font-bold text-slate-100">{meta.title}</h1>
          <p className="text-sm text-slate-400">{meta.subtitle}</p>
        </div>
      </div>

      <div className="flex items-center gap-4">
        <span className="px-3 py-1 bg-emerald-500/15 text-emerald-300 border border-emerald-500/30 rounded-full text-xs font-semibold">
          {meta.badge}
        </span>
      </div>
    </header>
  )
}
