import { useState, useEffect } from 'react'
import { useTranslation } from 'react-i18next'
import Section from '../../components/spec/Section.jsx'
import Callout from '../../components/spec/Callout.jsx'
import KeyValueTable from '../../components/spec/KeyValueTable.jsx'
import CodeBlock from '../../components/spec/CodeBlock.jsx'
import PageLayout from '../../components/PageLayout.jsx'
import {
  Shield,
  Database,
  RefreshCw,
  Clock,
  Server,
  Lock,
  Terminal,
  Zap,
  Play
} from 'lucide-react'

// Mock Initial Registry State
const INITIAL_FAMILIES = {
  access_token: {
    code: 'access_token',
    name: 'Access Token Signer Key',
    description: 'Bí mật ký JWT Access Token dành cho phiên làm việc ngắn hạn.',
    rotationInterval: '24 Giờ',
    gracePeriod: '24 Giờ',
    currentVersion: 4,
    lastRotated: '14 giờ trước',
    status: 'active',
    versions: [
      { id: '018fcc1a-2891-7fc3-a991-e402e3a1f11a', version: 4, fingerprint: 'SHA-256:f12a...7c9b', status: 'active', is_primary: true, activated_at: '2026-06-01 03:00:00' },
      { id: '018fb20f-b210-7fc3-bf30-58c0c9b0e11b', version: 3, fingerprint: 'SHA-256:d550...3ba2', status: 'retired', is_primary: false, activated_at: '2026-05-31 03:00:00', retired_at: '2026-06-01 03:00:00' }
    ]
  },
  refresh_token: {
    code: 'refresh_token',
    name: 'Refresh Token Encrypter',
    description: 'Khóa đối xứng mã hóa JWT Refresh Token lưu hành dài hạn ở client.',
    rotationInterval: '7 Ngày',
    gracePeriod: '7 Ngày',
    currentVersion: 2,
    lastRotated: '3 ngày trước',
    status: 'active',
    versions: [
      { id: '018fbcfc-e801-7fa1-a189-e501a3c2e11d', version: 2, fingerprint: 'SHA-256:911c...aef1', status: 'active', is_primary: true, activated_at: '2026-05-29 12:00:00' },
      { id: '018f4a1b-cd12-7fa1-8f20-9ba0e301211e', version: 1, fingerprint: 'SHA-256:e1b9...de52', status: 'retired', is_primary: false, activated_at: '2026-05-22 12:00:00', retired_at: '2026-05-29 12:00:00' }
    ]
  },
  admin_api_key: {
    code: 'admin_api_key',
    name: 'Admin API Signer Key',
    description: 'Cặp khóa bất đối xứng ký số cho các giao dịch nhạy cảm ở Admin Control Plane.',
    rotationInterval: '30 Ngày',
    gracePeriod: '30 Ngày',
    currentVersion: 1,
    lastRotated: '15 ngày trước',
    status: 'active',
    versions: [
      { id: '018f6f1c-7fc2-7fb3-810a-7fa0c9d3e41f', version: 1, fingerprint: 'SHA-256:4c2a...f982', status: 'active', is_primary: true, activated_at: '2026-05-17 08:00:00' }
    ]
  },
  one_time_token: {
    code: 'one_time_token',
    name: 'OTP & Password Reset Token Key',
    description: 'Mã hóa token dùng một lần và mã xác thực OTP.',
    rotationInterval: '12 Giờ',
    gracePeriod: '12 Giờ',
    currentVersion: 12,
    lastRotated: '4 giờ trước',
    status: 'active',
    versions: [
      { id: '018fcdc9-b901-7fb1-bc29-e093c8b1a32a', version: 12, fingerprint: 'SHA-256:3a1b...6e90', status: 'active', is_primary: true, activated_at: '2026-06-01 13:00:00' },
      { id: '018fd76c-da12-7fb1-901a-8fc0e3a2b12b', version: 11, fingerprint: 'SHA-256:77bc...19d2', status: 'retired', is_primary: false, activated_at: '2026-06-01 01:00:00', retired_at: '2026-06-01 13:00:00' }
    ]
  }
}

