import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import Section from '../../components/spec/Section.jsx'
import Callout from '../../components/spec/Callout.jsx'
import KeyValueTable from '../../components/spec/KeyValueTable.jsx'
import CodeBlock from '../../components/spec/CodeBlock.jsx'
import PageLayout from '../../components/PageLayout.jsx'
import { VisualSequenceFlow, VisualStateMachine, VisualHotReloadFlow } from '../../components/spec/VisualFlows.jsx'

export default function SecurityRateLimit() {
  const { t } = useTranslation()
  const [activeTab, setActiveTab] = useState('preauth')

  const nav = [
    { num: 1, label: t('rate_limit.overview.title', 'Tổng Quan Hệ Thống'), href: '#section-1' },
    { num: 2, label: t('rate_limit.comparison.title', 'Mô Hình Pre-Auth & Post-Auth'), href: '#section-2' },
    { num: 3, label: t('rate_limit.sequence.title', 'Quy Trình Hoạt Động (Sequence)'), href: '#section-3' },
    { num: 4, label: t('rate_limit.state_machine.title', 'State Machine: Xử Lý Vi Phạm'), href: '#section-4' },
    { num: 5, label: t('rate_limit.hot_reload.title', 'Atomic Hot-Reload & Hooks'), href: '#section-5' },
  ]

  const tArray = (key, fallback) => {
    const res = t(key, { returnObjects: true })
    return Array.isArray(res) ? res : fallback
  }

  const preauthHeaders = tArray('rate_limit.preauth_table.headers', [
    'Thuộc Tính Đặc Tả', 'Cấu Hình Pre-Auth', 'Mô Tả & Quy Tắc Vận Hành'
  ])

  const preauthRows = tArray('rate_limit.preauth_table.rows', [
    [
      'Đối Tượng Định Danh (Identity)',
      'Địa chỉ IP của client (Client IP)',
      'Pre-Auth bảo vệ hạ tầng ở rìa biên mạng trước DDoS thô bạo bằng cách theo dõi địa chỉ IP của Client.'
    ],
    [
      'Quy Tắc Match Đường Dẫn (Route Matching)',
      'Không cấu hình path (Áp dụng toàn cục)',
      'Tự động áp dụng cho mọi request đi vào hệ thống trước khi qua bộ giải quyết danh tính.'
    ],
    [
      'Hành Vi Mặc Định (Fallback)',
      'Fail-Closed (Chặn nếu Redis hoặc bộ lọc lỗi)',
      'Nếu Redis Cluster hoặc Middleware gặp sự cố, hệ thống sẽ tự động chặn toàn bộ truy cập ở biên mạng để bảo vệ tài nguyên lõi.'
    ],
    [
      'Vị Trí Redis Key',
      'rl:preauth:ip:<ip_address>',
      'Được lưu trữ tách biệt theo định dạng IP để dễ dàng truy vấn và xóa quota thủ công.'
    ]
  ])

  const postauthHeaders = tArray('rate_limit.postauth_table.headers', [
    'Thuộc Tính Đặc Tả', 'Cấu Hình Post-Auth', 'Mô Tả & Quy Tắc Vận Hành'
  ])

  const postauthRows = tArray('rate_limit.postauth_table.rows', [
    [
      'Đối Tượng Định Danh (Identity)',
      'Địa chỉ IP + Thiết bị (IP & Device ID) hoặc IP + User ID',
      'Sử dụng IP + Device ID cho các endpoint nhạy cảm khi CHƯA đăng nhập (như Login, OTP, Register) để chặn brute-force; sử dụng IP + User ID cho các API ĐÃ xác thực (JWT claims chứa User ID) để quản lý quota tài khoản.'
    ],
    [
      'Quy Tắc Match Đường Dẫn (Route Matching)',
      'Pre-compiled Route Path (O(1) Map Lookup)',
      'Cấu hình được biên dịch tĩnh thành một bản đồ trong bộ nhớ. Đường dẫn request được so khớp tức thời O(1), không tốn tài nguyên runtime.'
    ],
    [
      'Hành Vi Mặc Định (Fallback)',
      'Fail-Open / Bypass (Cho qua nếu route không cấu hình)',
      'Nếu một đường dẫn API mới không được đăng ký trong cấu hình, hệ thống mặc định cho phép vượt qua (Bypass), đảm bảo tính sẵn sàng cao.'
    ],
    [
      'Vị Trí Redis Key',
      '<api_path>:ip_device:<ip>:<device_id> hoặc <api_path>:ip_user:<ip>:<user_id>',
      'Được phân định rõ ràng theo namespace route và người dùng/thiết bị để tối ưu hóa khả năng giám sát tài nguyên.'
    ]
  ])

  return (
    <PageLayout
      nav={nav}
      header={
        <>
          <div className="flex items-center gap-3 mb-3">
            <span className="inline-flex items-center justify-center w-10 h-10 rounded-xl bg-gradient-to-br from-red-500 to-pink-500 text-white text-lg">
              ⏳
            </span>
            <div>
              <h1 className="text-3xl font-bold text-slate-900 dark:text-slate-100">
                {t('rate_limit.hero.title', 'Hệ Thống Giới Hạn Tần Suất (Rate Limiting)')}
              </h1>
              <p className="text-slate-500 dark:text-slate-400 text-base mt-0.5">
                {t('rate_limit.hero.subtitle', 'Kiến trúc kiểm soát lưu lượng bảo mật kép, tải động thông qua Policy Engine và đồng bộ hóa phi trạng thái bằng Redis Cluster.')}
              </p>
            </div>
          </div>
          <Callout type="info" title={t('rate_limit.callout.design_goals', 'MỤC TIÊU THIẾT KẾ CLOUD NATIVE & HA')}>
            {t('rate_limit.callout.design_goals_desc', 'Hệ thống được phát triển theo mô hình High-Availability (HA), xử lý hàng triệu request đồng thời với độ trễ dưới 1.5ms nhờ cơ chế biên dịch tĩnh route và lưu trữ phân tán hiệu năng cao trên Redis Cluster.')}
          </Callout>
        </>
      }
    >
      <Section number={1} title={t('rate_limit.overview.title', 'Tổng Quan Hệ Thống & Vai Trò')}>
        <p className="text-lg leading-relaxed text-slate-600 dark:text-slate-300 font-normal">
          {t('rate_limit.overview.desc', 'Hệ thống Rate Limiter trong hệ sinh thái Aurora được xây dựng trên mô hình bảo vệ hai lớp (Dual-Layer Shield). Lớp bảo vệ này chia tách rõ ràng trách nhiệm giữa việc ngăn chặn tấn công từ chối dịch vụ (DDoS) và việc quản lý công bằng tài nguyên sử dụng cho từng người dùng đã xác thực.')}
        </p>

        <div className="grid md:grid-cols-3 gap-6 mt-6">
          <div className="p-5 bg-slate-50 dark:bg-slate-900/60 border border-slate-200 dark:border-slate-800 rounded-xl hover:-translate-y-1 hover:shadow-lg hover:shadow-indigo-500/5 transition-all duration-300">
            <div className="text-xl mb-2 text-indigo-600 dark:text-indigo-400">
              <strong>{t('rate_limit.overview.sre_operator_title', '🧑‍💻 SRE Operator')}</strong>
            </div>
            <p className="text-base text-slate-600 dark:text-slate-400 leading-relaxed">
              {t('rate_limit.overview.sre_operator_desc', 'Quản lý toàn bộ cấu hình giới hạn (quota, refill rate, bypass routes) tập trung thông qua Policy Engine (file YAML). Có thể cập nhật nóng chính sách tức thời mà không cần restart server.')}
            </p>
          </div>
          <div className="p-5 bg-slate-50 dark:bg-slate-900/60 border border-slate-200 dark:border-slate-800 rounded-xl hover:-translate-y-1 hover:shadow-lg hover:shadow-indigo-500/5 transition-all duration-300">
            <div className="text-xl mb-2 text-indigo-600 dark:text-indigo-400">
              <strong>{t('rate_limit.overview.pre_auth_title', '🚦 Pre-Auth Shield')}</strong>
            </div>
            <p className="text-base text-slate-600 dark:text-slate-400 leading-relaxed">
              {t('rate_limit.overview.pre_auth_desc', 'Ngăn chặn các cuộc tấn công Brute-force, DDoS thô bạo ngay từ biên mạng dựa trên địa chỉ IP. Tránh quá tải cho các tác vụ giải mã mật mã (JWT, bcrypt) và bảo vệ cơ sở dữ liệu.')}
            </p>
          </div>
          <div className="p-5 bg-slate-50 dark:bg-slate-900/60 border border-slate-200 dark:border-slate-800 rounded-xl hover:-translate-y-1 hover:shadow-lg hover:shadow-indigo-500/5 transition-all duration-300">
            <div className="text-xl mb-2 text-indigo-600 dark:text-indigo-400">
              <strong>{t('rate_limit.overview.post_auth_title', '🛡️ Post-Auth Engine')}</strong>
            </div>
            <p className="text-base text-slate-600 dark:text-slate-400 leading-relaxed">
              {t('rate_limit.overview.post_auth_desc', 'Kiểm soát hạn mức sử dụng (quota) và chặn đứng spam thông minh theo từng endpoint/route nghiệp vụ cụ thể sau khi đã xác định danh tính (JWT). Áp dụng thuật toán đối khớp nhanh O(1) trong bộ nhớ để bảo vệ từng API nghiệp vụ riêng biệt khỏi việc bị quá tải hoặc khai thác quá mức.')}
            </p>
          </div>
        </div>
      </Section>

      {/* ── SECTION 2: ARCHITECTURE COMPARISON ── */}
      <Section number={2} title={t('rate_limit.comparison.title', 'Mô Hình Pre-Auth & Post-Auth Specification')}>
        <p className="text-base text-slate-600 dark:text-slate-300 mb-6">
          {t('rate_limit.comparison.subtitle', 'Đặc tả thông số kỹ thuật chi tiết của hai lớp bảo vệ Pre-Auth và Post-Auth được tách bạch rõ ràng:')}
        </p>

        <div className="space-y-8 mb-8">
          <div>
            <h4 className="text-lg font-bold text-indigo-600 dark:text-indigo-400 mb-3 flex items-center gap-2">
              <span className="w-2.5 h-2.5 rounded-full bg-indigo-500 animate-pulse"></span>
              {t('rate_limit.preauth_table.title', '1. Đặc tả Lớp Bảo vệ Tiền Xác Thực (Pre-Auth Specification)')}
            </h4>
            <KeyValueTable headers={preauthHeaders} rows={preauthRows} />
          </div>

          <div>
            <h4 className="text-lg font-bold text-purple-600 dark:text-purple-400 mb-3 flex items-center gap-2">
              <span className="w-2.5 h-2.5 rounded-full bg-purple-500 animate-pulse"></span>
              {t('rate_limit.postauth_table.title', '2. Đặc tả Lớp Bảo vệ Hậu Xác Thực (Post-Auth Specification)')}
            </h4>
            <KeyValueTable headers={postauthHeaders} rows={postauthRows} />
          </div>
        </div>

        <div className="mt-6 space-y-4">
          <div className="flex gap-2 border-b border-slate-200 dark:border-slate-800">
            <button
              onClick={() => setActiveTab('preauth')}
              className={`px-4 py-2 text-base font-semibold border-b-2 transition-colors ${
                activeTab === 'preauth'
                  ? 'border-indigo-500 text-indigo-600 dark:text-indigo-400'
                  : 'border-transparent text-slate-500 hover:text-slate-800 dark:hover:text-slate-200'
              }`}
            >
              {t('rate_limit.comparison.tab_preauth', 'Cấu hình Pre-Auth (Global Shield)')}
            </button>
            <button
              onClick={() => setActiveTab('postauth')}
              className={`px-4 py-2 text-base font-semibold border-b-2 transition-colors ${
                activeTab === 'postauth'
                  ? 'border-indigo-500 text-indigo-600 dark:text-indigo-400'
                  : 'border-transparent text-slate-500 hover:text-slate-800 dark:hover:text-slate-200'
              }`}
            >
              {t('rate_limit.comparison.tab_postauth', 'Cấu hình Post-Auth (Dynamic Paths)')}
            </button>
          </div>

          {activeTab === 'preauth' ? (
            <div className="space-y-2 animate-fadeIn">
              <p className="text-sm text-slate-500">
                {t('rate_limit.comparison.label_preauth_code', 'Ví dụ cấu hình giới hạn IP toàn cục khi chưa đăng nhập (Pre-Auth) trong `policy.yaml`:')}
              </p>
              <CodeBlock
                language="yaml"
                code={`rate_limit:
  preauth:
    global_instant:
      max_inflight: 2000          # Giới hạn số lượng request đồng thời xử lý tại một thời điểm
      queue_limit: 50             # Hàng đợi chờ xử lý khi quá tải tối đa
      retry_after_seconds: 2      # SRE gợi ý trình duyệt gửi lại sau x giây khi bị nghẽn
    ip:
      capacity: 100               # Dung lượng tối đa của Token Bucket
      refill: 10                  # Số token nạp lại sau mỗi chu kỳ
      period_seconds: 1           # Chu kỳ nạp lại token (mỗi 1 giây)`}
              />
            </div>
          ) : (
            <div className="space-y-2 animate-fadeIn">
              <p className="text-sm text-slate-500">
                {t('rate_limit.comparison.label_postauth_code', 'Ví dụ cấu hình danh sách đường dẫn nghiệp vụ động (Post-Auth) trong `policy.yaml`:')}
              </p>
              <CodeBlock
                language="yaml"
                code={`rate_limit:
  postauth:
    rules:
      - path: "/api/v1/auth/login"
        capacity: 5               # Tối đa 5 lần thử đăng nhập
        refill: 1                 # Hồi phục 1 token sau mỗi chu kỳ
        period_seconds: 60        # Chu kỳ phục hồi token (mỗi 1 phút)
      - path: "/api/v1/me/devices"
        capacity: 60              # API lấy thông tin thiết bị: tối đa 60 requests
        refill: 60
        period_seconds: 60        # Chu kỳ hồi phục đầy đủ (mỗi 1 phút)`}
              />
            </div>
          )}
        </div>
      </Section>

      {/* ── SECTION 3: SEQUENCE FLOW ── */}
      <Section number={3} title={t('rate_limit.sequence.title', 'Quy Trình Hoạt Động (Sequence Diagram)')}>
        <p className="text-base text-slate-600 dark:text-slate-300 mb-6">
          {t('rate_limit.sequence.subtitle', 'Sơ đồ luồng tương tác bảo mật kép xử lý một yêu cầu từ Client đi qua hệ thống điều phối rate limit:')}
        </p>

        <VisualSequenceFlow />

        <Callout type="info" title={t('rate_limit.callout.race_condition', 'LƯU Ý TRÁNH RACE CONDITION')}>
          {t('rate_limit.callout.race_condition_desc', 'Toán tử trừ token trong Redis được triển khai qua các Script Lua chạy nguyên tử (atomic Lua Script). Điều này đảm bảo tính bền vững dữ liệu, tránh xung đột ghi dữ liệu (race conditions) khi hệ thống chạy đa luồng hoặc được phân tán trên môi trường Cloud-Native HA với nhiều node ứng dụng hoạt động song song.')}
        </Callout>
      </Section>

      {/* ── SECTION 4: STATE MACHINE ── */}
      <Section number={4} title={t('rate_limit.state_machine.title', 'State Machine: Cơ Chế Xử Lý Vi Phạm')}>
        <p className="text-base text-slate-600 dark:text-slate-300 mb-6">
          {t('rate_limit.state_machine.subtitle', 'Hành vi của một Client IP khi tương tác với hệ thống Rate Limit được quản lý chặt chẽ qua một State Machine cục bộ tại mỗi node. Điều này ngăn chặn spam trực tiếp tới Redis khi client liên tục gửi request vi phạm (Anti-DDoS Hot-Path).')}
        </p>

        <VisualStateMachine />
      </Section>

      {/* ── SECTION 5: HOT-RELOAD & HOOKS ── */}
      <Section number={5} title={t('rate_limit.hot_reload.title', 'Atomic Hot-Reload & Hooks Specification')}>
        <p className="text-base text-slate-600 dark:text-slate-300 mb-4">
          {t('rate_limit.hot_reload.desc', 'Một trong những yêu cầu khắt khe nhất của hệ thống Cloud Native HA là không được gián đoạn dịch vụ khi vận hành. Aurora Rate Limiting hỗ trợ thay đổi cấu hình nóng (hot-reload) 100% atomic thông qua cơ chế Hook:')}
        </p>

        <div className="space-y-6">
          <VisualHotReloadFlow />

          <Callout type="success" title={t('rate_limit.callout.security_safe', 'AN TOÀN BẢO MẬT & TRÁNH TRANH CHẤP TRẠNG THÁI')}>
            {t('rate_limit.callout.security_safe_desc', 'Middleware sử dụng cơ chế bảo vệ bằng Khóa đọc/ghi (sync.RWMutex) hoặc hoán đổi nguyên tử (atomic.Value). Nhờ đó, trong suốt quá trình hoán đổi cấu hình, luồng request của khách hàng hoàn toàn không bị ảnh hưởng, không xảy ra tranh chấp dữ liệu (data race) hay lỗi bộ nhớ.')}
          </Callout>
        </div>
      </Section>
    </PageLayout>
  )
}
