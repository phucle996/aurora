import { useEffect, useRef } from 'react'
import { gsap } from 'gsap'
import { ScrollTrigger } from 'gsap/ScrollTrigger'
import PageLayout from '../../components/PageLayout.jsx'

gsap.registerPlugin(ScrollTrigger)

const CONTAINERS = [
  {
    name: 'NewApplication', tech: 'Go Process', color: 'border-emerald-500/40 bg-emerald-500/5', badge: 'bg-emerald-500/20 text-emerald-300',
    desc: 'Entry point — wires module graph, HTTP & gRPC servers. Implements Tier-0 fail-close and Tier-1 fail-open bootstrap policy.',
    icon: '⚡',
  },
  {
    name: 'HTTP Engine', tech: 'gin.Engine', color: 'border-sky-500/40 bg-sky-500/5', badge: 'bg-sky-500/20 text-sky-300',
    desc: 'Serves REST API via NewGlobalRoutes. Tier-0 and Tier-1 module routes wired at startup.',
    icon: '🌐',
  },
  {
    name: 'gRPC Server', tech: 'google.golang.org/grpc', color: 'border-violet-500/40 bg-violet-500/5', badge: 'bg-violet-500/20 text-violet-300',
    desc: 'Dataplane registration and heartbeat over mTLS.',
    icon: '📡',
  },
  {
    name: 'Module Graph', tech: 'NewGlobalModules', color: 'border-amber-500/40 bg-amber-500/5', badge: 'bg-amber-500/20 text-amber-300',
    desc: 'Bootstraps Tier-0 (Core, IAM, PolicyEngine) and Tier-1 (Hypervisor, Mail) modules with tier policy applied at startup.',
    icon: '🧩',
  },
]

