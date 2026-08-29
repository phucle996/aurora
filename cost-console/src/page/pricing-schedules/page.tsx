import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { AlertTriangle, Check, Coins, Layers, MapPin, Pencil, Plus, RefreshCw, Save } from 'lucide-react';
import { toast } from 'sonner';

import {
  billingApi,
  type PricingBracket,
  type PricingScheduleRateState,
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
  const [rateState, setRateState] = useState<PricingScheduleRateState | null>(null);
  const [loading, setLoading] = useState(true);
  const [selectedCode, setSelectedCode] = useState<string | null>(null);
  const [search, setSearch] = useState('');
  const [draftOpen, setDraftOpen] = useState(false);
  const [editingMetadata, setEditingMetadata] = useState(false);
  const [displayName, setDisplayName] = useState('');
  const [brackets, setBrackets] = useState<PricingBracket[]>([]);
  const [effectiveFrom, setEffectiveFrom] = useState('');
  const [changeReason, setChangeReason] = useState('');
  const [publishing, setPublishing] = useState(false);
  const [compareOpen, setCompareOpen] = useState(false);
  const tabListRef = useRef<HTMLDivElement>(null);
  const tabRefs = useRef<Record<PricingTab, HTMLButtonElement | null>>({
    'base-pricing': null,
    'resource-plans': null,
    'zone-adjustments': null,
  });
  const [activeIndicator, setActiveIndicator] = useState({ left: 0, width: 0 });

  const loadSchedules = useCallback(async () => {
    if (activeTab !== 'base-pricing') return;
    setLoading(true);
    try {
      const response = await billingApi.listPricingSchedules(1, 50, undefined, search.trim() || undefined);
      setSchedules(response.pricing_schedules);
      if (selectedCode) {
        const [detail, card] = await Promise.all([
          billingApi.getPricingScheduleDetail(selectedCode),
          billingApi.getPricingScheduleRateState(selectedCode),
        ]);
        setSelected(detail);
        setRateState(card);
      }
    } catch (error) {
      toast.error(error instanceof Error ? error.message : 'Unable to load pricing schedules');
    } finally {
      setLoading(false);
    }
  }, [activeTab, selectedCode, search]);

  useEffect(() => {
    void loadSchedules();
  }, [loadSchedules]);

  const selectSchedule = async (schedule: PricingSchedule) => {
    setSelectedCode(schedule.code);
    try {
      const [detail, card] = await Promise.all([
        billingApi.getPricingScheduleDetail(schedule.code),
        billingApi.getPricingScheduleRateState(schedule.code),
      ]);
      setSelected(detail);
      setRateState(card);
      setDisplayName(detail.display_name);
      setBrackets((card.effective_version ?? detail.latest_version)?.brackets.map((bracket) => ({ ...bracket })) ?? [{ ...EMPTY_BRACKET }]);
      setEffectiveFrom(new Date(Date.now() + 60_000).toISOString().slice(0, 16));
      setChangeReason('');
      setEditingMetadata(false);
      setDraftOpen(false);
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

  const validateDraft = () => {
    if (!selected || !changeReason.trim() || !effectiveFrom || brackets.length === 0) {
      toast.error('Effective time, change reason and at least one bracket are required');
      return false;
    }
    if (!brackets.every((bracket) => /^\d+$/.test(bracket.range_start_quantity) && (bracket.range_end_quantity === null || /^\d+$/.test(bracket.range_end_quantity)) && /^\d+$/.test(bracket.price_numerator_micro_units) && /^[1-9]\d*$/.test(bracket.price_denominator_quantity))) {
      toast.error('Each tier needs non-negative integer quantities, a micro-USD price, and a positive billing quantity');
      return false;
    }
    if (brackets[0]?.range_start_quantity !== '0') {
      toast.error(`The first tier must start at 0 ${unitHint}`);
      return false;
    }
    for (let index = 0; index < brackets.length; index += 1) {
      const bracket = brackets[index];
      const next = brackets[index + 1];
      if (bracket.range_end_quantity !== null && BigInt(bracket.range_end_quantity) <= BigInt(bracket.range_start_quantity)) {
        toast.error(`Tier ${index + 1} must end after it starts`);
        return false;
      }
      if (next && (bracket.range_end_quantity === null || next.range_start_quantity !== bracket.range_end_quantity)) {
        toast.error(`Tier ${index + 2} must begin exactly where tier ${index + 1} ends`);
        return false;
      }
      if (!next && bracket.range_end_quantity !== null) {
        toast.error('The final tier must be open-ended (∞)');
        return false;
      }
    }
    if (!brackets.at(-1)?.range_end_quantity && brackets.length > 1 && brackets.some((bracket, index) => index < brackets.length - 1 && bracket.range_end_quantity === null)) {
      toast.error('Only the final tier can be open-ended');
      return false;
    }
    return true;
  };

  const publish = async () => {
    if (!selected || !validateDraft()) return false;
    setPublishing(true);
    try {
      const payload = {
        expected_latest_version: rateState?.latest_version_number ?? selected.latest_version?.version_number ?? 0,
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
        return false;
      }
      setChangeReason('');
      toast.success(`Version ${published.version_number} published`);
      await loadSchedules();
      return true;
    } catch (error) {
      toast.error(error instanceof Error ? error.message : 'Pricing version publish failed');
    } finally {
      setPublishing(false);
    }
  };

  const openCompare = () => {
    if (validateDraft()) setCompareOpen(true);
  };

  const confirmPublish = async () => {
    if (await publish()) setCompareOpen(false);
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

  useLayoutEffect(() => {
    const updateIndicator = () => {
      const list = tabListRef.current;
      const tab = tabRefs.current[activeTab];
      if (!list || !tab) return;
      setActiveIndicator({ left: tab.offsetLeft, width: tab.offsetWidth });
    };
    updateIndicator();
    window.addEventListener('resize', updateIndicator);
    return () => window.removeEventListener('resize', updateIndicator);
  }, [activeTab]);

  const selectZoneCode = (zoneCode: string) => {
    const next = new URLSearchParams(searchParams);
    if (zoneCode) next.set('zone', zoneCode);
    else next.delete('zone');
    setSearchParams(next);
  };

  const canAppendBracket = Boolean(brackets.at(-1)?.range_end_quantity && /^\d+$/.test(brackets.at(-1)?.range_end_quantity ?? ''));
  const effectiveVersion = rateState?.effective_version ?? selected?.latest_version ?? null;
  const nextScheduledVersion = rateState?.next_scheduled_version ?? null;

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-center justify-between gap-3 border-b border-slate-800 pb-4">
        <div className="flex items-center gap-3">
          <Coins className="h-5 w-5 text-blue-400" />
          <div>
            <h1 className="text-xl font-bold tracking-tight text-slate-100">Pricing</h1>
            <p className="text-xs text-slate-400">Global PAYG base-price states and Hypervisor resource bundles are separate commercial workflows.</p>
          </div>
        </div>
        {activeTab === 'base-pricing' && <button type="button" onClick={() => void loadSchedules()} className="rounded border border-slate-700 p-2 text-slate-400 hover:text-white" aria-label="Refresh base pricing schedules">
          <RefreshCw size={15} className={cn(loading && 'animate-spin')} />
        </button>}
      </div>

      <div ref={tabListRef} role="tablist" aria-label="Pricing management" className="relative flex items-center gap-6 border-b border-slate-200 dark:border-slate-800">
        <button ref={(node) => { tabRefs.current['base-pricing'] = node; }} type="button" role="tab" aria-selected={activeTab === 'base-pricing'} onClick={() => selectTab('base-pricing')} className={cn('inline-flex items-center gap-2 px-0 py-3 text-xs font-semibold transition-colors', activeTab === 'base-pricing' ? 'text-blue-600 dark:text-blue-400' : 'text-slate-500 hover:text-slate-900 dark:text-slate-400 dark:hover:text-slate-100')}>
          <Coins size={14} /> Base pricing
        </button>
        <button ref={(node) => { tabRefs.current['resource-plans'] = node; }} type="button" role="tab" aria-selected={activeTab === 'resource-plans'} onClick={() => selectTab('resource-plans')} className={cn('inline-flex items-center gap-2 px-0 py-3 text-xs font-semibold transition-colors', activeTab === 'resource-plans' ? 'text-blue-600 dark:text-blue-400' : 'text-slate-500 hover:text-slate-900 dark:text-slate-400 dark:hover:text-slate-100')}>
          <Layers size={14} /> Resource plans
        </button>
        <button ref={(node) => { tabRefs.current['zone-adjustments'] = node; }} type="button" role="tab" aria-selected={activeTab === 'zone-adjustments'} onClick={() => selectTab('zone-adjustments')} className={cn('inline-flex items-center gap-2 px-0 py-3 text-xs font-semibold transition-colors', activeTab === 'zone-adjustments' ? 'text-blue-600 dark:text-blue-400' : 'text-slate-500 hover:text-slate-900 dark:text-slate-400 dark:hover:text-slate-100')}>
          <MapPin size={14} /> Zone adjustments
        </button>
        <span aria-hidden="true" className="pointer-events-none absolute bottom-[-1px] h-0.5 bg-blue-600 transition-[left,width] duration-200 ease-out dark:bg-blue-400" style={{ left: activeIndicator.left, width: activeIndicator.width }} />
      </div>

      <div key={activeTab} className="cost-tab-panel">
      {activeTab === 'resource-plans' ? <HypervisorResourcePlansPanel canPublish={canPublish} /> : activeTab === 'zone-adjustments' ? <ZonePriceAdjustmentsTab canPublish={canPublish} zoneCode={searchParams.get('zone') ?? ''} onZoneCodeChange={selectZoneCode} /> : <>
      <div className="grid gap-4 lg:grid-cols-[18rem_minmax(0,1fr)]">
        <section className="overflow-hidden rounded-[4px] border border-slate-800 bg-slate-900/60">
          <div className="border-b border-slate-800 p-3">
            <p className="text-[10px] font-bold tracking-wider text-slate-500 uppercase">Pricing schedules</p>
            <input value={search} onChange={(event) => setSearch(event.target.value)} placeholder="Search by name or code" className="mt-3 h-8 w-full rounded-[4px] border border-slate-700 bg-slate-950 px-2.5 text-xs text-slate-100 outline-none placeholder:text-slate-600 focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20" />
          </div>
          {loading && schedules.length === 0 ? (
            <div className="flex items-center justify-center gap-2 p-12 text-xs text-slate-500"><RefreshCw size={15} className="animate-spin" /> Loading…</div>
          ) : schedules.length === 0 ? (
            <div className="p-10 text-center text-xs text-slate-500">No pricing schedule is available.</div>
          ) : (
            <div className="space-y-2 bg-slate-50/70 p-2 dark:bg-slate-950/40">
              {schedules.map((schedule) => (
                <button type="button" key={schedule.id} onClick={() => void selectSchedule(schedule)} className={cn('cost-schedule-item w-full px-4 py-3.5 text-left', selectedCode === schedule.code && 'cost-schedule-item-selected')}>
                  <div className="flex items-center justify-between gap-3">
                    <span className="cost-schedule-title font-semibold">{schedule.display_name}</span>
                    <span className={cn('rounded-[3px] px-1.5 py-0.5 text-[10px] font-bold', schedule.status === 'ACTIVE' ? 'bg-emerald-100 text-emerald-700 dark:bg-emerald-950/60 dark:text-emerald-300' : 'bg-slate-100 text-slate-500 dark:bg-slate-800 dark:text-slate-400')}>{schedule.status}</span>
                  </div>
                  <div className="cost-schedule-code mt-1 font-mono text-[11px]">{schedule.code}</div>
                  <div className="cost-schedule-meta mt-2 flex flex-wrap gap-2 text-[10px]"><span>{schedule.charge_kind_code}</span><span>·</span><span>GLOBAL BASE</span><span>·</span><span>{schedule.currency}</span></div>
                </button>
              ))}
            </div>
          )}
        </section>

        <section className="rounded-[4px] border border-slate-800 bg-slate-900/60 p-5">
          {!selected ? (
            <div className="flex h-full min-h-64 items-center justify-center text-xs text-slate-500">Select a schedule to inspect its immutable version.</div>
          ) : (
            <div className="space-y-5">
              <div className="flex flex-wrap items-start justify-between gap-3 border-b border-slate-800 pb-5">
                <div className="min-w-0">
                  {editingMetadata ? (
                    <div className="flex gap-2"><input value={displayName} onChange={(event) => setDisplayName(event.target.value)} className="rounded border border-slate-700 bg-slate-950 px-2 py-1 text-sm text-white" /><button type="button" onClick={() => void saveMetadata()} className="rounded bg-blue-600 p-2 text-white"><Save size={14} /></button></div>
                  ) : <h2 className="text-xl font-bold tracking-tight text-white">{selected.display_name}</h2>}
                  <p className="mt-1 font-mono text-[11px] text-blue-300">{selected.code}</p>
                </div>
                <div className="flex items-center gap-2">
                  {canPublish && selected.pricing_model === 'PROGRESSIVE_UNIT' && <button type="button" onClick={() => setDraftOpen((open) => !open)} className="inline-flex h-8 items-center gap-1.5 rounded-[4px] bg-blue-600 px-3 text-xs font-bold text-white hover:bg-blue-500"><Plus size={14} />{draftOpen ? 'Close draft' : 'Draft price'}</button>}
                  {canPublish && !editingMetadata && <button type="button" onClick={() => { setEditingMetadata(true); setDisplayName(selected.display_name); }} className="rounded-[4px] border border-slate-700 p-2 text-slate-400 hover:text-white" aria-label="Edit schedule name"><Pencil size={14} /></button>}
                </div>
              </div>

              <div className="grid gap-3 text-xs sm:grid-cols-4"><div><span className="text-[10px] font-bold tracking-wide text-slate-500 uppercase">Charge kind</span><p className="mt-1 font-mono text-[11px] text-slate-200">{selected.charge_kind_code}</p></div><div><span className="text-[10px] font-bold tracking-wide text-slate-500 uppercase">Unit</span><p className="mt-1 text-slate-200">{unitHint}</p></div><div><span className="text-[10px] font-bold tracking-wide text-slate-500 uppercase">Scope</span><p className="mt-1 text-slate-200">Global base</p></div><div><span className="text-[10px] font-bold tracking-wide text-slate-500 uppercase">Currency</span><p className="mt-1 text-slate-200">{selected.currency}</p></div></div>

              {effectiveVersion ? <div className="overflow-hidden rounded-xl border border-slate-800 bg-slate-950/40"><div className="flex flex-wrap items-center justify-between gap-3 border-b border-slate-800 px-4 py-3"><div><p className="text-[10px] font-bold tracking-[0.16em] text-emerald-300 uppercase">Effective global base rate</p><span className="mt-1 block text-sm font-semibold text-slate-100">Version {effectiveVersion.version_number}</span></div><span className="rounded-full border border-emerald-900/60 bg-emerald-950/30 px-2.5 py-1 text-[10px] font-bold text-emerald-300">{effectiveVersion.status}</span></div><div className="overflow-x-auto"><table className="w-full text-left text-[11px]"><thead className="border-b border-slate-800 bg-slate-900/60 text-slate-500"><tr><th className="px-4 py-2.5">Usage range ({unitHint})</th><th className="px-4 py-2.5">Price (micro {selected.currency})</th><th className="px-4 py-2.5">Per billed quantity</th></tr></thead><tbody className="divide-y divide-slate-800">{effectiveVersion.brackets.map((bracket) => <tr key={bracket.id ?? `${bracket.range_start_quantity}-${bracket.range_end_quantity}`}><td className="px-4 py-3 font-mono text-slate-200">{formatQuantity(bracket.range_start_quantity)} — {formatQuantity(bracket.range_end_quantity)}</td><td className="px-4 py-3 font-mono text-slate-100">{formatQuantity(bracket.price_numerator_micro_units)}</td><td className="px-4 py-3 font-mono text-slate-300">{formatQuantity(bracket.price_denominator_quantity)} {unitHint}</td></tr>)}</tbody></table></div><div className="flex flex-wrap items-center justify-between gap-2 border-t border-slate-800 px-4 py-2.5 text-[10px] text-slate-500"><span>Effective {formatDate(effectiveVersion.effective_from)}</span><span className="font-mono">{effectiveVersion.checksum.slice(0, 16)}…</span></div></div> : <div className="rounded-xl border border-amber-800/50 bg-amber-950/20 p-4 text-xs text-amber-200">No base-price version is effective yet. The schedule remains unusable for settlement until an immutable version is published.</div>}

              {nextScheduledVersion && <div className="rounded-xl border border-blue-900/50 bg-blue-950/15 p-4"><div className="flex flex-wrap items-center justify-between gap-2"><div><p className="text-[10px] font-bold tracking-[0.16em] text-blue-300 uppercase">Next scheduled change</p><p className="mt-1 text-xs font-semibold text-slate-100">Version {nextScheduledVersion.version_number} takes effect {formatDate(nextScheduledVersion.effective_from)}</p></div><span className="rounded-full border border-blue-900/60 px-2.5 py-1 text-[10px] font-bold text-blue-200">{nextScheduledVersion.status}</span></div><p className="mt-3 text-[11px] text-slate-400">{nextScheduledVersion.change_reason}</p></div>}

              {draftOpen && canPublish && selected.pricing_model === 'PROGRESSIVE_UNIT' && (STORAGE_BASE_CHARGE_KINDS.has(selected.charge_kind_code) || HYPERVISOR_BASE_CHARGE_KINDS.has(selected.charge_kind_code) || MAIL_BASE_CHARGE_KINDS.has(selected.charge_kind_code)) && <div className="space-y-4 rounded-[4px] border border-blue-900/50 bg-blue-950/10 p-4"><div className="flex items-center justify-between"><div><p className="text-[10px] font-bold tracking-wider text-blue-300 uppercase">New immutable version</p><h3 className="mt-1 text-sm font-semibold text-blue-100">Draft a new {STORAGE_BASE_CHARGE_KINDS.has(selected.charge_kind_code) ? 'Storage' : HYPERVISOR_BASE_CHARGE_KINDS.has(selected.charge_kind_code) ? 'Hypervisor' : 'Mail'} base rate</h3><p className="mt-1 text-[11px] text-slate-400">Prepare contiguous tiers, then review the difference before the critical publish flow starts.</p></div><Plus size={15} className="text-blue-300" /></div><div className="grid gap-2 sm:grid-cols-2"><label className="text-[10px] text-slate-400">Effective from (UTC+0)<input type="datetime-local" step={60} value={effectiveFrom} onChange={(event) => setEffectiveFrom(event.target.value)} className="mt-1 w-full rounded-[4px] border border-slate-700 bg-slate-950 px-2 py-1.5 text-xs text-white" /></label><label className="text-[10px] text-slate-400">Change reason<input value={changeReason} onChange={(event) => setChangeReason(event.target.value)} placeholder="Why this rate changes" className="mt-1 w-full rounded-[4px] border border-slate-700 bg-slate-950 px-2 py-1.5 text-xs text-white" /></label></div><div className="rounded-[4px] border border-slate-800 bg-slate-950/40 p-3 text-[11px] text-slate-300"><span className="font-semibold text-slate-100">How a tier is priced: </span>price = <span className="font-mono">micro {selected.currency}</span> ÷ billed {unitHint}. One {selected.currency} is 1,000,000 micro {selected.currency}. Tiers must start at 0, touch without gaps, and the final tier has no upper limit.</div><div className="space-y-3">{brackets.map((bracket, index) => <fieldset key={bracket.id ?? index} className="rounded-[4px] border border-slate-800 bg-slate-950/30 p-3"><div className="mb-2 flex items-center justify-between gap-2"><legend className="text-xs font-bold text-slate-200">Tier {index + 1}</legend>{index === brackets.length - 1 && brackets.length > 1 && <button type="button" onClick={removeLastBracket} className="text-[10px] font-semibold text-rose-300 hover:text-rose-200">Remove final tier</button>}</div><div className="grid gap-3 sm:grid-cols-4"><label className="text-[10px] text-slate-400">Usage from ({unitHint})<input type="text" readOnly value={bracket.range_start_quantity} className="mt-1 w-full rounded-[4px] border border-slate-800 bg-slate-900 px-2 py-1.5 font-mono text-xs text-slate-300" /></label><label className="text-[10px] text-slate-400">Up to, exclusive ({unitHint})<input type="text" inputMode="numeric" pattern="[0-9]*" value={bracket.range_end_quantity ?? ''} onChange={(event) => updateBracketEnd(index, event.target.value)} placeholder="No limit (∞)" className="mt-1 w-full rounded-[4px] border border-slate-700 bg-slate-950 px-2 py-1.5 font-mono text-xs text-white" aria-label={`Tier ${index + 1} upper usage bound`} /></label><label className="text-[10px] text-slate-400">Price (micro {selected.currency})<input type="text" inputMode="numeric" pattern="[0-9]*" value={bracket.price_numerator_micro_units} onChange={(event) => updateBracketRate(index, 'price_numerator_micro_units', event.target.value)} placeholder="for example 15000" className="mt-1 w-full rounded-[4px] border border-slate-700 bg-slate-950 px-2 py-1.5 font-mono text-xs text-white" aria-label={`Tier ${index + 1} price in micro ${selected.currency}`} /></label><label className="text-[10px] text-slate-400">Billed quantity ({unitHint})<input type="text" inputMode="numeric" pattern="[0-9]*" value={bracket.price_denominator_quantity} onChange={(event) => updateBracketRate(index, 'price_denominator_quantity', event.target.value)} placeholder="for example 1" className="mt-1 w-full rounded-[4px] border border-slate-700 bg-slate-950 px-2 py-1.5 font-mono text-xs text-white" aria-label={`Tier ${index + 1} billed quantity`} /></label></div></fieldset>)}<div className="flex flex-wrap items-center gap-3"><button type="button" disabled={!canAppendBracket} onClick={appendBracket} className="text-[11px] font-semibold text-blue-300 hover:text-blue-200 disabled:cursor-not-allowed disabled:text-slate-600">+ Add next tier</button>{!canAppendBracket && <span className="text-[10px] text-slate-500">Set a finite upper bound for the current tier before adding the next one.</span>}</div></div><button type="button" disabled={publishing} onClick={openCompare} className="inline-flex items-center gap-2 rounded-[4px] bg-blue-600 px-3 py-2 text-xs font-bold text-white disabled:opacity-50"><Check size={14} />Review changes</button></div>}

              {!canPublish && <div className="flex items-start gap-2 rounded-lg border border-amber-900/40 bg-amber-950/20 p-3 text-[11px] text-amber-200"><AlertTriangle size={14} className="mt-0.5 shrink-0" />Read-only catalog. Publishing requires the billing pricing-schedule permission and a session proof.</div>}
            </div>
          )}
        </section>
      </div>

      </>}
      </div>

      {compareOpen && selected && <div className="fixed inset-0 z-50 flex items-end bg-slate-950/80 p-4 backdrop-blur-sm sm:items-center sm:justify-center" role="presentation">
        <section role="dialog" aria-modal="true" aria-labelledby="pricing-compare-title" className="max-h-[92vh] w-full max-w-5xl overflow-y-auto rounded-xl border border-slate-700 bg-slate-950 shadow-2xl">
          <div className="sticky top-0 z-10 flex items-start justify-between gap-4 border-b border-slate-800 bg-slate-950/95 px-5 py-4 backdrop-blur">
            <div>
              <p className="text-[10px] font-bold tracking-[0.16em] text-blue-300 uppercase">Required pricing review</p>
              <h2 id="pricing-compare-title" className="mt-1 text-base font-semibold text-white">Compare global base-rate change</h2>
              <p className="mt-1 text-[11px] text-slate-400">This review compares the authoritative current rate with your draft. Confirming starts the session-proof protected publish.</p>
            </div>
            <button type="button" onClick={() => setCompareOpen(false)} className="rounded border border-slate-700 px-2.5 py-1.5 text-xs font-semibold text-slate-300 hover:bg-slate-900">Close</button>
          </div>
          <div className="space-y-5 p-5">
            <div className="grid gap-3 text-xs sm:grid-cols-3">
              <div className="rounded-lg border border-slate-800 bg-slate-900/60 p-3"><p className="text-[10px] font-bold tracking-wide text-slate-500 uppercase">Schedule</p><p className="mt-1 font-mono text-slate-100">{selected.code}</p></div>
              <div className="rounded-lg border border-slate-800 bg-slate-900/60 p-3"><p className="text-[10px] font-bold tracking-wide text-slate-500 uppercase">Proposed effective time</p><p className="mt-1 font-semibold text-slate-100">{formatDate(`${effectiveFrom}:00.000Z`)}</p></div>
              <div className="rounded-lg border border-slate-800 bg-slate-900/60 p-3"><p className="text-[10px] font-bold tracking-wide text-slate-500 uppercase">OCC baseline</p><p className="mt-1 font-semibold text-slate-100">Latest version {rateState?.latest_version_number ?? selected.latest_version?.version_number ?? 0}</p></div>
            </div>
            {nextScheduledVersion && <div className="rounded-lg border border-amber-900/60 bg-amber-950/25 p-3 text-xs text-amber-100"><strong>Scheduled version {nextScheduledVersion.version_number}</strong> already begins at {formatDate(nextScheduledVersion.effective_from)}. The server remains authoritative for effective-window ordering and can reject a stale or conflicting publish.</div>}
            <div className="grid gap-4 lg:grid-cols-2">
              <section className="overflow-hidden rounded-xl border border-slate-800"><div className="border-b border-slate-800 bg-slate-900/60 px-4 py-3"><p className="text-[10px] font-bold tracking-wide text-slate-500 uppercase">Currently effective</p><p className="mt-1 text-xs font-semibold text-slate-100">{effectiveVersion ? `Version ${effectiveVersion.version_number} · ${formatDate(effectiveVersion.effective_from)}` : 'No effective version'}</p></div><div className="overflow-x-auto"><table className="w-full text-left text-[11px]"><thead className="text-slate-500"><tr><th className="px-4 py-2">Range</th><th className="px-4 py-2">Micro {selected.currency}</th><th className="px-4 py-2">Quantity</th></tr></thead><tbody className="divide-y divide-slate-800">{effectiveVersion?.brackets.map((bracket) => <tr key={bracket.id ?? `${bracket.range_start_quantity}-${bracket.range_end_quantity}`}><td className="px-4 py-2.5 font-mono text-slate-200">{formatQuantity(bracket.range_start_quantity)} — {formatQuantity(bracket.range_end_quantity)}</td><td className="px-4 py-2.5 font-mono text-slate-200">{formatQuantity(bracket.price_numerator_micro_units)}</td><td className="px-4 py-2.5 font-mono text-slate-200">{formatQuantity(bracket.price_denominator_quantity)}</td></tr>)}</tbody></table></div></section>
              <section className="overflow-hidden rounded-xl border border-blue-900/60"><div className="border-b border-blue-900/60 bg-blue-950/30 px-4 py-3"><p className="text-[10px] font-bold tracking-wide text-blue-300 uppercase">Proposed immutable version</p><p className="mt-1 text-xs font-semibold text-slate-100">{brackets.length} contiguous {brackets.length === 1 ? 'tier' : 'tiers'}</p></div><div className="overflow-x-auto"><table className="w-full text-left text-[11px]"><thead className="text-slate-500"><tr><th className="px-4 py-2">Range</th><th className="px-4 py-2">Micro {selected.currency}</th><th className="px-4 py-2">Quantity</th></tr></thead><tbody className="divide-y divide-slate-800">{brackets.map((bracket, index) => <tr key={`${bracket.range_start_quantity}-${bracket.range_end_quantity}-${index}`}><td className="px-4 py-2.5 font-mono text-slate-100">{formatQuantity(bracket.range_start_quantity)} — {formatQuantity(bracket.range_end_quantity)}</td><td className="px-4 py-2.5 font-mono text-blue-200">{formatQuantity(bracket.price_numerator_micro_units)}</td><td className="px-4 py-2.5 font-mono text-blue-200">{formatQuantity(bracket.price_denominator_quantity)}</td></tr>)}</tbody></table></div></section>
            </div>
            <div className="rounded-lg border border-slate-800 bg-slate-900/60 p-3 text-xs text-slate-300"><span className="font-semibold text-slate-100">Reason:</span> {changeReason}</div>
          </div>
          <div className="sticky bottom-0 flex flex-wrap justify-end gap-3 border-t border-slate-800 bg-slate-950/95 px-5 py-4 backdrop-blur"><button type="button" onClick={() => setCompareOpen(false)} className="rounded border border-slate-700 px-3 py-2 text-xs font-semibold text-slate-300 hover:bg-slate-900">Back to draft</button><button type="button" disabled={publishing} onClick={() => void confirmPublish()} className="inline-flex items-center gap-2 rounded bg-blue-600 px-3 py-2 text-xs font-bold text-white hover:bg-blue-500 disabled:opacity-50"><Check size={14} />{publishing ? 'Publishing…' : 'Confirm and publish'}</button></div>
        </section>
      </div>}
    </div>
  );
}
