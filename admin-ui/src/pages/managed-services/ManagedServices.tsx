import { useCallback, useEffect, useMemo, useState } from 'react'
import { Boxes, FileCode2, History, Plus, RefreshCcw, Send, ShieldCheck } from 'lucide-react'
import { toast } from 'sonner'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Textarea } from '@/components/ui/textarea'
import { usePageMeta } from '@/lib/page-meta'
import { listAuditEvents, type CatalogAuditEvent } from '@/lib/managed-services/audit'
import { createBlueprint, getBlueprintByVersion, type ServiceBlueprint } from '@/lib/managed-services/blueprint'
import { createCategory, listCategories, type ServiceCategory } from '@/lib/managed-services/category'
import { createDefinition, listDefinitions, type ServiceDefinition } from '@/lib/managed-services/definition'
import {
  createDraft,
  getDraft,
  listRevisions,
  patchDraft,
  publishDraft,
  validateDraft,
  type BlueprintRevision,
  type DraftArtifact,
} from '@/lib/managed-services/revision'
import { createVersion, listVersions, type ServiceVersion } from '@/lib/managed-services/version'

const initialTemplate = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: !aurora/component primary
spec:
  replicas: !aurora/param replicas
`

const initialComponentContract = JSON.stringify([
  { id: 'primary', apply_order: 10, delete_order: 10, readiness: { type: 'deployment_available', deadline_seconds: 600 } },
], null, 2)

export default function ManagedServices() {
  usePageMeta('Managed Services | Aurora Admin', 'Publish immutable managed-service catalog revisions.')
  const [categories, setCategories] = useState<ServiceCategory[]>([])
  const [definitions, setDefinitions] = useState<ServiceDefinition[]>([])
  const [versions, setVersions] = useState<ServiceVersion[]>([])
  const [audit, setAudit] = useState<CatalogAuditEvent[]>([])
  const [selectedCategory, setSelectedCategory] = useState('')
  const [selectedDefinition, setSelectedDefinition] = useState('')
  const [selectedVersion, setSelectedVersion] = useState('')
  const [blueprint, setBlueprint] = useState<ServiceBlueprint | null>(null)
  const [revisions, setRevisions] = useState<BlueprintRevision[]>([])
  const [draft, setDraft] = useState<BlueprintRevision | null>(null)
  const [loading, setLoading] = useState(true)
  const [working, setWorking] = useState(false)

  const [categoryCode, setCategoryCode] = useState('')
  const [categoryName, setCategoryName] = useState('')
  const [definitionCode, setDefinitionCode] = useState('')
  const [definitionName, setDefinitionName] = useState('')
  const [versionCode, setVersionCode] = useState('')
  const [versionName, setVersionName] = useState('')
  const [blueprintCode, setBlueprintCode] = useState('')
  const [blueprintName, setBlueprintName] = useState('')
  const [otpCode, setOtpCode] = useState('')

  const [templateYAML, setTemplateYAML] = useState(initialTemplate)
  const [componentContract, setComponentContract] = useState(initialComponentContract)
  const [inputSchema, setInputSchema] = useState('{\n  "fields": [\n    {\n      "key": "replicas",\n      "value_type": "INT64",\n      "cardinality": "ONE",\n      "required": true,\n      "mutable": true,\n      "min": 1,\n      "max": 100\n    }\n  ]\n}')
  const [uiSchema, setUISchema] = useState('{\n  "groups": [\n    {\n      "key": "capacity",\n      "order": 10,\n      "label_i18n": { "en": "Capacity" }\n    }\n  ],\n  "fields": [\n    {\n      "key": "replicas",\n      "group": "capacity",\n      "order": 10,\n      "widget": "NUMBER",\n      "label_i18n": { "en": "Replicas" }\n    }\n  ]\n}')
  const [outputSchema, setOutputSchema] = useState('{}')
  const [zoneSelector, setZoneSelector] = useState('{\n  "mode": "all"\n}')
  const [capabilityRequirement, setCapabilityRequirement] = useState('{\n  "all_of": ["kubernetes"]\n}')

  const loadCatalog = useCallback(async () => {
    setLoading(true)
    try {
      const [categoryRows, definitionRows, versionRows, auditRows] = await Promise.all([
        listCategories(), listDefinitions(), listVersions(), listAuditEvents(),
      ])
      setCategories(categoryRows)
      setDefinitions(definitionRows)
      setVersions(versionRows)
      setAudit(auditRows)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : 'Cannot load managed-service catalog')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    const timer = window.setTimeout(() => { void loadCatalog() }, 0)
    return () => window.clearTimeout(timer)
  }, [loadCatalog])

  const visibleDefinitions = useMemo(
    () => definitions.filter((item) => !selectedCategory || item.category_id === selectedCategory),
    [definitions, selectedCategory],
  )
  const visibleVersions = useMemo(
    () => versions.filter((item) => !selectedDefinition || item.definition_id === selectedDefinition),
    [versions, selectedDefinition],
  )

  useEffect(() => {
    if (!selectedVersion) return
    let active = true
    void getBlueprintByVersion(selectedVersion).then(async (result) => {
      const revisionRows = result ? await listRevisions(result.id) : []
      if (!active) return
      setBlueprint(result)
      setDraft(null)
      setRevisions(revisionRows)
    }).catch((error) => {
      if (active) toast.error(error instanceof Error ? error.message : 'Cannot load blueprint')
    })
    return () => { active = false }
  }, [selectedVersion])

  const openDraft = async (revision: BlueprintRevision) => {
    if (revision.state !== 'draft') return
    try {
      const result = await getDraft(revision.id)
      setDraft(result)
      setTemplateYAML(result.template_yaml ?? initialTemplate)
      setComponentContract(JSON.stringify(result.component_contract ?? [], null, 2))
      setInputSchema(JSON.stringify(result.input_schema ?? {}, null, 2))
      setUISchema(JSON.stringify(result.ui_schema ?? {}, null, 2))
      setOutputSchema(JSON.stringify(result.safe_observed_output_schema ?? {}, null, 2))
      setZoneSelector(JSON.stringify(result.zone_selector ?? {}, null, 2))
      setCapabilityRequirement(JSON.stringify(result.capability_requirement ?? {}, null, 2))
    } catch (error) {
      toast.error(error instanceof Error ? error.message : 'Cannot load draft')
    }
  }

  const createCategoryAction = async () => {
    setWorking(true)
    try {
      const created = await createCategory({ code: categoryCode, name: { en: categoryName }, description: {}, icon_key: '' })
      setCategories((current) => [created, ...current])
      setCategoryCode('')
      setCategoryName('')
      toast.success('Category created')
    } catch (error) { toast.error(error instanceof Error ? error.message : 'Cannot create category') }
    finally { setWorking(false) }
  }

  const createDefinitionAction = async () => {
    if (!selectedCategory) return
    setWorking(true)
    try {
      const created = await createDefinition({ category_id: selectedCategory, code: definitionCode, name: { en: definitionName }, description: {}, icon_key: '' })
      setDefinitions((current) => [created, ...current])
      setDefinitionCode('')
      setDefinitionName('')
      toast.success('Definition created')
    } catch (error) { toast.error(error instanceof Error ? error.message : 'Cannot create definition') }
    finally { setWorking(false) }
  }

  const createVersionAction = async () => {
    if (!selectedDefinition) return
    setWorking(true)
    try {
      const created = await createVersion({ definition_id: selectedDefinition, code: versionCode, display_version: versionName, name: { en: versionName }, description: {}, icon_key: '' })
      setVersions((current) => [created, ...current])
      setVersionCode('')
      setVersionName('')
      toast.success('Version created')
    } catch (error) { toast.error(error instanceof Error ? error.message : 'Cannot create version') }
    finally { setWorking(false) }
  }

  const createBlueprintAction = async () => {
    if (!selectedVersion || otpCode.length !== 6) return
    setWorking(true)
    try {
      const created = await createBlueprint(selectedVersion, { code: blueprintCode, name: { en: blueprintName }, description: {}, icon_key: '' }, otpCode)
      setBlueprint(created)
      setBlueprintCode('')
      setBlueprintName('')
      setOtpCode('')
      toast.success('Blueprint created through the critical boundary')
    } catch (error) { toast.error(error instanceof Error ? error.message : 'Cannot create blueprint') }
    finally { setWorking(false) }
  }

  const saveDraftAction = async () => {
    if (!blueprint || otpCode.length !== 6) return
    setWorking(true)
    try {
      const artifact: DraftArtifact = {
        template_yaml: templateYAML,
        contract_version: 'platform-form/v1',
        component_contract: JSON.parse(componentContract) as unknown[],
        input_schema: JSON.parse(inputSchema) as Record<string, unknown>,
        ui_schema: JSON.parse(uiSchema) as Record<string, unknown>,
        safe_observed_output_schema: JSON.parse(outputSchema) as Record<string, unknown>,
        zone_selector: JSON.parse(zoneSelector) as Record<string, unknown>,
        capability_requirement: JSON.parse(capabilityRequirement) as Record<string, unknown>,
      }
      const saved = draft
        ? await patchDraft(draft.id, draft.row_version, artifact, otpCode)
        : await createDraft(blueprint.id, artifact, otpCode)
      setDraft({ ...draft, ...saved })
      setRevisions(await listRevisions(blueprint.id))
      setOtpCode('')
      toast.success(draft ? 'Draft updated; prior validation receipt cleared' : 'Draft created')
    } catch (error) { toast.error(error instanceof Error ? error.message : 'Cannot save draft') }
    finally { setWorking(false) }
  }

  const validateDraftAction = async () => {
    if (!draft || otpCode.length !== 6) return
    setWorking(true)
    try {
      const artifact: DraftArtifact = {
        template_yaml: templateYAML,
        contract_version: 'platform-form/v1',
        component_contract: JSON.parse(componentContract) as unknown[],
        input_schema: JSON.parse(inputSchema) as Record<string, unknown>,
        ui_schema: JSON.parse(uiSchema) as Record<string, unknown>,
        safe_observed_output_schema: JSON.parse(outputSchema) as Record<string, unknown>,
        zone_selector: JSON.parse(zoneSelector) as Record<string, unknown>,
        capability_requirement: JSON.parse(capabilityRequirement) as Record<string, unknown>,
      }
      const validated = await validateDraft(draft.id, draft.row_version, artifact, otpCode)
      setDraft({ ...draft, ...validated })
      setRevisions(blueprint ? await listRevisions(blueprint.id) : revisions)
      setOtpCode('')
      toast.success('Current draft bytes validated')
    } catch (error) { toast.error(error instanceof Error ? error.message : 'Cannot validate draft') }
    finally { setWorking(false) }
  }

  const publishDraftAction = async () => {
    if (!draft || otpCode.length !== 6) return
    setWorking(true)
    try {
      const published = await publishDraft(draft.id, draft.row_version, draft.template_bundle_sha256, draft.contract_sha256, otpCode)
      setDraft(null)
      setRevisions(blueprint ? await listRevisions(blueprint.id) : revisions)
      if (selectedVersion) setBlueprint(await getBlueprintByVersion(selectedVersion))
      setOtpCode('')
      toast.success(`Revision ${published.revision} published immutably`)
    } catch (error) { toast.error(error instanceof Error ? error.message : 'Cannot publish draft') }
    finally { setWorking(false) }
  }

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 className="text-3xl font-bold tracking-tight">Managed Services</h1>
          <p className="mt-1 text-muted-foreground">SRE catalog, immutable blueprint revisions and publication audit.</p>
        </div>
        <Button variant="outline" onClick={() => void loadCatalog()} disabled={loading}>
          <RefreshCcw className={loading ? 'animate-spin' : ''} /> Refresh
        </Button>
      </div>

      <Tabs defaultValue="catalog">
        <TabsList>
          <TabsTrigger value="catalog"><Boxes /> Catalog</TabsTrigger>
          <TabsTrigger value="revision"><FileCode2 /> Blueprint revision</TabsTrigger>
          <TabsTrigger value="audit"><History /> Audit</TabsTrigger>
        </TabsList>

        <TabsContent value="catalog" className="space-y-6">
          <div className="grid gap-4 xl:grid-cols-3">
            <Card>
              <CardHeader><CardTitle>Category</CardTitle><CardDescription>Stable top-level resource family.</CardDescription></CardHeader>
              <CardContent className="space-y-3">
                <select className="h-9 w-full rounded-md border bg-background px-3 text-sm" value={selectedCategory} onChange={(event) => { setSelectedCategory(event.target.value); setSelectedDefinition(''); setSelectedVersion('') }}>
                  <option value="">Select category</option>
                  {categories.map((item) => <option key={item.id} value={item.id}>{item.name.en ?? item.code} · {item.state}</option>)}
                </select>
                <Input placeholder="category-code" value={categoryCode} onChange={(event) => setCategoryCode(event.target.value)} />
                <Input placeholder="English display name" value={categoryName} onChange={(event) => setCategoryName(event.target.value)} />
                <Button onClick={() => void createCategoryAction()} disabled={working || !categoryCode || !categoryName}><Plus /> Create category</Button>
              </CardContent>
            </Card>

            <Card>
              <CardHeader><CardTitle>Definition</CardTitle><CardDescription>Application offered inside a category.</CardDescription></CardHeader>
              <CardContent className="space-y-3">
                <select className="h-9 w-full rounded-md border bg-background px-3 text-sm" value={selectedDefinition} onChange={(event) => { setSelectedDefinition(event.target.value); setSelectedVersion('') }} disabled={!selectedCategory}>
                  <option value="">Select definition</option>
                  {visibleDefinitions.map((item) => <option key={item.id} value={item.id}>{item.name.en ?? item.code} · {item.state}</option>)}
                </select>
                <Input placeholder="definition-code" value={definitionCode} onChange={(event) => setDefinitionCode(event.target.value)} />
                <Input placeholder="English display name" value={definitionName} onChange={(event) => setDefinitionName(event.target.value)} />
                <Button onClick={() => void createDefinitionAction()} disabled={working || !selectedCategory || !definitionCode || !definitionName}><Plus /> Create definition</Button>
              </CardContent>
            </Card>

            <Card>
              <CardHeader><CardTitle>Version</CardTitle><CardDescription>Immutable runtime line starts below this object.</CardDescription></CardHeader>
              <CardContent className="space-y-3">
                <select className="h-9 w-full rounded-md border bg-background px-3 text-sm" value={selectedVersion} onChange={(event) => { setBlueprint(null); setRevisions([]); setDraft(null); setSelectedVersion(event.target.value) }} disabled={!selectedDefinition}>
                  <option value="">Select version</option>
                  {visibleVersions.map((item) => <option key={item.id} value={item.id}>{item.display_version} · {item.state}</option>)}
                </select>
                <Input placeholder="version-code (for example 16)" value={versionCode} onChange={(event) => setVersionCode(event.target.value)} />
                <Input placeholder="Display version" value={versionName} onChange={(event) => setVersionName(event.target.value)} />
                <Button onClick={() => void createVersionAction()} disabled={working || !selectedDefinition || !versionCode || !versionName}><Plus /> Create version</Button>
              </CardContent>
            </Card>
          </div>

          {selectedVersion && !blueprint && (
            <Card>
              <CardHeader><CardTitle>Create runtime blueprint</CardTitle><CardDescription>This operation enters the ACR critical path.</CardDescription></CardHeader>
              <CardContent className="grid gap-3 md:grid-cols-3">
                <Input placeholder="blueprint-code" value={blueprintCode} onChange={(event) => setBlueprintCode(event.target.value)} />
                <Input placeholder="Blueprint name" value={blueprintName} onChange={(event) => setBlueprintName(event.target.value)} />
                <Input inputMode="numeric" maxLength={6} placeholder="6-digit SRE TOTP" value={otpCode} onChange={(event) => setOtpCode(event.target.value.replace(/\D/g, '').slice(0, 6))} />
                <Button className="md:col-span-3 md:w-fit" onClick={() => void createBlueprintAction()} disabled={working || !blueprintCode || !blueprintName || otpCode.length !== 6}><ShieldCheck /> Create critical blueprint</Button>
              </CardContent>
            </Card>
          )}

          {blueprint && (
            <Card>
              <CardHeader><CardTitle>{blueprint.name.en ?? blueprint.code}</CardTitle><CardDescription>Blueprint {blueprint.id}</CardDescription></CardHeader>
              <CardContent className="flex flex-wrap gap-2">
                <Badge variant="outline">{blueprint.state}</Badge>
                <Badge variant="outline">row {blueprint.row_version}</Badge>
                <Badge variant="outline">default {blueprint.published_revision_id ?? 'not published'}</Badge>
              </CardContent>
            </Card>
          )}
        </TabsContent>

        <TabsContent value="revision" className="space-y-6">
          {!blueprint ? <Card><CardContent className="py-12 text-center text-muted-foreground">Select or create a blueprint in Catalog first.</CardContent></Card> : (
            <>
              <Card>
                <CardHeader><CardTitle>Revision history</CardTitle><CardDescription>Published rows are immutable; only draft rows open in the editor.</CardDescription></CardHeader>
                <CardContent className="space-y-2">
                  {revisions.length === 0 && <p className="text-muted-foreground">No revision yet.</p>}
                  {revisions.map((item) => (
                    <button key={item.id} type="button" className="flex w-full items-center justify-between rounded-lg border p-3 text-left hover:bg-muted/40" onClick={() => void openDraft(item)}>
                      <span>Revision {item.revision} · row {item.row_version}</span>
                      <span className="flex items-center gap-2"><Badge variant="outline">{item.state}</Badge>{item.validated_row_version === item.row_version && <Badge>validated</Badge>}</span>
                    </button>
                  ))}
                </CardContent>
              </Card>

              <Card>
                <CardHeader><CardTitle>{draft ? `Edit draft revision ${draft.revision}` : 'New draft revision'}</CardTitle><CardDescription>YAML remains exact bytes. Validation stores a receipt bound to the current row and hashes.</CardDescription></CardHeader>
                <CardContent className="space-y-4">
                  <div><Label>Template YAML</Label><Textarea className="mt-2 min-h-72 font-mono" value={templateYAML} onChange={(event) => setTemplateYAML(event.target.value)} /></div>
                  <div className="grid gap-4 xl:grid-cols-2">
                    <div><Label>Component contract</Label><Textarea className="mt-2 min-h-48 font-mono" value={componentContract} onChange={(event) => setComponentContract(event.target.value)} /></div>
                    <div><Label>Input schema</Label><Textarea className="mt-2 min-h-48 font-mono" value={inputSchema} onChange={(event) => setInputSchema(event.target.value)} /></div>
                    <div><Label>UI schema</Label><Textarea className="mt-2 min-h-36 font-mono" value={uiSchema} onChange={(event) => setUISchema(event.target.value)} /></div>
                    <div><Label>Safe output schema</Label><Textarea className="mt-2 min-h-36 font-mono" value={outputSchema} onChange={(event) => setOutputSchema(event.target.value)} /></div>
                    <div><Label>Zone selector</Label><Textarea className="mt-2 min-h-28 font-mono" value={zoneSelector} onChange={(event) => setZoneSelector(event.target.value)} /></div>
                    <div><Label>Capability requirement</Label><Textarea className="mt-2 min-h-28 font-mono" value={capabilityRequirement} onChange={(event) => setCapabilityRequirement(event.target.value)} /></div>
                  </div>
                  <div className="flex flex-wrap items-end gap-3 rounded-lg border p-4">
                    <div><Label>SRE TOTP for the next critical action</Label><Input className="mt-2 w-56" inputMode="numeric" maxLength={6} value={otpCode} onChange={(event) => setOtpCode(event.target.value.replace(/\D/g, '').slice(0, 6))} /></div>
                    <Button variant="outline" onClick={() => void saveDraftAction()} disabled={working || otpCode.length !== 6}><FileCode2 /> {draft ? 'Save draft' : 'Create draft'}</Button>
                    <Button variant="outline" onClick={() => void validateDraftAction()} disabled={working || !draft || otpCode.length !== 6}><ShieldCheck /> Validate exact bytes</Button>
                    <Button onClick={() => void publishDraftAction()} disabled={working || !draft || draft.validated_row_version !== draft.row_version || otpCode.length !== 6}><Send /> Publish immutable revision</Button>
                  </div>
                </CardContent>
              </Card>
            </>
          )}
        </TabsContent>

        <TabsContent value="audit">
          <Card>
            <CardHeader><CardTitle>Catalog audit</CardTitle><CardDescription>Actor, critical challenge ID and durable record version; no proof, nonce or template bytes.</CardDescription></CardHeader>
            <CardContent className="space-y-2">
              {audit.map((event) => (
                <div key={event.id} className="grid gap-2 rounded-lg border p-3 md:grid-cols-[180px_1fr_1fr_auto]">
                  <span className="font-medium">{event.action}</span>
                  <span className="font-mono text-xs text-muted-foreground">{event.record_kind}:{event.record_id}</span>
                  <span className="text-xs text-muted-foreground">{event.actor} · proof {event.critical_proof_id ?? 'none'}</span>
                  <Badge variant="outline">{event.outcome}</Badge>
                </div>
              ))}
              {!loading && audit.length === 0 && <p className="py-8 text-center text-muted-foreground">No audit event.</p>}
            </CardContent>
          </Card>
        </TabsContent>
      </Tabs>
    </div>
  )
}
