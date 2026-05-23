export type ThemeMode = 'light' | 'dark'

export const THEME_STORAGE_KEY = 'adminui-theme'
export const THEME_TRANSITION_CLASS = 'theme-transition'
export const THEME_TRANSITION_MS = 360

export function readStoredTheme(): ThemeMode | null {
  if (typeof window === 'undefined') {
    return null
  }

  try {
    const stored = window.localStorage.getItem(THEME_STORAGE_KEY)
    if (stored === 'light' || stored === 'dark') {
      return stored
    }
  } catch {
    return null
  }

  return null
}

export function resolveThemeFromDocument(): ThemeMode {
  if (typeof document !== 'undefined') {
    if (document.documentElement.classList.contains('dark')) return 'dark'
    if (document.documentElement.classList.contains('light')) return 'light'
  }

  return readStoredTheme() ?? 'light'
}

export function persistTheme(theme: ThemeMode) {
  if (typeof window === 'undefined') {
    return
  }

  try {
    window.localStorage.setItem(THEME_STORAGE_KEY, theme)
  } catch {
    // ignore storage write errors
  }
}

export function applyThemeToDocument(theme: ThemeMode) {
  if (typeof document === 'undefined') {
    return
  }

  const root = document.documentElement
  root.classList.remove('light', 'dark')
  root.classList.add(theme)
  root.style.colorScheme = theme === 'dark' ? 'only dark' : 'only light'
  root.setAttribute('data-theme-ready', 'true')

  document.querySelector('meta[name="theme-color"]')?.setAttribute(
    'content',
    theme === 'dark' ? '#0b1220' : '#f8fafc',
  )
}
