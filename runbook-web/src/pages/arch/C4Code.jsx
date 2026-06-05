import { useEffect, useRef } from 'react'
import { gsap } from 'gsap'
import { ScrollTrigger } from 'gsap/ScrollTrigger'
import PageLayout from '../../components/PageLayout.jsx'

gsap.registerPlugin(ScrollTrigger)

const CHAIN = [
  { step: 'NewGlobalModules', role: 'Caller', icon: '🌐', color: 'border-indigo-500/40 bg-indigo-500/5', badge: 'bg-indigo-500/20 text-indigo-300' },
  { step: 'NewModule', role: 'Module constructor', icon: '🏗️', color: 'border-slate-600/40 bg-slate-800/40', badge: 'bg-slate-700 text-slate-300' },
  { step: 'NewRepository', role: 'nil check → return err', icon: '🐘', color: 'border-amber-500/40 bg-amber-500/5', badge: 'bg-amber-500/20 text-amber-300' },
  { step: 'NewCache / FanoutBus', role: 'nil check → return err', icon: '💾', color: 'border-amber-500/40 bg-amber-500/5', badge: 'bg-amber-500/20 text-amber-300' },
  { step: 'NewService', role: 'panic if repo or cache nil', icon: '⚙️', color: 'border-emerald-500/40 bg-emerald-500/5', badge: 'bg-emerald-500/20 text-emerald-300' },
  { step: 'NewHandler', role: 'panic if service nil', icon: '🎯', color: 'border-emerald-500/40 bg-emerald-500/5', badge: 'bg-emerald-500/20 text-emerald-300' },
]

const GUARDS = [
  { location: 'NewRepository', guard: 'repo == nil', action: 'return nil, err', strategy: 'FAIL-CLOSE', color: 'text-red-400' },
  { location: 'NewCache', guard: 'cache == nil', action: 'return nil, err', strategy: 'FAIL-CLOSE', color: 'text-red-400' },
  { location: 'NewService', guard: 'repo or cache nil', action: 'panic()', strategy: 'PANIC', color: 'text-orange-400' },
  { location: 'NewHandler', guard: 'service == nil', action: 'panic()', strategy: 'PANIC', color: 'text-orange-400' },
  { location: 'module/route.go', guard: '(none — invariant)', action: 'NO nil guards', strategy: 'CONTRACT', color: 'text-emerald-400' },
]