export default function SecuritySecrets() {
  const { t } = useTranslation()
  const [families, setFamilies] = useState(INITIAL_FAMILIES)
  const [selectedFamilyCode, setSelectedFamilyCode] = useState('access_token')

  // Simulator State
  const [simulating, setSimulating] = useState(false)
  const [simStep, setSimStep] = useState(0)
  const [logs, setLogs] = useState([])
  const [tickerTime, setTickerTime] = useState(42) // Dynamic countdown ticker

  useEffect(() => {
    const timer = setInterval(() => {
      setTickerTime((prev) => (prev <= 1 ? 60 : prev - 1))
    }, 1000)
    return () => clearInterval(timer)
  }, [])

  const selectedFamily = families[selectedFamilyCode]

  const nav = [
    { num: 1, label: t('secrets.sec1_title'), href: '#section-1' },
    { num: 2, label: t('secrets.sec2_title'), href: '#section-2' },
    { num: 3, label: t('secrets.sec3_title'), href: '#section-3' },
  ]

  const runSimulation = () => {
    if (simulating) return
    setSimulating(true)
    setSimStep(1)
    setLogs([])

    const addLog = (text, delay) => {
      return new Promise((resolve) => {
        setTimeout(() => {
          setLogs((prev) => [...prev, `[${new Date().toLocaleTimeString()}] ${text}`])
          resolve()
        }, delay)
      })
    }

    const prevPrimary = selectedFamily.versions.find(v => v.is_primary)
    const oldestVersion = selectedFamily.versions.length >= 2 ? selectedFamily.versions[selectedFamily.versions.length - 1] : null

    addLog(`🔄 Bắt đầu chu trình Scheduler Worker cho family: ${selectedFamilyCode.toUpperCase()}...`, 0)
      .then(() => {
        setSimStep(2)
        return addLog(`🔍 [PlanRotation] Phân tích DB: Primary v${selectedFamily.currentVersion} đang hết hạn hoặc nhận yêu cầu xoay vòng khẩn cấp.`, 1000)
      })
      .then(() => {
        setSimStep(3)
        return addLog(`🔐 [Redis Lock] Đang giành quyền: "lock:secret_rotation:${selectedFamilyCode}" (SETNX)...`, 1200)
      })
      .then(() => {
        return addLog(`🔑 [Redis Lock] Lock ACQUIRED! Ngăn chặn Split-brain trên môi trường Multi-node.`, 800)
      })
      .then(() => {
        setSimStep(4)
        if (oldestVersion) {
          return addLog(`🗑️ [DB Transaction] Phát hiện tập khóa đạt ngưỡng tối đa (2). Thực thi SQL: DELETE FROM core_secret_versions WHERE id = '${oldestVersion.id}' (xóa v${oldestVersion.version} khỏi DB).`, 1200)
        } else {
          return addLog(`ℹ️ [DB Transaction] Tập khóa hiện tại chưa vượt ngưỡng, bỏ qua bước dọn dẹp.`, 800)
        }
      })
      .then(() => {
        return addLog(`💾 [DB Transaction] Thực thi SQL: set is_primary = false cho v${selectedFamily.currentVersion} và set status = 'retired' để giữ lại verify (Grace Period = 1 TTL).`, 1000)
      })
      .then(() => {
        return addLog(`⚙️ [DB Transaction] Chèn phiên bản mới v${selectedFamily.currentVersion + 1} với status = 'active' và is_primary = true làm Primary Signer mới.`, 1000)
      })
      .then(() => {
        setSimStep(5)
        return addLog(`🚀 [Redis Invalidation] Phát tín hiệu (Publish) hủy cache: "invalidate:secret_family:${selectedFamilyCode}"...`, 1200)
      })
      .then(() => {
        return addLog(`📡 [Cache Invalidation] 3 node API Control Plane khác nhận tín hiệu thành công -> Local cache được xóa lập tức.`, 800)
      })
      .then(() => {
        setSimStep(6)

        // Update local state to show newly generated version and respect the maximum 2-version invariant
        const newVersionNum = selectedFamily.currentVersion + 1
        const newVerObj = {
          id: `018f${Math.random().toString(16).substring(2, 6)}-${Math.random().toString(16).substring(2, 6)}-7fc3-a991-e402e3a1f11a`,
          version: newVersionNum,
          fingerprint: `SHA-256:${Math.random().toString(16).substring(2, 6)}...${Math.random().toString(16).substring(2, 6)}`,
          status: 'active',
          is_primary: true,
          activated_at: new Date().toISOString().replace('T', ' ').substring(0, 19)
        }

        const updatedVersions = [newVerObj]
        if (prevPrimary) {
          updatedVersions.push({
            ...prevPrimary,
            is_primary: false,
            status: 'retired',
            retired_at: new Date().toISOString().replace('T', ' ').substring(0, 19)
          })
        }

        setFamilies(prev => ({
          ...prev,
          [selectedFamilyCode]: {
            ...selectedFamily,
            currentVersion: newVersionNum,
            lastRotated: 'Vừa xong',
            versions: updatedVersions
          }
        }))

        return addLog(`✅ [Chu trình HOÀN TẤT] Family ${selectedFamilyCode.toUpperCase()} đã xoay vòng sang v${newVersionNum} thành công!`, 1000)
      })
      .then(() => {
        setSimulating(false)
      })
  }

  const dbHeaders = t('secrets.table.headers', { returnObjects: true }) || []
  const dbRows = Object.keys(INITIAL_FAMILIES).map(key =>
    t(`secrets.table.rows.${key}`, { returnObjects: true }) || []
  )

  return (
    <PageLayout
      nav={nav}
      header={
        <div className="flex items-center gap-3 mb-3">
          <span className="inline-flex items-center justify-center w-10 h-10 rounded-xl bg-gradient-to-br from-indigo-500 to-purple-600 text-white text-lg">
            🔑
          </span>
          <div>
            <h1 className="text-3xl font-bold text-slate-900 dark:text-slate-100">
              {t('secrets.title')}
            </h1>
            <p className="text-slate-500 dark:text-slate-400 text-base mt-0.5">
              {t('secrets.subtitle')}
            </p>
          </div>
        </div>
      }
    >
      <Callout type="success" title={t('secrets.callout_title')}>
        {t('secrets.callout_desc')}
      </Callout>

      {/* SECTION 1: TỔNG QUAN REGISTRY */}
      <Section id="section-1" number={1} title={t('secrets.sec1_title')}>
        <p className="text-slate-600 dark:text-slate-400 mb-6 leading-relaxed">
          {t('secrets.sec1_desc')}
        </p>

        {/* Real-time KPI Metric Cards */}
        <div className="grid grid-cols-1 md:grid-cols-4 gap-4 mb-6">
          <div className="bg-slate-50 dark:bg-slate-900 border border-slate-200 dark:border-slate-800 p-4 rounded-xl shadow-sm">
            <div className="flex items-center justify-between mb-2">
              <span className="text-xs font-semibold text-slate-500 uppercase">{t('secrets.cluster_status')}</span>
              <Server className="w-4 h-4 text-emerald-500" />
            </div>
            <p className="text-2xl font-bold text-emerald-500">{t('secrets.cluster_status_val')}</p>
            <p className="text-[10px] text-slate-500 mt-1">{t('secrets.cluster_status_sub')}</p>
          </div>

          <div className="bg-slate-50 dark:bg-slate-900 border border-slate-200 dark:border-slate-800 p-4 rounded-xl shadow-sm">
            <div className="flex items-center justify-between mb-2">
              <span className="text-xs font-semibold text-slate-500 uppercase">{t('secrets.worker_frequency')}</span>
              <Clock className="w-4 h-4 text-indigo-500" />
            </div>
            <p className="text-2xl font-bold text-indigo-600 dark:text-indigo-400">{t('secrets.worker_frequency_val')}</p>
            <p className="text-[10px] text-slate-500 mt-1">{t('secrets.worker_frequency_sub')}</p>
          </div>

          <div className="bg-slate-50 dark:bg-slate-900 border border-slate-200 dark:border-slate-800 p-4 rounded-xl shadow-sm">
            <div className="flex items-center justify-between mb-2">
              <span className="text-xs font-semibold text-slate-500 uppercase">{t('secrets.next_evaluator')}</span>
              <RefreshCw className="w-4 h-4 text-purple-500 animate-spin" style={{ animationDuration: '4s' }} />
            </div>
            <p className="text-2xl font-bold text-slate-700 dark:text-slate-300">{tickerTime}s</p>
            <p className="text-[10px] text-slate-500 mt-1">{t('secrets.next_evaluator_sub')}</p>
          </div>

          <div className="bg-slate-50 dark:bg-slate-900 border border-slate-200 dark:border-slate-800 p-4 rounded-xl shadow-sm">
            <div className="flex items-center justify-between mb-2">
              <span className="text-xs font-semibold text-slate-500 uppercase">{t('secrets.sync_channel')}</span>
              <Lock className="w-4 h-4 text-rose-500" />
            </div>
            <p className="text-2xl font-bold text-rose-600 dark:text-rose-400">{t('secrets.sync_channel_val')}</p>
            <p className="text-[10px] text-slate-500 mt-1">{t('secrets.sync_channel_sub')}</p>
          </div>
        </div>

        {/* Selected Secret Family Details */}
        <div className="grid grid-cols-1 lg:grid-cols-3 gap-6">
          {/* List of Families */}
          <div className="lg:col-span-1 space-y-3">
            <h3 className="text-sm font-bold uppercase tracking-wider text-slate-500 mb-2">{t('secrets.select_family')}</h3>
            {Object.values(families).map((fam) => {
              const isSelected = selectedFamilyCode === fam.code
              return (
                <div
                  key={fam.code}
                  onClick={() => setSelectedFamilyCode(fam.code)}
                  className={`p-4 rounded-xl border cursor-pointer transition-all duration-200 ${isSelected
                      ? 'border-indigo-500 bg-indigo-50/30 dark:bg-indigo-950/20 text-indigo-700 dark:text-indigo-300 shadow-sm'
                      : 'border-slate-200 dark:border-slate-800 hover:bg-slate-100/50 dark:hover:bg-slate-900/50'
                    }`}
                >
                  <div className="flex items-center gap-2 mb-1">
                    <span className="text-sm">🔑</span>
                    <span className="font-bold text-xs uppercase tracking-wide">{fam.code}</span>
                  </div>
                  <h4 className="text-sm font-extrabold truncate">{t(`secrets.families.${fam.code}.name`)}</h4>
                  <div className="flex items-center gap-2 mt-2 text-[10px] text-slate-500">
                    <span>{t('secrets.family_rotation')}: {fam.rotationInterval}</span>
                    <span>•</span>
                    <span>v{fam.currentVersion}</span>
                  </div>
                </div>
              )
            })}
          </div>

          {/* Details of Selected Family & Version Registry */}
          <div className="lg:col-span-2 bg-slate-900/90 border border-slate-800 rounded-2xl p-5 shadow-xl text-slate-300 flex flex-col justify-between">
            <div>
              <div className="flex items-center justify-between border-b border-slate-800 pb-3 mb-4">
                <div>
                  <span className="text-[10px] uppercase font-bold tracking-widest text-indigo-400 bg-indigo-950/50 px-2 py-0.5 rounded border border-indigo-900">
                    {t('secrets.active_record')}
                  </span>
                  <h3 className="text-lg font-bold text-slate-100 mt-1.5">{t(`secrets.families.${selectedFamily.code}.name`)}</h3>
                </div>
                <div className="flex items-center gap-1.5">
                  <span className="w-2.5 h-2.5 rounded-full bg-emerald-500 animate-ping" />
                  <span className="text-xs font-semibold text-emerald-400">HEALTHY</span>
                </div>
              </div>

              <p className="text-xs text-slate-400 leading-relaxed mb-4">
                {t(`secrets.families.${selectedFamily.code}.desc`)}
              </p>

              {/* Version List */}
              <div className="space-y-3">
                <h4 className="text-xs font-bold uppercase tracking-wider text-slate-500">{t('secrets.version_history')}</h4>
                <div className="max-h-48 overflow-y-auto space-y-2 pr-2">
                  {selectedFamily.versions.map((ver) => (
                    <div
                      key={ver.id}
                      className={`p-3 rounded-xl border text-xs flex items-center justify-between transition-colors ${ver.is_primary
                          ? 'border-emerald-500/40 bg-emerald-950/20 text-emerald-300'
                          : 'border-slate-800 bg-slate-950/50 text-slate-400'
                        }`}
                    >
                      <div className="space-y-1">
                        <div className="flex items-center gap-2">
                          <span className="font-bold text-slate-200">Version {ver.version}</span>
                          {ver.is_primary && (
                            <span className="text-[9px] px-1.5 py-0.5 rounded bg-emerald-500/20 text-emerald-400 border border-emerald-500/30 uppercase tracking-widest font-extrabold">
                              {t('secrets.primary_signer')}
                            </span>
                          )}
                        </div>
                        <p className="font-mono text-[10px] text-slate-500 select-all">UUID: {ver.id}</p>
                      </div>
                      <div className="text-right">
                        <span className={`px-2 py-0.5 rounded-full text-[9px] font-extrabold uppercase ${ver.status === 'active'
                            ? 'bg-emerald-500/20 text-emerald-400 border border-emerald-500/30'
                            : 'bg-slate-800 text-slate-500'
                          }`}>
                          {ver.status}
                        </span>
                        <p className="text-[9px] text-slate-600 mt-1 font-mono">Fingerprint: {ver.fingerprint}</p>
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            </div>

            <div className="border-t border-slate-800 pt-4 mt-4 flex items-center justify-between">
              <div className="text-xs text-slate-500">
                {t('secrets.last_rotated')}: <span className="font-bold text-slate-400">{selectedFamily.lastRotated}</span>
              </div>
              <button
                onClick={runSimulation}
                disabled={simulating}
                className={`flex items-center gap-2 px-4 py-2 rounded-xl text-xs font-bold transition-all duration-200 shadow-md ${simulating
                    ? 'bg-slate-800 text-slate-600 cursor-not-allowed'
                    : 'bg-indigo-600 hover:bg-indigo-500 text-white hover:shadow-indigo-600/25 active:scale-95'
                  }`}
              >
                <Play className="w-3.5 h-3.5" />
                {t('secrets.emergency_rotation')}
              </button>
            </div>
          </div>
        </div>
      </Section>

      {/* SECTION 2: ĐẶC TẢ DATABASE & STATUS RULES */}
      <Section id="section-2" number={2} title={t('secrets.sec2_title')}>
        <p className="text-slate-600 dark:text-slate-400 leading-relaxed mb-4">
          {t('secrets.sec2_desc')}
        </p>

        <Callout type="warning" title={t('secrets.sec2_callout_title')}>
          {t('secrets.sec2_callout_desc')}
        </Callout>

        <h3 className="text-sm font-bold uppercase tracking-wider text-slate-500 mt-6 mb-2">{t('secrets.sec2_table_title')}</h3>
        <KeyValueTable headers={dbHeaders} rows={dbRows} />

        <div className="space-y-6 mt-6">
          <div>
            <h4 className="text-lg font-extrabold text-slate-900 dark:text-slate-100 flex items-center gap-2 mb-2">
              <Zap className="w-4 h-4 text-indigo-500" />
              {t('secrets.sec2_sub1_title')}
            </h4>
            <p className="text-sm text-slate-600 dark:text-slate-400 leading-relaxed">
              {t('secrets.sec2_sub1_desc')}
            </p>
            <CodeBlock
              language="sql"
              code={`CREATE UNIQUE INDEX ux_core_secret_versions_one_primary
ON core_secret_versions(family_id)
WHERE is_primary = true AND revoked_at IS NULL;`}
            />
          </div>

          <div>
            <h4 className="text-lg font-extrabold text-slate-900 dark:text-slate-100 flex items-center gap-2 mb-2">
              <Shield className="w-4 h-4 text-emerald-500" />
              {t('secrets.sec2_sub2_title')}
            </h4>
            <p className="text-sm text-slate-600 dark:text-slate-400 leading-relaxed mb-2">
              {t('secrets.sec2_sub2_desc')}
            </p>
            <CodeBlock
              language="go"
              code={`// Thuật toán mã hóa đối xứng AES-GCM an toàn tuyệt đối
block, err := aes.NewCipher(runtimeMasterKey) // Khóa Master 32-byte (AES-256)
gcm, err := cipher.NewGCM(block)

// Sinh ngẫu nhiên Nonce an toàn
nonce := make([]byte, gcm.NonceSize())
if _, err := io.ReadFull(rand.Reader, nonce); err != nil { ... }

// Thực hiện mã hóa và ghép nonce vào payload trước khi lưu
cipherText := gcm.Seal(nil, nonce, []byte(plainText), nil)
payload := append(nonce, cipherText...)`}
            />
          </div>

          <div>
            <h4 className="text-lg font-extrabold text-slate-900 dark:text-slate-100 flex items-center gap-2 mb-2">
              <Database className="w-4 h-4 text-indigo-500" />
              {t('secrets.sec2_sub3_title')}
            </h4>
            <p className="text-sm text-slate-600 dark:text-slate-400 leading-relaxed">
              {t('secrets.sec2_sub3_desc1')}
            </p>
            <p className="text-sm text-slate-600 dark:text-slate-400 mt-2 leading-relaxed">
              {t('secrets.sec2_sub3_desc2')}
            </p>
          </div>
        </div>
      </Section>

      {/* SECTION 3: SCHEDULER WORKER SIMULATION */}
      <Section id="section-3" number={3} title={t('secrets.sec3_title')}>
        <p className="text-slate-600 dark:text-slate-400 leading-relaxed mb-6">
          {t('secrets.sec3_desc')}
        </p>

        {/* Live Interactive Simulation Board */}
        <div className="bg-slate-950 border border-slate-800 rounded-2xl p-6 shadow-xl text-slate-300">
          <div className="flex items-center justify-between mb-4 border-b border-slate-800 pb-3">
            <div>
              <h3 className="font-extrabold text-sm text-indigo-400 flex items-center gap-1.5">
                <Terminal className="w-4 h-4" />
                {t('secrets.sim_title')}
              </h3>
              <p className="text-[10px] text-slate-500 mt-0.5">
                {t('secrets.sim_subtitle')}
              </p>
            </div>
            {!simulating ? (
              <button
                onClick={runSimulation}
                className="flex items-center gap-1.5 px-3 py-1.5 bg-indigo-600 hover:bg-indigo-500 active:scale-95 text-white rounded-lg text-xs font-bold transition-all"
              >
                <Play className="w-3.5 h-3.5" />
                {t('secrets.sim_start')}
              </button>
            ) : (
              <div className="flex items-center gap-2 text-xs text-indigo-400">
                <RefreshCw className="w-3.5 h-3.5 animate-spin" />
                <span>{t('secrets.sim_running')}</span>
              </div>
            )}
          </div>

          <div className="grid grid-cols-1 lg:grid-cols-12 gap-6 items-stretch">
            {/* Step status */}
            <div className="lg:col-span-5 space-y-3 flex flex-col justify-between">
              <div className="space-y-2">
                <div className={`flex items-center gap-3 p-2.5 rounded-xl border text-xs transition-colors ${simStep >= 1 ? 'border-indigo-500/40 bg-indigo-950/20 text-indigo-300' : 'border-slate-800 text-slate-500'
                  }`}>
                  <div className={`w-5 h-5 rounded-full flex items-center justify-center font-bold text-[10px] ${simStep > 1 ? 'bg-indigo-500 text-white' : 'border border-current'
                    }`}>
                    {simStep > 1 ? '✓' : '1'}
                  </div>
                  <span>{t('secrets.sim_step1')}</span>
                </div>

                <div className={`flex items-center gap-3 p-2.5 rounded-xl border text-xs transition-colors ${simStep >= 2 ? 'border-indigo-500/40 bg-indigo-950/20 text-indigo-300' : 'border-slate-800 text-slate-500'
                  }`}>
                  <div className={`w-5 h-5 rounded-full flex items-center justify-center font-bold text-[10px] ${simStep > 2 ? 'bg-indigo-500 text-white' : 'border border-current'
                    }`}>
                    {simStep > 2 ? '✓' : '2'}
                  </div>
                  <span>{t('secrets.sim_step2')}</span>
                </div>

                <div className={`flex items-center gap-3 p-2.5 rounded-xl border text-xs transition-colors ${simStep >= 3 ? 'border-indigo-500/40 bg-indigo-950/20 text-indigo-300' : 'border-slate-800 text-slate-500'
                  }`}>
                  <div className={`w-5 h-5 rounded-full flex items-center justify-center font-bold text-[10px] ${simStep > 3 ? 'bg-indigo-500 text-white' : 'border border-current'
                    }`}>
                    {simStep > 3 ? '✓' : '3'}
                  </div>
                  <span>{t('secrets.sim_step3')}</span>
                </div>

                <div className={`flex items-center gap-3 p-2.5 rounded-xl border text-xs transition-colors ${simStep >= 4 ? 'border-indigo-500/40 bg-indigo-950/20 text-indigo-300' : 'border-slate-800 text-slate-500'
                  }`}>
                  <div className={`w-5 h-5 rounded-full flex items-center justify-center font-bold text-[10px] ${simStep > 4 ? 'bg-indigo-500 text-white' : 'border border-current'
                    }`}>
                    {simStep > 4 ? '✓' : '4'}
                  </div>
                  <span>{t('secrets.sim_step4', { version: selectedFamily.currentVersion + (simulating ? 1 : 0) })}</span>
                </div>

                <div className={`flex items-center gap-3 p-2.5 rounded-xl border text-xs transition-colors ${simStep >= 5 ? 'border-indigo-500/40 bg-indigo-950/20 text-indigo-300' : 'border-slate-800 text-slate-500'
                  }`}>
                  <div className={`w-5 h-5 rounded-full flex items-center justify-center font-bold text-[10px] ${simStep > 5 ? 'bg-indigo-500 text-white' : 'border border-current'
                    }`}>
                    {simStep > 5 ? '✓' : '5'}
                  </div>
                  <span>{t('secrets.sim_step5')}</span>
                </div>

                <div className={`flex items-center gap-3 p-2.5 rounded-xl border text-xs transition-colors ${simStep >= 6 ? 'border-emerald-500/40 bg-emerald-950/20 text-emerald-300 animate-pulse' : 'border-slate-800 text-slate-500'
                  }`}>
                  <div className={`w-5 h-5 rounded-full flex items-center justify-center font-bold text-[10px] ${simStep >= 6 ? 'bg-emerald-500 text-white' : 'border border-current'
                    }`}>
                    {simStep >= 6 ? '✓' : '6'}
                  </div>
                  <span>{t('secrets.sim_step6')}</span>
                </div>
              </div>

              <div className="p-3 bg-slate-900 border border-slate-800 rounded-xl text-[10px] text-slate-500">
                <span className="font-bold text-slate-400 block mb-1">{t('secrets.sim_fallback_title')}</span>
                {t('secrets.sim_fallback_desc')}
              </div>
            </div>

            {/* Log Terminal Screen */}
            <div className="lg:col-span-7 flex flex-col">
              <div className="flex-1 bg-black/80 rounded-xl p-4 font-mono text-[11px] text-slate-400 h-64 overflow-y-auto border border-slate-900 flex flex-col justify-between">
                <div className="space-y-1.5">
                  {logs.length === 0 && (
                    <div className="text-slate-600 italic">
                      {t('secrets.sim_waiting')}
                    </div>
                  )}
                  {logs.map((log, idx) => (
                    <div key={idx} className="leading-relaxed whitespace-pre-wrap">
                      {log.includes('✅') ? (
                        <span className="text-emerald-400">{log}</span>
                      ) : log.includes('🚨') || log.includes('🔄') ? (
                        <span className="text-indigo-400">{log}</span>
                      ) : (
                        log
                      )}
                    </div>
                  ))}
                </div>
                {simulating && (
                  <div className="flex items-center gap-1.5 text-indigo-400 mt-2 animate-pulse">
                    <span>█</span>
                    <span>{t('secrets.sim_next_step')}</span>
                  </div>
                )}
              </div>
            </div>
          </div>
        </div>
      </Section>

    </PageLayout>
  )
}
