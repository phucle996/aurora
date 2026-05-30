import { useTranslation } from 'react-i18next'
import Section from '../components/spec/Section.jsx'
import Callout from '../components/spec/Callout.jsx'
import KeyValueTable from '../components/spec/KeyValueTable.jsx'
import FragmentCards from '../components/spec/FragmentCards.jsx'
import StateDiagram from '../components/spec/StateDiagram.jsx'
import CodeBlock from '../components/spec/CodeBlock.jsx'

export default function AuthTokenModel() {
  const { t } = useTranslation()
  return (
    <div className="max-w-5xl mx-auto px-8 py-10 space-y-12">
      {/* Hero */}
      <header className="bg-gradient-to-br from-indigo-100 via-white to-purple-100 dark:from-indigo-900/40 dark:via-slate-900 dark:to-purple-900/40 border border-indigo-200 dark:border-indigo-800/50 rounded-2xl p-8">
        <div className="flex flex-wrap items-center gap-2 text-xs mb-4">
          <span className="px-2.5 py-1 bg-indigo-100 dark:bg-indigo-500/20 text-indigo-700 dark:text-indigo-300 rounded-full border border-indigo-300 dark:border-indigo-500/30">{t('common.hero_badges.version')}</span>
          <span className="px-2.5 py-1 bg-slate-100 dark:bg-slate-800 text-slate-700 dark:text-slate-300 rounded-full border border-slate-300 dark:border-slate-700">{t('common.hero_badges.updated')}</span>
          <span className="px-2.5 py-1 bg-emerald-100 dark:bg-emerald-500/20 text-emerald-700 dark:text-emerald-300 rounded-full border border-emerald-300 dark:border-emerald-500/30">{t('common.hero_badges.production_ready')}</span>
        </div>
        <h1 className="text-4xl font-bold mb-3 bg-gradient-to-r from-indigo-700 to-purple-700 dark:from-white dark:to-indigo-200 bg-clip-text text-transparent">
          {t('auth_token_model.hero.title')}
        </h1>
        <p className="text-lg text-slate-700 dark:text-slate-300 mb-2">{t('auth_token_model.hero.subtitle')}</p>
        <p className="text-sm text-slate-500 dark:text-slate-400 mb-6">{t('auth_token_model.hero.scope')}</p>

        <div className="grid grid-cols-2 md:grid-cols-3 gap-3">
          {[
            { labelKey: 'auth_token_model.key_props.fragment', valueKey: 'auth_token_model.key_props.fragment_value' },
            { labelKey: 'auth_token_model.key_props.session_ttl', valueKey: 'auth_token_model.key_props.session_ttl_value' },
            { labelKey: 'auth_token_model.key_props.device_binding', valueKey: 'auth_token_model.key_props.device_binding_value' },
            { labelKey: 'auth_token_model.key_props.revocation', valueKey: 'auth_token_model.key_props.revocation_value' },
            { labelKey: 'auth_token_model.key_props.ha_grace', valueKey: 'auth_token_model.key_props.ha_grace_value' },
            { labelKey: 'auth_token_model.key_props.plane_isolation', valueKey: 'auth_token_model.key_props.plane_isolation_value' },
          ].map((p) => (
            <div key={p.labelKey} className="bg-white/80 dark:bg-slate-900/60 border border-slate-200 dark:border-slate-800 rounded-lg p-3">
              <p className="text-[11px] uppercase tracking-wider text-slate-500">{t(p.labelKey)}</p>
              <p className="font-semibold text-slate-900 dark:text-slate-100 text-sm mt-1">{t(p.valueKey)}</p>
            </div>
          ))}
        </div>
      </header>

      {/* Executive summary */}
      <Section number="0" title="Executive Summary">
        <p>
          Aurora Admin uses a <strong>Fragment Token</strong> architecture with{' '}
          <strong>4-layer defense-in-depth</strong> to protect infrastructure
          operations. This page documents the token model and session lifecycle,
          not authentication flows.
        </p>
        <ul className="list-disc list-inside text-slate-300 space-y-1">
          <li>3-fragment token (JWT + AccessKey + AccessSecret)</li>
          <li>Device binding via Ed25519 public key</li>
          <li>15-minute inactivity timeout (Redis TTL)</li>
          <li>Instant session revocation (&lt; 1ms)</li>
          <li>HA-safe token rotation (10s grace period)</li>
          <li>Separate admin plane (isolated from user plane)</li>
        </ul>
      </Section>

      {/* 1. Token Model */}
      <Section number="1" title="Token Model">
        <h3 className="text-xl font-semibold text-slate-100 mt-2 mb-3">1.1 Fragment Token Architecture</h3>
        <p>Admin authentication uses <strong>3 independent token fragments</strong> that must all be valid for a request to succeed.</p>
        <FragmentCards />

        <h3 className="text-xl font-semibold text-slate-100 mt-8 mb-3">1.2 Why 3 Fragments?</h3>
        <KeyValueTable
          headers={['Fragment', 'Protects Against', 'Mechanism']}
          rows={[
            ['JWT', 'Token tampering', 'Signature verification'],
            ['AccessKey', 'Token substitution', 'Claim binding + Redis lookup'],
            ['AccessSecret', 'Session hijacking', 'Hash comparison + entropy'],
          ]}
        />

        <Callout type="danger" title="Attack scenario — XSS steals JWT">
          <ul className="space-y-1 text-sm">
            <li>❌ Cannot use JWT alone (missing AccessKey + AccessSecret cookies)</li>
            <li>❌ Cannot forge AccessSecret (48-byte entropy + hash)</li>
            <li>❌ Cannot substitute AccessKey (claim binding check)</li>
            <li className="text-emerald-300">✅ Request rejected with 401</li>
          </ul>
        </Callout>

        <h3 className="text-xl font-semibold text-slate-100 mt-8 mb-3">1.3 Separation from User Plane</h3>
        <p>Admin tokens are <strong>completely isolated</strong> from user/customer tokens.</p>
        <KeyValueTable
          headers={['Aspect', 'Admin', 'User']}
          rows={[
            ['Secret Family', 'SecretFamilyAdminAPIKey', 'SecretFamilyAccess'],
            ['Cookie Path', '/admin', '/'],
            ['Session Key Prefix', 'iam:admin:session:*', 'iam:user:session:*'],
            ['Cookie Names', 'access_token, access_key, access_secret', 'Same names, different path'],
            ['Device Cookie', 'client_device_id (365d)', 'device_id (user-specific)'],
            ['Middleware', 'AdminAPIKeyAuth', 'Access'],
            ['Verification', '3-fragment + device binding', 'JWT only'],
          ]}
        />
        <Callout type="info">
          Admin session cannot be used for user operations and vice versa.
        </Callout>
      </Section>

      {/* 2. Token Lifecycle */}
      <Section number="2" title="Token Lifecycle">
        <h3 className="text-xl font-semibold text-slate-100 mt-2 mb-3">2.1 State Diagram</h3>
        <StateDiagram />

        <h3 className="text-xl font-semibold text-slate-100 mt-8 mb-3">2.2 Session States</h3>
        <KeyValueTable
          headers={['State', 'Duration', 'Condition', 'Action']}
          rows={[
            ['ACTIVE', '15m', 'Session exists in Redis', 'Accept requests'],
            ['GRACE', '10s', 'Old session after refresh', 'Accept in-flight'],
            ['BLACKLISTED', '15m logout / 15s refresh', 'JTI in blacklist', 'Reject requests'],
            ['EXPIRED', '—', 'TTL=0 in Redis', 'Auto-delete, reject'],
            ['REVOKED', '—', 'Explicit logout', 'Immediate delete + blacklist'],
          ]}
        />
      </Section>

      {/* 3. Token Components */}
      <Section number="3" title="Token Components">
        <h3 className="text-xl font-semibold text-slate-100 mt-2 mb-3">3.1 JWT (access_token)</h3>
        <CodeBlock language="json" code={`Header: {
  "alg": "HS256",
  "typ": "JWT"
}

Payload: {
  "sub": "admin",
  "access_key": "<UUIDv7>",
  "jti": "<UUIDv7>",
  "token_use": "admin_api",
  "iat": <unix_seconds>,
  "exp": <unix_seconds>
}

Signature: HMAC-SHA256(header.payload, secret_family=admin_api_key)`} />
        <KeyValueTable
          headers={['Property', 'Value']}
          rows={[
            ['Verification', 'Stateless at gateway (no DB/Redis)'],
            ['Lifetime', '15 minutes (AdminSessionTTL)'],
            ['Rotation', 'New JWT on every refresh'],
            ['Revocation', 'JTI tracked in blacklist'],
          ]}
        />
        <p className="text-sm text-slate-400">Verification steps: signature → expiry → JTI blacklist.</p>

        <h3 className="text-xl font-semibold text-slate-100 mt-8 mb-3">3.2 AccessKey (access_key cookie)</h3>
        <KeyValueTable
          headers={['Property', 'Value']}
          rows={[
            ['Type', 'UUIDv7 (128-bit random)'],
            ['TTL', '15 minutes'],
            ['Storage', 'Redis session key'],
            ['Binding', 'Must match JWT claim access_key'],
            ['Rotation', 'New UUIDv7 on every refresh'],
          ]}
        />
        <CodeBlock language="text" code={`Cookie:    access_key=550e8400-e29b-41d4-a716-446655440000
JWT Claim: "access_key": "550e8400-e29b-41d4-a716-446655440000"
Redis Key: iam:admin:session:550e8400-e29b-41d4-a716-446655440000`} />

        <h3 className="text-xl font-semibold text-slate-100 mt-8 mb-3">3.3 AccessSecret (access_secret cookie)</h3>
        <KeyValueTable
          headers={['Property', 'Value']}
          rows={[
            ['Type', '48-byte random entropy (384 bits)'],
            ['TTL', '15 minutes'],
            ['Storage', 'SHA256 hash in Redis session'],
            ['Plaintext', 'Only in RAM during request'],
            ['Rotation', 'New 48-byte on every refresh'],
          ]}
        />
        <CodeBlock language="text" code={`Received:  access_secret (plaintext from cookie)
Stored:    SHA256(access_secret) in Redis session
Check:     SHA256(received) == stored`} />

        <h3 className="text-xl font-semibold text-slate-100 mt-8 mb-3">3.4 ClientDeviceID (client_device_id cookie)</h3>
        <KeyValueTable
          headers={['Property', 'Value']}
          rows={[
            ['Type', 'UUIDv7 (128-bit random)'],
            ['TTL', '365 days (AdminTrustedDeviceTTL)'],
            ['Storage', 'Redis session + PostgreSQL'],
            ['Binding', 'Linked to device public key'],
            ['Renewal', 'Extended 365d on every refresh'],
          ]}
        />
      </Section>

      {/* 4. Redis Session Storage */}
      <Section number="4" title="Redis Session Storage">
        <h3 className="text-xl font-semibold text-slate-100 mt-2 mb-3">4.1 Session Record</h3>
        <p>Key: <code className="text-amber-700 dark:text-amber-300">iam:admin:session:{'{AccessKey}'}</code> — TTL 15 minutes.</p>
        <CodeBlock language="json" code={`{
  "AccessKey": "<UUIDv7>",
  "AccessSecretHash": "<SHA256 hash>",
  "TrackedDeviceID": "<UUIDv7>",
  "DevicePublicKey": "<Ed25519 public key>",
  "TokenJTI": "<UUIDv7>",
  "Version": 1,
  "LastSeenAt": <unix_seconds>,
  "LastSeenIP": "<IP address>",
  "LastSeenUserAgent": "<User-Agent>",
  "LastSeenDirty": false,
  "ExpiresAt": <unix_seconds>
}`} />

        <h3 className="text-xl font-semibold text-slate-100 mt-8 mb-3">4.2 Session Verification</h3>
        <p>On every request, middleware verifies all 6 checks. Any failure → 401.</p>
        <ol className="list-decimal list-inside space-y-1 text-slate-300">
          <li>JWT Signature: HMAC-SHA256 valid</li>
          <li>JWT Expiry: <code>exp &gt; now</code></li>
          <li>AccessKey Binding: JWT claim == cookie</li>
          <li>AccessSecret Hash: <code>SHA256(cookie) == Redis value</code></li>
          <li>Session Exists: Redis key found</li>
          <li>JTI Blacklist: Not in <code>iam:blacklist:{'{JTI}'}</code></li>
        </ol>

        <h3 className="text-xl font-semibold text-slate-100 mt-8 mb-3">4.3 JTI Blacklist</h3>
        <KeyValueTable
          headers={['Property', 'Value']}
          rows={[
            ['Key', 'iam:blacklist:{JTI}'],
            ['Value', '"1" (marker)'],
            ['TTL on logout', '15 minutes (AdminSessionTTL)'],
            ['TTL on refresh', '15 seconds (grace + buffer)'],
            ['Local cache', '5-minute TTL for revoked=true only'],
          ]}
        />
        <Callout type="warning" title="Cache policy">
          Local in-process cache only stores <code>revoked=true</code>. We never
          cache <code>revoked=false</code> so logout / revoke is effective
          immediately across the cluster.
        </Callout>
      </Section>

      {/* 5. Token Rotation */}
      <Section number="5" title="Token Rotation">
        <h3 className="text-xl font-semibold text-slate-100 mt-2 mb-3">5.1 Rotation Trigger</h3>
        <p>Token rotation happens on <strong>every refresh</strong> (not on every request).</p>
        <ul className="list-disc list-inside text-slate-300 space-y-1">
          <li>Frontend detects <code>X-Session-Expires-In &lt; 300s</code></li>
          <li>Frontend calls <code>POST /admin/auth/refresh</code></li>
          <li>Backend generates new tokens</li>
          <li>Frontend receives new cookies</li>
        </ul>

        <h3 className="text-xl font-semibold text-slate-100 mt-8 mb-3">5.2 What Rotates</h3>
        <KeyValueTable
          headers={['Component', 'Old', 'New', 'Reason']}
          rows={[
            ['AccessKey', 'UUIDv7', 'UUIDv7', 'Session ID rotation'],
            ['AccessSecret', '48-byte', '48-byte', 'Entropy refresh'],
            ['JWT Token', 'Signed', 'Signed', 'Token refresh'],
            ['JTI', 'UUIDv7', 'UUIDv7', 'Revocation ID rotation'],
          ]}
        />
        <Callout type="info">All 4 components rotate together to maintain consistency.</Callout>

        <h3 className="text-xl font-semibold text-slate-100 mt-8 mb-3">5.3 HA Grace Period</h3>
        <p>Old session is not deleted immediately on refresh — it lingers for <strong>10 seconds</strong>.</p>
        <CodeBlock language="text" code={`T=0s:    Refresh → new session (TTL=15m), old session (TTL=10s)
T=0-10s: In-flight requests with old cookies still pass
T=10s:   Old session auto-expires
T=10s+:  Old cookies → 401 Unauthorized`} />
        <p className="text-sm text-slate-400">
          Mechanism: old session TTL=10s, old JTI blacklisted with TTL=15s,
          CAS version check prevents concurrent refresh conflicts.
        </p>
      </Section>

      {/* 6. Cookie spec */}
      <Section number="6" title="Cookie Specification">
        <h3 className="text-xl font-semibold text-slate-100 mt-2 mb-3">6.1 Security Matrix</h3>
        <KeyValueTable
          headers={['Cookie', 'HttpOnly', 'Secure', 'SameSite', 'TTL', 'Path', 'Purpose']}
          rows={[
            ['access_token', '✅', '✅', 'Lax', '15m', '/admin', 'JWT stateless auth'],
            ['access_key', '✅', '✅', 'Lax', '15m', '/admin', 'Session ID'],
            ['access_secret', '✅', '✅', 'Lax', '15m', '/admin', 'Session entropy'],
            ['client_device_id', '✅', '✅', 'Lax', '365d', '/admin', 'Device tracking'],
          ]}
        />

        <h3 className="text-xl font-semibold text-slate-100 mt-8 mb-3">6.2 Cookie Flags</h3>
        <div className="grid md:grid-cols-2 gap-3">
          {[
            { flag: 'HttpOnly = true', desc: 'JavaScript cannot read via document.cookie. Protects against XSS token theft.' },
            { flag: 'Secure = true', desc: 'Cookie only transmitted over HTTPS. Protects against man-in-the-middle.' },
            { flag: 'SameSite = Lax', desc: 'Cookie sent on cross-site navigation in Lax mode. Protects against CSRF.' },
            { flag: 'Path = /admin', desc: 'Cookie only sent for /admin/* requests. Isolates admin scope.' },
          ].map((f) => (
            <div key={f.flag} className="bg-slate-900/60 border border-slate-800 rounded-lg p-4">
              <p className="font-mono text-sm text-indigo-300 mb-1">{f.flag}</p>
              <p className="text-sm text-slate-400">{f.desc}</p>
            </div>
          ))}
        </div>
      </Section>

      {/* 7. Inactivity Timeout */}
      <Section number="7" title="Inactivity Timeout">
        <KeyValueTable
          headers={['Property', 'Value']}
          rows={[
            ['Redis TTL', '15 minutes (AdminSessionTTL)'],
            ['Auto-extend', 'NO (stateless design)'],
            ['Refresh required', 'Before TTL expires'],
            ['Expiry action', 'Redis auto-delete'],
          ]}
        />
        <CodeBlock language="text" code={`T=0m:    Login → session TTL=15m
T=5m:    Request → session TTL=10m (NOT extended)
T=10m:   Request → session TTL=5m  (NOT extended)
T=15m:   Session expires → Redis auto-delete
T=15.5m: Request with old cookies → 401 Unauthorized`} />
        <Callout type="info" title="Frontend prevention">
          Frontend reads <code>X-Session-Expires-In</code> header. If &lt; 300s,
          it triggers a silent refresh through <code>POST /admin/auth/refresh</code>.
          A localStorage mutex ensures only one tab refreshes at a time.
        </Callout>
      </Section>

      {/* 8. Device Binding */}
      <Section number="8" title="Device Binding">
        <h3 className="text-xl font-semibold text-slate-100 mt-2 mb-3">8.1 Device Identification</h3>
        <ul className="list-disc list-inside text-slate-300 space-y-1">
          <li>Generated at login (UUIDv7)</li>
          <li>Stored in 365-day cookie</li>
          <li>Bound to Ed25519 public key</li>
          <li>Persisted in Redis session and PostgreSQL</li>
        </ul>

        <h3 className="text-xl font-semibold text-slate-100 mt-8 mb-3">8.2 Device Public Key</h3>
        <KeyValueTable
          headers={['Property', 'Value']}
          rows={[
            ['Format', 'Ed25519 public key (32 bytes)'],
            ['Provided by', 'Client at login'],
            ['Stored in', 'Redis session + PostgreSQL device registry'],
            ['Used for', 'Signature verification on ultra-sensitive ops'],
          ]}
        />

        <h3 className="text-xl font-semibold text-slate-100 mt-8 mb-3">8.3 Device Revocation</h3>
        <ol className="list-decimal list-inside text-slate-300 space-y-1">
          <li>Delete device public key from PostgreSQL</li>
          <li>Delete all sessions for that device from Redis</li>
          <li>Admin must login from a different device</li>
        </ol>
      </Section>

      {/* 9. Threat model */}
      <Section number="9" title="Threat Model & Mitigations">
        <KeyValueTable
          headers={['Threat', 'Mitigation', 'Token Component']}
          rows={[
            ['Session Hijacking', '3-fragment token', 'JWT + AccessKey + AccessSecret'],
            ['Token Substitution', 'AccessKey binding', 'Claim == cookie check'],
            ['XSS Token Theft', 'HttpOnly cookie', 'JavaScript cannot read'],
            ['CSRF Attack', 'SameSite=Lax', 'Browser-level protection'],
            ['Man-in-the-Middle', 'Secure flag', 'HTTPS only'],
            ['Replay Attack', 'JTI + timestamp', 'Blacklist + rotation'],
            ['Inactivity Leak', 'Redis TTL', 'Auto-expire 15m'],
            ['Device Hijacking', 'Ed25519 binding', 'Public key verification'],
            ['Brute Force', 'Rate limiting', 'Per-IP limit on login'],
            ['User Enumeration', 'Generic errors', 'Same 401 for all failures'],
            ['Multi-Tab Race', 'Grace period', '10s window for in-flight'],
            ['HA Inconsistency', 'CAS version', 'Optimistic concurrency'],
          ]}
        />
      </Section>

      {/* 10. Configuration */}
      <Section number="10" title="Configuration">
        <h3 className="text-xl font-semibold text-slate-100 mt-2 mb-3">10.1 Security Config</h3>
        <CodeBlock language="go" code={`type SecurityCfg struct {
    AdminAPITokenTTL      time.Duration  // 15 * 24 * time.Hour (API key lifetime)
    AdminSessionTTL       time.Duration  // 15 * time.Minute    (session + JWT TTL)
    AdminTrustedDeviceTTL time.Duration  // 365 * 24 * time.Hour (device cookie)
    AdminAllowedCIDRs     []string       // IP whitelist
}`} />

        <h3 className="text-xl font-semibold text-slate-100 mt-8 mb-3">10.2 Token TTLs</h3>
        <KeyValueTable
          headers={['Token', 'TTL', 'Config']}
          rows={[
            ['JWT (access_token)', '15 minutes', 'AdminSessionTTL'],
            ['AccessKey', '15 minutes', 'AdminSessionTTL'],
            ['AccessSecret', '15 minutes', 'AdminSessionTTL'],
            ['ClientDeviceID', '365 days', 'AdminTrustedDeviceTTL'],
            ['API Key (plaintext)', '15 days', 'AdminAPITokenTTL'],
            ['JTI Blacklist (logout)', '15 minutes', 'AdminSessionTTL'],
            ['JTI Blacklist (refresh)', '15 seconds', 'Grace + buffer'],
            ['Grace Period', '10 seconds', 'Hardcoded'],
          ]}
        />
      </Section>

      {/* 11. Security Principles */}
      <Section number="11" title="Security Principles">
        <ol className="list-decimal list-inside text-slate-300 space-y-1">
          <li><strong>Defense in Depth</strong>: 3 independent fragments</li>
          <li><strong>Zero Trust</strong>: Every request re-verifies all 3 fragments</li>
          <li><strong>Short-Lived</strong>: 15-minute tokens, rotate on every refresh</li>
          <li><strong>Fail-Closed</strong>: Missing fragment → 401, no fallback</li>
          <li><strong>Stateless Edge</strong>: JWT verify at gateway (no DB)</li>
          <li><strong>Stateful Revocation</strong>: Redis session for instant logout</li>
          <li><strong>Device Binding</strong>: Ed25519 for ultra-sensitive ops</li>
          <li><strong>Plane Isolation</strong>: Admin tokens separate from user tokens</li>
        </ol>
      </Section>

      {/* 12. Operational Guarantees */}
      <Section number="12" title="Operational Guarantees">
        <KeyValueTable
          headers={['Capability', 'Guarantee', 'Latency']}
          rows={[
            ['Instant Logout', 'Supported', '< 1ms (Redis DEL)'],
            ['Session Revocation', 'Immediate', '< 1ms'],
            ['Token Verification', 'Stateless', '< 100ms (Redis timeout)'],
            ['HA Safe Refresh', 'Supported', '10s grace window'],
            ['Device Tracking', 'Supported', '365d cookie'],
            ['Inactivity Auto Logout', 'Supported', '15 minutes'],
            ['Replay Resistance', 'Supported', 'JTI blacklist'],
          ]}
        />
      </Section>

      {/* 13. References */}
      <Section number="13" title="References">
        <ul className="list-disc list-inside text-slate-300 space-y-1">
          <li><strong>JWT</strong>: RFC 7519</li>
          <li><strong>TOTP</strong>: RFC 6238</li>
          <li><strong>Ed25519</strong>: RFC 8032</li>
          <li><strong>UUID v7</strong>: RFC 9562</li>
          <li><strong>OWASP</strong>: Session Management Cheat Sheet</li>
          <li><strong>NIST</strong>: SP 800-63B (Authentication &amp; Lifecycle Management)</li>
        </ul>
      </Section>

      <footer className="border-t border-slate-200 dark:border-slate-800 pt-6 text-center text-sm text-slate-500">
        <p>{t('auth_token_model.footer')}</p>
        <p className="mt-1">{t('auth_token_model.footer_sub')}</p>
      </footer>
    </div>
  )
}
