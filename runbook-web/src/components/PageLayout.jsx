import { useEffect } from 'react'
import { useQuickNav } from '../context/QuickNavContext.jsx'

/**
 * PageLayout — 1-column layout with dynamically published Quick Navigation in Header.
 *
 * Props:
 *   header   — JSX for the page header (title, subtitle, callout)
 *   nav      — array of { num, label, href } for the quick nav
 *   children — the section content
 */
export default function PageLayout({ header, nav = [], children }) {
  const { setNavItems } = useQuickNav()

  useEffect(() => {
    setNavItems(nav)
    return () => {
      setNavItems([])
    }
  }, [nav, setNavItems])

  return (
    <div className="flex gap-8 items-start">
      {/* ── Main content ── */}
      <div className="flex-1 min-w-0 space-y-12">
        {/* Page header */}
        <div className="border-b border-slate-200 dark:border-slate-800 pb-8">
          {header}
        </div>
        {children}
      </div>
    </div>
  )
}

