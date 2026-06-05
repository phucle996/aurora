import { useEffect, useRef } from 'react'
import { gsap } from 'gsap'
import { ScrollTrigger } from 'gsap/ScrollTrigger'
import PageLayout from '../../components/PageLayout.jsx'

gsap.registerPlugin(ScrollTrigger)

const TIER0 = [
  { name: 'Core Module', role: 'Tier-0 — FAIL-CLOSE', desc: 'Runtime secret provider, security keys', icon: '🔐', color: 'border-red-500/40 bg-red-500/5', badge: 'bg-red-500/20 text-red-300' },
  { name: 'IAM Module', role: 'Tier-0 — FAIL-CLOSE', desc: 'Auth, tokens, admin API-key lifecycle', icon: '🪪', color: 'border-red-500/40 bg-red-500/5', badge: 'bg-red-500/20 text-red-300' },
  { name: 'PolicyEngine', role: 'Tier-0 — FAIL-CLOSE', desc: 'RBAC, rate-limit, AdminCIDR enforcement', icon: '🔒', color: 'border-red-500/40 bg-red-500/5', badge: 'bg-red-500/20 text-red-300' },
]

const TIER1 = [
  { name: 'Hypervisor Module', role: 'Tier-1 — FAIL-OPEN', desc: 'VM orchestration — degrades to HTTP 503', icon: '🖥️', color: 'border-amber-500/40 bg-amber-500/5', badge: 'bg-amber-500/20 text-amber-300' },
  { name: 'Mail Module', role: 'Tier-1 — FAIL-OPEN', desc: 'Email delivery — degrades to HTTP 503', icon: '📧', color: 'border-amber-500/40 bg-amber-500/5', badge: 'bg-amber-500/20 text-amber-300' },
]

const ROUTES = [
  { name: 'Health Routes', tech: 'Direct — router.GET', desc: 'Liveness, Readiness, Startup probes', icon: '❤️', color: 'border-emerald-500/30' },
  { name: 'Tier-0 Routes', tech: 'module.RegisterRoutes', desc: 'Core, IAM — guaranteed non-nil handlers', icon: '⚡', color: 'border-red-500/30' },
  { name: 'Tier-1 Routes', tech: 'RegisterRoutes / 503 Fallback', desc: 'Active routes or apires.503 fallback', icon: '🟡', color: 'border-amber-500/30' },
]

