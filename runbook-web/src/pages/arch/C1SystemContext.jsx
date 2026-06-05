import { useEffect, useRef } from 'react'
import { gsap } from 'gsap'
import { ScrollTrigger } from 'gsap/ScrollTrigger'
import PageLayout from '../../components/PageLayout.jsx'
import TelemetryCanvas from '../../components/TelemetryCanvas.jsx'
import TopologyCanvas from '../../components/TopologyCanvas.jsx'
import TopologySpec from '../../components/TopologySpec.jsx'

gsap.registerPlugin(ScrollTrigger)

// ── Data ─────────────────────────────────────────────────────────────────────

const ACTORS = [
  { icon: '🔑', label: 'Admin', desc: 'Manages zones, secrets, IAM keys via Admin Portal', color: 'from-blue-500 to-indigo-600' },
  { icon: '☁️', label: 'Cloud Tenant', desc: 'Self-service cloud resource management via Cloud UI', color: 'from-sky-500 to-blue-600' },
  { icon: '🔧', label: 'SRE Engineer', desc: 'Monitors health, operates runbooks, manages deployment', color: 'from-violet-500 to-purple-600' },
]
const INTEGRATIONS = [
  { id: 'CTXT-INT-001', title: 'All traffic through Envoy', color: 'border-orange-500/40', badge: 'bg-orange-500/20 text-orange-700 dark:text-orange-300', desc: 'No CP node exposes HTTP/gRPC directly. Admin UI, Cloud UI, Runbook must target Envoy (:443/:80).' },
  { id: 'CTXT-INT-002', title: 'CP → PgBouncer only', color: 'border-amber-500/40', badge: 'bg-amber-500/20 text-amber-700 dark:text-amber-300', desc: 'All DB connections must go through PgBouncer (:6432). Direct PostgreSQL (:15434) is debug-only.' },
  { id: 'CTXT-INT-003', title: 'Zone Redis isolation', color: 'border-purple-500/40', badge: 'bg-purple-500/20 text-purple-700 dark:text-purple-300', desc: 'redis-dataplane-z1/z2 are zone-exclusive. CP must not write to zone-local Redis. Cross-zone data via Redis Job or PostgreSQL.' },
  { id: 'CTXT-INT-004', title: 'Dataplane → assigned CP node', color: 'border-emerald-500/40', badge: 'bg-emerald-500/20 text-emerald-700 dark:text-emerald-300', desc: 'z1-n1→cp1 · z2-n1→cp2 · z2-n2→cp3. Prevents all nodes flooding a single CP instance.' },
  { id: 'CTXT-INT-005', title: 'Observability is non-blocking', color: 'border-rose-500/40', badge: 'bg-rose-500/20 text-rose-700 dark:text-rose-300', desc: 'CP must boot even if OTel Collector is unavailable. Telemetry failures are silently dropped.' },
]

// ── Main Page ─────────────────────────────────────────────────────────────────

