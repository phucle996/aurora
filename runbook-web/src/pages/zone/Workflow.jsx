import { useTranslation } from 'react-i18next'
import Section from '../../components/spec/Section.jsx'
import Callout from '../../components/spec/Callout.jsx'
import KeyValueTable from '../../components/spec/KeyValueTable.jsx'
import CodeBlock from '../../components/spec/CodeBlock.jsx'
import PageLayout from '../../components/PageLayout.jsx'

const CREATE_PAYLOAD = `POST /api/v1/zones
Authorization: Bearer <admin-jwt>

{
  "name": "US East 1",
  "code": "us-east-1",
  "location": "us-east",
  "services": {
    "hypervisor": true,
    "storage": true,
    "mail": false,
    "k8s": false,
    "ai": false
  }
}`

const CREATE_RESPONSE = `HTTP/1.1 201 Created

{
  "id": "01903e8c-55c1-7da3-87de-6e69622d1df9",
  "name": "US East 1",
  "code": "us-east-1",
  "status": "planned",
  "location": "us-east",
  "services": { "hypervisor": true, "storage": true, "mail": false, "k8s": false, "ai": false },
  "created_at": "2026-05-30T19:00:00Z"
}`

const ACTIVATE_PAYLOAD = `PATCH /api/v1/zones/01903e8c-55c1-7da3-87de-6e69622d1df9/status
Authorization: Bearer <admin-jwt>

{
  "status": "active"
}`

const DATAPLANE_REGISTER = `POST /api/v1/zones/01903e8c-55c1-7da3-87de-6e69622d1df9/dataplanes
Authorization: Bearer <admin-jwt>

{
  "node_id": "01903e8c-55c1-7da3-87de-6e69622d1df7",
  "grpc_endpoint": "dataplane-z1-n1:9090",
  "capabilities": ["hypervisor", "storage"]
}`

const STEP_COLORS = [
  'from-slate-500 to-slate-600',
  'from-indigo-500 to-blue-500',
  'from-blue-500 to-cyan-500',
  'from-emerald-500 to-green-500',
  'from-purple-500 to-pink-500',
]

const STEP_ICONS = ['📋', '🏗️', '🔌', '✅', '📊']

