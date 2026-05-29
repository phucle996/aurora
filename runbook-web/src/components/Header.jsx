import { useState, useEffect, useRef } from 'react'

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

const SEARCH_INDEX = [
  { page: 'auth-token-model', anchor: 'section-0', title: 'Executive Summary', keywords: 'overview summary fragment token defense depth' },
  { page: 'auth-token-model', anchor: 'section-1', title: 'Token Model', keywords: 'fragment jwt accesskey accesssecret architecture user plane separation' },
  { page: 'auth-token-model', anchor: 'section-2', title: 'Token Lifecycle', keywords: 'state diagram active grace blacklisted expired revoked transitions' },
  { page: 'auth-token-model', anchor: 'section-3', title: 'Token Components', keywords: 'jwt access_token access_key access_secret client_device_id uuid v7 hs256 hmac sha256' },
  { page: 'auth-token-model', anchor: 'section-4', title: 'Redis Session Storage', keywords: 'redis session record verification jti blacklist iam admin cache' },
  { page: 'auth-token-model', anchor: 'section-5', title: 'Token Rotation', keywords: 'rotation refresh ha grace period cas version' },
  { page: 'auth-token-model', anchor: 'section-6', title: 'Cookie Specification', keywords: 'cookie httponly secure samesite path lax matrix flags' },
  { page: 'auth-token-model', anchor: 'section-7', title: 'Inactivity Timeout', keywords: 'inactivity timeout 15 minutes redis ttl auto expire silent refresh' },
  { page: 'auth-token-model', anchor: 'section-8', title: 'Device Binding', keywords: 'device binding ed25519 public key tracking revocation' },
  { page: 'auth-token-model', anchor: 'section-9', title: 'Threat Model & Mitigations', keywords: 'threat hijacking xss csrf replay mitm brute force enumeration' },
  { page: 'auth-token-model', anchor: 'section-10', title: 'Configuration', keywords: 'config security cfg go struct ttl admin api token session' },
  { page: 'auth-token-model', anchor: 'section-11', title: 'Security Principles', keywords: 'principles defense depth zero trust short lived fail closed stateless' },
  { page: 'auth-token-model', anchor: 'section-12', title: 'Operational Guarantees', keywords: 'guarantees instant logout revocation latency' },
  { page: 'auth-token-model', anchor: 'section-13', title: 'References', keywords: 'rfc 7519 6238 8032 9562 owasp nist' },
]

function search(query) {
  const q = query.toLowerCase().trim()
  if (!q) return []
  return SEARCH_INDEX.filter((entry) => {
    const haystack = (entry.title + ' ' + entry.keywords).toLowerCase()
    return q.split(/\s+/).every((token) => haystack.includes(token))
  }).slice(0, 8)
}

