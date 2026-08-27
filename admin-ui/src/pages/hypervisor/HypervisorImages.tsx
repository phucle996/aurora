import { useCallback, useEffect, useMemo, useState } from 'react'
import type { FormEvent } from 'react'
import { Database, Loader2, RefreshCw, Trash2, UploadCloud } from 'lucide-react'
import { toast } from 'sonner'

import { PageContent } from '@/components/layout/layout'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { useZoneStore } from '@/hooks/useZoneStore'
import {
  deleteHypervisorImage,
  importHypervisorImage,
  listHypervisorImages,
  registerHypervisorImage,
  type HypervisorImage,
  type RegisterHypervisorImageInput,
} from '@/lib/hypervisor-images'

const emptyForm: RegisterHypervisorImageInput = {
  name: '',
  code: '',
  distribution: 'ubuntu',
  release: '24.04',
  revision: 1,
  architecture: 'x86_64',
  format: 'qcow2',
  size_bytes: 1,
  sha256: '',
}

function formatBytes(value: number) {
  if (value < 1024) return `${value} B`
  const units = ['KiB', 'MiB', 'GiB', 'TiB']
  let amount = value
  let index = -1
  while (amount >= 1024 && index < units.length - 1) {
    amount /= 1024
    index += 1
  }
  return `${amount.toFixed(amount >= 10 ? 0 : 1)} ${units[index]}`
}

