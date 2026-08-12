import { useCallback, useEffect, useMemo, useState } from 'react';
import { AlertTriangle, Check, Coins, Pencil, Plus, RefreshCw, Save } from 'lucide-react';
import { toast } from 'sonner';

import {
  billingApi,
  type PricingBracket,
  type PricingSchedule,
  type PricingScheduleDetail,
} from '../../lib/api/billing';
import { useAuthStore } from '../../lib/store/useAuthStore';
import { cn } from '../../lib/utils';

const EMPTY_BRACKET: PricingBracket = {
  range_start_quantity: 0,
  range_end_quantity: null,
  price_numerator_micro_units: 0,
  price_denominator_quantity: 1,
};

function formatDate(value: string): string {
  return new Date(value).toLocaleString();
}

function formatQuantity(value: number | null): string {
  return value === null ? '∞' : value.toLocaleString();
}

export default function PricingSchedulesPage() {
  const canPublish = useAuthStore((state) => state.checkPermission('billing:pricing_schedule', 'publish'));
  const [schedules, setSchedules] = useState<PricingSchedule[]>([]);
  const [selected, setSelected] = useState<PricingScheduleDetail | null>(null);
  const [loading, setLoading] = useState(true);
  const [selectedCode, setSelectedCode] = useState<string | null>(null);
  const [editingMetadata, setEditingMetadata] = useState(false);
  const [displayName, setDisplayName] = useState('');
  const [brackets, setBrackets] = useState<PricingBracket[]>([]);
  const [effectiveFrom, setEffectiveFrom] = useState('');
  const [changeReason, setChangeReason] = useState('');
  const [publishing, setPublishing] = useState(false);

  const loadSchedules = useCallback(async () => {
    setLoading(true);
    try {
      const response = await billingApi.listPricingSchedules();
      setSchedules(response.pricing_schedules);
      if (selectedCode) {
        const detail = await billingApi.getPricingScheduleDetail(selectedCode);
        setSelected(detail);
      }
    } catch (error) {
      toast.error(error instanceof Error ? error.message : 'Unable to load pricing schedules');
    } finally {
      setLoading(false);
    }
  }, [selectedCode]);

  useEffect(() => {
    void loadSchedules();
  }, [loadSchedules]);

  const selectSchedule = async (schedule: PricingSchedule) => {
    setSelectedCode(schedule.code);
    try {
      const detail = await billingApi.getPricingScheduleDetail(schedule.code);
      setSelected(detail);
      setDisplayName(detail.display_name);
      setBrackets(detail.latest_version.brackets.map((bracket) => ({ ...bracket })));
      setEffectiveFrom(new Date(Date.now() + 60_000).toISOString().slice(0, 16));
      setChangeReason('');
      setEditingMetadata(false);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : 'Unable to load pricing schedule');
    }
  };

  const updateBracket = (index: number, field: keyof PricingBracket, value: string) => {
    setBrackets((current) => current.map((bracket, currentIndex) => {
      if (currentIndex !== index) return bracket;
      if (field === 'range_end_quantity' && value.trim() === '') return { ...bracket, [field]: null };
      return { ...bracket, [field]: Number(value) };
    }));
  };

  const publish = async () => {
    if (!selected || !changeReason.trim() || !effectiveFrom || brackets.length === 0) {
      toast.error('Effective time, change reason and at least one bracket are required');
      return;
    }
    setPublishing(true);
    try {
      const next = await billingApi.publishPricingScheduleVersion(selected.code, {
        expected_latest_version: selected.latest_version.version_number,
        effective_from: new Date(effectiveFrom).toISOString(),
        change_reason: changeReason.trim(),
        brackets,
      });
      setSelected({ ...selected, latest_version: next });
      setChangeReason('');
      toast.success(`Version ${next.version_number} published`);
      await loadSchedules();
    } catch (error) {
      toast.error(error instanceof Error ? error.message : 'Pricing version publish failed');
    } finally {
      setPublishing(false);
    }
  };

  const saveMetadata = async () => {
    if (!selected || !displayName.trim()) return;
    try {
      const updated = await billingApi.updatePricingScheduleMetadata(selected.code, {
        metadata_version: selected.metadata_version,
        display_name: displayName.trim(),
      });
      setSelected({ ...selected, display_name: updated.display_name, metadata_version: updated.metadata_version });
      setEditingMetadata(false);
      toast.success('Schedule metadata updated');
      await loadSchedules();
    } catch (error) {
      toast.error(error instanceof Error ? error.message : 'Schedule metadata update failed');
    }
  };

  const unitHint = useMemo(() => {
    if (!selected) return '';
    if (selected.charge_kind_code === 'storage.capacity.gb_hour') return 'GB_HOUR_MICRO';
    return 'BYTE';
  }, [selected]);

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-center justify-between gap-3 border-b border-slate-800 pb-4">
        <div className="flex items-center gap-3">
          <Coins className="h-5 w-5 text-blue-400" />
          <div>
            <h1 className="text-lg font-bold text-slate-100">Pricing schedules</h1>
            <p className="text-xs text-slate-400">Immutable PAYG rate cards. No plans, tiers or monthly entitlement.</p>
          </div>
        </div>
        <button type="button" onClick={() => void loadSchedules()} className="rounded border border-slate-700 p-2 text-slate-400 hover:text-white" aria-label="Refresh pricing schedules">
          <RefreshCw size={15} className={cn(loading && 'animate-spin')} />
        </button>
      </div>

      <div className="grid gap-5 lg:grid-cols-[minmax(0,1fr)_minmax(0,1.4fr)]">
        <section className="overflow-hidden rounded-xl border border-slate-800 bg-slate-900/60">
          <div className="border-b border-slate-800 px-4 py-3 text-xs font-bold uppercase tracking-wide text-slate-400">Controlled catalog</div>
          {loading && schedules.length === 0 ? (
            <div className="flex items-center justify-center gap-2 p-12 text-xs text-slate-500"><RefreshCw size={15} className="animate-spin" /> Loading…</div>
          ) : schedules.length === 0 ? (
            <div className="p-10 text-center text-xs text-slate-500">No pricing schedule is available.</div>
          ) : (
            <div className="divide-y divide-slate-800">
              {schedules.map((schedule) => (
                <button type="button" key={schedule.id} onClick={() => void selectSchedule(schedule)} className={cn('w-full px-4 py-4 text-left transition-colors hover:bg-slate-800/60', selectedCode === schedule.code && 'bg-blue-950/30')}>
                  <div className="flex items-center justify-between gap-3">
                    <span className="font-semibold text-slate-100">{schedule.display_name}</span>
                    <span className={cn('rounded px-2 py-0.5 text-[10px] font-bold', schedule.status === 'ACTIVE' ? 'bg-emerald-950/60 text-emerald-300' : 'bg-slate-800 text-slate-400')}>{schedule.status}</span>
                  </div>
                  <div className="mt-1 font-mono text-[11px] text-blue-300">{schedule.code}</div>
                  <div className="mt-2 flex flex-wrap gap-2 text-[10px] text-slate-400"><span>{schedule.charge_kind_code}</span><span>·</span><span>{schedule.scope_type}</span><span>·</span><span>{schedule.currency}</span></div>
                </button>
              ))}
            </div>
          )}
        </section>

        <section className="rounded-xl border border-slate-800 bg-slate-900/60 p-5">
          {!selected ? (
            <div className="flex h-full min-h-64 items-center justify-center text-xs text-slate-500">Select a schedule to inspect its immutable version.</div>
          ) : (
            <div className="space-y-5">
              <div className="flex flex-wrap items-start justify-between gap-3">
                <div>
                  {editingMetadata ? (
                    <div className="flex gap-2"><input value={displayName} onChange={(event) => setDisplayName(event.target.value)} className="rounded border border-slate-700 bg-slate-950 px-2 py-1 text-sm text-white" /><button type="button" onClick={() => void saveMetadata()} className="rounded bg-blue-600 p-2 text-white"><Save size={14} /></button></div>
                  ) : <h2 className="text-base font-bold text-white">{selected.display_name}</h2>}
                  <p className="mt-1 font-mono text-xs text-blue-300">{selected.code}</p>
                </div>
                {canPublish && !editingMetadata && <button type="button" onClick={() => { setEditingMetadata(true); setDisplayName(selected.display_name); }} className="rounded border border-slate-700 p-2 text-slate-400 hover:text-white" aria-label="Edit schedule name"><Pencil size={14} /></button>}
              </div>

              <div className="grid gap-3 text-xs sm:grid-cols-2"><div><span className="text-slate-500">Charge kind</span><p className="mt-1 font-mono text-slate-200">{selected.charge_kind_code}</p></div><div><span className="text-slate-500">Model / unit</span><p className="mt-1 text-slate-200">{selected.pricing_model} · {unitHint}</p></div><div><span className="text-slate-500">Scope</span><p className="mt-1 text-slate-200">{selected.scope_type}{selected.zone_id ? ` · ${selected.zone_id}` : ''}</p></div><div><span className="text-slate-500">Currency</span><p className="mt-1 text-slate-200">{selected.currency}</p></div></div>

              <div className="rounded-lg border border-slate-800"><div className="flex items-center justify-between border-b border-slate-800 px-3 py-2"><div><span className="text-xs font-bold text-slate-200">Version {selected.latest_version.version_number}</span><span className="ml-2 text-[10px] text-emerald-300">{selected.latest_version.status}</span></div><span className="font-mono text-[10px] text-slate-500">{selected.latest_version.checksum.slice(0, 16)}…</span></div><div className="overflow-x-auto"><table className="w-full text-left text-[11px]"><thead className="text-slate-500"><tr><th className="px-3 py-2">Start ({unitHint})</th><th className="px-3 py-2">End</th><th className="px-3 py-2">Numerator µ</th><th className="px-3 py-2">Denominator</th></tr></thead><tbody className="divide-y divide-slate-800">{selected.latest_version.brackets.map((bracket) => <tr key={bracket.id ?? `${bracket.range_start_quantity}-${bracket.range_end_quantity}`}><td className="px-3 py-2 text-slate-200">{formatQuantity(bracket.range_start_quantity)}</td><td className="px-3 py-2 text-slate-200">{formatQuantity(bracket.range_end_quantity)}</td><td className="px-3 py-2 text-slate-200">{bracket.price_numerator_micro_units.toLocaleString()}</td><td className="px-3 py-2 text-slate-200">{bracket.price_denominator_quantity.toLocaleString()}</td></tr>)}</tbody></table></div><div className="border-t border-slate-800 px-3 py-2 text-[10px] text-slate-500">Effective {formatDate(selected.latest_version.effective_from)}</div></div>

              {canPublish && selected.pricing_model === 'PROGRESSIVE_UNIT' && <div className="space-y-3 rounded-lg border border-blue-900/50 bg-blue-950/10 p-4"><div className="flex items-center justify-between"><div><h3 className="text-xs font-bold text-blue-100">Publish immutable version</h3><p className="mt-1 text-[10px] text-slate-400">Ranges are raw quantities; the API validates contiguous coverage and exact rational pricing.</p></div><Plus size={15} className="text-blue-300" /></div><div className="grid gap-2 sm:grid-cols-2"><label className="text-[10px] text-slate-400">Effective from<input type="datetime-local" value={effectiveFrom} onChange={(event) => setEffectiveFrom(event.target.value)} className="mt-1 w-full rounded border border-slate-700 bg-slate-950 px-2 py-1.5 text-xs text-white" /></label><label className="text-[10px] text-slate-400">Change reason<input value={changeReason} onChange={(event) => setChangeReason(event.target.value)} className="mt-1 w-full rounded border border-slate-700 bg-slate-950 px-2 py-1.5 text-xs text-white" /></label></div><div className="space-y-2">{brackets.map((bracket, index) => <div key={bracket.id ?? index} className="grid gap-2 sm:grid-cols-4"><input type="number" value={bracket.range_start_quantity} onChange={(event) => updateBracket(index, 'range_start_quantity', event.target.value)} className="rounded border border-slate-700 bg-slate-950 px-2 py-1 text-xs text-white" aria-label="Range start" /><input type="number" value={bracket.range_end_quantity ?? ''} onChange={(event) => updateBracket(index, 'range_end_quantity', event.target.value)} placeholder="∞" className="rounded border border-slate-700 bg-slate-950 px-2 py-1 text-xs text-white" aria-label="Range end" /><input type="number" value={bracket.price_numerator_micro_units} onChange={(event) => updateBracket(index, 'price_numerator_micro_units', event.target.value)} className="rounded border border-slate-700 bg-slate-950 px-2 py-1 text-xs text-white" aria-label="Price numerator" /><input type="number" value={bracket.price_denominator_quantity} onChange={(event) => updateBracket(index, 'price_denominator_quantity', event.target.value)} className="rounded border border-slate-700 bg-slate-950 px-2 py-1 text-xs text-white" aria-label="Price denominator" /></div>)}<button type="button" onClick={() => setBrackets((current) => [...current, { ...EMPTY_BRACKET, range_start_quantity: current.at(-1)?.range_end_quantity ?? 0 }])} className="text-[10px] font-semibold text-blue-300 hover:text-blue-200">+ Add bracket</button></div><button type="button" disabled={publishing} onClick={() => void publish()} className="inline-flex items-center gap-2 rounded bg-blue-600 px-3 py-2 text-xs font-bold text-white disabled:opacity-50"><Check size={14} />{publishing ? 'Publishing…' : 'Publish version'}</button></div>}

              {!canPublish && <div className="flex items-start gap-2 rounded-lg border border-amber-900/40 bg-amber-950/20 p-3 text-[11px] text-amber-200"><AlertTriangle size={14} className="mt-0.5 shrink-0" />Read-only catalog. Publishing requires the billing pricing-schedule permission and a session proof.</div>}
            </div>
          )}
        </section>
      </div>
    </div>
  );
}
