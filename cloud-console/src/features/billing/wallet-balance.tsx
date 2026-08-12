"use client";

import { useMemo } from "react";
import { WalletCards } from "lucide-react";
import { useQuery } from "@tanstack/react-query";

import { isAPIError } from "@/shared/api/http";
import { useConsoleQueryScope } from "@/shared/query/scope";
import { useUserSession } from "@/session/use-session";
import { billingStartURL } from "@/shell/navigation";
import { getPersonalWalletSummary } from "@/features/billing/api";
import { formatWalletBalance } from "@/features/billing/money";

function walletQueryKey(scope: ReturnType<typeof useConsoleQueryScope>) {
  // Personal wallet is independent of zone/workspace; auth generation still fences logout/user switches.
  return [scope[0], scope[1], "wallet", "personal", "USD"] as const;
}

export function WalletBalance() {
  const { status } = useUserSession();
  const scope = useConsoleQueryScope();
  const queryKey = useMemo(() => walletQueryKey(scope), [scope]);
  const costConsoleURL = billingStartURL();
  const walletQuery = useQuery({
    queryKey,
    queryFn: ({ signal }) => getPersonalWalletSummary(signal),
    enabled: status === "authenticated",
    staleTime: 60_000,
    retry: (failureCount, error) => isAPIError(error) && error.retryable && failureCount < 1,
  });

  const amount = walletQuery.data
    ? formatWalletBalance(walletQuery.data.cash_balance_micro_units, walletQuery.data.promotional_balance_micro_units, walletQuery.data.currency)
    : null;
  const notProvisioned = isAPIError(walletQuery.error) && (walletQuery.error.status === 403 || walletQuery.error.status === 404);
  if (status !== "authenticated" || notProvisioned) return null;

  const unavailable = walletQuery.isError && !walletQuery.data;
  const stale = walletQuery.isError && Boolean(walletQuery.data);
  const label = unavailable ? "Wallet unavailable" : stale ? "Last known wallet balance" : "Wallet balance";

  const content = (
    <span
      className="flex items-center gap-1.5 rounded-[6px] px-2 py-1.5 text-xs font-semibold text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
      title={label}
      aria-label={amount ? `${label}: ${amount}` : label}
    >
      <WalletCards className="h-4 w-4 shrink-0" aria-hidden="true" />
      {walletQuery.isPending ? (
        <span className="h-3.5 w-14 animate-pulse rounded bg-muted-foreground/20" aria-hidden="true" />
      ) : (
        <span className={stale ? "text-amber-600 dark:text-amber-400" : "hidden sm:inline"}>
          {amount ?? "—"}
        </span>
      )}
    </span>
  );

  if (!costConsoleURL) return content;
  return (
    <a
      href={costConsoleURL}
      target="_blank"
      rel="noopener noreferrer"
      aria-label={`${label}. Open Cost Management`}
    >
      {content}
    </a>
  );
}
