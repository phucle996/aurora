import { useState } from 'react'

export default function CodeBlock({ code, language = 'text' }) {
  const [copied, setCopied] = useState(false)

  const onCopy = async () => {
    try {
      await navigator.clipboard.writeText(code)
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    } catch (_) {
      // ignore
    }
  }

  return (
    <div className="relative my-4 rounded-lg border border-slate-200 dark:border-slate-800 bg-slate-50 dark:bg-slate-950 overflow-hidden">
      <div className="flex items-center justify-between px-4 py-2 border-b border-slate-200 dark:border-slate-800 bg-slate-100 dark:bg-slate-900/60">
        <span className="text-xs uppercase tracking-wider text-slate-500">
          {language}
        </span>
        <button
          onClick={onCopy}
          className="text-xs text-slate-500 dark:text-slate-400 hover:text-slate-900 dark:hover:text-slate-100 transition-colors"
        >
          {copied ? '✓ Copied' : 'Copy'}
        </button>
      </div>
      <pre className="px-4 py-3 overflow-x-auto text-sm leading-relaxed">
        <code className="text-slate-800 dark:text-slate-200 font-mono whitespace-pre">
          {code}
        </code>
      </pre>
    </div>
  )
}
