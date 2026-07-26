import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  CalendarClock,
  Gift,
  LoaderCircle,
  PauseCircle,
  PlayCircle,
  Plus,
  TicketPercent,
  X,
} from "lucide-react";
import { toast } from "sonner";

import { billingApi, type ReferralCampaign } from "../../lib/api/billing";
import { formatUSDMicroUnits, usdToMicroUnits } from "../../lib/money";

function localDateTime(hoursFromNow: number): string {
  const value = new Date(Date.now() + hoursFromNow * 60 * 60 * 1000);
  value.setMinutes(value.getMinutes() - value.getTimezoneOffset());
  return value.toISOString().slice(0, 16);
}

export function ReferralCampaigns() {
  const queryClient = useQueryClient();
  const [showCreate, setShowCreate] = useState(false);
  const [code, setCode] = useState("");
  const [name, setName] = useState("");
  const [grantUSD, setGrantUSD] = useState("25");
  const [minimumUSD, setMinimumUSD] = useState("1");
  const [maxRedemptions, setMaxRedemptions] = useState("");
  const [startsAt, setStartsAt] = useState(() => localDateTime(0));
  const [endsAt, setEndsAt] = useState(() => localDateTime(24 * 30));

  const campaigns = useQuery({
    queryKey: ["billing", "referral-campaigns"],
    queryFn: ({ signal }) => billingApi.listReferralCampaigns(signal),
    staleTime: 15_000,
  });

  const createCampaign = useMutation({
    mutationFn: async () => {
      const amount = usdToMicroUnits(grantUSD);
      const minimum = usdToMicroUnits(minimumUSD);
      if (!amount || !minimum) throw new Error("Grant/minimum USD không hợp lệ.");
      return billingApi.createReferralCampaign({
        code: code.trim().toUpperCase(),
        name: name.trim(),
        amount_micro_units: amount,
        minimum_top_up_micro_units: minimum,
        currency: "USD",
        max_redemptions: maxRedemptions.trim() || undefined,
        starts_at: new Date(startsAt).toISOString(),
        ends_at: endsAt ? new Date(endsAt).toISOString() : undefined,
      });
    },
    onSuccess: async () => {
      setShowCreate(false);
      toast.success("Campaign đã được tạo ở trạng thái PAUSED.");
      await queryClient.invalidateQueries({ queryKey: ["billing", "referral-campaigns"] });
    },
    onError: (error) => toast.error(error instanceof Error ? error.message : "Không thể tạo campaign"),
  });

  const updateStatus = useMutation({
    mutationFn: ({ campaign, status }: { campaign: ReferralCampaign; status: ReferralCampaign["status"] }) =>
      billingApi.updateReferralCampaignStatus(campaign.id, status, campaign.version),
    onSuccess: async () => {
      toast.success("Campaign status đã được cập nhật.");
      await queryClient.invalidateQueries({ queryKey: ["billing", "referral-campaigns"] });
    },
    onError: (error) => toast.error(error instanceof Error ? error.message : "Không thể đổi status"),
  });

  return (
    <div className="space-y-6">
      <div className="flex flex-col justify-between gap-4 sm:flex-row sm:items-end">
        <div>
          <p className="text-[10px] font-bold uppercase tracking-[0.22em] text-violet-500">
            Promotional control
          </p>
          <h2 className="mt-1 text-xl font-bold text-slate-900 dark:text-slate-100">Referral campaigns</h2>
          <p className="mt-1 max-w-xl text-xs leading-relaxed text-slate-500">
            Campaign mới luôn PAUSED. Activation là critical mutation có session proof và OCC version.
          </p>
        </div>
        <button
          type="button"
          onClick={() => setShowCreate(true)}
          className="inline-flex items-center justify-center gap-2 rounded-md bg-violet-600 px-4 py-2.5 text-xs font-bold text-white hover:bg-violet-500 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-violet-500"
        >
          <Plus size={15} /> Tạo referral
        </button>
      </div>

      <div className="overflow-hidden rounded-xl border border-slate-200 bg-white dark:border-slate-800 dark:bg-slate-900">
        <div className="overflow-x-auto">
          <table className="w-full min-w-[860px] text-left">
            <thead className="border-b border-slate-200 bg-slate-50 text-[11px] uppercase tracking-wider text-slate-500 dark:border-slate-800 dark:bg-slate-950/60">
              <tr>
                <th className="px-4 py-3">Campaign</th>
                <th className="px-4 py-3">Grant / Minimum</th>
                <th className="px-4 py-3">Capacity</th>
                <th className="px-4 py-3">Window</th>
                <th className="px-4 py-3">Status</th>
                <th className="px-4 py-3 text-right">Action</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-200 text-xs dark:divide-slate-800/70">
              {campaigns.isLoading && (
                <tr><td colSpan={6} className="px-4 py-12 text-center text-slate-500">
                  <LoaderCircle className="mx-auto h-5 w-5 animate-spin" />
                </td></tr>
              )}
              {campaigns.isError && (
                <tr><td colSpan={6} className="px-4 py-10 text-center text-rose-400">
                  Không thể tải referral campaigns.
                </td></tr>
              )}
              {campaigns.data?.map((campaign) => (
                <tr key={campaign.id} className="hover:bg-slate-800/20">
                  <td className="px-4 py-4">
                    <div className="font-semibold text-slate-900 dark:text-slate-200">{campaign.name}</div>
                    <code className="mt-1 block text-[10px] text-violet-400">{campaign.code}</code>
                  </td>
                  <td className="px-4 py-4">
                    <div className="font-semibold text-emerald-400">{formatUSDMicroUnits(campaign.amount_micro_units)}</div>
                    <div className="mt-1 text-[10px] text-slate-600">min {formatUSDMicroUnits(campaign.minimum_top_up_micro_units)}</div>
                  </td>
                  <td className="px-4 py-4 text-slate-400">
                    <div>{campaign.redemptions} redeemed</div>
                    <div className="mt-1 text-[10px] text-slate-600">
                      {campaign.active_reservations} reserved / {campaign.max_redemptions ?? "∞"}
                    </div>
                  </td>
                  <td className="px-4 py-4 text-[10px] text-slate-500">
                    <div>{new Date(campaign.starts_at).toLocaleString()}</div>
                    <div className="mt-1">{campaign.ends_at ? new Date(campaign.ends_at).toLocaleString() : "No end"}</div>
                  </td>
                  <td className="px-4 py-4">
                    <span className={`inline-flex items-center gap-1.5 text-[11px] font-semibold ${
                      campaign.status === "ACTIVE"
                        ? "text-emerald-600 dark:text-emerald-400"
                        : campaign.status === "PAUSED"
                          ? "text-amber-600 dark:text-amber-400"
                          : "text-slate-500"
                    }`}>
                      <span className={`h-2 w-2 rounded-full ${
                        campaign.status === "ACTIVE"
                          ? "bg-emerald-500"
                          : campaign.status === "PAUSED"
                            ? "bg-amber-500"
                            : "bg-slate-400"
                      }`} />
                      {campaign.status}
                    </span>
                  </td>
                  <td className="px-4 py-4 text-right">
                    {campaign.status !== "ENDED" && (
                      <button
                        type="button"
                        disabled={updateStatus.isPending}
                        onClick={() => updateStatus.mutate({
                          campaign,
                          status: campaign.status === "ACTIVE" ? "PAUSED" : "ACTIVE",
                        })}
                        className="inline-flex items-center gap-1.5 rounded-md border border-slate-300 px-2.5 py-1.5 text-[11px] font-semibold text-slate-700 hover:border-violet-600 hover:text-violet-600 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-violet-500 disabled:opacity-40 dark:border-slate-700 dark:text-slate-300 dark:hover:text-violet-300"
                      >
                        {campaign.status === "ACTIVE" ? <PauseCircle size={13} /> : <PlayCircle size={13} />}
                        {campaign.status === "ACTIVE" ? "Pause" : "Activate"}
                      </button>
                    )}
                  </td>
                </tr>
              ))}
              {campaigns.data?.length === 0 && (
                <tr><td colSpan={6} className="px-4 py-12 text-center text-slate-600">
                  Chưa có onboarding referral campaign.
                </td></tr>
              )}
            </tbody>
          </table>
        </div>
      </div>

      {showCreate && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/75 p-4">
          <form
            onSubmit={(event) => {
              event.preventDefault();
              createCampaign.mutate();
            }}
            className="max-h-[92vh] w-full max-w-2xl overflow-y-auto rounded-xl border border-slate-200 bg-white shadow-xl dark:border-slate-700 dark:bg-slate-950"
          >
            <div className="flex items-center justify-between border-b border-slate-800 px-5 py-4">
              <div className="flex items-center gap-2">
                <TicketPercent size={18} className="text-violet-400" />
                <h3 className="text-sm font-semibold text-slate-900 dark:text-slate-100">New onboarding referral</h3>
              </div>
              <button type="button" onClick={() => setShowCreate(false)} className="text-slate-500 hover:text-slate-200">
                <X size={18} />
              </button>
            </div>
            <div className="grid gap-4 p-5 sm:grid-cols-2">
              <label className="space-y-1.5 text-[10px] font-bold uppercase tracking-wider text-slate-500">
                Code
                <input value={code} onChange={(e) => setCode(e.target.value.toUpperCase())} maxLength={32} required className="w-full rounded-md border border-slate-300 bg-white px-3 py-2.5 text-xs normal-case text-slate-900 outline-none focus-visible:border-violet-500 focus-visible:ring-2 focus-visible:ring-violet-500/30 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100" />
              </label>
              <label className="space-y-1.5 text-[10px] font-bold uppercase tracking-wider text-slate-500">
                Name
                <input value={name} onChange={(e) => setName(e.target.value)} maxLength={128} required className="w-full rounded-md border border-slate-300 bg-white px-3 py-2.5 text-xs normal-case text-slate-900 outline-none focus-visible:border-violet-500 focus-visible:ring-2 focus-visible:ring-violet-500/30 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100" />
              </label>
              <label className="space-y-1.5 text-[10px] font-bold uppercase tracking-wider text-slate-500">
                Promotional grant (USD)
                <input inputMode="decimal" value={grantUSD} onChange={(e) => setGrantUSD(e.target.value)} required className="w-full rounded-md border border-slate-300 bg-white px-3 py-2.5 text-xs normal-case text-slate-900 outline-none focus-visible:border-violet-500 focus-visible:ring-2 focus-visible:ring-violet-500/30 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100" />
              </label>
              <label className="space-y-1.5 text-[10px] font-bold uppercase tracking-wider text-slate-500">
                Minimum top-up (USD)
                <input inputMode="decimal" value={minimumUSD} onChange={(e) => setMinimumUSD(e.target.value)} required className="w-full rounded-md border border-slate-300 bg-white px-3 py-2.5 text-xs normal-case text-slate-900 outline-none focus-visible:border-violet-500 focus-visible:ring-2 focus-visible:ring-violet-500/30 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100" />
              </label>
              <label className="space-y-1.5 text-[10px] font-bold uppercase tracking-wider text-slate-500">
                Max redemptions
                <input inputMode="numeric" value={maxRedemptions} onChange={(e) => setMaxRedemptions(e.target.value)} placeholder="Unlimited" className="w-full rounded-md border border-slate-300 bg-white px-3 py-2.5 text-xs normal-case text-slate-900 outline-none focus-visible:border-violet-500 focus-visible:ring-2 focus-visible:ring-violet-500/30 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100" />
              </label>
              <div />
              <label className="space-y-1.5 text-[10px] font-bold uppercase tracking-wider text-slate-500">
                <span className="inline-flex items-center gap-1"><CalendarClock size={12} /> Starts at</span>
                <input type="datetime-local" value={startsAt} onChange={(e) => setStartsAt(e.target.value)} required className="w-full rounded-md border border-slate-300 bg-white px-3 py-2.5 text-xs normal-case text-slate-900 outline-none focus-visible:border-violet-500 focus-visible:ring-2 focus-visible:ring-violet-500/30 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100" />
              </label>
              <label className="space-y-1.5 text-[10px] font-bold uppercase tracking-wider text-slate-500">
                Ends at
                <input type="datetime-local" value={endsAt} onChange={(e) => setEndsAt(e.target.value)} className="w-full rounded-md border border-slate-300 bg-white px-3 py-2.5 text-xs normal-case text-slate-900 outline-none focus-visible:border-violet-500 focus-visible:ring-2 focus-visible:ring-violet-500/30 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100" />
              </label>
            </div>
            <div className="flex items-center justify-between border-t border-slate-800 px-5 py-4">
              <p className="flex items-center gap-1.5 text-[10px] text-slate-600"><Gift size={12} /> Creation does not activate the campaign.</p>
              <button type="submit" disabled={createCampaign.isPending} className="inline-flex items-center gap-2 rounded-md bg-violet-600 px-4 py-2.5 text-xs font-bold text-white hover:bg-violet-500 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-violet-500 disabled:opacity-40">
                {createCampaign.isPending && <LoaderCircle size={14} className="animate-spin" />}
                Create paused campaign
              </button>
            </div>
          </form>
        </div>
      )}
    </div>
  );
}