export default function ArchC1SystemContext() {
  const heroRef = useRef(null)
  const titleRef = useRef(null)

  const nav = [
    { num: 1, label: 'Human Actors', href: '#c1-actors' },
    { num: 2, label: 'System Topology', href: '#c1-topology' },
    { num: 3, label: 'Observability Workflow Arch', href: '#c1-telemetry' },
    { num: 4, label: 'Integration Contracts', href: '#c1-contracts' },
  ]

  useEffect(() => {
    const tl = gsap.timeline()
    tl.fromTo(titleRef.current,
      { opacity: 0, y: -20 },
      { opacity: 1, y: 0, duration: 0.7, ease: 'power3.out' }
    )
    tl.fromTo(heroRef.current?.querySelectorAll('.hero-badge') ?? [],
      { opacity: 0, x: -16 },
      { opacity: 1, x: 0, duration: 0.4, stagger: 0.1, ease: 'power2.out' },
      '-=0.3'
    )

    // Animating actors with ScrollTrigger
    const actorCards = gsap.utils.toArray('.actor-card')
    actorCards.forEach((card, index) => {
      gsap.fromTo(card,
        { opacity: 0, y: 24 },
        {
          opacity: 1,
          y: 0,
          duration: 0.5,
          delay: index * 0.1,
          ease: 'power3.out',
          scrollTrigger: {
            trigger: card,
            start: 'top 88%',
            toggleActions: 'play none none none'
          }
        }
      )
    })

    // Animating integrations with ScrollTrigger
    const integrationCards = gsap.utils.toArray('.integration-card')
    integrationCards.forEach((card, index) => {
      gsap.fromTo(card,
        { opacity: 0, x: -24 },
        {
          opacity: 1,
          x: 0,
          duration: 0.5,
          delay: index * 0.07,
          ease: 'power3.out',
          scrollTrigger: {
            trigger: card,
            start: 'top 90%',
            toggleActions: 'play none none none'
          }
        }
      )
    })

    return () => {
      tl.kill()
    }
  }, [])

  return (
    <PageLayout nav={nav} header={
      <div ref={titleRef} className="opacity-0">
        <div className="flex items-center gap-3 mb-4">
          <span className="inline-flex items-center justify-center w-12 h-12 rounded-2xl bg-gradient-to-br from-slate-200 to-slate-300 dark:from-slate-600 dark:to-slate-800 border border-slate-300 dark:border-slate-600 text-2xl shadow-lg">
            🗺️
          </span>
          <div>
            <div className="flex items-center gap-2 mb-1">
              <span className="text-[10px] font-mono px-2 py-0.5 rounded-full bg-indigo-500/20 text-indigo-600 dark:text-indigo-300 border border-indigo-500/30 uppercase tracking-widest">
                C4 · Level 1
              </span>
            </div>
            <h1 className="text-3xl font-bold text-slate-800 dark:text-slate-100">System Context</h1>
            <p className="text-slate-600 dark:text-slate-400 text-sm mt-0.5">
              Who uses the platform, what it depends on — Controlplane as a black box.
            </p>
          </div>
        </div>

        <div ref={heroRef} className="flex flex-wrap gap-3">
          {[
            { value: '3', label: 'CP Nodes', color: 'border-emerald-500/30 bg-emerald-500/5' },
            { value: '2', label: 'DP Zones', color: 'border-purple-500/30 bg-purple-500/5' },
            { value: '3', label: 'Client UIs', color: 'border-sky-500/30 bg-sky-500/5' },
            { value: '5', label: 'INT Contracts', color: 'border-orange-500/30 bg-orange-500/5' },
          ].map((s, i) => (
            <div key={i} className={`hero-badge flex flex-col items-center px-4 py-2.5 rounded-xl border ${s.color}`}>
              <span className="text-xl font-bold text-slate-800 dark:text-slate-100">{s.value}</span>
              <span className="text-[10px] text-slate-500 dark:text-slate-400 mt-0.5">{s.label}</span>
            </div>
          ))}
        </div>
      </div>
    }>

      {/* ── Section 1: Human Actors ── */}
      <section id="c1-actors" className="scroll-mt-6">
        <h2 className="text-lg font-bold text-slate-800 dark:text-slate-100 mb-4 flex items-center gap-2">
          <span className="w-6 h-6 rounded-lg bg-indigo-500/20 border border-indigo-500/30 text-indigo-600 dark:text-indigo-300 text-xs flex items-center justify-center font-bold">1</span>
          Human Actors
        </h2>
        <div className="grid md:grid-cols-3 gap-4">
          {ACTORS.map((a, i) => (
            <div key={i} className="actor-card group relative bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-2xl p-5 hover:border-slate-400 dark:hover:border-slate-600 transition-all duration-300 hover:shadow-lg hover:shadow-black/5 dark:hover:shadow-black/20">
              <div className={`absolute inset-0 rounded-2xl opacity-0 group-hover:opacity-100 transition-opacity duration-300 bg-gradient-to-br ${a.color} opacity-[0.03] dark:opacity-[0.05]`} />
              <div className="flex items-center gap-3 mb-3">
                <span className={`w-10 h-10 rounded-xl bg-gradient-to-br ${a.color} flex items-center justify-center text-xl shadow-md text-white`}>
                  {a.icon}
                </span>
                <span className="font-bold text-slate-800 dark:text-slate-100">{a.label}</span>
              </div>
              <p className="text-sm text-slate-600 dark:text-slate-400 leading-relaxed">{a.desc}</p>
            </div>
          ))}
        </div>
      </section>

      {/* ── Section 2: System Topology ── */}
      <section id="c1-topology" className="scroll-mt-6">
        <h2 className="text-lg font-bold text-slate-800 dark:text-slate-100 mb-4 flex items-center gap-2">
          <span className="w-6 h-6 rounded-lg bg-indigo-500/20 border border-indigo-500/30 text-indigo-600 dark:text-indigo-300 text-xs flex items-center justify-center font-bold">2</span>
          System Topology
          <span className="text-xs font-normal text-slate-500 ml-1">— technical context diagram</span>
        </h2>

        <TopologyCanvas />
        <TopologySpec />
      </section>

      {/* ── Section 3: Telemetry Flow ── */}
      <section id="c1-telemetry" className="scroll-mt-6">
        <h2 className="text-lg font-bold text-slate-800 dark:text-slate-100 mb-4 flex items-center gap-2">
          <span className="w-6 h-6 rounded-lg bg-indigo-500/20 border border-indigo-500/30 text-indigo-600 dark:text-indigo-300 text-xs flex items-center justify-center font-bold">3</span>
          Observability Workflow Arch
          <span className="text-xs font-normal text-slate-500 ml-1">— detailed observability architecture</span>
        </h2>

        <TelemetryCanvas />
      </section>

      {/* ── Section 4: Integration Contracts ── */}
      <section id="c1-contracts" className="scroll-mt-6">
        <h2 className="text-lg font-bold text-slate-800 dark:text-slate-100 mb-4 flex items-center gap-2">
          <span className="w-6 h-6 rounded-lg bg-indigo-500/20 border border-indigo-500/30 text-indigo-600 dark:text-indigo-300 text-xs flex items-center justify-center font-bold">4</span>
          Integration Contracts
        </h2>
        <div className="space-y-3">
          {INTEGRATIONS.map((c, i) => (
            <div key={i} className={`integration-card flex gap-4 p-4 rounded-2xl border bg-white dark:bg-slate-900/60 border-slate-200 dark:border-slate-800/80 ${c.color} hover:bg-slate-50 dark:hover:bg-slate-900 transition-colors`}>
              <span className={`text-[10px] font-mono font-bold px-2 py-1 rounded-lg whitespace-nowrap self-start mt-0.5 ${c.badge}`}>
                {c.id}
              </span>
              <div>
                <p className="font-semibold text-slate-800 dark:text-slate-100 text-sm mb-1">{c.title}</p>
                <p className="text-sm text-slate-600 dark:text-slate-400 leading-relaxed">{c.desc}</p>
              </div>
            </div>
          ))}
        </div>
      </section>

    </PageLayout>
  )
}