export default function ArchC2Container() {
  const titleRef = useRef(null)

  const nav = [
    { num: 1, label: 'Application Containers', href: '#c2-containers' },
    { num: 2, label: 'External Dependencies', href: '#c2-ext' },
    { num: 3, label: 'Startup Contract', href: '#c2-startup' },
  ]

  useEffect(() => {
    gsap.fromTo(titleRef.current,
      { opacity: 0, y: -20 },
      { opacity: 1, y: 0, duration: 0.7, ease: 'power3.out' }
    )

    // Animating app containers with ScrollTrigger
    const containerCards = gsap.utils.toArray('.app-container-card')
    containerCards.forEach((card, index) => {
      gsap.fromTo(card,
        { opacity: 0, y: 28, scale: 0.97 },
        {
          opacity: 1,
          y: 0,
          scale: 1,
          duration: 0.55,
          delay: index * 0.1,
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
          <span className="inline-flex items-center justify-center w-12 h-12 rounded-2xl bg-gradient-to-br from-slate-200 to-slate-300 dark:from-slate-600 dark:to-slate-800 border border-slate-300 dark:border-slate-600 text-2xl shadow-lg">
            📦
          </span>
          <div>
            <div className="flex items-center gap-2 mb-1">
              <span className="text-[10px] font-mono px-2 py-0.5 rounded-full bg-emerald-500/20 text-emerald-600 dark:text-emerald-300 border border-emerald-500/30 uppercase tracking-widest">
                C4 · Level 2
              </span>
            </div>
            <h1 className="text-3xl font-bold text-slate-850 dark:text-slate-100">Container Diagram</h1>
            <p className="text-slate-600 dark:text-slate-400 text-sm mt-0.5">
              Internal structure of the Controlplane Go process and its runtime dependencies.
            </p>
          </div>
        </div>
      </div>
    }>

      <section id="c2-containers" className="scroll-mt-6">
        <h2 className="text-lg font-bold text-slate-805 dark:text-slate-100 mb-4 flex items-center gap-2">
          <span className="w-6 h-6 rounded-lg bg-indigo-500/20 border border-indigo-500/30 text-indigo-600 dark:text-indigo-300 text-xs flex items-center justify-center font-bold">1</span>
          Application Containers
        </h2>
        <div className="grid md:grid-cols-2 gap-4">
          {CONTAINERS.map((c, i) => (
            <div key={i} className={`app-container-card opacity-0 border rounded-2xl p-5 ${c.color} hover:shadow-lg transition-all`}>
              <div className="flex items-center gap-2 mb-3">
                <span className="text-2xl">{c.icon}</span>
                <div>
                  <p className="font-bold text-slate-800 dark:text-slate-100">{c.name}</p>
                  <span className={`text-[10px] font-mono px-1.5 py-0.5 rounded ${c.badge}`}>{c.tech}</span>
                </div>
              </div>
              <p className="text-sm text-slate-600 dark:text-slate-400 leading-relaxed">{c.desc}</p>
            </div>
          ))}
        </div>
      </section>

      <section id="c2-ext" className="scroll-mt-6">
        <h2 className="text-lg font-bold text-slate-805 dark:text-slate-100 mb-4 flex items-center gap-2">
          <span className="w-6 h-6 rounded-lg bg-indigo-500/20 border border-indigo-500/30 text-indigo-600 dark:text-indigo-300 text-xs flex items-center justify-center font-bold">2</span>
          External Dependencies
        </h2>
        <div className="grid sm:grid-cols-3 gap-3">
          {[
            { name: 'PostgreSQL 17', desc: 'Persistent store — all module data', icon: '🐘', color: 'border-amber-500/30' },
            { name: 'Redis', desc: 'Cache · rate-limit counters · pub/sub fanout bus', icon: '💾', color: 'border-rose-500/30' },
            { name: 'PolicyEngine', desc: 'Injected as Tier-0 — RBAC · rate-limit · AdminCIDR', icon: '🔒', color: 'border-violet-500/30' },
          ].map((e, i) => (
            <div key={i} className={`border rounded-xl p-4 bg-white dark:bg-slate-900/60 border-slate-200 dark:border-slate-800/80 ${e.color}`}>
              <p className="text-2xl mb-2">{e.icon}</p>
              <p className="font-semibold text-slate-800 dark:text-slate-100 text-sm">{e.name}</p>
              <p className="text-xs text-slate-600 dark:text-slate-400 mt-1">{e.desc}</p>
            </div>
          ))}
        </div>
      </section>

      <section id="c2-startup" className="scroll-mt-6">
        <h2 className="text-lg font-bold text-slate-805 dark:text-slate-100 mb-4 flex items-center gap-2">
          <span className="w-6 h-6 rounded-lg bg-indigo-500/20 border border-indigo-500/30 text-indigo-600 dark:text-indigo-300 text-xs flex items-center justify-center font-bold">3</span>
          Startup Contract
        </h2>
        <div className="space-y-2">
          {[
            { tier: 'Tier-0', color: 'bg-red-500/20 text-red-700 dark:text-red-300 border-red-500/30', policy: 'FAIL-CLOSE', desc: 'Core, IAM, PolicyEngine, gin.Engine — any init failure crashes the process immediately.' },
            { tier: 'Tier-1', color: 'bg-amber-500/20 text-amber-700 dark:text-amber-300 border-amber-500/30', policy: 'FAIL-OPEN', desc: 'Hypervisor, Mail — init failure logs the error and injects a Null Object (returns HTTP 503 for those routes).' },
          ].map((t, i) => (
            <div key={i} className="flex gap-4 items-start p-4 rounded-xl border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900/60">
              <span className={`text-[10px] font-mono font-bold px-2 py-1 rounded border whitespace-nowrap ${t.color}`}>{t.tier}</span>
              <span className={`text-[10px] font-mono px-2 py-1 rounded border whitespace-nowrap self-start ${t.color}`}>{t.policy}</span>
              <p className="text-sm text-slate-600 dark:text-slate-400">{t.desc}</p>
            </div>
          ))}
        </div>
      </section>

    </PageLayout>
  )
}
