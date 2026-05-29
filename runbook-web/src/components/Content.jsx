export default function Content() {
  return (
    <main className="flex-1 overflow-y-auto">
      <div className="max-w-4xl mx-auto p-8 space-y-12">
        {/* Hero */}
        <section id="overview" className="bg-gradient-to-br from-indigo-900/30 to-purple-900/30 border border-indigo-800/50 rounded-xl p-8">
          <h2 className="text-3xl font-bold mb-4">🔐 Fragment Token Architecture</h2>
          <p className="text-slate-300 mb-6">
            Aurora Admin uses a Fragment Token architecture with 4-layer defense-in-depth to protect infrastructure operations.
          </p>
          <div className="grid grid-cols-2 gap-4">
            <div className="bg-slate-800/50 p-4 rounded-lg">
              <p className="text-sm text-slate-400">3-Fragment Token</p>
              <p className="text-lg font-bold text-indigo-400">JWT + AccessKey + AccessSecret</p>
            </div>
            <div className="bg-slate-800/50 p-4 rounded-lg">
              <p className="text-sm text-slate-400">Session TTL</p>
              <p className="text-lg font-bold text-green-400">15 minutes</p>
            </div>
            <div className="bg-slate-800/50 p-4 rounded-lg">
              <p className="text-sm text-slate-400">Device Binding</p>
              <p className="text-lg font-bold text-pink-400">Ed25519 Public Key</p>
            </div>
            <div className="bg-slate-800/50 p-4 rounded-lg">
              <p className="text-sm text-slate-400">Revocation</p>
              <p className="text-lg font-bold text-orange-400">&lt; 1ms</p>
            </div>
          </div>
        </section>

        {/* Fragment Cards */}
        <section id="token-model">
          <h2 className="text-2xl font-bold mb-6">Token Model</h2>
          <div className="grid grid-cols-3 gap-6">
            {[
              {
                num: 1,
                name: 'JWT Token',
                cookie: 'access_token',
                type: 'HS256 signed JWT',
                ttl: '15 minutes',
                verify: 'Stateless (gateway)',
                color: 'from-indigo-500 to-blue-500'
              },
              {
                num: 2,
                name: 'AccessKey',
                cookie: 'access_key',
                type: 'UUIDv7',
                ttl: '15 minutes',
                verify: 'Stateful (Redis)',
                color: 'from-purple-500 to-pink-500'
              },
              {
                num: 3,
                name: 'AccessSecret',
                cookie: 'access_secret',
                type: '48-byte entropy',
                ttl: '15 minutes',
                verify: 'Hash comparison',
                color: 'from-pink-500 to-orange-500'
              }
            ].map(frag => (
              <div key={frag.num} className={`bg-gradient-to-br ${frag.color} p-0.5 rounded-xl`}>
                <div className="bg-slate-900 rounded-xl p-6 h-full">
                  <div className={`w-8 h-8 rounded-lg bg-gradient-to-br ${frag.color} flex items-center justify-center text-white font-bold text-sm mb-4`}>
                    {frag.num}
                  </div>
                  <h3 className="font-bold text-lg mb-4">{frag.name}</h3>
                  <div className="space-y-3 text-sm">
                    <div>
                      <p className="text-slate-400">Cookie</p>
                      <p className="font-mono text-indigo-400">{frag.cookie}</p>
                    </div>
                    <div>
                      <p className="text-slate-400">Type</p>
                      <p className="text-slate-200">{frag.type}</p>
                    </div>
                    <div>
                      <p className="text-slate-400">TTL</p>
                      <p className="text-slate-200">{frag.ttl}</p>
                    </div>
                    <div>
                      <p className="text-slate-400">Verification</p>
                      <p className="text-slate-200">{frag.verify}</p>
                    </div>
                  </div>
                </div>
              </div>
            ))}
          </div>
        </section>

        {/* Key Properties */}
        <section id="lifecycle" className="bg-slate-800/50 border border-slate-700 rounded-xl p-8">
          <h2 className="text-2xl font-bold mb-6">Key Security Properties</h2>
          <div className="grid grid-cols-2 gap-4">
            {[
              { icon: '✅', label: 'Instant Logout', value: '< 1ms' },
              { icon: '🔄', label: 'Token Rotation', value: 'Every refresh' },
              { icon: '⏱️', label: 'Inactivity Timeout', value: '15 minutes' },
              { icon: '🛡️', label: 'Replay Protection', value: 'JTI blacklist' },
              { icon: '📱', label: 'Device Binding', value: 'Ed25519' },
              { icon: '🏗️', label: 'HA Safe', value: '10s grace period' },
            ].map((prop, i) => (
              <div key={i} className="bg-slate-900 p-4 rounded-lg border border-slate-700">
                <p className="text-2xl mb-2">{prop.icon}</p>
                <p className="text-sm text-slate-400">{prop.label}</p>
                <p className="font-bold text-slate-100">{prop.value}</p>
              </div>
            ))}
          </div>
        </section>

        {/* Footer */}
        <footer className="border-t border-slate-800 pt-8 text-center text-slate-400 text-sm">
          <p>Aurora Admin Authentication • Token Model & Lifecycle Specification v2.2</p>
          <p className="mt-2">Updated: 2026-05-29</p>
        </footer>
      </div>
    </main>
  )
}
