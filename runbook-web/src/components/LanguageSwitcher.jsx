import { useState, useRef, useEffect } from 'react'
import { useTranslation } from 'react-i18next'
import { SUPPORTED_LANGUAGES } from '../i18n/index.js'

export default function LanguageSwitcher() {
  const { i18n, t } = useTranslation()
  const [open, setOpen] = useState(false)
  const wrapRef = useRef(null)

  const current = SUPPORTED_LANGUAGES.find((l) => l.code === i18n.language)
    || SUPPORTED_LANGUAGES.find((l) => i18n.language?.startsWith(l.code))
    || SUPPORTED_LANGUAGES[0]

  useEffect(() => {
    const onClick = (e) => {
      if (wrapRef.current && !wrapRef.current.contains(e.target)) setOpen(false)
    }
    document.addEventListener('mousedown', onClick)
    return () => document.removeEventListener('mousedown', onClick)
  }, [])

  const change = (code) => {
    i18n.changeLanguage(code)
    setOpen(false)
  }

  return (
    <div ref={wrapRef} className="relative flex-shrink-0">
      <button
        onClick={() => setOpen((v) => !v)}
        title={t('header.language')}
        aria-label={t('header.language')}
        className="flex items-center gap-1.5 px-2.5 py-1.5 rounded-lg text-sm text-slate-700 dark:text-slate-200 hover:bg-slate-100 dark:hover:bg-slate-800 transition-colors"
      >
        <span className="text-base leading-none">{current.flag}</span>
        <span className="hidden sm:inline text-xs font-medium uppercase tracking-wider">
          {current.code}
        </span>
        <svg
          className={`w-3 h-3 text-slate-400 transition-transform ${open ? 'rotate-180' : ''}`}
          fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="2.5"
        >
          <path strokeLinecap="round" strokeLinejoin="round" d="M19 9l-7 7-7-7" />
        </svg>
      </button>

      {open && (
        <div className="absolute right-0 top-full mt-1.5 w-44 bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-700 rounded-lg shadow-xl shadow-slate-300/40 dark:shadow-black/40 z-50 overflow-hidden">
          <ul className="py-1">
            {SUPPORTED_LANGUAGES.map((lang) => {
              const active = lang.code === current.code
              return (
                <li key={lang.code}>
                  <button
                    onClick={() => change(lang.code)}
                    className={`w-full text-left px-3 py-2 flex items-center gap-2 text-sm transition-colors ${
                      active
                        ? 'bg-indigo-100 dark:bg-indigo-500/15 text-indigo-700 dark:text-indigo-200'
                        : 'text-slate-700 dark:text-slate-200 hover:bg-slate-100 dark:hover:bg-slate-800'
                    }`}
                  >
                    <span className="text-base">{lang.flag}</span>
                    <span className="flex-1">{lang.label}</span>
                    <span className="text-[10px] uppercase font-mono text-slate-500">{lang.code}</span>
                    {active && <span className="text-emerald-500">✓</span>}
                  </button>
                </li>
              )
            })}
          </ul>
        </div>
      )}
    </div>
  )
}