export default function ZoneWorkflow() {
  const { t } = useTranslation()

  const stepsFlow = t('zone_workflow.steps_flow', { returnObjects: true })

  const nav = [
    { num: 1, label: t('zone_workflow.nav.s1'), href: '#section-1' },
    { num: 2, label: t('zone_workflow.nav.s2'), href: '#section-2' },
    { num: 3, label: t('zone_workflow.nav.s3'), href: '#section-3' },
    { num: 4, label: t('zone_workflow.nav.s4'), href: '#section-4' },
    { num: 5, label: t('zone_workflow.nav.s5'), href: '#section-5' },
    { num: 6, label: t('zone_workflow.nav.s6'), href: '#section-6' },
  ]

  const planningTable = t('zone_workflow.planning.table', { returnObjects: true })
  const dataplaneTable = t('zone_workflow.dataplane.table', { returnObjects: true })
  const verifyTable = t('zone_workflow.verify.table', { returnObjects: true })

  return (
    <PageLayout
      nav={nav}
      header={
        <>
          <div className="flex items-center gap-3 mb-3">
            <span className="inline-flex items-center justify-center w-10 h-10 rounded-xl bg-gradient-to-br from-blue-500 to-cyan-500 text-white text-lg">
              🔄
            </span>
            <div>
              <h1 className="text-3xl font-bold text-slate-900 dark:text-slate-100">
                {t('zone_workflow.hero.title')}
              </h1>
              <p className="text-slate-500 dark:text-slate-400 text-sm mt-0.5">
                {t('zone_workflow.hero.subtitle')}
              </p>
            </div>
          </div>

          {/* Flow overview */}
          <div className="mt-4 flex flex-wrap gap-2 items-center">
            {stepsFlow.map((label, i) => (
              <div key={i} className="flex items-center gap-2">
                <div className={`flex items-center gap-2 bg-gradient-to-r ${STEP_COLORS[i]} text-white text-xs font-semibold px-3 py-1.5 rounded-full`}>
                  <span>{STEP_ICONS[i]}</span>
                  <span>{label}</span>
                </div>
                {i < stepsFlow.length - 1 && (
                  <span className="text-slate-400 dark:text-slate-600 text-sm">→</span>
                )}
              </div>
            ))}
          </div>
        </>
      }
    >

      {/* ── Section 1: Planning ── */}
      <Section number={1} title={t('zone_workflow.planning.title')}>
        <Callout type="info" title={t('zone_workflow.planning.checklist_title')}>
          <ul className="list-disc list-inside space-y-1 mt-1">
            {t('zone_workflow.planning.checklist', { returnObjects: true }).map((item, i) => (
              <li key={i}>{item}</li>
            ))}
          </ul>
        </Callout>

        <KeyValueTable headers={planningTable[0]} rows={planningTable.slice(1)} />
      </Section>

      {/* ── Section 2: Create Zone ── */}
      <Section number={2} title={t('zone_workflow.create.title')}>
        <p className="text-sm">{t('zone_workflow.create.intro')}</p>

        <p className="font-semibold text-slate-800 dark:text-slate-200 mt-4">
          {t('zone_workflow.create.ui_title')}
        </p>
        <ol className="list-decimal list-inside space-y-2 text-sm">
          {t('zone_workflow.create.ui_steps', { returnObjects: true }).map((step, i) => (
            <li key={i}>{step}</li>
          ))}
        </ol>

        <p className="font-semibold text-slate-800 dark:text-slate-200 mt-6">
          {t('zone_workflow.create.api_title')}
        </p>
        <CodeBlock language="http" code={CREATE_PAYLOAD} />
        <CodeBlock language="http" code={CREATE_RESPONSE} />

        <Callout type="success" title={t('zone_workflow.create.result_title')}>
          {t('zone_workflow.create.result')}
        </Callout>
      </Section>

      {/* ── Section 3: Register Dataplane ── */}
      <Section number={3} title={t('zone_workflow.dataplane.title')}>
        <Callout type="warning" title={t('zone_workflow.dataplane.warning_title')}>
          {t('zone_workflow.dataplane.warning')}
        </Callout>

        <p className="font-semibold text-slate-800 dark:text-slate-200 mt-4">
          {t('zone_workflow.dataplane.register_title')}
        </p>
        <CodeBlock language="http" code={DATAPLANE_REGISTER} />

        <KeyValueTable headers={dataplaneTable[0]} rows={dataplaneTable.slice(1)} />

        <p className="font-semibold text-slate-800 dark:text-slate-200 mt-4">
          {t('zone_workflow.dataplane.verify_title')}
        </p>
        <ol className="list-decimal list-inside space-y-2 text-sm">
          {t('zone_workflow.dataplane.verify', { returnObjects: true }).map((step, i) => (
            <li key={i}>{step}</li>
          ))}
        </ol>
      </Section>

      {/* ── Section 4: Activate Zone ── */}
      <Section number={4} title={t('zone_workflow.activate.title')}>
        <Callout type="info" title={t('zone_workflow.activate.prereq_title')}>
          <ul className="list-disc list-inside space-y-1 mt-1">
            {t('zone_workflow.activate.prereq', { returnObjects: true }).map((item, i) => (
              <li key={i}>{item}</li>
            ))}
          </ul>
        </Callout>

        <p className="font-semibold text-slate-800 dark:text-slate-200 mt-4">
          {t('zone_workflow.activate.ui_title')}
        </p>
        <ol className="list-decimal list-inside space-y-2 text-sm">
          {t('zone_workflow.activate.ui_steps', { returnObjects: true }).map((step, i) => (
            <li key={i}>{step}</li>
          ))}
        </ol>

        <p className="font-semibold text-slate-800 dark:text-slate-200 mt-6">
          {t('zone_workflow.activate.api_title')}
        </p>
        <CodeBlock language="http" code={ACTIVATE_PAYLOAD} />

        <Callout type="success" title={t('zone_workflow.activate.result_title')}>
          {t('zone_workflow.activate.result')}
        </Callout>
      </Section>

      {/* ── Section 5: Verify & Monitor ── */}
      <Section number={5} title={t('zone_workflow.verify.title')}>
        <p className="font-semibold text-slate-800 dark:text-slate-200">
          {t('zone_workflow.verify.checklist_title')}
        </p>
        <KeyValueTable headers={verifyTable[0]} rows={verifyTable.slice(1)} />

        <Callout type="info" title={t('zone_workflow.verify.grafana_title')}>
          {t('zone_workflow.verify.grafana')}
        </Callout>
      </Section>

      {/* ── Section 6: Rollback ── */}
      <Section number={6} title={t('zone_workflow.rollback.title')}>
        <Callout type="danger" title={t('zone_workflow.rollback.when_title')}>
          {t('zone_workflow.rollback.when')}
        </Callout>

        <p className="font-semibold text-slate-800 dark:text-slate-200 mt-4">
          {t('zone_workflow.rollback.steps_title')}
        </p>
        <ol className="list-decimal list-inside space-y-2 text-sm">
          {t('zone_workflow.rollback.steps', { returnObjects: true }).map((step, i) => (
            <li key={i}>{step}</li>
          ))}
        </ol>

        <Callout type="info" title={t('zone_workflow.rollback.help_title')}>
          {t('zone_workflow.rollback.help')}
        </Callout>
      </Section>

    </PageLayout>
  )
}