export default function Header({ onMenuClick, currentPage, onNavigate, theme, onToggleTheme }) {
  const meta = PAGE_TITLES[currentPage] || PAGE_TITLES.home
  const [query, setQuery] = useState('')
  const [open, setOpen] = useState(false)
  const [activeIdx, setActiveIdx] = useState(0)
  const inputRef = useRef(null)
  const wrapRef = useRef(null)

  const results = search(query)

  useEffect(() => {
    const onClick = (e) => {
      if (wrapRef.current && !wrapRef.current.contains(e.target)) {
        setOpen(false)
      }
    }
    document.addEventListener('mousedown', onClick)
    return () => document.removeEventListener('mousedown', onClick)
  }, [])

  useEffect(() => {
    const onKey = (e) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault()
        inputRef.current?.focus()
        setOpen(true)
      }
      if (e.key === 'Escape') {
        setOpen(false)
        inputRef.current?.blur()
      }
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [])

  const goTo = (entry) => {
    if (entry.page !== currentPage && onNavigate) {
      onNavigate(entry.page)
    }
    setOpen(false)
    setQuery('')
    setTimeout(() => {
      const el = document.getElementById(entry.anchor)
      if (el) el.scrollIntoView({ behavior: 'smooth', block: 'start' })
    }, 50)
  }

  const onKeyDown = (e) => {
    if (!open || results.length === 0) return
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      setActiveIdx((i) => (i + 1) % results.length)
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      setActiveIdx((i) => (i - 1 + results.length) % results.length)
    } else if (e.key === 'Enter') {
      e.preventDefault()
      goTo(results[activeIdx])
    }
  }

  return (
    <header className="bg-white dark:bg-slate-900 border-b border-slate-200 dark:border-slate-800 px-6 py-3 flex items-center gap-4">
      <button
        onClick={onMenuClick}
        className="p-2 hover:bg-slate-100 dark:hover:bg-slate-800 rounded-lg transition-colors flex-shrink-0 text-slate-700 dark:text-slate-200"
        aria-label="Toggle sidebar"
      >
        ☰
      </button>

      <div className="hidden md:block flex-shrink-0 min-w-0 max-w-sm">
        <h1 className="text-base font-semibold text-slate-900 dark:text-slate-100 truncate">
          {meta.title}
        </h1>
        <p className="text-xs text-slate-500 dark:text-slate-400 truncate">
          {meta.subtitle}
        </p>
      </div>

      {/* Search bar */}
      <div ref={wrapRef} className="flex-1 max-w-2xl mx-auto relative">
        <div className="relative">
          <span className="absolute left-3 top-1/2 -translate-y-1/2 text-slate-400 dark:text-slate-500 pointer-events-none">
            🔍
          </span>
          <input
            ref={inputRef}
            type="text"
            placeholder="Search runbook…"
            value={query}
            onChange={(e) => {
              setQuery(e.target.value)
              setOpen(true)
              setActiveIdx(0)
            }}
            onFocus={() => setOpen(true)}
            onKeyDown={onKeyDown}
            className="w-full pl-9 pr-16 py-2 bg-slate-100 dark:bg-slate-800/70 border border-slate-200 dark:border-slate-700 rounded-lg text-sm text-slate-900 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:border-indigo-400 dark:focus:border-indigo-500/60 focus:bg-white dark:focus:bg-slate-800 transition-colors"
          />
          <kbd className="hidden sm:inline-flex absolute right-3 top-1/2 -translate-y-1/2 items-center gap-1 px-1.5 py-0.5 text-[10px] font-mono text-slate-500 dark:text-slate-400 bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-700 rounded">
            ⌘K
          </kbd>
        </div>

        {open && query && (
          <div className="absolute top-full left-0 right-0 mt-1.5 bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-700 rounded-lg shadow-xl shadow-slate-300/40 dark:shadow-black/40 z-50 overflow-hidden">
            {results.length === 0 ? (
              <div className="p-4 text-sm text-slate-500 text-center">
                No results for{' '}
                <span className="text-slate-700 dark:text-slate-300">"{query}"</span>
              </div>
            ) : (
              <ul className="py-1 max-h-80 overflow-y-auto">
                {results.map((r, i) => (
                  <li key={`${r.page}-${r.anchor}`}>
                    <button
                      onMouseEnter={() => setActiveIdx(i)}
                      onClick={() => goTo(r)}
                      className={`w-full text-left px-4 py-2.5 flex items-start gap-3 transition-colors ${
                        i === activeIdx
                          ? 'bg-indigo-100 dark:bg-indigo-500/15 text-indigo-700 dark:text-indigo-200'
                          : 'text-slate-700 dark:text-slate-200 hover:bg-slate-100 dark:hover:bg-slate-800'
                      }`}
                    >
                      <span className="text-base mt-0.5">📄</span>
                      <span className="flex-1 min-w-0">
                        <span className="block text-sm font-medium truncate">
                          {r.title}
                        </span>
                        <span className="block text-[11px] text-slate-500 truncate">
                          {r.page} → #{r.anchor}
                        </span>
                      </span>
                    </button>
                  </li>
                ))}
              </ul>
            )}
            <div className="px-4 py-2 border-t border-slate-200 dark:border-slate-800 text-[11px] text-slate-500 flex items-center justify-between">
              <span>Use ↑ ↓ to navigate, ↵ to open</span>
              <span>Esc to close</span>
            </div>
          </div>
        )}
      </div>

      {/* Theme toggle */}
      <button
        onClick={onToggleTheme}
        className="p-2 rounded-lg text-slate-600 dark:text-slate-300 hover:bg-slate-100 dark:hover:bg-slate-800 hover:text-slate-900 dark:hover:text-slate-100 transition-colors flex-shrink-0"
        aria-label={theme === 'dark' ? 'Switch to light theme' : 'Switch to dark theme'}
        title={theme === 'dark' ? 'Switch to light' : 'Switch to dark'}
      >
        {theme === 'dark' ? (
          <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="2">
            <circle cx="12" cy="12" r="4" />
            <path strokeLinecap="round" d="M12 2v2M12 20v2M4.93 4.93l1.41 1.41M17.66 17.66l1.41 1.41M2 12h2M20 12h2M4.93 19.07l1.41-1.41M17.66 6.34l1.41-1.41" />
          </svg>
        ) : (
          <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="2">
            <path strokeLinecap="round" strokeLinejoin="round" d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z" />
          </svg>
        )}
      </button>

      <span className="px-3 py-1 bg-emerald-100 dark:bg-emerald-500/15 text-emerald-700 dark:text-emerald-300 border border-emerald-300 dark:border-emerald-500/30 rounded-full text-xs font-semibold flex-shrink-0 hidden lg:inline-block">
        {meta.badge}
      </span>
    </header>
  )
}
