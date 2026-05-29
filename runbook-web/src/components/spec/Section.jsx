export default function Section({ number, title, children }) {
  return (
    <section id={`section-${number}`} className="scroll-mt-24">
      <div className="flex items-center gap-3 mb-5 pb-3 border-b-2 border-slate-200 dark:border-slate-800">
        <span className="inline-flex items-center justify-center w-9 h-9 rounded-lg bg-gradient-to-br from-indigo-500 to-purple-500 text-white font-bold text-sm">
          {number}
        </span>
        <h2 className="text-2xl font-bold text-slate-900 dark:text-slate-100">{title}</h2>
      </div>
      <div className="space-y-3 text-slate-700 dark:text-slate-300 leading-relaxed">{children}</div>
    </section>
  )
}
