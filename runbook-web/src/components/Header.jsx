import { useState, useEffect, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate, useLocation } from 'react-router-dom'
import LanguageSwitcher from './LanguageSwitcher.jsx'
import { useQuickNav } from '../context/QuickNavContext.jsx'

// Map route path → i18n page_titles key
const PATH_TO_PAGE_KEY = {
  '/auth/token-model': 'auth-token-model',
  '/zone/management': 'zone-management',
  '/zone/workflow': 'zone-workflow',
  '/home': 'home',
}

const SEARCH_INDEX = [
  { path: '/auth/token-model', anchor: 'section-0', titleKey: 'search.exec_summary', keywords: 'overview summary fragment token defense depth' },
  { path: '/auth/token-model', anchor: 'section-1', titleKey: 'search.token_model', keywords: 'fragment jwt accesskey accesssecret architecture user plane separation' },
  { path: '/auth/token-model', anchor: 'section-2', titleKey: 'search.lifecycle', keywords: 'state diagram active grace blacklisted expired revoked transitions' },
  { path: '/auth/token-model', anchor: 'section-3', titleKey: 'search.token_components', keywords: 'jwt access_token access_key access_secret client_device_id uuid v7 hs256 hmac sha256' },
  { path: '/auth/token-model', anchor: 'section-4', titleKey: 'search.redis_storage', keywords: 'redis session record verification jti blacklist iam admin cache' },
  { path: '/auth/token-model', anchor: 'section-5', titleKey: 'search.token_rotation', keywords: 'rotation refresh ha grace period cas version' },
  { path: '/auth/token-model', anchor: 'section-6', titleKey: 'search.cookie_spec', keywords: 'cookie httponly secure samesite path lax matrix flags' },
  { path: '/auth/token-model', anchor: 'section-7', titleKey: 'search.inactivity', keywords: 'inactivity timeout 15 minutes redis ttl auto expire silent refresh' },
  { path: '/auth/token-model', anchor: 'section-8', titleKey: 'search.device_binding', keywords: 'device binding ed25519 public key tracking revocation' },
  { path: '/auth/token-model', anchor: 'section-9', titleKey: 'search.threat_model', keywords: 'threat hijacking xss csrf replay mitm brute force enumeration' },
  { path: '/auth/token-model', anchor: 'section-10', titleKey: 'search.config', keywords: 'config security cfg go struct ttl admin api token session' },
  { path: '/auth/token-model', anchor: 'section-11', titleKey: 'search.principles', keywords: 'principles defense depth zero trust short lived fail closed stateless' },
  { path: '/auth/token-model', anchor: 'section-12', titleKey: 'search.guarantees', keywords: 'guarantees instant logout revocation latency' },
  { path: '/auth/token-model', anchor: 'section-13', titleKey: 'search.references', keywords: 'rfc 7519 6238 8032 9562 owasp nist' },
]

function searchEntries(query, t) {
  const q = query.toLowerCase().trim()
  if (!q) return []
  return SEARCH_INDEX.filter((entry) => {
    const localized = t(entry.titleKey, { defaultValue: '' })
    const haystack = (localized + ' ' + entry.keywords).toLowerCase()
    return q.split(/\s+/).every((token) => haystack.includes(token))
  }).slice(0, 8)
}

