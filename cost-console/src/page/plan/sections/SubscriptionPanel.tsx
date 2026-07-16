import { useEffect, useState } from "react";
import { CheckCircle2, ShieldAlert, XCircle, RefreshCw } from "lucide-react";
import { billingApi, type Subscription, getDemoOwner } from "../../../lib/api/billing";

interface SubscriptionPanelProps {
  onSubscribeSuccess: () => void;
  refreshTrigger: number;
}

export function SubscriptionPanel({ onSubscribeSuccess, refreshTrigger }: SubscriptionPanelProps) {
  const [sub, setSub] = useState<Subscription | null>(null);
  const [loading, setLoading] = useState(true);
  const [cancelling, setCancelling] = useState(false);
  const owner = getDemoOwner();

  const fetchSub = async () => {
    setLoading(true);
    try {
      const activeSub = await billingApi.getActiveSubscription();
      setSub(activeSub);
    } catch (err) {
      console.error(err);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchSub();
  }, [refreshTrigger]);

  const handleCancel = async () => {
    if (!window.confirm("Bạn có chắc chắn muốn huỷ gói cước hiện tại không? Quota còn lại sẽ mất.")) return;
    setCancelling(true);
    try {
      await billingApi.cancelSubscription();
      await fetchSub();
      onSubscribeSuccess();
    } catch (err: any) {
      alert("Lỗi khi huỷ gói: " + err.message);
    } finally {
      setCancelling(false);
    }
  };

  if (loading) {
    return (
      <div className="p-4 bg-slate-50 dark:bg-slate-900/40 border border-slate-200 dark:border-slate-800 rounded-lg flex items-center justify-center gap-2 text-xs text-slate-400">
        <RefreshCw size={14} className="animate-spin" />
        Đang tải thông tin gói đăng ký...
      </div>
    );
  }

  return (
    <div className="bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-xl p-5 shadow-sm text-xs flex flex-col md:flex-row md:items-center justify-between gap-4">
      <div className="space-y-2">
        <div className="flex items-center gap-2">
          {sub ? (
            <CheckCircle2 size={16} className="text-emerald-500 shrink-0" />
          ) : (
            <XCircle size={16} className="text-amber-500 shrink-0" />
          )}
          <span className="font-bold text-slate-800 dark:text-slate-200 text-sm">
            {sub ? `Gói đang kích hoạt: ${sub.plan?.name}` : "Đang sử dụng Pay-as-you-go"}
          </span>
        </div>
        <p className="text-[11px] text-slate-500 dark:text-slate-400">
          Demo Owner ID: <strong className="font-mono">{owner.id}</strong> ({owner.type})
        </p>

        {sub && sub.plan?.metrics && (
          <div className="flex flex-wrap gap-x-4 gap-y-1.5 pt-1.5 border-t border-slate-100 dark:border-slate-800/80">
            {sub.plan.metrics.map((m) => (
              <div key={m.id} className="text-[10px] text-slate-400 font-medium">
                {m.metric_type.replace("STORAGE_", "").replace("_AT_REST", "")}:{" "}
                <strong className="text-slate-700 dark:text-slate-300">
                  {m.quota} {m.unit}
                </strong>
              </div>
            ))}
          </div>
        )}
      </div>

      <div className="flex items-center gap-2 shrink-0">
        {sub ? (
          <button
            onClick={handleCancel}
            disabled={cancelling}
            className="flex items-center gap-1.5 bg-rose-50 hover:bg-rose-100 text-rose-600 dark:bg-rose-950/20 dark:hover:bg-rose-950/30 border border-rose-200 dark:border-rose-900/50 font-bold px-3 py-2 rounded-md transition-colors cursor-pointer"
          >
            Huỷ gói cước
          </button>
        ) : (
          <div className="flex items-center gap-2 p-2 bg-amber-50/50 dark:bg-amber-950/10 border border-amber-100/50 dark:border-amber-900/20 rounded-md text-amber-600 dark:text-amber-400">
            <ShieldAlert size={14} />
            <span>Mọi lưu lượng sử dụng sẽ tính theo đơn giá pay-as-you-go</span>
          </div>
        )}
      </div>
    </div>
  );
}
