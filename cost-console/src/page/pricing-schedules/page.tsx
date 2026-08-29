import { useCallback, useEffect, useMemo, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
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
import { HypervisorResourcePlansPanel } from './HypervisorResourcePlansPanel';
import { ZonePriceAdjustmentsTab } from './ZonePriceAdjustmentsTab';

const EMPTY_BRACKET: PricingBracket = {
  range_start_quantity: '0',
  range_end_quantity: null,
  price_numerator_micro_units: '0',
  price_denominator_quantity: '1',
};

const STORAGE_BASE_CHARGE_KINDS = new Set([
  'storage.capacity.gb_hour',
  'storage.network_in.byte',
  'storage.network_out.byte',
]);

const HYPERVISOR_BASE_CHARGE_KINDS = new Set([
  'hypervisor.vcpu.allocated_second',
  'hypervisor.memory_mib.allocated_second',
  'hypervisor.disk_gib.allocated_second',
  'hypervisor.network_in.byte',
  'hypervisor.network_out.byte',
]);

const MAIL_BASE_CHARGE_KINDS = new Set(['mail.delivery.accepted_recipient']);

type PricingTab = 'base-pricing' | 'resource-plans' | 'zone-adjustments';

function formatDate(value: string): string {
  return new Date(value).toISOString().replace('T', ' ').replace('Z', ' UTC');
}

function formatQuantity(value: string | null): string {
  if (value === null) return '∞';
  const match = /^(-?)(\d+)$/.exec(value);
  if (!match) return value;
  return `${match[1]}${match[2].replace(/\B(?=(\d{3})+(?!\d))/g, ',')}`;
}

export default function PricingSchedulesPage() {
  const [searchParams, setSearchParams] = useSearchParams();
  const canPublish = useAuthStore((state) => state.checkPermission('billing:pricing_schedule', 'publish'));
  const activeTab: PricingTab = searchParams.get('tab') === 'resource-plans' ? 'resource-plans' : searchParams.get('tab') === 'zone-adjustments' ? 'zone-adjustments' : 'base-pricing';
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
    if (activeTab !== 'base-pricing') return;
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
  }, [activeTab, selectedCode]);

  useEffect(() => {
    void loadSchedules();
  }, [loadSchedules]);

  const selectSchedule = async (schedule: PricingSchedule) => {
    setSelectedCode(schedule.code);
    try {
      const detail = await billingApi.getPricingScheduleDetail(schedule.code);
      setSelected(detail);
      setDisplayName(detail.display_name);
      setBrackets(detail.latest_version ? detail.latest_version.brackets.map((bracket) => ({ ...bracket })) : [{ ...EMPTY_BRACKET }]);
      setEffectiveFrom(new Date(Date.now() + 60_000).toISOString().slice(0, 16));
      setChangeReason('');
      setEditingMetadata(false);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : 'Unable to load pricing schedule');
    }
  };

  const updateBracketEnd = (index: number, value: string) => {
    setBrackets((current) => current.map((bracket, currentIndex) => {
      if (currentIndex === index) return { ...bracket, range_end_quantity: value.trim() === '' ? null : value };
      if (currentIndex === index + 1 && value.trim() !== '') return { ...bracket, range_start_quantity: value };
      return bracket;
    }));
  };

  const updateBracketRate = (index: number, field: 'price_numerator_micro_units' | 'price_denominator_quantity', value: string) => {
    setBrackets((current) => current.map((bracket, currentIndex) => currentIndex === index ? { ...bracket, [field]: value } : bracket));
  };

  const appendBracket = () => {
    setBrackets((current) => {
      const last = current.at(-1);
      if (!last?.range_end_quantity || !/^\d+$/.test(last.range_end_quantity)) return current;
      return [...current, { ...EMPTY_BRACKET, range_start_quantity: last.range_end_quantity }];
    });
  };

  const removeLastBracket = () => {
    setBrackets((current) => {
      if (current.length <= 1) return current;
      const remaining = current.slice(0, -1);
      return remaining.map((bracket, index) => index === remaining.length - 1 ? { ...bracket, range_end_quantity: null } : bracket);
    });
  };

  const publish = async () => {
    if (!selected || !changeReason.trim() || !effectiveFrom || brackets.length === 0) {
      toast.error('Effective time, change reason and at least one bracket are required');
      return;
    }
    if (!brackets.every((bracket) => /^\d+$/.test(bracket.range_start_quantity) && (bracket.range_end_quantity === null || /^\d+$/.test(bracket.range_end_quantity)) && /^\d+$/.test(bracket.price_numerator_micro_units) && /^[1-9]\d*$/.test(bracket.price_denominator_quantity))) {
      toast.error('Each tier needs non-negative integer quantities, a micro-USD price, and a positive billing quantity');
      return;
    }
    if (brackets[0]?.range_start_quantity !== '0') {
      toast.error(`The first tier must start at 0 ${unitHint}`);
      return;
    }
    for (let index = 0; index < brackets.length; index += 1) {
      const bracket = brackets[index];
      const next = brackets[index + 1];
      if (bracket.range_end_quantity !== null && BigInt(bracket.range_end_quantity) <= BigInt(bracket.range_start_quantity)) {
        toast.error(`Tier ${index + 1} must end after it starts`);
        return;
      }
      if (next && (bracket.range_end_quantity === null || next.range_start_quantity !== bracket.range_end_quantity)) {
        toast.error(`Tier ${index + 2} must begin exactly where tier ${index + 1} ends`);
        return;
      }
      if (!next && bracket.range_end_quantity !== null) {
        toast.error('The final tier must be open-ended (∞)');
        return;
      }
    }
    if (!brackets.at(-1)?.range_end_quantity && brackets.length > 1 && brackets.some((bracket, index) => index < brackets.length - 1 && bracket.range_end_quantity === null)) {
      toast.error('Only the final tier can be open-ended');
      return;
    }
    setPublishing(true);
    try {
      const payload = {
        expected_latest_version: selected.latest_version?.version_number ?? 0,
        effective_from: `${effectiveFrom}:00.000Z`,
        change_reason: changeReason.trim(),
        brackets,
      };
      let published;
      if (STORAGE_BASE_CHARGE_KINDS.has(selected.charge_kind_code)) {
        published = await billingApi.publishStorageBasePriceVersion(selected.code, payload);
      } else if (HYPERVISOR_BASE_CHARGE_KINDS.has(selected.charge_kind_code)) {
        published = await billingApi.publishHypervisorBasePriceVersion(selected.code, payload);
      } else if (MAIL_BASE_CHARGE_KINDS.has(selected.charge_kind_code)) {
        published = await billingApi.publishMailBasePriceVersion(selected.code, payload);
      } else {
        toast.error('This module must publish its base price through its own workflow');
        return;
      }
      setChangeReason('');
      toast.success(`Version ${published.version_number} published`);
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
    switch (selected.charge_kind_code) {
      case 'storage.capacity.gb_hour': return 'GB-hour';
      case 'storage.network_in.byte':
      case 'storage.network_out.byte':
      case 'hypervisor.network_in.byte':
      case 'hypervisor.network_out.byte': return 'byte';
      case 'hypervisor.vcpu.allocated_second': return 'vCPU-second';
      case 'hypervisor.memory_mib.allocated_second': return 'MiB-second';
      case 'hypervisor.disk_gib.allocated_second': return 'GiB-second';
      case 'mail.delivery.accepted_recipient': return 'recipient';
      default: return 'raw quantity';
    }
  }, [selected]);

  const selectTab = (tab: PricingTab) => {
    const next = new URLSearchParams(searchParams);
    if (tab === 'base-pricing') next.delete('tab');
    else next.set('tab', tab);
    setSearchParams(next);
  };

  const selectZoneCode = (zoneCode: string) => {
    const next = new URLSearchParams(searchParams);
    if (zoneCode) next.set('zone', zoneCode);
    else next.delete('zone');
    setSearchParams(next);
  };

  const canAppendBracket = Boolean(brackets.at(-1)?.range_end_quantity && /^\d+$/.test(brackets.at(-1)?.range_end_quantity ?? ''));

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-center justify-between gap-3 border-b border-slate-800 pb-4">
        <div className="flex items-center gap-3">
          <Coins className="h-5 w-5 text-blue-400" />
          <div>
            <h1 className="text-lg font-bold text-slate-100">Pricing</h1>
            <p className="text-xs text-slate-400">Global PAYG rate cards and Hypervisor resource bundles are separate commercial workflows.</p>
          </div>
        </div>
        {activeTab === 'base-pricing' && <button type="button" onClick={() => void loadSchedules()} className="rounded border border-slate-700 p-2 text-slate-400 hover:text-white" aria-label="Refresh base pricing schedules">
          <RefreshCw size={15} className={cn(loading && 'animate-spin')} />
        </button>}
      </div>

      <div role="tablist" aria-label="Pricing management" className="flex w-fit rounded-lg border border-slate-800 bg-slate-900/60 p-1">
        <button type="button" role="tab" aria-selected={activeTab === 'base-pricing'} onClick={() => selectTab('base-pricing')} className={cn('rounded-md px-3 py-2 text-xs font-semibold transition-colors', activeTab === 'base-pricing' ? 'bg-blue-600 text-white' : 'text-slate-400 hover:text-slate-100')}>
          Base pricing
        </button>
        <button type="button" role="tab" aria-selected={activeTab === 'resource-plans'} onClick={() => selectTab('resource-plans')} className={cn('rounded-md px-3 py-2 text-xs font-semibold transition-colors', activeTab === 'resource-plans' ? 'bg-violet-600 text-white' : 'text-slate-400 hover:text-slate-100')}>
          Resource plans
        </button>
        <button type="button" role="tab" aria-selected={activeTab === 'zone-adjustments'} onClick={() => selectTab('zone-adjustments')} className={cn('rounded-md px-3 py-2 text-xs font-semibold transition-colors', activeTab === 'zone-adjustments' ? 'bg-cyan-600 text-white' : 'text-slate-400 hover:text-slate-100')}>
          Zone adjustments
        </button>
      </div>

      {activeTab === 'resource-plans' ? <HypervisorResourcePlansPanel canPublish={canPublish} /> : activeTab === 'zone-adjustments' ? <ZonePriceAdjustmentsTab canPublish={canPublish} zoneCode={searchParams.get('zone') ?? ''} onZoneCodeChange={selectZoneCode} /> : <>
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
                  <div className="mt-2 flex flex-wrap gap-2 text-[10px] text-slate-400"><span>{schedule.charge_kind_code}</span><span>·</span><span>GLOBAL BASE</span><span>·</span><span>{schedule.currency}</span></div>
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

              <div className="grid gap-3 text-xs sm:grid-cols-2"><div><span className="text-slate-500">Charge kind</span><p className="mt-1 font-mono text-slate-200">{selected.charge_kind_code}</p></div><div><span className="text-slate-500">Model / unit</span><p className="mt-1 text-slate-200">{selected.pricing_model} · {unitHint}</p></div><div><span className="text-slate-500">Base catalog</span><p className="mt-1 text-slate-200">GLOBAL</p></div><div><span className="text-slate-500">Currency</span><p className="mt-1 text-slate-200">{selected.currency}</p></div></div>

              {selected.latest_version ? <div className="rounded-lg border border-slate-800"><div className="flex items-center justify-between border-b border-slate-800 px-3 py-2"><div><span className="text-xs font-bold text-slate-200">Latest version {selected.latest_version.version_number}</span><span className="ml-2 text-[10px] text-emerald-300">{selected.latest_version.status}</span></div><span className="font-mono text-[10px] text-slate-500">{selected.latest_version.checksum.slice(0, 16)}…</span></div><div className="overflow-x-auto"><table className="w-full text-left text-[11px]"><thead className="text-slate-500"><tr><th className="px-3 py-2">Start ({unitHint})</th><th className="px-3 py-2">End</th><th className="px-3 py-2">Numerator µ</th><th className="px-3 py-2">Denominator</th></tr></thead><tbody className="divide-y divide-slate-800">{selected.latest_version.brackets.map((bracket) => <tr key={bracket.id ?? `${bracket.range_start_quantity}-${bracket.range_end_quantity}`}><td className="px-3 py-2 text-slate-200">{formatQuantity(bracket.range_start_quantity)}</td><td className="px-3 py-2 text-slate-200">{formatQuantity(bracket.range_end_quantity)}</td><td className="px-3 py-2 text-slate-200">{formatQuantity(bracket.price_numerator_micro_units)}</td><td className="px-3 py-2 text-slate-200">{formatQuantity(bracket.price_denominator_quantity)}</td></tr>)}</tbody></table></div><div className="border-t border-slate-800 px-3 py-2 text-[10px] text-slate-500">Effective {formatDate(selected.latest_version.effective_from)}</div></div> : <div className="rounded-lg border border-amber-800/50 bg-amber-950/20 p-3 text-xs text-amber-200">No base-price version is published for this schedule yet.</div>}

              {canPublish && selected.pricing_model === 'PROGRESSIVE_UNIT' && (STORAGE_BASE_CHARGE_KINDS.has(selected.charge_kind_code) || HYPERVISOR_BASE_CHARGE_KINDS.has(selected.charge_kind_code) || MAIL_BASE_CHARGE_KINDS.has(selected.charge_kind_code)) && <div className="space-y-4 rounded-lg border border-blue-900/50 bg-blue-950/10 p-4"><div className="flex items-center justify-between"><div><h3 className="text-xs font-bold text-blue-100">Publish immutable {STORAGE_BASE_CHARGE_KINDS.has(selected.charge_kind_code) ? 'Storage' : HYPERVISOR_BASE_CHARGE_KINDS.has(selected.charge_kind_code) ? 'Hypervisor' : 'Mail'} base version</h3><p className="mt-1 text-[10px] text-slate-400">A tier sets one rate for a contiguous range of {unitHint}. Amounts remain exact base-10 integers; the API receives the same immutable BIGINT payload.</p></div><Plus size={15} className="text-blue-300" /></div><div className="grid gap-2 sm:grid-cols-2"><label className="text-[10px] text-slate-400">Effective from (UTC+0)<input type="datetime-local" step={60} value={effectiveFrom} onChange={(event) => setEffectiveFrom(event.target.value)} className="mt-1 w-full rounded border border-slate-700 bg-slate-950 px-2 py-1.5 text-xs text-white" /></label><label className="text-[10px] text-slate-400">Change reason<input value={changeReason} onChange={(event) => setChangeReason(event.target.value)} placeholder="Why this rate changes" className="mt-1 w-full rounded border border-slate-700 bg-slate-950 px-2 py-1.5 text-xs text-white" /></label></div><div className="rounded border border-slate-800 bg-slate-950/40 p-3 text-[11px] text-slate-300"><span className="font-semibold text-slate-100">How a tier is priced: </span>price = <span className="font-mono">micro {selected.currency}</span> ÷ billed {unitHint}. One {selected.currency} is 1,000,000 micro {selected.currency}. Tiers must start at 0, touch without gaps, and the final tier has no upper limit.</div><div className="space-y-3">{brackets.map((bracket, index) => <fieldset key={bracket.id ?? index} className="rounded border border-slate-800 bg-slate-950/30 p-3"><div className="mb-2 flex items-center justify-between gap-2"><legend className="text-xs font-bold text-slate-200">Tier {index + 1}</legend>{index === brackets.length - 1 && brackets.length > 1 && <button type="button" onClick={removeLastBracket} className="text-[10px] font-semibold text-rose-300 hover:text-rose-200">Remove final tier</button>}</div><div className="grid gap-3 sm:grid-cols-4"><label className="text-[10px] text-slate-400">Usage from ({unitHint})<input type="text" readOnly value={bracket.range_start_quantity} className="mt-1 w-full rounded border border-slate-800 bg-slate-900 px-2 py-1.5 font-mono text-xs text-slate-300" /></label><label className="text-[10px] text-slate-400">Up to, exclusive ({unitHint})<input type="text" inputMode="numeric" pattern="[0-9]*" value={bracket.range_end_quantity ?? ''} onChange={(event) => updateBracketEnd(index, event.target.value)} placeholder="No limit (∞)" className="mt-1 w-full rounded border border-slate-700 bg-slate-950 px-2 py-1.5 font-mono text-xs text-white" aria-label={`Tier ${index + 1} upper usage bound`} /></label><label className="text-[10px] text-slate-400">Price (micro {selected.currency})<input type="text" inputMode="numeric" pattern="[0-9]*" value={bracket.price_numerator_micro_units} onChange={(event) => updateBracketRate(index, 'price_numerator_micro_units', event.target.value)} placeholder="for example 15000" className="mt-1 w-full rounded border border-slate-700 bg-slate-950 px-2 py-1.5 font-mono text-xs text-white" aria-label={`Tier ${index + 1} price in micro ${selected.currency}`} /></label><label className="text-[10px] text-slate-400">Billed quantity ({unitHint})<input type="text" inputMode="numeric" pattern="[0-9]*" value={bracket.price_denominator_quantity} onChange={(event) => updateBracketRate(index, 'price_denominator_quantity', event.target.value)} placeholder="for example 1" className="mt-1 w-full rounded border border-slate-700 bg-slate-950 px-2 py-1.5 font-mono text-xs text-white" aria-label={`Tier ${index + 1} billed quantity`} /></label></div></fieldset>)}<div className="flex flex-wrap items-center gap-3"><button type="button" disabled={!canAppendBracket} onClick={appendBracket} className="text-[11px] font-semibold text-blue-300 hover:text-blue-200 disabled:cursor-not-allowed disabled:text-slate-600">+ Add next tier</button>{!canAppendBracket && <span className="text-[10px] text-slate-500">Set a finite upper bound for the current tier before adding the next one.</span>}</div></div><button type="button" disabled={publishing} onClick={() => void publish()} className="inline-flex items-center gap-2 rounded bg-blue-600 px-3 py-2 text-xs font-bold text-white disabled:opacity-50"><Check size={14} />{publishing ? 'Publishing…' : 'Publish base version'}</button></div>}

              {!canPublish && <div className="flex items-start gap-2 rounded-lg border border-amber-900/40 bg-amber-950/20 p-3 text-[11px] text-amber-200"><AlertTriangle size={14} className="mt-0.5 shrink-0" />Read-only catalog. Publishing requires the billing pricing-schedule permission and a session proof.</div>}
            </div>
          )}
        </section>
      </div>

      </>}
    </div>
  );
}
