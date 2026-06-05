import { Server, ShieldCheck } from 'lucide-react'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Textarea } from '@/components/ui/textarea'

export interface EndpointForm {
  name: string
  host: string
  port: number
  username: string
  priority: number
  weight: number
  warmup_state: string
  status: string
  tls_mode: string
  password: string
  ca_cert_pem: string
  client_cert_pem: string
  client_key_pem: string
  max_connections: number
}

interface EndpointFormFieldsProps {
  form: EndpointForm
  update: (key: keyof EndpointForm, value: string) => void
}

const tlsModeOptions = [
  { value: 'none', label: 'None' },
  { value: 'starttls', label: 'STARTTLS' },
  { value: 'tls', label: 'TLS' },
  { value: 'mtls', label: 'mTLS' },
]

const warmupOptions = [
  { value: 'stable', label: 'Stable' },
  { value: 'warming', label: 'Warming' },
  { value: 'paused', label: 'Paused' },
]

function FieldHint({ children }: { children: React.ReactNode }) {
  return <p className="mt-2 text-sm text-muted-foreground">{children}</p>
}

function Required() {
  return <span className="text-destructive">*</span>
}

export function EndpointFormFields({ form, update }: EndpointFormFieldsProps) {
  return (
    <>
      {/* Card 1: Basic Configuration */}
      <div className="rounded-xl border border-border bg-card p-6 shadow-xs md:p-7 space-y-6">
        <div className="flex items-center gap-3 border-b border-border/60 pb-4">
          <Server className="size-5 text-primary" />
          <h2 className="text-xl font-semibold tracking-[-0.02em] text-foreground">Basic Configuration</h2>
        </div>

        <div className="grid gap-6 md:grid-cols-2">
          <div>
            <Label className="text-sm font-semibold text-foreground">Endpoint Name <Required /></Label>
            <Input
              value={form.name}
              onChange={(e) => update('name', e.target.value)}
              placeholder="e.g., SendGrid Primary"
              className="mt-3 h-12 rounded-lg border-border bg-background px-4 shadow-none font-medium"
              required
            />
            <FieldHint>A friendly name to identify this SMTP endpoint.</FieldHint>
          </div>

          <div>
            <Label className="text-sm font-semibold text-foreground">Host Address <Required /></Label>
            <Input
              value={form.host}
              onChange={(e) => update('host', e.target.value)}
              placeholder="e.g., smtp.sendgrid.net"
              className="mt-3 h-12 rounded-lg border-border bg-background px-4 shadow-none font-medium"
              required
            />
            <FieldHint>The hostname or IP address of the SMTP server.</FieldHint>
          </div>

          <div>
            <Label className="text-sm font-semibold text-foreground">Port <Required /></Label>
            <Input
              type="number"
              value={form.port}
              onChange={(e) => update('port', e.target.value)}
              placeholder="587"
              className="mt-3 h-12 rounded-lg border-border bg-background px-4 shadow-none font-medium"
              required
            />
            <FieldHint>Usually 587 for STARTTLS, 465 for TLS, or 25.</FieldHint>
          </div>

          <div>
            <Label className="text-sm font-semibold text-foreground">Username</Label>
            <Input
              value={form.username}
              onChange={(e) => update('username', e.target.value)}
              placeholder="apikey or smtp_user"
              className="mt-3 h-12 rounded-lg border-border bg-background px-4 shadow-none font-medium"
            />
            <FieldHint>Username for SMTP authentication.</FieldHint>
          </div>

          <div>
            <Label className="text-sm font-semibold text-foreground">Password</Label>
            <Input
              type="password"
              value={form.password}
              onChange={(e) => update('password', e.target.value)}
              placeholder="••••••••••••"
              className="mt-3 h-12 rounded-lg border-border bg-background px-4 shadow-none font-medium"
            />
            <FieldHint>Password or API key for SMTP authentication.</FieldHint>
          </div>

          <div>
            <Label className="text-sm font-semibold text-foreground">TLS Mode <Required /></Label>
            <Select value={form.tls_mode} onValueChange={(value) => update('tls_mode', value)}>
              <SelectTrigger className="mt-3 h-12 w-full rounded-lg border-border bg-background px-4 shadow-none font-medium">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {tlsModeOptions.map((opt) => (
                  <SelectItem key={opt.value} value={opt.value}>{opt.label}</SelectItem>
                ))}
              </SelectContent>
            </Select>
            <FieldHint>Secure connection protocols to be used.</FieldHint>
          </div>

          <div>
            <Label className="text-sm font-semibold text-foreground">Warmup State <Required /></Label>
            <Select value={form.warmup_state} onValueChange={(value) => update('warmup_state', value)}>
              <SelectTrigger className="mt-3 h-12 w-full rounded-lg border-border bg-background px-4 shadow-none font-medium">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {warmupOptions.map((opt) => (
                  <SelectItem key={opt.value} value={opt.value}>{opt.label}</SelectItem>
                ))}
              </SelectContent>
            </Select>
            <FieldHint>Control traffic ramping speed for new IPs.</FieldHint>
          </div>

          <div>
            <Label className="text-sm font-semibold text-foreground">Max Connections</Label>
            <Input
              type="number"
              min={0}
              value={form.max_connections}
              onChange={(e) => update('max_connections', e.target.value)}
              className="mt-3 h-12 rounded-lg border-border bg-background px-4 shadow-none font-medium"
            />
            <FieldHint>Concurrency pool limits to prevent SMTP blocks.</FieldHint>
          </div>

          <div>
            <Label className="text-sm font-semibold text-foreground">Priority</Label>
            <Input
              type="number"
              value={form.priority}
              onChange={(e) => update('priority', e.target.value)}
              className="mt-3 h-12 rounded-lg border-border bg-background px-4 shadow-none font-medium"
            />
            <FieldHint>Lower priority value takes precedence first.</FieldHint>
          </div>

          <div>
            <Label className="text-sm font-semibold text-foreground">Weight</Label>
            <Input
              type="number"
              value={form.weight}
              onChange={(e) => update('weight', e.target.value)}
              className="mt-3 h-12 rounded-lg border-border bg-background px-4 shadow-none font-medium"
            />
            <FieldHint>Traffic balance weight when priorities match.</FieldHint>
          </div>
        </div>
      </div>

      {/* Card 2: Security & Certificates (chỉ hiện khi không phải None TLS) */}
      {form.tls_mode !== 'none' && (
        <div className="rounded-xl border border-border bg-card p-6 shadow-xs md:p-7 space-y-6">
          <div className="flex items-center gap-3 border-b border-border/60 pb-4">
            <ShieldCheck className="size-5 text-primary" />
            <h2 className="text-xl font-semibold tracking-[-0.02em] text-foreground">Security & Certificates</h2>
          </div>

          <div className="grid gap-6 md:grid-cols-2">
            <div className="md:col-span-2">
              <Label className="text-sm font-semibold text-foreground">CA Cert PEM</Label>
              <Textarea
                value={form.ca_cert_pem}
                onChange={(e) => update('ca_cert_pem', e.target.value)}
                placeholder="-----BEGIN CERTIFICATE-----&#10;...&#10;-----END CERTIFICATE-----"
                className="mt-3 min-h-24 rounded-lg border-border bg-background px-4 py-3 font-mono text-sm shadow-none"
              />
              <FieldHint>Trusted root CA to verify the SMTP server certificate.</FieldHint>
            </div>

            {form.tls_mode === 'mtls' && (
              <>
                <div>
                  <Label className="text-sm font-semibold text-foreground">Client Cert PEM</Label>
                  <Textarea
                    value={form.client_cert_pem}
                    onChange={(e) => update('client_cert_pem', e.target.value)}
                    placeholder="-----BEGIN CERTIFICATE-----&#10;..."
                    className="mt-3 min-h-24 rounded-lg border-border bg-background px-4 py-3 font-mono text-sm shadow-none"
                  />
                  <FieldHint>Public client certificate for mutual TLS.</FieldHint>
                </div>

                <div>
                  <Label className="text-sm font-semibold text-foreground">Client Private Key PEM</Label>
                  <Textarea
                    value={form.client_key_pem}
                    onChange={(e) => update('client_key_pem', e.target.value)}
                    placeholder="-----BEGIN PRIVATE KEY-----&#10;..."
                    className="mt-3 min-h-24 rounded-lg border-border bg-background px-4 py-3 font-mono text-sm shadow-none"
                  />
                  <FieldHint>Private client key for mutual TLS authentication.</FieldHint>
                </div>
              </>
            )}
          </div>
        </div>
      )}
    </>
  )
}
