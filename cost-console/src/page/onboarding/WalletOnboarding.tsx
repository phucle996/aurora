import { useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  BadgeDollarSign,
  CheckCircle2,
  Clock3,
  CreditCard,
  Gift,
  LoaderCircle,
  ShieldCheck,
  WalletCards,
} from "lucide-react";
import { toast } from "sonner";

import { billingApi } from "../../lib/api/billing";
import { formatUSDMicroUnits, usdToMicroUnits } from "../../lib/money";
import { useAuthStore } from "../../lib/store/useAuthStore";

export function WalletOnboarding({ personal }: { personal: boolean }) {
  const userID = useAuthStore((state) => state.user?.id ?? "");
  const queryClient = useQueryClient();
  const referralKey = useRef(crypto.randomUUID());
  const topUpKey = useRef(crypto.randomUUID());
  const [referralCode, setReferralCode] = useState("");
  const [amountUSD, setAmountUSD] = useState("1.00");

  const onboarding = useQuery({
    queryKey: ["billing", "wallet", personal ? "personal" : "tenant", userID],
    queryFn: async ({ signal }) => {
      if (personal) return billingApi.getOnboarding(signal);
      const wallet = await billingApi.getWalletSummary(signal);
      return {
        wallet,
        minimum_top_up_micro_units: wallet.minimum_top_up_micro_units ?? "1000000",
        referral: null,
        latest_payment_intent: null,
      };
    },
    enabled: Boolean(userID),
    staleTime: 10_000,
    retry: 2,
    refetchInterval: (query) =>
      query.state.data?.latest_payment_intent?.status === "PENDING" ? 3_000 : false,
  });

  const reserveReferral = useMutation({
    mutationFn: () => {
      if (!personal) throw new Error("Tenant wallet không hỗ trợ referral.");
      return billingApi.reserveReferral(referralCode.trim().toUpperCase(), referralKey.current);
    },
    onSuccess: async () => {
      referralKey.current = crypto.randomUUID();
      toast.success("Referral đã được giữ chỗ cho lần nạp đầu tiên.");
      await queryClient.invalidateQueries({ queryKey: ["billing", "wallet", "personal", userID] });
    },
    onError: (error) => toast.error(error instanceof Error ? error.message : "Không thể giữ referral"),
  });

  const createTopUp = useMutation({
    mutationFn: async () => {
      const amount = usdToMicroUnits(amountUSD);
      if (!amount) throw new Error("Số tiền USD không hợp lệ.");
      const intent = await billingApi.createTopUp(amount, topUpKey.current);
      if (!intent.checkout_url) throw new Error("Payment gateway không trả checkout URL.");
      return intent;
    },
    onSuccess: (intent) => {
      topUpKey.current = crypto.randomUUID();
      window.location.assign(intent.checkout_url!);
    },
    onError: (error) => toast.error(error instanceof Error ? error.message : "Không thể tạo phiên thanh toán"),
  });

  if (onboarding.isLoading) {
    return (
      <div className="flex min-h-72 items-center justify-center rounded-lg border border-slate-200 bg-white dark:border-slate-800 dark:bg-slate-900">
        <LoaderCircle className="h-6 w-6 animate-spin text-blue-500" />
      </div>
    );
  }
  if (onboarding.isError || !onboarding.data) {
    return (
      <div className="rounded-lg border border-rose-200 bg-rose-50 p-6 text-sm text-rose-700 dark:border-rose-900/50 dark:bg-rose-950/20 dark:text-rose-300">
        <p className="font-semibold">Không thể tải trạng thái ví.</p>
        <p className="mt-1 text-xs text-rose-400/80">
          {onboarding.error instanceof Error ? onboarding.error.message : "Wallet có thể vẫn đang được provision."}
        </p>
        <button
          type="button"
          onClick={() => onboarding.refetch()}
          className="mt-4 rounded-md bg-rose-600 px-3 py-2 text-xs font-semibold text-white hover:bg-rose-500 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-rose-500"
        >
          Thử lại
        </button>
      </div>
    );
  }

  const state = onboarding.data;
  const walletActive = state.wallet.status === "ACTIVE";
  const walletPending = state.wallet.status === "PENDING_ACTIVATION";
  const walletClosed = state.wallet.status === "CLOSED";
  const minimum = BigInt(state.minimum_top_up_micro_units);
  const referralMinimum = walletPending && state.referral?.status === "RESERVED"
    ? BigInt(state.referral.minimum_top_up_micro_units)
    : 0n;
  const requiredMinimum = referralMinimum > minimum ? referralMinimum : minimum;
  const parsedAmount = usdToMicroUnits(amountUSD);
  const amountValid = parsedAmount !== null && BigInt(parsedAmount) >= requiredMinimum;
  const hasLiveReferral = state.referral?.status === "RESERVED" || state.referral?.status === "REDEEMED";
  const ownerLabel = personal ? "Personal account" : "Tenant account";

  return (
    <div className="space-y-6">
      <div className="flex flex-col justify-between gap-4 md:flex-row md:items-end">
        <div>
          <p className="text-[10px] font-bold uppercase tracking-[0.22em] text-blue-400">
            Account overview · {ownerLabel}
          </p>
          <h2 className="mt-1 text-xl font-bold tracking-tight text-slate-100">
            {walletPending ? "Activate your billing wallet" : "Billing balance"}
          </h2>
          <p className="mt-1 max-w-2xl text-xs leading-relaxed text-slate-400">
            Cash and promotional credit are shown separately. A payment only changes this balance after gateway settlement commits.
          </p>
        </div>
        <div className={`inline-flex w-fit items-center gap-2 rounded-full border px-3 py-1.5 text-xs font-semibold ${
          walletActive
            ? "border-emerald-900/60 bg-emerald-950/30 text-emerald-300"
            : "border-amber-900/60 bg-amber-950/30 text-amber-300"
        }`}>
          <span className={`h-2 w-2 rounded-full ${walletActive ? "bg-emerald-500" : "bg-amber-500"}`} />
          {walletActive ? <CheckCircle2 size={14} /> : <Clock3 size={14} />}
          {state.wallet.status}
        </div>
      </div>

      <div className="grid gap-4 xl:grid-cols-[minmax(0,1.6fr)_minmax(13rem,0.7fr)_minmax(13rem,0.7fr)]">
        <section className="relative overflow-hidden rounded-xl border border-blue-900/60 bg-[linear-gradient(135deg,rgba(30,64,175,0.36),rgba(15,23,42,0.94)_58%)] p-6 shadow-[0_20px_45px_-30px_rgba(59,130,246,0.7)]">
          <div className="absolute -top-16 -right-12 h-44 w-44 rounded-full bg-blue-500/15 blur-2xl" />
          <div className="relative flex items-start justify-between gap-4 text-slate-400">
            <span className="text-[10px] font-bold uppercase tracking-[0.16em]">Cash balance</span>
            <WalletCards size={18} className="text-blue-300" />
          </div>
          <p className="relative mt-8 text-3xl font-bold tracking-tight tabular-nums text-white sm:text-4xl">
            {formatUSDMicroUnits(state.wallet.cash_balance_micro_units)}
          </p>
          <p className="relative mt-2 text-[11px] text-slate-400">Settled funds in your {personal ? "personal" : "tenant"} wallet</p>
        </section>
        <section className="rounded-xl border border-slate-800 bg-slate-900/70 p-5">
          <div className="flex items-center justify-between text-slate-500">
            <span className="text-[10px] font-bold uppercase tracking-wider">Promotional credit</span>
            <Gift size={17} className="text-violet-300" />
          </div>
          <p className="mt-6 text-xl font-bold tabular-nums text-slate-100">
            {formatUSDMicroUnits(state.wallet.promotional_balance_micro_units)}
          </p>
          <p className="mt-2 text-[10px] leading-relaxed text-slate-500">Campaign credit; tracked independently from cash.</p>
        </section>
        <section className="rounded-xl border border-slate-800 bg-slate-900/70 p-5">
          <div className="flex items-center justify-between text-slate-500">
            <span className="text-[10px] font-bold uppercase tracking-wider">Wallet safety</span>
            <ShieldCheck size={17} className="text-emerald-300" />
          </div>
          <p className="mt-6 text-sm font-semibold text-slate-100">Exact USD micro-units</p>
          <p className="mt-2 text-[10px] leading-relaxed text-slate-500">
            Every debit and settlement is serialized by the wallet durable boundary.
          </p>
        </section>
      </div>

      {!walletClosed && (
        <div className={`grid gap-5 ${walletPending ? "lg:grid-cols-[0.9fr_1.1fr]" : "grid-cols-1"}`}>
          {walletPending && personal && (
          <section className="rounded-lg border border-slate-200 bg-white p-5 dark:border-slate-800 dark:bg-slate-900">
            <div className="flex items-center gap-3">
              <div className="rounded-lg bg-violet-500/10 p-2 text-violet-400"><Gift size={18} /></div>
              <div>
                <h3 className="text-sm font-semibold text-slate-900 dark:text-slate-100">Referral onboarding</h3>
                <p className="text-[10px] text-slate-500">Mỗi account chỉ nhận một onboarding grant.</p>
              </div>
            </div>

            {hasLiveReferral && state.referral ? (
              <div className="mt-5 rounded-lg border border-violet-900/60 bg-violet-950/20 p-4">
                <div className="flex items-center justify-between gap-3">
                  <code className="text-sm font-bold text-violet-300">{state.referral.code}</code>
                  <span className="rounded-full bg-violet-500/10 px-2 py-1 text-[10px] font-bold text-violet-300">
                    {state.referral.status}
                  </span>
                </div>
                <p className="mt-3 text-xs text-slate-400">
                  Grant: <strong className="text-slate-200">
                    <span className="text-slate-900 dark:text-slate-200">
                      {formatUSDMicroUnits(state.referral.grant_amount_micro_units)}
                    </span>
                  </strong>
                </p>
                <p className="mt-1 text-[10px] text-slate-600">
                  Reserved until {new Date(state.referral.expires_at).toLocaleString()}
                </p>
              </div>
            ) : (
              <form
                className="mt-5 space-y-3"
                onSubmit={(event) => {
                  event.preventDefault();
                  reserveReferral.mutate();
                }}
              >
                <label className="block text-[10px] font-bold uppercase tracking-wider text-slate-500">
                  Referral code
                </label>
                {state.referral?.rejection_reason && (
                  <p className="rounded-md border border-amber-900/50 bg-amber-950/20 px-3 py-2 text-[10px] normal-case tracking-normal text-amber-300">
                    Referral trước không được áp dụng: {state.referral.rejection_reason}
                  </p>
                )}
                <input
                  value={referralCode}
                  onChange={(event) => {
                    referralKey.current = crypto.randomUUID();
                    setReferralCode(event.target.value.toUpperCase());
                  }}
                  maxLength={32}
                  placeholder="AURORA_START"
                  className="w-full rounded-md border border-slate-300 bg-white px-3 py-2.5 text-sm font-semibold uppercase text-slate-900 outline-none focus-visible:border-violet-500 focus-visible:ring-2 focus-visible:ring-violet-500/30 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100"
                />
                <button
                  type="submit"
                  disabled={referralCode.trim().length < 4 || reserveReferral.isPending}
                  className="inline-flex w-full items-center justify-center gap-2 rounded-md border border-violet-700 bg-violet-600/10 px-4 py-2.5 text-xs font-bold text-violet-700 hover:bg-violet-600/15 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-violet-500 disabled:cursor-not-allowed disabled:opacity-40 dark:text-violet-300"
                >
                  {reserveReferral.isPending && <LoaderCircle size={14} className="animate-spin" />}
                  Giữ referral
                </button>
              </form>
            )}
          </section>
          )}

          <section className="rounded-lg border border-blue-200 bg-white p-5 dark:border-blue-900/60 dark:bg-slate-900">
            <div className="flex items-center gap-3">
              <div className="rounded-lg bg-blue-500/10 p-2 text-blue-400"><CreditCard size={18} /></div>
              <div>
                <h3 className="text-sm font-semibold text-slate-900 dark:text-slate-100">
                  {walletPending
                    ? "Nạp tiền để kích hoạt"
                    : walletActive
                      ? "Nạp thêm tiền"
                      : "Nạp tiền để mở lại wallet"}
                </h3>
                <p className="text-[10px] text-slate-500">
                  Tối thiểu {formatUSDMicroUnits(requiredMinimum.toString())}; không cố định đúng 1 USD.
                </p>
              </div>
            </div>
            <form
              className="mt-5"
              onSubmit={(event) => {
                event.preventDefault();
                createTopUp.mutate();
              }}
            >
              <label className="block text-[10px] font-bold uppercase tracking-wider text-slate-500">
                Amount in USD
              </label>
              <div className="mt-2 flex rounded-md border border-slate-300 bg-white focus-within:border-blue-500 focus-within:ring-2 focus-within:ring-blue-500/30 dark:border-slate-700 dark:bg-slate-950">
                <span className="border-r border-slate-800 px-3 py-3 text-sm font-bold text-slate-500">$</span>
                <input
                  inputMode="decimal"
                  value={amountUSD}
                  onChange={(event) => {
                    topUpKey.current = crypto.randomUUID();
                    setAmountUSD(event.target.value);
                  }}
                  className="min-w-0 flex-1 bg-transparent px-3 text-base font-bold text-slate-900 outline-none dark:text-slate-100"
                />
              </div>
              {!amountValid && (
                <p className="mt-2 text-[10px] text-amber-400">
                  Số tiền chưa đạt minimum của wallet/referral.
                </p>
              )}
              <button
                type="submit"
                disabled={!amountValid || createTopUp.isPending}
                className="mt-4 inline-flex w-full items-center justify-center gap-2 rounded-md bg-blue-600 px-4 py-3 text-xs font-bold text-white transition hover:bg-blue-500 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-500 focus-visible:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-40"
              >
                {createTopUp.isPending ? <LoaderCircle size={15} className="animate-spin" /> : <BadgeDollarSign size={15} />}
                Tiếp tục tới payment gateway
              </button>
            </form>
            {state.latest_payment_intent?.status === "PENDING" && (
              <div className="mt-4 flex items-center gap-2 rounded-lg border border-amber-900/50 bg-amber-950/20 px-3 py-2 text-[10px] text-amber-300">
                <Clock3 size={13} />
                Đang chờ settlement cho intent {state.latest_payment_intent.id.slice(0, 8)}…
              </div>
            )}
          </section>
        </div>
      )}
      {walletClosed && (
        <div className="rounded-lg border border-rose-200 bg-rose-50 p-5 text-sm text-rose-700 dark:border-rose-900/50 dark:bg-rose-950/20 dark:text-rose-300">
          Wallet đã đóng và không thể nhận thêm tiền. Hãy liên hệ Billing Support để đối soát.
        </div>
      )}
    </div>
  );
}
