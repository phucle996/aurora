"use client";

import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, Clipboard, KeyRound, Loader2, RefreshCw, ShieldCheck, ShieldOff } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  confirmMyMfaSetup,
  getMyMfa,
  regenerateMyRecoveryCodes,
  removeMyMfa,
  startMyMfaSetup,
  type MfaSetup,
} from "@/features/iam/mfa-api";
import { useConsoleQueryScope } from "@/shared/query/scope";

export function MfaSettingsScreen() {
  const scope = useConsoleQueryScope();
  const queryClient = useQueryClient();
  const queryKey = useMemo(() => [...scope, "settings", "mfa"] as const, [scope]);
  const [setup, setSetup] = useState<MfaSetup | null>(null);
  const [setupCode, setSetupCode] = useState("");
  const [verificationCode, setVerificationCode] = useState("");
  const [recoveryCodes, setRecoveryCodes] = useState<string[] | null>(null);

  const statusQuery = useQuery({
    queryKey,
    queryFn: ({ signal }) => getMyMfa(signal),
    staleTime: 15_000,
  });

  const startMutation = useMutation({
    mutationFn: startMyMfaSetup,
    onSuccess: (result) => {
      setSetup(result);
      setSetupCode("");
      setRecoveryCodes(null);
    },
    onError: (error) => toast.error(error instanceof Error ? error.message : "MFA setup could not start"),
  });

  const confirmMutation = useMutation({
    mutationFn: () => confirmMyMfaSetup(setup!.setup_id, setupCode),
    onSuccess: async (result) => {
      setRecoveryCodes(result.recovery_codes);
      setSetup(null);
      setSetupCode("");
      await queryClient.invalidateQueries({ queryKey });
      toast.success("MFA enabled");
    },
    onError: (error) => toast.error(error instanceof Error ? error.message : "The verification code was rejected"),
  });

  const regenerateMutation = useMutation({
    mutationFn: () => regenerateMyRecoveryCodes(verificationCode),
    onSuccess: async (codes) => {
      setRecoveryCodes(codes);
      setVerificationCode("");
      await queryClient.invalidateQueries({ queryKey });
      toast.success("Recovery codes regenerated");
    },
    onError: (error) => toast.error(error instanceof Error ? error.message : "Recovery codes could not be regenerated"),
  });

  const removeMutation = useMutation({
    mutationFn: () => removeMyMfa(verificationCode),
    onSuccess: async () => {
      setRecoveryCodes(null);
      setVerificationCode("");
      await queryClient.invalidateQueries({ queryKey });
      toast.success("MFA removed");
    },
    onError: (error) => toast.error(error instanceof Error ? error.message : "MFA could not be removed"),
  });

  if (statusQuery.isLoading) {
    return <div className="rounded-[8px] border border-border p-12 text-center text-xs text-muted-foreground">Loading MFA settings…</div>;
  }
  if (statusQuery.isError || !statusQuery.data) {
    return (
      <div className="rounded-[8px] border border-border p-10 text-center">
        <p className="text-sm font-semibold text-red-500">MFA settings could not be loaded</p>
        <Button className="mt-4" size="sm" variant="outline" onClick={() => void statusQuery.refetch()}>
          Try again
        </Button>
      </div>
    );
  }

  const enabled = statusQuery.data.status === "enabled";
  const codeReady = /^\d{6}$/.test(verificationCode);

  return (
    <div className="space-y-5">
      <section className="overflow-hidden rounded-[8px] border border-border bg-card">
        <div className="flex flex-col gap-3 border-b border-border p-4 sm:flex-row sm:items-center sm:justify-between sm:p-5">
          <div className="flex items-start gap-3">
            <div className={enabled ? "rounded-[6px] bg-emerald-500/10 p-2 text-emerald-600" : "rounded-[6px] bg-amber-500/10 p-2 text-amber-600"}>
              {enabled ? <ShieldCheck className="h-5 w-5" /> : <ShieldOff className="h-5 w-5" />}
            </div>
            <div>
              <h2 className="text-sm font-semibold">Authenticator app</h2>
              <p className="mt-1 text-xs text-muted-foreground">
                {enabled ? "A time-based one-time password is required during sign in." : "Protect your account with a six-digit authenticator code."}
              </p>
            </div>
          </div>
          <span className={enabled ? "w-fit rounded-full bg-emerald-500/10 px-2.5 py-1 text-[11px] font-bold text-emerald-600" : "w-fit rounded-full bg-muted px-2.5 py-1 text-[11px] font-bold text-muted-foreground"}>
            {enabled ? "Enabled" : "Not enabled"}
          </span>
        </div>

        {!enabled && !setup && (
          <div className="p-4 sm:p-5">
            <Button onClick={() => startMutation.mutate()} disabled={startMutation.isPending}>
              {startMutation.isPending ? <Loader2 className="animate-spin" /> : <KeyRound />}
              Set up MFA
            </Button>
          </div>
        )}

        {!enabled && setup && (
          <div className="space-y-5 p-4 sm:p-5">
            <div>
              <p className="text-xs font-semibold">1. Add this account to your authenticator app</p>
              <div className="mt-2 overflow-x-auto rounded-[6px] border border-border bg-muted/30 p-3 font-mono text-xs tracking-wider">
                {setup.manual_secret}
              </div>
              <p className="mt-2 text-[11px] text-muted-foreground">
                This setup expires at {new Date(setup.expires_at).toLocaleString()}.
              </p>
            </div>
            <div>
              <label htmlFor="mfa-setup-code" className="text-xs font-semibold">2. Enter the six-digit code</label>
              <div className="mt-2 flex max-w-sm flex-col gap-2 sm:flex-row">
                <Input
                  id="mfa-setup-code"
                  inputMode="numeric"
                  autoComplete="one-time-code"
                  maxLength={6}
                  value={setupCode}
                  onChange={(event) => setSetupCode(event.target.value.replace(/\D/g, "").slice(0, 6))}
                  placeholder="000000"
                  className="font-mono tracking-[0.25em]"
                />
                <Button
                  disabled={!/^\d{6}$/.test(setupCode) || confirmMutation.isPending}
                  onClick={() => confirmMutation.mutate()}
                >
                  {confirmMutation.isPending ? <Loader2 className="animate-spin" /> : <Check />}
                  Confirm
                </Button>
              </div>
            </div>
          </div>
        )}

        {enabled && (
          <div className="grid gap-5 p-4 sm:grid-cols-2 sm:p-5">
            <div>
              <p className="text-xs font-semibold">Enabled at</p>
              <p className="mt-1 text-xs text-muted-foreground">
                {statusQuery.data.enabled_at ? new Date(statusQuery.data.enabled_at).toLocaleString() : "Available"}
              </p>
            </div>
            <div>
              <p className="text-xs font-semibold">Recovery codes remaining</p>
              <p className="mt-1 text-xs text-muted-foreground">{statusQuery.data.recovery_codes_remaining}</p>
            </div>
          </div>
        )}
      </section>

      {recoveryCodes && (
        <section className="rounded-[8px] border border-amber-500/30 bg-amber-500/5 p-4 sm:p-5">
          <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
            <div>
              <h2 className="text-sm font-semibold">Save your recovery codes now</h2>
              <p className="mt-1 text-xs text-muted-foreground">Each code can be used once. They will not be shown again.</p>
            </div>
            <Button
              size="sm"
              variant="outline"
              onClick={async () => {
                await navigator.clipboard.writeText(recoveryCodes.join("\n"));
                toast.success("Recovery codes copied");
              }}
            >
              <Clipboard />
              Copy
            </Button>
          </div>
          <div className="mt-4 grid gap-2 rounded-[6px] border border-border bg-background p-3 font-mono text-xs sm:grid-cols-2">
            {recoveryCodes.map((code) => <span key={code}>{code}</span>)}
          </div>
          <Button className="mt-3" size="sm" variant="ghost" onClick={() => setRecoveryCodes(null)}>
            I have saved these codes
          </Button>
        </section>
      )}

      {enabled && (
        <section className="overflow-hidden rounded-[8px] border border-border bg-card">
          <div className="border-b border-border p-4 sm:p-5">
            <h2 className="text-sm font-semibold">Recovery and removal</h2>
            <p className="mt-1 text-xs text-muted-foreground">
              Enter a current authenticator code to generate a new recovery set or permanently remove MFA.
            </p>
          </div>
          <div className="space-y-3 p-4 sm:p-5">
            <Input
              aria-label="Current authenticator code"
              inputMode="numeric"
              autoComplete="one-time-code"
              maxLength={6}
              value={verificationCode}
              onChange={(event) => setVerificationCode(event.target.value.replace(/\D/g, "").slice(0, 6))}
              placeholder="Current six-digit code"
              className="max-w-xs font-mono tracking-[0.2em]"
            />
            <div className="flex flex-col gap-2 sm:flex-row">
              <Button
                variant="outline"
                disabled={!codeReady || regenerateMutation.isPending || removeMutation.isPending}
                onClick={() => regenerateMutation.mutate()}
              >
                {regenerateMutation.isPending ? <Loader2 className="animate-spin" /> : <RefreshCw />}
                Regenerate recovery codes
              </Button>
              <Button
                variant="destructive"
                disabled={!codeReady || regenerateMutation.isPending || removeMutation.isPending}
                onClick={() => removeMutation.mutate()}
              >
                {removeMutation.isPending ? <Loader2 className="animate-spin" /> : <ShieldOff />}
                Remove MFA
              </Button>
            </div>
          </div>
        </section>
      )}
    </div>
  );
}
