import { useTranslation, Trans } from 'react-i18next'
import SystemArchitecture from '../components/home/SystemArchitecture.jsx'

export default function Home() {
  const { t } = useTranslation()

  return (
    <div className="max-w-6xl mx-auto px-8 py-10 space-y-12">
      {/* Hero */}
      <header className="bg-gradient-to-br from-indigo-100 via-white to-pink-100 dark:from-indigo-900/40 dark:via-slate-900 dark:to-pink-900/30 border border-indigo-200 dark:border-indigo-800/50 rounded-2xl p-10">
        <div className="flex flex-wrap items-center gap-2 text-xs mb-5">
          <span className="px-2.5 py-1 bg-indigo-100 dark:bg-indigo-500/20 text-indigo-700 dark:text-indigo-300 rounded-full border border-indigo-300 dark:border-indigo-500/30">
            {t('home.hero.badges.platform')}
          </span>
          <span className="px-2.5 py-1 bg-emerald-100 dark:bg-emerald-500/20 text-emerald-700 dark:text-emerald-300 rounded-full border border-emerald-300 dark:border-emerald-500/30">
            {t('home.hero.badges.cloud_native')}
          </span>
          <span className="px-2.5 py-1 bg-slate-100 dark:bg-slate-800 text-slate-700 dark:text-slate-300 rounded-full border border-slate-300 dark:border-slate-700">
            {t('home.hero.badges.stack')}
          </span>
        </div>
        <h1 className="text-4xl md:text-5xl font-bold mb-4 bg-gradient-to-r from-indigo-700 via-purple-700 to-pink-700 dark:from-indigo-300 dark:via-purple-300 dark:to-pink-300 bg-clip-text text-transparent">
          {t('home.hero.title')}
        </h1>
        <p className="text-lg md:text-xl text-slate-700 dark:text-slate-200 mb-3 max-w-3xl">
          <Trans i18nKey="home.hero.tagline" components={{ strong: <strong /> }} />
        </p>
        <p className="text-sm text-slate-500 dark:text-slate-400 max-w-3xl">
          {t('home.hero.goal')}
        </p>
      </header>

      {/* Quick stats */}
      <section className="grid grid-cols-2 md:grid-cols-4 gap-3">
        {[
          { labelKey: 'home.stats.source_of_truth', value: 'PostgreSQL', icon: '🐘', accent: 'from-blue-500 to-cyan-500' },
          { labelKey: 'home.stats.coordination', value: 'Redis Stream', icon: '⚡', accent: 'from-red-500 to-orange-500' },
          { labelKey: 'home.stats.edge_proxy', value: 'Envoy', icon: '🚦', accent: 'from-purple-500 to-pink-500' },
          { labelKey: 'home.stats.runtime', value: 'Go Service', icon: '🦫', accent: 'from-emerald-500 to-teal-500' },
        ].map((s) => (
          <div key={s.labelKey} className={`bg-gradient-to-br ${s.accent} p-[1px] rounded-xl`}>
            <div className="bg-white dark:bg-slate-900 rounded-xl p-4 h-full">
              <div className="text-2xl mb-1">{s.icon}</div>
              <p className="text-[11px] uppercase tracking-wider text-slate-500">{t(s.labelKey)}</p>
              <p className="font-semibold text-slate-900 dark:text-slate-100">{s.value}</p>
            </div>
          </div>
        ))}
      </section>

      {/* Three planes */}
      <section>
        <h2 className="text-2xl font-bold mb-2 text-slate-900 dark:text-slate-100">
          {t('home.planes.section_title')}
        </h2>
        <p className="text-slate-600 dark:text-slate-400 mb-6">
          {t('home.planes.section_desc')}
        </p>
        <div className="grid md:grid-cols-3 gap-4">
          {[
            { id: 'controlplane', icon: '🧠', gradient: 'from-indigo-500 to-purple-500' },
            { id: 'dataplane', icon: '⚙️', gradient: 'from-blue-500 to-cyan-500' },
            { id: 'agent', icon: '🛰️', gradient: 'from-emerald-500 to-teal-500' },
          ].map((plane) => (
            <div key={plane.id} className={`bg-gradient-to-br ${plane.gradient} p-[1px] rounded-xl`}>
              <div className="bg-white dark:bg-slate-900 rounded-xl p-6 h-full">
                <div className="flex items-center justify-between mb-3">
                  <div className="flex items-center gap-2">
                    <span className="text-2xl">{plane.icon}</span>
                    <h3 className="text-lg font-bold text-slate-900 dark:text-slate-100">
                      {t(`home.planes.${plane.id}.title`)}
                    </h3>
                  </div>
                  <span className={`text-[10px] uppercase tracking-wider px-2 py-0.5 rounded-full bg-gradient-to-br ${plane.gradient} text-white font-semibold`}>
                    {t(`home.planes.${plane.id}.tag`)}
                  </span>
                </div>
                <p className="text-sm text-slate-600 dark:text-slate-300 mb-3">
                  {t(`home.planes.${plane.id}.desc`)}
                </p>
                <ul className="space-y-1 text-xs text-slate-500 dark:text-slate-400">
                  {['p1', 'p2', 'p3', 'p4'].map((p) => (
                    <li key={p} className="flex items-start gap-1.5">
                      <span className="text-emerald-500">▸</span>
                      <span>{t(`home.planes.${plane.id}.${p}`)}</span>
                    </li>
                  ))}
                </ul>
              </div>
            </div>
          ))}
        </div>
      </section>

      {/* System architecture */}
      <section>
        <h2 className="text-2xl font-bold mb-2 text-slate-900 dark:text-slate-100">
          {t('home.architecture.title')}
        </h2>
        <p className="text-slate-600 dark:text-slate-400 mb-6">
          {t('home.architecture.desc')}
        </p>
        <SystemArchitecture footerText={t('home.architecture.footer')} />
      </section>

      {/* Request flow */}
      <section>
        <h2 className="text-2xl font-bold mb-2 text-slate-900 dark:text-slate-100">
          {t('home.request_flow.title')}
        </h2>
        <p className="text-slate-600 dark:text-slate-400 mb-6">
          {t('home.request_flow.desc')}
        </p>
        <ol className="space-y-2">
          {[1, 2, 3, 4, 5, 6, 7, 8].map((i) => (
            <li key={i} className="flex items-start gap-4 p-4 bg-white dark:bg-slate-900/60 border border-slate-200 dark:border-slate-800 rounded-lg">
              <span className="flex-shrink-0 w-7 h-7 rounded-full bg-gradient-to-br from-indigo-500 to-purple-500 text-white font-bold text-xs flex items-center justify-center">
                {i}
              </span>
              <div className="flex-1">
                <p className="font-semibold text-slate-900 dark:text-slate-100">
                  {t(`home.request_flow.s${i}`)}
                </p>
                <p className="text-sm text-slate-600 dark:text-slate-400">
                  {t(`home.request_flow.s${i}_desc`)}
                </p>
              </div>
            </li>
          ))}
        </ol>
      </section>

      {/* Service modules */}
      <section>
        <h2 className="text-2xl font-bold mb-2 text-slate-900 dark:text-slate-100">
          {t('home.modules.title')}
        </h2>
        <p className="text-slate-600 dark:text-slate-400 mb-6">
          {t('home.modules.desc_prefix')}{' '}
          <code className="text-amber-600 dark:text-amber-400 bg-slate-100 dark:bg-slate-800 px-1.5 py-0.5 rounded text-sm">internal/</code>
        </p>
        <div className="grid grid-cols-2 md:grid-cols-3 lg:grid-cols-4 gap-3">
          {[
            { icon: '🔐', name: 'IAM', descKey: 'home.modules.iam_desc' },
            { icon: '🏢', name: 'Tenant', descKey: 'home.modules.tenant_desc' },
            { icon: '⚙️', name: 'Core', descKey: 'home.modules.core_desc' },
            { icon: '📜', name: 'Policy Engine', descKey: 'home.modules.policy_desc' },
            { icon: '🛡️', name: 'Security', descKey: 'home.modules.security_desc' },
            { icon: '👁️', name: 'Observability', descKey: 'home.modules.observability_desc' },
            { icon: '📧', name: 'Mail', descKey: 'home.modules.mail_desc' },
            { icon: '🖥️', name: 'Hypervisor', descKey: 'home.modules.hypervisor_desc' },
          ].map((m) => (
            <div key={m.name} className="bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-lg p-4 hover:border-indigo-400 dark:hover:border-indigo-500 transition-colors">
              <div className="text-2xl mb-2">{m.icon}</div>
              <p className="font-semibold text-slate-900 dark:text-slate-100">{m.name}</p>
              <p className="text-xs text-slate-500 dark:text-slate-400 mt-1">{t(m.descKey)}</p>
            </div>
          ))}
        </div>
      </section>

      {/* Design principles */}
      <section>
        <h2 className="text-2xl font-bold mb-2 text-slate-900 dark:text-slate-100">
          {t('home.principles.title')}
        </h2>
        <p className="text-slate-600 dark:text-slate-400 mb-6">
          {t('home.principles.desc')}
        </p>
        <div className="grid md:grid-cols-2 gap-3">
          {[1, 2, 3, 4, 5, 6].map((i) => (
            <div key={i} className="bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-lg p-4">
              <p className="font-semibold text-slate-900 dark:text-slate-100 mb-1">
                {t(`home.principles.p${i}_title`)}
              </p>
              <p className="text-sm text-slate-600 dark:text-slate-400">
                {t(`home.principles.p${i}_desc`)}
              </p>
            </div>
          ))}
        </div>
      </section>

      <footer className="border-t border-slate-200 dark:border-slate-800 pt-6 text-center text-sm text-slate-500">
        <p>{t('home.footer.line1')}</p>
        <p className="mt-1">{t('home.footer.line2')}</p>
      </footer>
    </div>
  )
}
