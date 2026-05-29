import { useState } from 'react'
import { useTranslation } from 'react-i18next'

const NAV_TREE = [
  {
    id: 'auth',
    icon: '🔐',
    children: [
      { id: 'auth-token-model', icon: '🧩', status: 'ready' },
      { id: 'auth-login-flow', icon: '🚪', status: 'todo' },
      { id: 'auth-refresh-flow', icon: '♻️', status: 'todo' },
      { id: 'auth-logout-flow', icon: '🚫', status: 'todo' },
      { id: 'auth-mfa', icon: '🛡️', status: 'todo' },
      { id: 'auth-device-binding', icon: '📱', status: 'todo' },
    ],
  },
  {
    id: 'security',
    icon: '🔒',
    children: [
      { id: 'sec-threat-model', icon: '⚠️', status: 'todo' },
      { id: 'sec-secrets', icon: '🔑', status: 'todo' },
      { id: 'sec-audit', icon: '📋', status: 'todo' },
    ],
  },
  {
    id: 'infra',
    icon: '🏗️',
    children: [
      { id: 'infra-redis', icon: '💾', status: 'todo' },
      { id: 'infra-postgres', icon: '🐘', status: 'todo' },
      { id: 'infra-envoy', icon: '🚦', status: 'todo' },
    ],
  },
  {
    id: 'general',
    icon: '📚',
    children: [{ id: 'home', icon: '🏠', status: 'ready' }],
  },
]

const PAGE_TO_GROUP = NAV_TREE.reduce((acc, g) => {
  g.children.forEach((c) => {
    acc[c.id] = g.id
  })
  return acc
}, {})

export default function Sidebar({ open, currentPage, onNavigate }) {
  const { t } = useTranslation()
  const initialGroup = PAGE_TO_GROUP[currentPage] || 'auth'
  const [expanded, setExpanded] = useState({ [initialGroup]: true })

  const toggle = (id) =>
    setExpanded((prev) => ({ ...prev, [id]: !prev[id] }))

  const handleClick = (page) => {
    if (page.status === 'todo') return
    onNavigate(page.id)
  }

  return (
    <aside
      className={`${open ? 'w-72' : 'w-0'} bg-slate-50 dark:bg-slate-900 border-r border-slate-200 dark:border-slate-800 transition-all duration-300 overflow-hidden flex flex-col flex-shrink-0`}
    >
      <div className="p-5 border-b border-slate-200 dark:border-slate-800">
        <h2 className="text-lg font-bold bg-gradient-to-r from-indigo-600 to-pink-600 dark:from-indigo-400 dark:to-pink-400 bg-clip-text text-transparent">
          🚀 {t('app.title')}
        </h2>
        <p className="text-xs text-slate-500 dark:text-slate-400 mt-1">
          {t('app.subtitle')}
        </p>
      </div>

      <nav className="flex-1 overflow-y-auto p-3 space-y-1">
        {NAV_TREE.map((group) => {
          const isExpanded = !!expanded[group.id]
          const hasActiveChild = group.children.some((c) => c.id === currentPage)
          return (
            <div key={group.id}>
              <button
                onClick={() => toggle(group.id)}
                className={`w-full flex items-center justify-between px-3 py-2 rounded-lg text-sm font-medium transition-colors ${
                  hasActiveChild
                    ? 'text-slate-900 dark:text-slate-100 bg-slate-200/60 dark:bg-slate-800/40'
                    : 'text-slate-700 dark:text-slate-300 hover:bg-slate-200/40 dark:hover:bg-slate-800/60 hover:text-slate-900 dark:hover:text-slate-100'
                }`}
              >
                <span className="flex items-center gap-2">
                  <span className="text-base">{group.icon}</span>
                  <span>{t(`nav.groups.${group.id}`)}</span>
                </span>
                <svg
                  className={`w-3.5 h-3.5 text-slate-400 dark:text-slate-500 transition-transform ${
                    isExpanded ? 'rotate-90' : ''
                  }`}
                  fill="none"
                  viewBox="0 0 24 24"
                  stroke="currentColor"
                  strokeWidth="2.5"
                >
                  <path strokeLinecap="round" strokeLinejoin="round" d="M9 5l7 7-7 7" />
                </svg>
              </button>

              {isExpanded && (
                <div className="ml-4 mt-1 mb-2 pl-3 border-l border-slate-200 dark:border-slate-800 space-y-0.5">
                  {group.children.map((child) => {
                    const active = child.id === currentPage
                    const todo = child.status === 'todo'
                    return (
                      <button
                        key={child.id}
                        onClick={() => handleClick(child)}
                        disabled={todo}
                        className={`w-full text-left flex items-center gap-2 px-2.5 py-1.5 rounded-md text-sm transition-colors ${
                          active
                            ? 'bg-indigo-100 dark:bg-indigo-500/15 text-indigo-700 dark:text-indigo-300 border border-indigo-300 dark:border-indigo-500/30'
                            : todo
                            ? 'text-slate-400 dark:text-slate-500 cursor-not-allowed border border-transparent'
                            : 'text-slate-700 dark:text-slate-300 hover:bg-slate-200/60 dark:hover:bg-slate-800 hover:text-slate-900 dark:hover:text-slate-100 border border-transparent'
                        }`}
                        title={todo ? t('nav.soon_tooltip') : t(`nav.items.${child.id}`)}
                      >
                        <span className="text-sm">{child.icon}</span>
                        <span className="flex-1 truncate">{t(`nav.items.${child.id}`)}</span>
                        {todo && (
                          <span className="text-[9px] px-1.5 py-0.5 rounded bg-slate-200 dark:bg-slate-800 text-slate-500 border border-slate-300 dark:border-slate-700 uppercase tracking-wider">
                            {t('nav.soon')}
                          </span>
                        )}
                      </button>
                    )
                  })}
                </div>
              )}
            </div>
          )
        })}
      </nav>

      <div className="p-4 border-t border-slate-200 dark:border-slate-800 text-xs text-slate-500">
        <p className="font-mono">v2.2 • runbook.aurora.local</p>
      </div>
    </aside>
  )
}
