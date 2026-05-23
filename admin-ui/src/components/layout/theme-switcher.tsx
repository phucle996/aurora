import { useEffect, useRef, useState } from 'react'
import { SunMedium, MoonStar } from 'lucide-react'

import {
  THEME_STORAGE_KEY,
  THEME_TRANSITION_CLASS,
  THEME_TRANSITION_MS,
  applyThemeToDocument,
  persistTheme,
  resolveThemeFromDocument,
  type ThemeMode,
} from '@/lib/theme'
import { cn } from '@/lib/utils'

export default function ThemeSwitcher() {
  const [theme, setTheme] = useState<ThemeMode>(resolveThemeFromDocument)
  const transitionTimerRef = useRef<number | null>(null)

  useEffect(() => {
    applyThemeToDocument(theme)
    persistTheme(theme)
  }, [theme])

  useEffect(() => {
    const onStorage = (event: StorageEvent) => {
      if (event.key !== THEME_STORAGE_KEY) {
        return
      }
      setTheme(resolveThemeFromDocument())
    }

    window.addEventListener('storage', onStorage)

    return () => {
      window.removeEventListener('storage', onStorage)
    }
  }, [])

  useEffect(() => {
    return () => {
      if (transitionTimerRef.current != null) {
        window.clearTimeout(transitionTimerRef.current)
      }
    }
  }, [])

  const toggleTheme = () => {
    if (typeof document !== 'undefined') {
      const root = document.documentElement
      root.classList.add(THEME_TRANSITION_CLASS)
      if (transitionTimerRef.current != null) {
        window.clearTimeout(transitionTimerRef.current)
      }
      transitionTimerRef.current = window.setTimeout(() => {
        root.classList.remove(THEME_TRANSITION_CLASS)
        transitionTimerRef.current = null
      }, THEME_TRANSITION_MS)
    }

    setTheme((prev) => (prev === 'dark' ? 'light' : 'dark'))
  }

  const isDarkMode = theme === 'dark'

  return (
    <button
      type="button"
      onClick={toggleTheme}
      className="relative inline-flex size-8 items-center justify-center overflow-hidden rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
      aria-label={isDarkMode ? 'Switch to light theme' : 'Switch to dark theme'}
      title={isDarkMode ? 'Switch to light theme' : 'Switch to dark theme'}
      aria-pressed={isDarkMode}
    >
      <span className="relative inline-flex size-4 items-center justify-center">
        <MoonStar
          className={cn(
            'absolute size-4 transition-all duration-300',
            isDarkMode ? 'scale-60 opacity-0 -rotate-12' : 'scale-100 opacity-100 rotate-0',
          )}
        />
        <SunMedium
          className={cn(
            'absolute size-4 transition-all duration-300',
            isDarkMode ? 'scale-100 opacity-100 rotate-0' : 'scale-60 opacity-0 rotate-12',
          )}
        />
      </span>
    </button>
  )
}