export default function HypervisorImagesPage() {
  const { zones, activeZone } = useZoneStore()
  const selectedZone = useMemo(
    () => zones.find((zone) => zone.code === activeZone) ?? null,
    [activeZone, zones],
  )
  const [images, setImages] = useState<HypervisorImage[]>([])
  const [form, setForm] = useState<RegisterHypervisorImageInput>(emptyForm)
  const [loading, setLoading] = useState(false)
  const [submitting, setSubmitting] = useState(false)

  const loadImages = useCallback(async () => {
    if (!selectedZone) {
      setImages([])
      return
    }
    setLoading(true)
    try {
      setImages(await listHypervisorImages())
    } catch (error) {
      toast.error(error instanceof Error ? error.message : 'Cannot load image registry')
    } finally {
      setLoading(false)
    }
  }, [selectedZone])

  useEffect(() => {
    void loadImages()
  }, [loadImages])

  async function onRegister(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!selectedZone) {
      toast.error('Select a concrete Zone first.')
      return
    }
    setSubmitting(true)
    try {
      await registerHypervisorImage(form)
      toast.success('Image metadata registered.')
      setForm(emptyForm)
      await loadImages()
    } catch (error) {
      toast.error(error instanceof Error ? error.message : 'Cannot register image')
    } finally {
      setSubmitting(false)
    }
  }

  async function onImport(image: HypervisorImage) {
    if (!selectedZone) return
    try {
      await importHypervisorImage(image.id)
      toast.success(`Import queued for ${image.code} revision ${image.revision}.`)
      await loadImages()
    } catch (error) {
      toast.error(error instanceof Error ? error.message : 'Cannot start image import')
    }
  }

  async function onDelete(image: HypervisorImage) {
    if (!selectedZone || !window.confirm(`Delete ${image.code} revision ${image.revision}?`)) return
    try {
      await deleteHypervisorImage(image.id)
      toast.success('Image deletion queued.')
      await loadImages()
    } catch (error) {
      toast.error(error instanceof Error ? error.message : 'Cannot delete image')
    }
  }

  if (!selectedZone) {
    return (
      <PageContent>
        <section className="rounded-3xl border border-border/60 bg-card p-8 text-center shadow-sm">
          <Database className="mx-auto size-10 text-primary" />
          <h1 className="mt-4 text-2xl font-semibold">Hypervisor image registry</h1>
          <p className="mt-2 text-sm text-muted-foreground">Select a concrete Zone from the header.</p>
        </section>
      </PageContent>
    )
  }

  return (
    <PageContent>
      <header className="flex flex-col gap-4 border-b border-border pb-5 sm:flex-row sm:items-start sm:justify-between">
        <div>
          <p className="text-xs font-semibold uppercase tracking-[0.2em] text-primary">Hypervisor / Images</p>
          <h1 className="mt-2 text-3xl font-bold tracking-tight">Zone image registry</h1>
          <p className="mt-1 text-sm text-muted-foreground">
            {selectedZone.name} · Admin metadata and import lifecycle. Node telemetry belongs in Grafana.
          </p>
        </div>
        <Button variant="outline" onClick={() => void loadImages()} disabled={loading}>
          <RefreshCw className="mr-2 size-4" /> Refresh
        </Button>
      </header>

      <div className="grid gap-6 xl:grid-cols-[minmax(0,0.8fr)_minmax(0,1.2fr)]">
        <form onSubmit={onRegister} className="rounded-2xl border border-border bg-card p-5 shadow-sm">
          <div className="flex items-center gap-2 border-b border-border pb-4">
            <UploadCloud className="size-5 text-primary" />
            <div>
              <h2 className="font-semibold">Register image revision</h2>
              <p className="text-xs text-muted-foreground">Bytes stay in this Zone; this form stores the immutable contract.</p>
            </div>
          </div>
          <div className="mt-5 grid gap-4 sm:grid-cols-2">
            <div className="space-y-2 sm:col-span-2">
              <Label htmlFor="image-file">Auto-fill from local image file (Optional)</Label>
              <Input
                id="image-file"
                type="file"
                accept=".qcow2,.raw,.iso,.img"
                onChange={async (event) => {
                  const file = event.target.files?.[0]
                  if (!file) return
                  const size = file.size
                  const format = file.name.endsWith('.raw') ? 'raw' : 'qcow2'
                  const rawName = file.name.replace(/\.[^/.]+$/, '')
                  setForm((prev) => ({
                    ...prev,
                    name: prev.name || rawName,
                    code: prev.code || rawName.toLowerCase().replace(/[^a-z0-9._-]/g, '-'),
                    size_bytes: size,
                    format,
                  }))
                  if (size <= 500 * 1024 * 1024) {
                    try {
                      toast.info('Computing SHA-256 checksum...')
                      const buffer = await file.arrayBuffer()
                      const hashBuffer = await crypto.subtle.digest('SHA-256', buffer)
                      const hashArray = Array.from(new Uint8Array(hashBuffer))
                      const hashHex = hashArray.map((b) => b.toString(16).padStart(2, '0')).join('')
                      setForm((prev) => ({ ...prev, sha256: hashHex }))
                      toast.success('SHA-256 checksum computed.')
                    } catch {
                      toast.error('Could not compute SHA-256 in browser.')
                    }
                  }
                }}
              />
              <p className="text-xs text-muted-foreground">Selecting a file will auto-fill size, format, code and calculate SHA-256 for files under 500MB.</p>
            </div>
            <div className="space-y-2 sm:col-span-2">
              <Label htmlFor="image-name">Display name</Label>
              <Input id="image-name" value={form.name} onChange={(event) => setForm({ ...form, name: event.target.value })} required />
            </div>
            <div className="space-y-2">
              <Label htmlFor="image-code">Stable code</Label>
              <Input id="image-code" value={form.code} onChange={(event) => setForm({ ...form, code: event.target.value })} placeholder="ubuntu-24.04" required />
            </div>
            <div className="space-y-2">
              <Label htmlFor="image-revision">Revision</Label>
              <Input id="image-revision" type="number" min={1} value={form.revision} onChange={(event) => setForm({ ...form, revision: Number(event.target.value) })} required />
            </div>
            <div className="space-y-2">
              <Label htmlFor="image-distribution">Distribution</Label>
              <Input id="image-distribution" value={form.distribution} onChange={(event) => setForm({ ...form, distribution: event.target.value })} required />
            </div>
            <div className="space-y-2">
              <Label htmlFor="image-release">Release label</Label>
              <Input id="image-release" value={form.release} onChange={(event) => setForm({ ...form, release: event.target.value })} required />
            </div>
            <div className="space-y-2">
              <Label htmlFor="image-format">Format</Label>
              <Input id="image-format" value={form.format} onChange={(event) => setForm({ ...form, format: event.target.value as 'qcow2' | 'raw' })} placeholder="qcow2 or raw" required />
            </div>
            <div className="space-y-2">
              <Label htmlFor="image-size">Size (bytes)</Label>
              <Input id="image-size" type="number" min={1} value={form.size_bytes} onChange={(event) => setForm({ ...form, size_bytes: Number(event.target.value) })} required />
            </div>
            <div className="space-y-2 sm:col-span-2">
              <Label htmlFor="image-sha">SHA-256</Label>
              <Input id="image-sha" value={form.sha256} onChange={(event) => setForm({ ...form, sha256: event.target.value })} placeholder="64 hexadecimal characters" required />
            </div>
          </div>
          <Button className="mt-5 w-full" type="submit" disabled={submitting}>
            {submitting ? <Loader2 className="mr-2 size-4 animate-spin" /> : <UploadCloud className="mr-2 size-4" />}
            Register revision
          </Button>
        </form>

        <section className="rounded-2xl border border-border bg-card p-5 shadow-sm">
          <div className="flex items-center justify-between border-b border-border pb-4">
            <div>
              <h2 className="font-semibold">Registered revisions</h2>
              <p className="text-xs text-muted-foreground">{images.length} records in this Zone</p>
            </div>
          </div>
          <div className="mt-4 space-y-3">
            {loading && <p className="text-sm text-muted-foreground">Loading…</p>}
            {!loading && images.length === 0 && <p className="text-sm text-muted-foreground">No image revisions registered.</p>}
            {images.map((image) => (
              <article key={image.id} className="rounded-xl border border-border/70 p-4">
                <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
                  <div className="min-w-0">
                    <p className="truncate font-semibold">{image.name}</p>
                    <p className="mt-1 text-xs text-muted-foreground">
                      {image.code} · revision {image.revision} · {image.distribution} {image.release}
                    </p>
                    <p className="mt-2 text-xs text-muted-foreground">{formatBytes(image.size_bytes)} · {image.state}</p>
                  </div>
                  <div className="flex shrink-0 gap-2">
                    {['UPLOADING', 'FAILED', 'QUARANTINED'].includes(image.state) && (
                      <Button size="sm" onClick={() => void onImport(image)}>Import</Button>
                    )}
                    {['AVAILABLE', 'FAILED', 'QUARANTINED'].includes(image.state) && (
                      <Button size="icon" variant="outline" onClick={() => void onDelete(image)} aria-label="Delete image">
                        <Trash2 className="size-4" />
                      </Button>
                    )}
                  </div>
                </div>
              </article>
            ))}
          </div>
        </section>
      </div>
    </PageContent>
  )
}
