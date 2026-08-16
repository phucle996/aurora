import { useCallback, useEffect, useMemo, useState } from 'react';
import { AlertTriangle, Check, RefreshCw } from 'lucide-react';
import { toast } from 'sonner';

import {
  billingApi,
  type MailZonePriceAdjustmentsResponse,
} from '../../lib/api/billing';
import { cn } from '../../lib/utils';

type MailZoneAdjustmentPanelProps = {
  canPublish: boolean;
};

export function MailZoneAdjustmentPanel({ canPublish }: MailZoneAdjustmentPanelProps) {
  const [result, setResult] = useState<MailZonePriceAdjustmentsResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [publishing, setPublishing] = useState(false);
  const [effectiveFrom, setEffectiveFrom] = useState(
    new Date(Date.now() + 60_000).toISOString().slice(0, 16),
  );
  const [changeReason, setChangeReason] = useState('');
  const [multiplierNumerator, setMultiplierNumerator] = useState('100');
  const [multiplierDenominator, setMultiplierDenominator] = useState('100');

  const load = useCallback(async (signal?: AbortSignal) => {
    setLoading(true);
    try {
      setResult(await billingApi.listMailZonePriceAdjustments(100, signal));
    } catch (error) {
      if (!(error instanceof DOMException && error.name === 'AbortError')) {
        toast.error(error instanceof Error ? error.message : 'Unable to load Mail Zone price adjustments');
      }
    } finally {
      if (!signal?.aborted) setLoading(false);
    }
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    void load(controller.signal);
    return () => controller.abort();
  }, [load]);

  const adjustments = useMemo(() => (result?.adjustments ?? []).map((adjustment) => {
    let percentage = 'Invalid ratio';
    try {
      const numerator = BigInt(adjustment.multiplier_numerator);
      const denominator = BigInt(adjustment.multiplier_denominator);
      if (denominator > 0n) {
        const hundredths = (numerator * 10_000n + denominator / 2n) / denominator;
        percentage = `${hundredths / 100n}.${String(hundredths % 100n).padStart(2, '0')}%`;
      }
    } catch {
      // The API contract only emits signed BIGINT decimal strings. Keep an explicit fallback for corrupt responses.
    }
    return { ...adjustment, percentage };
  }), [result]);

  const latest = adjustments.find((adjustment) => adjustment.is_latest) ?? null;
  const current = adjustments.find((adjustment) => adjustment.is_effective) ?? null;
  const upcoming = useMemo(() => {
    const observedAt = result ? new Date(result.observed_at).getTime() : Date.now();
    return adjustments
      .filter((adjustment) => !adjustment.is_effective && new Date(adjustment.effective_from).getTime() > observedAt)
      .sort((left, right) => left.effective_from.localeCompare(right.effective_from));
  }, [adjustments, result]);

  const publish = async () => {
    const numeratorValue = multiplierNumerator.trim();
    const denominatorValue = multiplierDenominator.trim();
    if (!effectiveFrom || !changeReason.trim()) {
      toast.error('Effective time and change reason are required');
      return;
    }
    if (!/^\d+$/.test(numeratorValue) || !/^[1-9]\d*$/.test(denominatorValue)) {
      toast.error('Multiplier values must be decimal integers and the denominator must be positive');
      return;
    }
    try {
      const int64Max = 9_223_372_036_854_775_807n;
      if (BigInt(numeratorValue) > int64Max || BigInt(denominatorValue) > int64Max) {
        toast.error('Multiplier values must fit within signed BIGINT range');
        return;
      }
    } catch {
      toast.error('Multiplier values must be valid decimal integers');
      return;
    }

    setPublishing(true);
    try {
      const published = await billingApi.publishMailZonePriceAdjustment({
        expected_latest_version: latest?.version_number ?? 0,
        effective_from: `${effectiveFrom}:00.000Z`,
        change_reason: changeReason.trim(),
        multiplier_numerator: numeratorValue,
        multiplier_denominator: denominatorValue,
      });
      setChangeReason('');
      setEffectiveFrom(new Date(Date.now() + 60_000).toISOString().slice(0, 16));
      toast.success(`Mail Zone adjustment version ${published.version_number} published`);
      await load();
    } catch (error) {
      toast.error(error instanceof Error ? error.message : 'Mail Zone price adjustment publish failed');
      await load();
    } finally {
      setPublishing(false);
    }
  };

  return (
    <div className="space-y-4 rounded-lg border border-violet-900/50 bg-violet-950/10 p-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h3 className="text-xs font-bold text-violet-100">Mail Zone price adjustment</h3>
          <p className="mt-1 text-[10px] text-slate-400">
            Multiplies the immutable GLOBAL Mail recipient rate for the trusted operator Zone.
          </p>
          {result && <p className="mt-1 font-mono text-[10px] text-violet-300">Zone {result.zone_id}</p>}
        </div>
        <button
          type="button"
          onClick={() => void load()}
          className="rounded border border-slate-700 p-2 text-slate-400 hover:text-white"
          aria-label="Refresh Mail Zone price adjustments"
        >
          <RefreshCw size={14} className={cn(loading && 'animate-spin')} />
        </button>
      </div>

      {loading && !result ? (
        <div className="flex items-center gap-2 py-5 text-xs text-slate-500">
          <RefreshCw size={14} className="animate-spin" /> Loading Zone adjustment history…
        </div>
      ) : (
        <>
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="rounded border border-slate-800 bg-slate-950/50 p-3">
              <p className="text-[10px] font-bold uppercase tracking-wide text-slate-500">Effective now</p>
              {current ? (
                <>
                  <p className="mt-2 text-lg font-bold text-emerald-300">{current.percentage}</p>
                  <p className="font-mono text-[10px] text-slate-400">
                    {current.multiplier_numerator} / {current.multiplier_denominator} · v{current.version_number}
                  </p>
                </>
              ) : (
                <>
                  <p className="mt-2 text-lg font-bold text-slate-200">100.00%</p>
                  <p className="text-[10px] text-slate-400">No effective override; GLOBAL rate applies at 1 / 1.</p>
                </>
              )}
            </div>
            <div className="rounded border border-slate-800 bg-slate-950/50 p-3">
              <p className="text-[10px] font-bold uppercase tracking-wide text-slate-500">Next scheduled</p>
              {upcoming[0] ? (
                <>
                  <p className="mt-2 text-lg font-bold text-blue-300">{upcoming[0].percentage}</p>
                  <p className="text-[10px] text-slate-400">
                    v{upcoming[0].version_number} · {new Date(upcoming[0].effective_from).toISOString().replace('T', ' ').replace('Z', ' UTC')}
                  </p>
                </>
              ) : <p className="mt-2 text-xs text-slate-500">No future adjustment scheduled.</p>}
            </div>
          </div>

          {canPublish ? (
            <div className="space-y-3 rounded border border-violet-900/40 bg-slate-950/30 p-3">
              <div>
                <p className="text-xs font-bold text-slate-200">Publish immutable Zone version</p>
                <p className="mt-1 text-[10px] text-slate-500">
                  Expected latest version: {latest?.version_number ?? 0}. Session proof is requested when publishing.
                </p>
              </div>
              <div className="grid gap-2 sm:grid-cols-2">
                <label className="text-[10px] text-slate-400">
                  Multiplier numerator (BIGINT string)
                  <input type="text" inputMode="numeric" pattern="[0-9]*" value={multiplierNumerator} onChange={(event) => setMultiplierNumerator(event.target.value)} className="mt-1 w-full rounded border border-slate-700 bg-slate-950 px-2 py-1.5 font-mono text-xs text-white" />
                </label>
                <label className="text-[10px] text-slate-400">
                  Multiplier denominator (BIGINT string)
                  <input type="text" inputMode="numeric" pattern="[0-9]*" value={multiplierDenominator} onChange={(event) => setMultiplierDenominator(event.target.value)} className="mt-1 w-full rounded border border-slate-700 bg-slate-950 px-2 py-1.5 font-mono text-xs text-white" />
                </label>
                <label className="text-[10px] text-slate-400">
                  Effective from (UTC+0)
                  <input type="datetime-local" step={60} value={effectiveFrom} onChange={(event) => setEffectiveFrom(event.target.value)} className="mt-1 w-full rounded border border-slate-700 bg-slate-950 px-2 py-1.5 text-xs text-white" />
                </label>
                <label className="text-[10px] text-slate-400">
                  Change reason
                  <input value={changeReason} maxLength={2000} onChange={(event) => setChangeReason(event.target.value)} className="mt-1 w-full rounded border border-slate-700 bg-slate-950 px-2 py-1.5 text-xs text-white" />
                </label>
              </div>
              <button type="button" disabled={publishing} onClick={() => void publish()} className="inline-flex items-center gap-2 rounded bg-violet-600 px-3 py-2 text-xs font-bold text-white disabled:opacity-50">
                <Check size={14} /> {publishing ? 'Publishing…' : 'Publish Zone adjustment'}
              </button>
            </div>
          ) : (
            <div className="flex items-start gap-2 rounded border border-amber-900/40 bg-amber-950/20 p-3 text-[11px] text-amber-200">
              <AlertTriangle size={14} className="mt-0.5 shrink-0" />
              Read-only Zone history. Publishing requires the billing pricing-schedule permission and a session proof.
            </div>
          )}

          <div className="overflow-hidden rounded border border-slate-800">
            <div className="border-b border-slate-800 px-3 py-2 text-[10px] font-bold uppercase tracking-wide text-slate-500">Zone version history</div>
            {adjustments.length === 0 ? (
              <div className="p-4 text-xs text-slate-500">No Zone-specific version has been published.</div>
            ) : (
              <div className="overflow-x-auto">
                <table className="w-full text-left text-[10px]">
                  <thead className="text-slate-500"><tr><th className="px-3 py-2">Version</th><th className="px-3 py-2">Ratio</th><th className="px-3 py-2">Effective (UTC+0)</th><th className="px-3 py-2">Reason</th></tr></thead>
                  <tbody className="divide-y divide-slate-800">
                    {adjustments.map((adjustment) => (
                      <tr key={adjustment.id}>
                        <td className="px-3 py-2 text-slate-200">v{adjustment.version_number}{adjustment.is_latest ? ' · latest' : ''}</td>
                        <td className="px-3 py-2 font-mono text-slate-200">{adjustment.multiplier_numerator}/{adjustment.multiplier_denominator} · {adjustment.percentage}</td>
                        <td className="whitespace-nowrap px-3 py-2 text-slate-300">{new Date(adjustment.effective_from).toISOString().replace('T', ' ').replace('Z', ' UTC')}</td>
                        <td className="max-w-52 truncate px-3 py-2 text-slate-400" title={adjustment.change_reason}>{adjustment.change_reason}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
            {result?.has_more && <div className="border-t border-amber-900/30 px-3 py-2 text-[10px] text-amber-300">Only the newest 100 versions are shown.</div>}
          </div>
        </>
      )}
    </div>
  );
}