export default function ArchC4Code() {
  const titleRef = useRef(null)
  const nav = [
    { num: 1, label: 'Constructor Chain', href: '#c4-chain' },
    { num: 2, label: 'Fail-Fast Guards', href: '#c4-guards' },
    { num: 3, label: 'Invariants', href: '#c4-invariants' },
  ]

  useEffect(() => {
    gsap.fromTo(titleRef.current, { opacity: 0, y: -20 }, { opacity: 1, y: 0, duration: 0.7, ease: 'power3.out' })

    // Animating constructor chain steps with ScrollTrigger
    const stepCards = gsap.utils.toArray('.chain-step-card')
    stepCards.forEach((card, index) => {
      gsap.fromTo(card,
        { opacity: 0, x: -32 },
        {
          opacity: 1,
          x: 0,
          duration: 0.45,
          delay: index * 0.07,
          ease: 'power3.out',
          scrollTrigger: {
            trigger: card,
            start: 'top 92%',
            toggleActions: 'play none none none'
          }
        }
      )
    })
  }, [])

  return (
    <PageLayout nav={nav} header={
      <div ref={titleRef} className="opacity-0">
        <div className="flex items-center gap-3 mb-4">
          <span className="inline-flex items-center justify-center w-12 h-12 rounded-2xl bg-gradient-to-br from-slate-200 to-slate-300 dark:from-slate-600 dark:to-slate-800 border border-slate-300 dark:border-slate-600 text-2xl shadow-lg">⌨️</span>
          <div>
            <div className="flex items-center gap-2 mb-1">
              <span className="text-[10px] font-mono px-2 py-0.5 rounded-full bg-rose-500/20 text-rose-600 dark:text-rose-300 border border-rose-500/30 uppercase tracking-widest">C4 · Level 4</span>
            </div>
            <h1 className="text-3xl font-bold text-slate-850 dark:text-slate-100">Code — Constructor Pattern</h1>
            <p className="text-slate-600 dark:text-slate-400 text-sm mt-0.5">
              How each module chains <code className="font-mono text-rose-600 dark:text-rose-300">repo → cache → service → handler</code> with fail-fast guards.
            </p>
          </div>
        </div>
      </div>
    }>

      <section id="c4-chain" className="scroll-mt-6">
        <h2 className="text-lg font-bold text-slate-850 dark:text-slate-100 mb-4 flex items-center gap-2">
          <span className="w-6 h-6 rounded-lg bg-indigo-500/20 border border-indigo-500/30 text-indigo-600 dark:text-indigo-300 text-xs flex items-center justify-center font-bold">1</span>
          Constructor Dependency Chain
        </h2>
        <div className="flex flex-col gap-0">
          {CHAIN.map((c, i) => (
            <div key={i} className="flex flex-col items-start w-full">
              <div className={`chain-step-card opacity-0 w-full border rounded-xl p-4 bg-white dark:bg-slate-900/60 ${c.color} flex items-center gap-3`}>
                <span className="text-xl">{c.icon}</span>
                <div className="flex-1">
                  <p className="font-mono font-bold text-slate-800 dark:text-slate-100 text-sm">{c.step}</p>
                  <span className={`text-[10px] font-mono px-2 py-0.5 rounded ${c.badge} mt-1 inline-block`}>{c.role}</span>
                </div>
                <span className="text-xs font-mono text-slate-500">step {i + 1}</span>
              </div>
              {i < CHAIN.length - 1 && (
                <div className="ml-7 flex flex-col items-center">
                  <div className="w-px h-4 bg-slate-300 dark:bg-slate-700" />
                  <svg className="w-3 h-3 text-slate-400 dark:text-slate-650" fill="currentColor" viewBox="0 0 24 24">
                    <path d="M12 16l-6-6h12l-6 6z" />
                  </svg>
                </div>
              )}
            </div>
          ))}
        </div>
      </section>

      <section id="c4-guards" className="scroll-mt-6">
        <h2 className="text-lg font-bold text-slate-850 dark:text-slate-100 mb-4 flex items-center gap-2">
          <span className="w-6 h-6 rounded-lg bg-indigo-500/20 border border-indigo-500/30 text-indigo-600 dark:text-indigo-300 text-xs flex items-center justify-center font-bold">2</span>
          Fail-Fast Guards Map
        </h2>
        <div className="overflow-x-auto rounded-xl border border-slate-200 dark:border-slate-800">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-slate-200 dark:border-slate-800 bg-slate-50 dark:bg-slate-900/80">
                <th className="text-left px-4 py-3 text-xs uppercase tracking-widest text-slate-500 font-semibold">Location</th>
                <th className="text-left px-4 py-3 text-xs uppercase tracking-widest text-slate-500 font-semibold">Guard Condition</th>
                <th className="text-left px-4 py-3 text-xs uppercase tracking-widest text-slate-500 font-semibold">Action</th>
                <th className="text-left px-4 py-3 text-xs uppercase tracking-widest text-slate-500 font-semibold">Strategy</th>
              </tr>
            </thead>
            <tbody>
              {GUARDS.map((g, i) => (
                <tr key={i} className="border-b border-slate-200 dark:border-slate-800/60 hover:bg-slate-100 dark:hover:bg-slate-800/30 transition-colors">
                  <td className="px-4 py-3 font-mono text-slate-700 dark:text-slate-300 text-xs">{g.location}</td>
                  <td className="px-4 py-3 font-mono text-xs text-slate-600 dark:text-slate-400">{g.guard}</td>
                  <td className="px-4 py-3 font-mono text-xs text-slate-600 dark:text-slate-400">{g.action}</td>
                  <td className={`px-4 py-3 font-mono text-xs font-bold ${g.color}`}>{g.strategy}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>

      <section id="c4-invariants" className="scroll-mt-6">
        <h2 className="text-lg font-bold text-slate-850 dark:text-slate-100 mb-4 flex items-center gap-2">
          <span className="w-6 h-6 rounded-lg bg-indigo-500/20 border border-indigo-500/30 text-indigo-600 dark:text-indigo-300 text-xs flex items-center justify-center font-bold">3</span>
          Code Invariants
        </h2>
        <div className="space-y-3">
          {[
            { id: 'INV-001', text: 'Fail-fast decisions MUST happen at construction time, not at request-handling time.', color: 'border-red-500/30 bg-red-500/5 text-red-750 dark:text-red-300' },
            { id: 'INV-002', text: 'NewGlobalRoutes / module.RegisterRoutes are guaranteed to receive valid, non-nil modules and handler instances.', color: 'border-emerald-500/30 bg-emerald-500/5 text-emerald-700 dark:text-emerald-300' },
            { id: 'INV-003', text: 'No nil-check or silent return is allowed inside route registration definitions (module/route.go). Late-stage silent checks mask misconfigurations.', color: 'border-amber-500/30 bg-amber-500/5 text-amber-700 dark:text-amber-300' },
          ].map((inv, i) => (
            <div key={i} className={`flex gap-3 p-4 rounded-xl border bg-white dark:bg-slate-900/60 ${inv.color}`}>
              <span className="font-mono text-[10px] font-bold whitespace-nowrap self-start mt-0.5">{inv.id}</span>
              <p className="text-sm text-slate-700 dark:text-slate-300 leading-relaxed">{inv.text}</p>
            </div>
          ))}
        </div>
      </section>

    </PageLayout>
  )
}