export default function ArchC3Component() {
  const titleRef = useRef(null)
  const nav = [
    { num: 1, label: 'Module Graph Components', href: '#c3-modules' },
    { num: 2, label: 'Route Orchestrator', href: '#c3-routes' },
    { num: 3, label: 'Degradation Flow', href: '#c3-degrade' },
  ]

  useEffect(() => {
    gsap.fromTo(titleRef.current, { opacity: 0, y: -20 }, { opacity: 1, y: 0, duration: 0.7, ease: 'power3.out' })

    // Animating tier-0 cards with ScrollTrigger
    const tier0Cards = gsap.utils.toArray('.tier0-module-card')
    tier0Cards.forEach((card, index) => {
      gsap.fromTo(card,
        { opacity: 0, y: 24 },
        {
          opacity: 1,
          y: 0,
          duration: 0.5,
          delay: index * 0.09,
          ease: 'power3.out',
          scrollTrigger: {
            trigger: card,
            start: 'top 90%',
            toggleActions: 'play none none none'
          }
        }
      )
    })

    // Animating tier-1 cards with ScrollTrigger
    const tier1Cards = gsap.utils.toArray('.tier1-module-card')
    tier1Cards.forEach((card, index) => {
      gsap.fromTo(card,
        { opacity: 0, y: 24 },
        {
          opacity: 1,
          y: 0,
          duration: 0.5,
          delay: index * 0.09,
          ease: 'power3.out',
          scrollTrigger: {
            trigger: card,
            start: 'top 90%',
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
          <span className="inline-flex items-center justify-center w-12 h-12 rounded-2xl bg-gradient-to-br from-slate-200 to-slate-300 dark:from-slate-600 dark:to-slate-800 border border-slate-300 dark:border-slate-600 text-2xl shadow-lg">🧩</span>
          <div>
            <div className="flex items-center gap-2 mb-1">
              <span className="text-[10px] font-mono px-2 py-0.5 rounded-full bg-violet-500/20 text-violet-600 dark:text-violet-300 border border-violet-500/30 uppercase tracking-widest">C4 · Level 3</span>
            </div>
            <h1 className="text-3xl font-bold text-slate-850 dark:text-slate-100">Component Diagram</h1>
            <p className="text-slate-650 dark:text-slate-400 text-sm mt-0.5">Module Graph internals — tier classification and route orchestrator structure.</p>
          </div>
        </div>
      </div>
    }>

      <section id="c3-modules" className="scroll-mt-6">
        <h2 className="text-lg font-bold text-slate-805 dark:text-slate-100 mb-4 flex items-center gap-2">
          <span className="w-6 h-6 rounded-lg bg-indigo-500/20 border border-indigo-500/30 text-indigo-600 dark:text-indigo-300 text-xs flex items-center justify-center font-bold">1</span>
          Module Graph Components
        </h2>

        <p className="text-xs uppercase tracking-widest text-red-600 dark:text-red-400 font-semibold mb-3">Tier-0 — Critical · Fail-Close</p>
        <div className="grid md:grid-cols-3 gap-3 mb-6">
          {TIER0.map((m, i) => (
            <div key={i} className={`tier0-module-card opacity-0 border rounded-2xl p-4 bg-white dark:bg-slate-900/60 ${m.color}`}>
              <div className="flex items-center gap-2 mb-2">
                <span className="text-xl">{m.icon}</span>
                <div>
                  <p className="font-bold text-slate-800 dark:text-slate-100 text-sm">{m.name}</p>
                  <span className={`text-[9px] font-mono px-1.5 py-0.5 rounded ${m.badge}`}>{m.role}</span>
                </div>
              </div>
              <p className="text-xs text-slate-600 dark:text-slate-400">{m.desc}</p>
            </div>
          ))}
        </div>

        <p className="text-xs uppercase tracking-widest text-amber-600 dark:text-amber-400 font-semibold mb-3">Tier-1 — Non-Critical · Fail-Open</p>
        <div className="grid md:grid-cols-2 gap-3">
          {TIER1.map((m, i) => (
            <div key={i} className={`tier1-module-card opacity-0 border rounded-2xl p-4 bg-white dark:bg-slate-900/60 ${m.color}`}>
              <div className="flex items-center gap-2 mb-2">
                <span className="text-xl">{m.icon}</span>
                <div>
                  <p className="font-bold text-slate-800 dark:text-slate-100 text-sm">{m.name}</p>
                  <span className={`text-[9px] font-mono px-1.5 py-0.5 rounded ${m.badge}`}>{m.role}</span>
                </div>
              </div>
              <p className="text-xs text-slate-600 dark:text-slate-400">{m.desc}</p>
            </div>
          ))}
        </div>
      </section>

      <section id="c3-routes" className="scroll-mt-6">
        <h2 className="text-lg font-bold text-slate-805 dark:text-slate-100 mb-4 flex items-center gap-2">
          <span className="w-6 h-6 rounded-lg bg-indigo-500/20 border border-indigo-500/30 text-indigo-600 dark:text-indigo-300 text-xs flex items-center justify-center font-bold">2</span>
          Route Orchestrator — <code className="text-sm font-mono text-emerald-600 dark:text-emerald-400 ml-1">NewGlobalRoutes</code>
        </h2>
        <div className="grid md:grid-cols-3 gap-3">
          {ROUTES.map((r, i) => (
            <div key={i} className={`border rounded-xl p-4 bg-white dark:bg-slate-900/60 border-slate-200 dark:border-slate-800/85 ${r.color}`}>
              <span className="text-2xl mb-2 block">{r.icon}</span>
              <p className="font-semibold text-slate-800 dark:text-slate-100 text-sm">{r.name}</p>
              <p className="text-[10px] font-mono text-slate-500 mt-1">{r.tech}</p>
              <p className="text-xs text-slate-600 dark:text-slate-400 mt-2">{r.desc}</p>
            </div>
          ))}
        </div>
        <div className="mt-4 p-3 rounded-xl border border-emerald-500/20 bg-emerald-500/5 text-xs text-emerald-700 dark:text-emerald-305 font-mono">
          ✓ Invariant — NO nil guards allowed inside module/route.go
        </div>
      </section>

      <section id="c3-degrade" className="scroll-mt-6">
        <h2 className="text-lg font-bold text-slate-850 dark:text-slate-100 mb-4 flex items-center gap-2">
          <span className="w-6 h-6 rounded-lg bg-indigo-500/20 border border-indigo-500/30 text-indigo-600 dark:text-indigo-300 text-xs flex items-center justify-center font-bold">3</span>
          Degradation Flow
        </h2>
        <div className="space-y-2 text-sm font-mono">
          {[
            { label: 'Tier-1 init fails', action: 'log.Error + Suppress', result: 'Inject NullObject (Null Object Pattern)', color: 'text-amber-600 dark:text-amber-400' },
            { label: 'NullObject injected', action: 'module.IsEnabled() → false', result: 'Route returns apires.HTTP503', color: 'text-amber-600 dark:text-amber-400' },
            { label: 'Health endpoint', action: '/readiness check', result: 'Reports partial degradation', color: 'text-sky-600 dark:text-sky-400' },
            { label: 'Tier-0 init fails', action: 'return nil, err', result: 'app.Stop() → process crash', color: 'text-red-600 dark:text-red-400' },
          ].map((row, i) => (
            <div key={i} className="flex flex-wrap gap-2 items-center p-3 rounded-lg border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900/60">
              <span className="text-slate-700 dark:text-slate-300 whitespace-nowrap">{row.label}</span>
              <span className="text-slate-400 dark:text-slate-600">→</span>
              <span className="text-slate-500 whitespace-nowrap">{row.action}</span>
              <span className="text-slate-400 dark:text-slate-600">→</span>
              <span className={`whitespace-nowrap ${row.color}`}>{row.result}</span>
            </div>
          ))}
        </div>
      </section>

    </PageLayout>
  )
}
