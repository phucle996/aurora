/**
 * PageLayout — 2-column layout: main content left, sticky quick-nav right.
 *
 * Props:
 *   header   — JSX for the page header (title, subtitle, callout)
 *   nav      — array of { num, label, href } for the sticky nav
 *   children — the section content
 */
export default function PageLayout({ header, nav, children }) {
  return (
    <div className="flex gap-8 items-start">

      {/* ── Main content ── */}
      <div className="flex-1 min-w-0 space-y-12">
        {/* Page header (no nav box here anymore) */}
        <div className="border-b border-slate-200 dark:border-slate-800 pb-8">
          {header}
        </div>
        {children}
      </div>

      {/* ── Sticky Quick Navigation ── */}
      <aside className="hidden lg:block w-56 flex-shrink-0 sticky top-5 self-start">
        <div className="bg-slate-50 dark:bg-slate-900/60 border border-slate-200 dark:border-slate-800 rounded-xl p-4">
          <p className="text-[11px] uppercase tracking-wider text-slate-500 mb-3 font-semibold">
            Quick Navigation
          </p>
          <ul className="space-y-0.5">
            {nav.map(({ num, label, href }) => (
              <li key={num}>
                <a
                  href={href}
                  className="flex items-center gap-2 px-2 py-1.5 rounded-lg text-sm text-slate-600 dark:text-slate-400 hover:text-indigo-600 dark:hover:text-indigo-400 hover:bg-slate-100 dark:hover:bg-slate-800 transition-colors"
                >
                  <span className="inline-flex items-center justify-center w-4 h-4 rounded bg-indigo-100 dark:bg-indigo-500/20 text-indigo-600 dark:text-indigo-300 text-[10px] font-bold flex-shrink-0">
                    {num}
                  </span>
                  <span className="truncate">{label}</span>
                </a>
              </li>
            ))}
          </ul>
        </div>
      </aside>

    </div>
  )
}
