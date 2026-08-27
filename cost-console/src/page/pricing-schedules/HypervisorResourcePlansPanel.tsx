import { useCallback, useEffect, useState } from 'react';
import { Check, RefreshCw } from 'lucide-react';
import { toast } from 'sonner';

import { billingApi, type HypervisorResourcePlanAdminItem, type HypervisorResourcePlanHistoryResponse, type HypervisorResourcePlansResponse } from '../../lib/api/billing';
import { cn } from '../../lib/utils';

type Props = { canPublish: boolean };

// Cost owns the resource bundle. Administrative reads include scheduled revisions;
// the customer catalog remains effective-only. Both belong to resource plan.
export function HypervisorResourcePlansPanel({ canPublish }: Props) {
	const [result, setResult] = useState<HypervisorResourcePlansResponse | null>(null);
	const [after, setAfter] = useState('');
	const [loading, setLoading] = useState(true);
	const [publishing, setPublishing] = useState(false);
	const [selected, setSelected] = useState<HypervisorResourcePlanAdminItem | null>(null);
	const [history, setHistory] = useState<HypervisorResourcePlanHistoryResponse | null>(null);
	const [before, setBefore] = useState('');
	const [historyLoading, setHistoryLoading] = useState(false);
	const [editorRevision, setEditorRevision] = useState('');
	const [latestEffectiveFrom, setLatestEffectiveFrom] = useState('');
	const [refresh, setRefresh] = useState(0);
	const [code, setCode] = useState('');
	const [displayName, setDisplayName] = useState('');
	const [description, setDescription] = useState('');
	const [cpuCores, setCPUCores] = useState('1');
	const [memoryMib, setMemoryMib] = useState('2048');
	const [bootDiskGib, setBootDiskGib] = useState('32');
	const [effectiveFrom, setEffectiveFrom] = useState(new Date(Date.now() + 60_000).toISOString().slice(0, 16));
	const [changeReason, setChangeReason] = useState('');

	const load = useCallback(async (signal?: AbortSignal) => {
		setLoading(true);
		try {
			const response = await billingApi.listHypervisorResourcePlans(50, signal, after);
			if (signal?.aborted) return;
			setResult(response);
			setSelected((current) => current ? response.plans.find((plan) => plan.plan_id === current.plan_id) ?? current : null);
		} catch (error) {
			if (!signal?.aborted) toast.error(error instanceof Error ? error.message : 'Unable to load resource plans');
		} finally {
			if (!signal?.aborted) setLoading(false);
		}
	}, [after]);

	useEffect(() => {
		const controller = new AbortController();
		void load(controller.signal);
		return () => controller.abort();
	}, [load, refresh]);

	const planID = selected?.plan_id;
	const latestNumber = selected?.latest_revision_number;
	useEffect(() => {
		setHistory(null);
		if (!planID) { setEditorRevision(''); setHistoryLoading(false); return; }
		const controller = new AbortController();
		setHistoryLoading(true);
		if (!before) setEditorRevision('');
		void billingApi.listHypervisorResourcePlanRevisions(planID, controller.signal, before).then((response) => {
			if (controller.signal.aborted) return;
			setHistory(response);
			if (!before) {
				const latest = response.revisions.find((revision) => revision.is_latest);
				if (latest) {
					setEditorRevision(latest.revision_number);
					setCPUCores(latest.cpu_cores); setMemoryMib(latest.memory_mib); setBootDiskGib(latest.boot_disk_gib);
					setLatestEffectiveFrom(latest.effective_from);
					setEffectiveFrom(new Date(Math.max(Date.now(), Date.parse(latest.effective_from)) + 60_000).toISOString().slice(0, 16));
				}
			}
		}).catch((error: unknown) => {
			if (!controller.signal.aborted) toast.error(error instanceof Error ? error.message : 'Unable to load revisions');
		}).finally(() => {
			if (!controller.signal.aborted) setHistoryLoading(false);
		});
		return () => controller.abort();
	}, [planID, latestNumber, before, refresh]);

	const publish = async () => {
		if (!canPublish || publishing || (selected && (!editorRevision || historyLoading))) return;
		const cpu = cpuCores.trim(), memory = memoryMib.trim(), disk = bootDiskGib.trim();
		if (![cpu, memory, disk].every((value) => /^[1-9][0-9]{0,18}$/.test(value)) ||
			BigInt(cpu) > 1024n || BigInt(memory) > 4_194_304n || BigInt(disk) > 65_536n ||
			!effectiveFrom || !changeReason.trim() || changeReason.trim().length > 2000) {
			toast.error('Use valid capacity limits, a UTC effective time and a bounded change reason.'); return;
		}
		const effective = new Date(`${effectiveFrom}:00.000Z`);
		if (!Number.isFinite(effective.getTime()) || (selected && effective.getTime() <= Date.parse(latestEffectiveFrom))) {
			toast.error('Effective time must be valid UTC and later than the latest revision.'); return;
		}
		if (!selected && (!/^[a-z0-9][a-z0-9._-]{0,127}$/.test(code.trim().toLowerCase()) || !displayName.trim() || displayName.trim().length > 256)) {
			toast.error('Provide a valid plan code and display name.'); return;
		}
		setPublishing(true);
		try {
			const limits = { cpu_cores: cpu, memory_mib: memory, boot_disk_gib: disk, effective_from: effective.toISOString(), change_reason: changeReason.trim() };
			if (selected) {
				const published = await billingApi.publishHypervisorResourcePlanRevision(selected.plan_id, { ...limits, expected_latest_revision: editorRevision });
				setSelected({ ...selected, latest_revision_number: published.revision_number });
				toast.success(`Resource plan revision ${published.revision_number} published`);
			} else {
				const created = await billingApi.createHypervisorResourcePlan({ ...limits, code: code.trim().toLowerCase(), display_name: displayName.trim(), description: description.trim() });
				setSelected({ plan_id: created.plan_id, code: created.code, display_name: created.display_name, description: created.description, state: 'ACTIVE', latest_revision_number: created.revision_number, effective_revision_number: Date.parse(created.effective_from) <= Date.now() ? created.revision_number : '0' });
				toast.success('Resource plan created');
				setCode(''); setDisplayName(''); setDescription('');
			}
			setChangeReason('');
		} catch (error) {
			// Refresh only: a stale OCC token never causes an automatic mutation retry.
			toast.error(`${error instanceof Error ? error.message : 'Publish failed'}. Review the refreshed revision before publishing again.`);
		} finally {
			setBefore(''); setRefresh((value) => value + 1); setPublishing(false);
		}
	};

	const inputClass = 'rounded border border-slate-700 bg-slate-950 px-2 py-1.5 text-xs text-white';
	return (
		<section className="space-y-4 rounded-lg border border-violet-900/50 bg-violet-950/10 p-4">
			<div className="flex justify-between gap-3">
				<div><h3 className="text-xs font-bold text-violet-100">Hypervisor resource plans</h3><p className="mt-1 text-[10px] text-slate-400">Global LIMIT_HOURLY bundles. Scheduled revisions remain visible; Zone prices are separate.</p></div>
				<button type="button" disabled={publishing || loading} onClick={() => { setBefore(''); setRefresh((v) => v + 1); }} aria-label="Refresh resource plans"><RefreshCw size={14} className={cn(loading && 'animate-spin')} /></button>
			</div>
			<div className="grid gap-2 sm:grid-cols-3">
				{result?.plans.map((plan) => <button type="button" key={plan.plan_id} disabled={publishing || loading} onClick={() => { if (selected?.plan_id === plan.plan_id) return; setEditorRevision(''); setBefore(''); setSelected(plan); }} className={cn('rounded border p-3 text-left text-xs', selected?.plan_id === plan.plan_id ? 'border-violet-500 bg-violet-950/40' : 'border-slate-800')}>
					<p className="font-bold text-slate-100">{plan.display_name}</p><p className="font-mono text-violet-300">{plan.code} · latest r{plan.latest_revision_number}</p>
					<p className="mt-2 text-slate-400">{plan.effective_revision_number === '0' ? 'No effective revision yet' : `Effective r${plan.effective_revision_number}`}</p>
				</button>)}
			</div>
			{loading && <p className="text-xs text-slate-400">Loading plans…</p>}
			<div className="flex gap-3 text-xs text-violet-300">
				{after && <button type="button" disabled={loading || publishing} onClick={() => setAfter('')}>First plans</button>}
				{result?.next_cursor && <button type="button" disabled={loading || publishing} onClick={() => setAfter(result.next_cursor)}>Next plans</button>}
			</div>
			{selected && <div className="space-y-2 text-xs text-slate-300">
				<h4 className="font-bold">{selected.display_name} — revisions</h4>
				{historyLoading ? <p>Loading revisions…</p> : history?.revisions.map((revision) => <div key={revision.revision_id} className="rounded border border-slate-800 p-2">
					<p>r{revision.revision_number} {revision.is_latest && '· Latest'} {revision.is_effective && '· Effective'} · {revision.state}</p>
					<p>{revision.cpu_cores} vCPU · {revision.memory_mib} MiB · {revision.boot_disk_gib} GiB</p>
					<p>UTC: {revision.effective_from} → {revision.effective_to ?? 'open'}</p><p>{revision.change_reason}</p>
				</div>)}
				{before && <button type="button" disabled={historyLoading || publishing} onClick={() => setBefore('')}>Latest revisions</button>}
				{history?.next_cursor && <button type="button" disabled={historyLoading || publishing} onClick={() => setBefore(history.next_cursor)}>Older revisions</button>}
			</div>}
			{canPublish ? <fieldset disabled={publishing} className="space-y-3 rounded border border-slate-800 p-3">
				<div className="flex justify-between"><p className="text-xs font-semibold text-slate-200">{selected ? `Publish next revision for ${selected.display_name}` : 'Create resource plan'}</p>
					{selected && <button type="button" className="text-xs text-violet-300" onClick={() => { setSelected(null); setBefore(''); setCPUCores('1'); setMemoryMib('2048'); setBootDiskGib('32'); setEffectiveFrom(new Date(Date.now() + 60_000).toISOString().slice(0, 16)); }}>Create a different plan</button>}
				</div>
				{!selected && <div className="grid gap-2 sm:grid-cols-2">
					<input aria-label="Plan code" value={code} onChange={(e) => setCode(e.target.value)} placeholder="plan code" className={inputClass} />
					<input aria-label="Display name" value={displayName} onChange={(e) => setDisplayName(e.target.value)} placeholder="display name" className={inputClass} />
					<input aria-label="Description" value={description} onChange={(e) => setDescription(e.target.value)} placeholder="description" className={inputClass} />
				</div>}
				<div className="grid gap-2 sm:grid-cols-3">
					<input aria-label="vCPU" value={cpuCores} onChange={(e) => setCPUCores(e.target.value)} inputMode="numeric" className={inputClass} />
					<input aria-label="Memory MiB" value={memoryMib} onChange={(e) => setMemoryMib(e.target.value)} inputMode="numeric" className={inputClass} />
					<input aria-label="Boot disk GiB" value={bootDiskGib} onChange={(e) => setBootDiskGib(e.target.value)} inputMode="numeric" className={inputClass} />
				</div>
				<div className="grid gap-2 sm:grid-cols-2">
					<label className="text-xs text-slate-400">Effective from (UTC+0)<input type="datetime-local" step={60} value={effectiveFrom} onChange={(e) => setEffectiveFrom(e.target.value)} className={inputClass} /></label>
					<label className="text-xs text-slate-400">Change reason<input value={changeReason} onChange={(e) => setChangeReason(e.target.value)} className={inputClass} /></label>
				</div>
				<button type="button" disabled={publishing || !!selected && (!editorRevision || historyLoading || selected.state !== 'ACTIVE')} onClick={() => void publish()} className="inline-flex items-center gap-2 rounded bg-violet-600 px-3 py-2 text-xs font-bold text-white disabled:opacity-50"><Check size={14} />{publishing ? 'Publishing…' : selected ? 'Publish revision' : 'Create resource plan'}</button>
			</fieldset> : <p className="text-xs text-amber-200">Read-only. Publishing requires pricing-schedule publish permission and session proof.</p>}
		</section>
	);
}
