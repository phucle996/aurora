import { useEffect, useMemo, useState } from 'react'

import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'

import i18n from '@/lib/i18n'

type LanguageCode = 'vi' | 'en' | 'zh-CN' | 'hi' | 'ja' | 'ko'

type LanguageOption = {
  code: LanguageCode
  label: string
  short: string
}

const LANGUAGE_STORAGE_KEY = 'adminui-language'
const DEFAULT_LANGUAGE: LanguageCode = 'vi'

const LANGUAGE_OPTIONS: readonly LanguageOption[] = [
  { code: 'vi', label: 'Tiếng Việt', short: 'vi' },
  { code: 'en', label: 'English', short: 'en' },
  { code: 'zh-CN', label: 'Chinese (Simplified)', short: 'zh' },
  { code: 'hi', label: 'Hindi', short: 'hi' },
  { code: 'ja', label: 'Japanese', short: 'ja' },
  { code: 'ko', label: 'Korean', short: 'ko' },
]

function isLanguageCode(value: string): value is LanguageCode {
  return LANGUAGE_OPTIONS.some((option) => option.code === value)
}

function readStoredLanguage(): LanguageCode | null {
  if (typeof window === 'undefined') {
    return null
  }

  try {
    const stored = window.localStorage.getItem(LANGUAGE_STORAGE_KEY)
    if (stored != null && isLanguageCode(stored)) {
      return stored
    }
  } catch {
    // Ignore storage read errors and fallback to default.
  }

  return null
}

function persistLanguage(language: LanguageCode) {
  if (typeof window === 'undefined') {
    return
  }

  try {
    window.localStorage.setItem(LANGUAGE_STORAGE_KEY, language)
  } catch {
    // Ignore storage write errors.
  }
}

function applyLanguageToDocument(language: LanguageCode) {
  if (typeof document === 'undefined') {
    return
  }

  const root = document.documentElement
  root.lang = language
  root.setAttribute('data-language', language)
}

function resolveInitialLanguage(): LanguageCode {
  return readStoredLanguage() ?? DEFAULT_LANGUAGE
}

export default function LanguageSwitcher() {
  const [language, setLanguage] = useState<LanguageCode>(resolveInitialLanguage)

  const currentLanguage = useMemo(
    () =>
      LANGUAGE_OPTIONS.find((option) => option.code === language) ??
      LANGUAGE_OPTIONS[0],
    [language],
  )

  useEffect(() => {
    applyLanguageToDocument(language)
    persistLanguage(language)
    if (typeof window !== 'undefined') {
      window.dispatchEvent(new CustomEvent('adminui-language-change', { detail: language }))
    }
    if (i18n.language !== language) {
      void i18n.changeLanguage(language)
    }
  }, [language])

  useEffect(() => {
    const onStorage = (event: StorageEvent) => {
      if (event.key !== LANGUAGE_STORAGE_KEY) {
        return
      }
      setLanguage(resolveInitialLanguage())
    }

    const onCustomEvent = (e: Event) => {
      const customEvent = e as CustomEvent<string>
      if (isLanguageCode(customEvent.detail)) {
        setLanguage(customEvent.detail)
      }
    }

    window.addEventListener('storage', onStorage)
    window.addEventListener('adminui-language-change', onCustomEvent)
    return () => {
      window.removeEventListener('storage', onStorage)
      window.removeEventListener('adminui-language-change', onCustomEvent)
    }
  }, [])

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          className="relative inline-flex h-11 w-11 items-center justify-center overflow-hidden rounded-full border border-sky-200/80 bg-white/90 text-sky-700 shadow-[0_10px_30px_-18px_rgba(14,116,144,0.7)] transition-all duration-300 hover:scale-[1.03] hover:border-sky-300 hover:bg-sky-50 hover:shadow-[0_14px_34px_-18px_rgba(2,132,199,0.65)] active:scale-95 dark:border-slate-600 dark:bg-slate-900/90 dark:text-cyan-300 dark:shadow-[0_10px_32px_-18px_rgba(34,211,238,0.45)] dark:hover:border-cyan-400/70 dark:hover:bg-slate-800"
          aria-label={`Language: ${currentLanguage.label}`}
          title={`Language: ${currentLanguage.label}`}
        >
          <span className="pointer-events-none absolute inset-0 rounded-full bg-[radial-gradient(circle_at_30%_30%,rgba(14,165,233,0.22),transparent_64%)] dark:bg-[radial-gradient(circle_at_68%_30%,rgba(34,211,238,0.24),transparent_64%)]" />
          <span className="relative text-[11px] font-semibold tracking-[0.12em] uppercase">
            {currentLanguage.short.toUpperCase()}
          </span>
        </button>
      </DropdownMenuTrigger>

      <DropdownMenuContent
        align="end"
        side="top"
        className="w-56 border border-border/70 bg-background/95 backdrop-blur"
      >
        <DropdownMenuLabel>Interface language</DropdownMenuLabel>
        <DropdownMenuRadioGroup
          value={language}
          onValueChange={(value) => {
            if (isLanguageCode(value)) {
              setLanguage(value)
            }
          }}
        >
          {LANGUAGE_OPTIONS.map((option) => (
            <DropdownMenuRadioItem
              key={option.code}
              value={option.code}
              className="hover:bg-linear-to-r hover:from-sky-500 hover:via-blue-500 hover:to-cyan-500 hover:text-white focus:bg-linear-to-r focus:from-sky-500 focus:via-blue-500 focus:to-cyan-500 focus:text-white data-highlighted:bg-linear-to-r data-highlighted:from-sky-500 data-highlighted:via-blue-500 data-highlighted:to-cyan-500 data-highlighted:text-white"
            >
              <span>{option.label}</span>
              <span className="ml-auto text-xs text-muted-foreground group-hover/dropdown-menu-item:text-white/90 group-focus/dropdown-menu-item:text-white/90 group-data-highlighted/dropdown-menu-item:text-white/90">
                {option.short.toUpperCase()}
              </span>
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
