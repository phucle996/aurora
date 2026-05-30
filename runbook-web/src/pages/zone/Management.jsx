import { useTranslation } from 'react-i18next'
import Section from '../../components/spec/Section.jsx'
import Callout from '../../components/spec/Callout.jsx'
import KeyValueTable from '../../components/spec/KeyValueTable.jsx'
import CodeBlock from '../../components/spec/CodeBlock.jsx'
import PageLayout from '../../components/PageLayout.jsx'

const STATE_COLORS = [
  'border-slate-300 dark:border-slate-600 bg-slate-100 dark:bg-slate-800/50 text-slate-700 dark:text-slate-300',
  'border-emerald-400 dark:border-emerald-500/50 bg-emerald-50 dark:bg-emerald-500/10 text-emerald-700 dark:text-emerald-300',
  'border-amber-400 dark:border-amber-500/50 bg-amber-50 dark:bg-amber-500/10 text-amber-700 dark:text-amber-300',
  'border-blue-400 dark:border-blue-500/50 bg-blue-50 dark:bg-blue-500/10 text-blue-700 dark:text-blue-300',
  'border-red-400 dark:border-red-500/50 bg-red-50 dark:bg-red-500/10 text-red-700 dark:text-red-300',
]

export default function ZoneManagement() {
  const { t } = useTranslation()

  const nav = [
    { num: 1, label: t('zone_management.nav.s1'), href: '#section-1' },
    { num: 2, label: t('zone_management.nav.s2'), href: '#section-2' },
    { num: 3, label: t('zone_management.nav.s3'), href: '#section-3' },
    { num: 4, label: t('zone_management.nav.s4'), href: '#section-4' },
    { num: 5, label: t('zone_management.nav.s5'), href: '#section-5' },
  ]

  const states = t('zone_management.transitions.states', { returnObjects: true })
  const transitions = t('zone_management.transitions.transitions', { returnObjects: true })
  const services = t('zone_management.services.table', { returnObjects: true })
  const createFields = t('zone_management.create.fields', { returnObjects: true })
  const precond = t('zone_management.deletion.precond', { returnObjects: true })
  const troubleItems = t('zone_management.troubleshooting.items', { returnObjects: true })

  return (
    <PageLayout
      nav={nav}
      header={
        <>
          <div className="flex items-center gap-3 mb-3">
            <span className="inline-flex items-center justify-center w-10 h-10 rounded-xl bg-gradient-to-br from-indigo-500 to-purple-500 text-white text-lg">
              🗺️
            </span>
            <div>
              <h1 className="text-3xl font-bold text-slate-900 dark:text-slate-100">
                {t('zone_management.hero.title')}
              </h1>
              <p className="text-slate-500 dark:text-slate-400 text-sm mt-0.5">
                {t('zone_management.hero.subtitle')}
              </p>
            </div>
          </div>
          <Callout type="warning" title={t('zone_management.warning.title')}>
            {t('zone_management.warning.desc')}
          </Callout>
        </>
      }
    >

      {/* ── Section 1: Creating a Zone ── */}
      <Section number={1} title={t('zone_management.create.title')}>
        <Callout type="info" title={t('zone_management.create.prereq_title')}>
          <ul className="list-disc list-inside space-y-1 mt-1">
            {t('zone_management.create.prereq', { returnObjects: true }).map((item, i) => (
              <li key={i}>{item}</li>
            ))}
          </ul>
        </Callout>

        <KeyValueTable
          headers={createFields[0]}
          rows={createFields.slice(1)}
        />

        <p className="font-semibold text-slate-800 dark:text-slate-200 mt-4">
          {t('zone_management.create.steps_title')}
        </p>
        <ol className="list-decimal list-inside space-y-2 text-sm">
          {t('zone_management.create.steps', { returnObjects: true }).map((step, i) => (
            <li key={i}>{step}</li>
          ))}
        </ol>

        <Callout type="info" title={t('zone_management.create.note_title')}>
          {t('zone_management.create.note')}
        </Callout>
      </Section>

      {/* ── Section 2: Status Transitions ── */}
      <Section number={2} title={t('zone_management.transitions.title')}>
        <div className="bg-slate-50 dark:bg-slate-900/60 border border-slate-200 dark:border-slate-800 rounded-xl p-5">
          <p className="text-xs uppercase tracking-wider text-slate-500 mb-3 font-semibold">
            {t('zone_management.transitions.states_title')}
          </p>
          <div className="grid sm:grid-cols-2 lg:grid-cols-5 gap-3">
            {states.map((s, i) => (
              <div key={i} className={`border-2 rounded-lg p-3 ${STATE_COLORS[i]}`}>
                <p className="font-mono font-bold text-sm">{s.label}</p>
                <p className="text-xs text-slate-500 dark:text-slate-400 mt-1.5">{s.note}</p>
              </div>
            ))}
          </div>

          <p className="text-xs uppercase tracking-wider text-slate-500 mt-5 mb-2 font-semibold">
            {t('zone_management.transitions.transitions_title')}
          </p>
          <ul className="space-y-1.5 text-sm">
            {transitions.map((tr, i) => (
              <li key={i} className="flex items-start gap-2">
                <span className="font-mono text-xs px-1.5 py-0.5 rounded bg-slate-200 dark:bg-slate-800 text-slate-700 dark:text-slate-300 whitespace-nowrap">{tr.from}</span>
                <span className="text-slate-400 dark:text-slate-500">→</span>
                <span className="font-mono text-xs px-1.5 py-0.5 rounded bg-slate-200 dark:bg-slate-800 text-slate-700 dark:text-slate-300 whitespace-nowrap">{tr.to}</span>
                <span className="text-slate-600 dark:text-slate-400">{tr.label}</span>
              </li>
            ))}
          </ul>
        </div>

        <CodeBlock
          language="ascii"
          code={`planned ──→ active ──→ draining ──→ maintenance ──→ active
                  └──────────────────────────────→ disabled
                                                       ↑
draining ──────────────────────────────────────────────┘`}
        />

        <p className="font-semibold text-slate-800 dark:text-slate-200 mt-4">
          {t('zone_management.transitions.how_to_title')}
        </p>
        <ol className="list-decimal list-inside space-y-2 text-sm">
          {t('zone_management.transitions.how_to', { returnObjects: true }).map((step, i) => (
            <li key={i}>{step}</li>
          ))}
        </ol>

        <Callout type="warning" title={t('zone_management.transitions.warning_title')}>
          {t('zone_management.transitions.warning')}
        </Callout>
      </Section>

      {/* ── Section 3: Service Management ── */}
      <Section number={3} title={t('zone_management.services.title')}>
        <KeyValueTable headers={services[0]} rows={services.slice(1)} />

        <Callout type="info" title={t('zone_management.services.when_title')}>
          {t('zone_management.services.when')}
        </Callout>

        <p className="font-semibold text-slate-800 dark:text-slate-200 mt-4">
          {t('zone_management.services.workflow_title')}
        </p>
        <ol className="list-decimal list-inside space-y-3 text-sm">
          {t('zone_management.services.workflow', { returnObjects: true }).map((step, i) => (
            <li key={i}>
              {step}
              {i === 0 && (
                <ul className="list-disc list-inside ml-5 mt-1.5 space-y-1 text-slate-600 dark:text-slate-400">
                  <li>{t('zone_management.services.drain_note')}</li>
                </ul>
              )}
            </li>
          ))}
        </ol>

        <Callout type="danger" title={t('zone_management.services.warning_title')}>
          {t('zone_management.services.warning')}
        </Callout>
      </Section>

      {/* ── Section 4: Deletion ── */}
      <Section number={4} title={t('zone_management.deletion.title')}>
        <Callout type="danger" title={t('zone_management.deletion.danger_title')}>
          {t('zone_management.deletion.danger')}
        </Callout>

        <p className="font-semibold text-slate-800 dark:text-slate-200 mt-4">
          {t('zone_management.deletion.precond_title')}
        </p>
        <KeyValueTable headers={precond[0]} rows={precond.slice(1)} />

        <p className="font-semibold text-slate-800 dark:text-slate-200 mt-4">
          {t('zone_management.deletion.workflow_title')}
        </p>
        <ol className="list-decimal list-inside space-y-2 text-sm">
          {t('zone_management.deletion.workflow', { returnObjects: true }).map((step, i) => (
            <li key={i}>{step}</li>
          ))}
        </ol>
      </Section>

      {/* ── Section 5: Troubleshooting ── */}
      <Section number={5} title={t('zone_management.troubleshooting.title')}>
        <div className="space-y-4">
          {troubleItems.map((item, i) => (
            <div key={i} className="bg-slate-50 dark:bg-slate-900/60 border border-slate-200 dark:border-slate-800 rounded-xl p-5">
              <p className="font-semibold text-slate-900 dark:text-slate-100 mb-2">{item.title}</p>
              <p className="text-sm text-slate-600 dark:text-slate-400 mb-1"><strong>Cause:</strong> {item.cause}</p>
              <p className="text-sm text-slate-600 dark:text-slate-400"><strong>Solution:</strong> {item.solution}</p>
            </div>
          ))}
        </div>

        <Callout type="info" title={t('zone_management.troubleshooting.help_title')}>
          {t('zone_management.troubleshooting.help')}
        </Callout>
      </Section>

    </PageLayout>
  )
}
