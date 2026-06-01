const STYLES = {
  info: {
    border: 'border-indigo-500/40 dark:border-indigo-500/40',
    bg: 'bg-indigo-50 dark:bg-indigo-500/10',
    accent: 'text-indigo-700 dark:text-indigo-300',
    icon: 'ℹ️',
  },
  warning: {
    border: 'border-amber-500/40 dark:border-amber-500/40',
    bg: 'bg-amber-50 dark:bg-amber-500/10',
    accent: 'text-amber-700 dark:text-amber-300',
    icon: '⚠️',
  },
  danger: {
    border: 'border-red-500/40 dark:border-red-500/40',
    bg: 'bg-red-50 dark:bg-red-500/10',
    accent: 'text-red-700 dark:text-red-300',
    icon: '🛡️',
  },
  success: {
    border: 'border-emerald-500/40 dark:border-emerald-500/40',
    bg: 'bg-emerald-50 dark:bg-emerald-500/10',
    accent: 'text-emerald-700 dark:text-emerald-300',
    icon: '✅',
  },
}

export default function Callout({ type = 'info', title, children }) {
  const s = STYLES[type] || STYLES.info
  return (
    <div className={`${s.bg} ${s.border} border-l-4 rounded-r-lg p-4 my-4`}>
      {title && (
        <p className={`${s.accent} font-semibold mb-1.5 flex items-center gap-2`}>
          <span>{s.icon}</span>
          <span>{title}</span>
        </p>
      )}
      <div className="text-slate-700 dark:text-slate-300 text-base">{children}</div>
    </div>
  )
}