export default function Header({ onMenuClick, theme, onToggleTheme }) {
  const { t } = useTranslation()
  const { pathname } = useLocation()
  const navigate = useNavigate()
  const { navItems } = useQuickNav()
  const pageKey = PATH_TO_PAGE_KEY[pathname] || 'home'
  const meta = t(`page_titles.${pageKey}`, { returnObjects: true, defaultValue: t('page_titles.home', { returnObjects: true }) })
  const [query, setQuery] = useState('')
  const [open, setOpen] = useState(false)
  const [navOpen, setNavOpen] = useState(false)
  const [activeIdx, setActiveIdx] = useState(0)
  const inputRef = useRef(null)
  const wrapRef = useRef(null)
  const navRef = useRef(null)

  const results = searchEntries(query, t)

  useEffect(() => {
    const onClick = (e) => {
      if (wrapRef.current && !wrapRef.current.contains(e.target)) setOpen(false)
      if (navRef.current && !navRef.current.contains(e.target)) setNavOpen(false)
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
    if (entry.path !== pathname) navigate(entry.path)
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

  const handleNavClick = (e, href) => {
    e.preventDefault()
    setNavOpen(false)
    const targetId = href.replace('#', '')
    const el = document.getElementById(targetId)
    if (el) {
      el.scrollIntoView({ behavior: 'smooth', block: 'start' })
    }
  }

  return (
    <header className="bg-white dark:bg-slate-900 border-b border-slate-200 dark:border-slate-800 px-6 py-3 flex items-center gap-3">
      <button
        onClick={onMenuClick}
        className="p-2 hover:bg-slate-100 dark:hover:bg-slate-800 rounded-lg transition-colors flex-shrink-0 text-slate-700 dark:text-slate-200"
        aria-label={t('header.toggle_sidebar')}
      >
        ☰
      </button>

      <div className="hidden md:block flex-shrink-0 min-w-0 max-w-xs">
        <h1 className="text-sm font-semibold text-slate-900 dark:text-slate-100 truncate">
          {meta.title}
        </h1>
        <p className="text-[11px] text-slate-500 dark:text-slate-400 truncate">
          {meta.subtitle}
        </p>
      </div>

      {/* Quick Navigation Dropdown */}
      {navItems && navItems.length > 0 && (
        <div ref={navRef} className="relative flex-shrink-0 ml-2">
          <button
            onClick={() => setNavOpen(!navOpen)}
            className="flex items-center gap-1.5 px-3 py-1.5 bg-slate-50 dark:bg-slate-800/80 border border-slate-200 dark:border-slate-700 hover:bg-slate-100 dark:hover:bg-slate-750 rounded-lg text-xs font-semibold text-slate-700 dark:text-slate-200 transition-colors"
            aria-haspopup="true"
            aria-expanded={navOpen}
          >
            <span className="text-sm">🧭</span>
            <span>{t('header.quick_nav', { defaultValue: 'Quick Navigation' })}</span>
            <svg
              className={`w-3 h-3 text-slate-400 dark:text-slate-500 transition-transform duration-200 ${navOpen ? 'rotate-180' : ''}`}
              fill="none"
              viewBox="0 0 24 24"
              stroke="currentColor"
              strokeWidth="2.5"
            >
              <path strokeLinecap="round" strokeLinejoin="round" d="M19 9l-7 7-7-7" />
            </svg>
          </button>

          {navOpen && (
            <div className="absolute top-full left-0 mt-1.5 w-60 bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-xl shadow-xl z-50 py-1.5">
              <ul className="space-y-0.5 px-1.5">
                {navItems.map(({ num, label, href }) => (
                  <li key={num}>
                    <a
                      href={href}
                      onClick={(e) => handleNavClick(e, href)}
                      className="flex items-center gap-2.5 px-2.5 py-2 rounded-lg text-xs font-medium text-slate-600 dark:text-slate-350 hover:text-indigo-600 dark:hover:text-indigo-400 hover:bg-slate-50 dark:hover:bg-slate-800 transition-colors"
                    >
                      <span className="inline-flex items-center justify-center w-5 h-5 rounded bg-indigo-100 dark:bg-indigo-500/20 text-indigo-600 dark:text-indigo-300 text-[10px] font-bold flex-shrink-0">
                        {num}
                      </span>
                      <span className="truncate">{label}</span>
                    </a>
                  </li>
                ))}
              </ul>
            </div>
          )}
        </div>
      )}


      <div ref={wrapRef} className="flex-1 max-w-xl mx-auto relative">


        <div className="relative">
          <span className="absolute left-3 top-1/2 -translate-y-1/2 text-slate-400 dark:text-slate-500 pointer-events-none">
            🔍
          </span>
          <input
            ref={inputRef}
            type="text"
            placeholder={t('header.search_placeholder')}
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
                {t('header.search_no_results')}{' '}
                <span className="text-slate-700 dark:text-slate-300">"{query}"</span>
              </div>
            ) : (
              <ul className="py-1 max-h-80 overflow-y-auto">
                {results.map((r, i) => (
                  <li key={`${r.path}-${r.anchor}`}>
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
                          {t(r.titleKey, { defaultValue: r.titleKey })}
                        </span>
                        <span className="block text-[11px] text-slate-500 truncate">
                          {r.path} → #{r.anchor}
                        </span>
                      </span>
                    </button>
                  </li>
                ))}
              </ul>
            )}
            <div className="px-4 py-2 border-t border-slate-200 dark:border-slate-800 text-[11px] text-slate-500 flex items-center justify-between">
              <span>{t('header.search_navigate_hint')}</span>
              <span>{t('header.search_close_hint')}</span>
            </div>
          </div>
        )}
      </div>

      <LanguageSwitcher />

      <button
        onClick={onToggleTheme}
        className="p-2 rounded-lg text-slate-600 dark:text-slate-300 hover:bg-slate-100 dark:hover:bg-slate-800 hover:text-slate-900 dark:hover:text-slate-100 transition-colors flex-shrink-0"
        aria-label={theme === 'dark' ? t('header.toggle_theme_light') : t('header.toggle_theme_dark')}
        title={theme === 'dark' ? t('header.toggle_theme_light') : t('header.toggle_theme_dark')}
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

      <span className="px-3 py-1 bg-emerald-100 dark:bg-emerald-500/15 text-emerald-700 dark:text-emerald-300 border border-emerald-300 dark:border-emerald-500/30 rounded-full text-xs font-semibold flex-shrink-0 hidden xl:inline-block">
        {meta.badge}
      </span>
    </header>
  )
}
