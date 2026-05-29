const FRAGMENTS = [
  {
    num: 1,
    name: 'JWT Token',
    cookie: 'access_token',
    gradient: 'from-indigo-500 to-blue-500',
    props: [
      ['Type', 'HS256 signed JWT'],
      ['TTL', '15 minutes'],
      ['Claims', 'sub=admin, access_key, jti, token_use, exp'],
      ['Signed with', 'SecretFamilyAdminAPIKey'],
      ['Verification', 'Stateless (gateway-level)'],
    ],
  },
  {
    num: 2,
    name: 'AccessKey',
    cookie: 'access_key',
    gradient: 'from-purple-500 to-pink-500',
    props: [
      ['Type', 'UUIDv7'],
      ['TTL', '15 minutes'],
      ['Purpose', 'Redis session identifier'],
      ['Binding', 'Must match JWT claim'],
      ['Verification', 'Stateful (Redis lookup)'],
    ],
  },
  {
    num: 3,
    name: 'AccessSecret',
    cookie: 'access_secret',
    gradient: 'from-pink-500 to-orange-500',
    props: [
      ['Type', '48-byte random entropy'],
      ['TTL', '15 minutes'],
      ['Storage', 'SHA256 hash in Redis'],
      ['Binding', 'Must match hash in session'],
      ['Verification', 'Hash comparison (Redis)'],
    ],
  },
]

export default function FragmentCards() {
  return (
    <div className="grid md:grid-cols-3 gap-4 my-4">
      {FRAGMENTS.map((f) => (
        <div
          key={f.num}
          className={`bg-gradient-to-br ${f.gradient} p-[1px] rounded-xl`}
        >
          <div className="bg-white dark:bg-slate-900 rounded-xl p-5 h-full">
            <div
              className={`w-9 h-9 rounded-lg bg-gradient-to-br ${f.gradient} flex items-center justify-center text-white font-bold text-sm mb-3`}
            >
              {f.num}
            </div>
            <h4 className="font-bold text-lg text-slate-900 dark:text-slate-100">{f.name}</h4>
            <p className="font-mono text-xs text-pink-600 dark:text-pink-300 mb-4">{f.cookie}</p>
            <dl className="space-y-2">
              {f.props.map(([k, v]) => (
                <div key={k} className="flex flex-col">
                  <dt className="text-[11px] uppercase tracking-wider text-slate-500">
                    {k}
                  </dt>
                  <dd className="text-sm text-slate-700 dark:text-slate-200">{v}</dd>
                </div>
              ))}
            </dl>
          </div>
        </div>
      ))}
    </div>
  )
}
