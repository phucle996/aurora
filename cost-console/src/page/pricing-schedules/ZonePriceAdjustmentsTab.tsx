import { useEffect, useState } from 'react';
import { RefreshCw } from 'lucide-react';
import { toast } from 'sonner';

import { billingApi, type ZoneCatalogEntry } from '../../lib/api/billing';
import { cn } from '../../lib/utils';
import { HypervisorZoneAdjustmentPanel } from './HypervisorZoneAdjustmentPanel';
import { MailZoneAdjustmentPanel } from './MailZoneAdjustmentPanel';
import { StorageZoneAdjustmentPanel } from './StorageZoneAdjustmentPanel';

type ZoneAdjustmentModule = 'storage' | 'mail' | 'hypervisor';

type ZonePriceAdjustmentsTabProps = {
  canPublish: boolean;
  zoneCode: string;
  onZoneCodeChange: (zoneCode: string) => void;
};

export function ZonePriceAdjustmentsTab({ canPublish, zoneCode, onZoneCodeChange }: ZonePriceAdjustmentsTabProps) {
  const [zones, setZones] = useState<ZoneCatalogEntry[]>([]);
  const [loadingZones, setLoadingZones] = useState(true);
  const [module, setModule] = useState<ZoneAdjustmentModule>('storage');

  const loadZones = async () => {
    setLoadingZones(true);
    try {
      const catalog = await billingApi.listZoneCatalog();
      setZones(catalog);
      if (!zoneCode && catalog[0]) onZoneCodeChange(catalog[0].code);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : 'Unable to load Zone catalog');
    } finally {
      setLoadingZones(false);
    }
  };

  useEffect(() => {
    void loadZones();
  }, []);

  return (
    <section className="space-y-5 rounded-xl border border-slate-800 bg-slate-900/60 p-5">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 className="text-base font-bold text-slate-100">Zone price adjustments</h2>
          <p className="mt-1 text-xs text-slate-400">A Zone multiplier changes only that Zone’s final rate. It never edits the Global base-price state.</p>
        </div>
        <button type="button" onClick={() => void loadZones()} className="rounded border border-slate-700 p-2 text-slate-400 hover:text-white" aria-label="Refresh Zone catalog">
          <RefreshCw size={15} className={cn(loadingZones && 'animate-spin')} />
        </button>
      </div>

      <label className="block max-w-lg text-xs font-semibold text-slate-300">
        Target Zone code
        <select value={zoneCode} disabled={loadingZones || zones.length === 0} onChange={(event) => onZoneCodeChange(event.target.value)} className="mt-2 w-full rounded border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-white disabled:cursor-not-allowed disabled:opacity-60">
          <option value="">Select a Zone</option>
          {zones.map((zone) => <option key={zone.code} value={zone.code}>{zone.name} ({zone.code})</option>)}
        </select>
      </label>

      {zoneCode && <div className="grid gap-3 rounded-xl border border-slate-800 bg-slate-950/45 p-4 text-xs sm:grid-cols-3">
        <div><p className="text-[10px] font-bold tracking-[0.14em] text-slate-500 uppercase">Commercial scope</p><p className="mt-1 font-semibold text-slate-100">{zoneCode}</p></div>
        <div><p className="text-[10px] font-bold tracking-[0.14em] text-slate-500 uppercase">Rate composition</p><p className="mt-1 font-semibold text-slate-100">Global base × Zone multiplier</p></div>
        <div><p className="text-[10px] font-bold tracking-[0.14em] text-slate-500 uppercase">Trust boundary</p><p className="mt-1 text-slate-300">ACR resolves this code to the trusted Zone ID.</p></div>
      </div>}

      {zones.length === 0 && !loadingZones ? <p className="rounded border border-amber-900/40 bg-amber-950/20 p-3 text-xs text-amber-200">No active or draining Zone is available for price adjustment.</p> : zoneCode && <>
        <div className="flex flex-wrap gap-2" role="tablist" aria-label="Zone adjustment module">
          <button type="button" role="tab" aria-selected={module === 'storage'} onClick={() => setModule('storage')} className={cn('rounded border px-3 py-1.5 text-xs font-semibold', module === 'storage' ? 'border-cyan-500 bg-cyan-950/50 text-cyan-200' : 'border-slate-700 text-slate-400 hover:text-slate-100')}>Storage</button>
          <button type="button" role="tab" aria-selected={module === 'mail'} onClick={() => setModule('mail')} className={cn('rounded border px-3 py-1.5 text-xs font-semibold', module === 'mail' ? 'border-violet-500 bg-violet-950/50 text-violet-200' : 'border-slate-700 text-slate-400 hover:text-slate-100')}>Mail</button>
          <button type="button" role="tab" aria-selected={module === 'hypervisor'} onClick={() => setModule('hypervisor')} className={cn('rounded border px-3 py-1.5 text-xs font-semibold', module === 'hypervisor' ? 'border-amber-500 bg-amber-950/50 text-amber-200' : 'border-slate-700 text-slate-400 hover:text-slate-100')}>Hypervisor</button>
        </div>
        {module === 'storage' && <StorageZoneAdjustmentPanel canPublish={canPublish} zoneCode={zoneCode} />}
        {module === 'mail' && <MailZoneAdjustmentPanel canPublish={canPublish} zoneCode={zoneCode} />}
        {module === 'hypervisor' && <HypervisorZoneAdjustmentPanel canPublish={canPublish} zoneCode={zoneCode} />}
      </>}
    </section>
  );
}
