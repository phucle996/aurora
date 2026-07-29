"use client";

import { useEffect, useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link2, Loader2, ShieldCheck, Unlink } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import {
  getMySocialLinks,
  startSocialLink,
  unlinkSocialLink,
  type SocialProvider,
} from "@/features/settings/api";
import { useConsoleQueryScope } from "@/shared/query/scope";
import { useUserSession } from "@/session/use-session";

type SocialLinksScreenProps = {
  callbackOutcome?: "linked" | "failed";
};

const providers: Array<{
  id: SocialProvider;
  name: string;
  description: string;
}> = [
  { id: "google", name: "Google", description: "Use your verified Google identity as another way to sign in." },
  { id: "github", name: "GitHub", description: "Use your verified GitHub identity as another way to sign in." },
];

export function SocialLinksScreen({ callbackOutcome }: SocialLinksScreenProps) {
  const router = useRouter();
  const scope = useConsoleQueryScope();
  const queryClient = useQueryClient();
  const { profile } = useUserSession();
  const queryKey = useMemo(() => [...scope, "settings", "social-links"] as const, [scope]);
  const [confirmProvider, setConfirmProvider] = useState<SocialProvider | null>(null);

  const linksQuery = useQuery({
    queryKey,
    queryFn: ({ signal }) => getMySocialLinks(signal),
    staleTime: 10_000,
  });

  useEffect(() => {
    if (!callbackOutcome) return;
    if (callbackOutcome === "linked") {
      toast.success("Social account linked");
      void queryClient.invalidateQueries({ queryKey });
    } else {
      toast.error("The social account could not be linked");
    }
    router.replace("/settings/social-links", { scroll: false });
  }, [callbackOutcome, queryClient, queryKey, router]);

  const startMutation = useMutation({
    mutationFn: startSocialLink,
    onSuccess: (authorizationURL) => window.location.assign(authorizationURL),
    onError: (error) => toast.error(error instanceof Error ? error.message : "Social linking could not start"),
  });

  const unlinkMutation = useMutation({
    mutationFn: unlinkSocialLink,
    onSuccess: async () => {
      setConfirmProvider(null);
      await queryClient.invalidateQueries({ queryKey });
      toast.success("Social account unlinked");
    },
    onError: (error) => toast.error(error instanceof Error ? error.message : "Social account could not be unlinked"),
  });

  return (
    <div className="space-y-5">
      <section className="rounded-[8px] border border-border bg-card p-4 sm:p-5">
        <div className="flex items-start gap-3">
          <div className="rounded-[6px] bg-blue-500/10 p-2 text-blue-600">
            <ShieldCheck className="h-5 w-5" />
          </div>
          <div>
            <h2 className="text-sm font-semibold">Linked sign-in identities</h2>
            <p className="mt-1 max-w-2xl text-xs leading-relaxed text-muted-foreground">
              Google and GitHub are alternate sign-in methods for your existing Aurora account. Your account username,
              identifier email and password do not change.
            </p>
            <p className="mt-2 text-[11px] font-medium text-muted-foreground">
              Account identifier: {profile?.account_email ?? "Unavailable"}
            </p>
          </div>
        </div>
      </section>

      {linksQuery.isLoading ? (
        <div className="rounded-[8px] border border-border p-12 text-center text-xs text-muted-foreground">Loading social links…</div>
      ) : linksQuery.isError || !linksQuery.data ? (
        <div className="rounded-[8px] border border-border p-10 text-center">
          <p className="text-sm font-semibold text-red-500">Social links could not be loaded</p>
          <Button className="mt-4" variant="outline" size="sm" onClick={() => void linksQuery.refetch()}>
            Try again
          </Button>
        </div>
      ) : (
        <div className="overflow-hidden rounded-[8px] border border-border bg-card">
          {providers.map((provider, index) => {
            const link = linksQuery.data.find((item) => item.provider === provider.id);
            const linked = link?.state === "linked";
            const busy = startMutation.isPending || unlinkMutation.isPending;
            return (
              <div key={provider.id} className={index > 0 ? "border-t border-border" : undefined}>
                <div className="flex flex-col gap-4 p-4 sm:flex-row sm:items-center sm:p-5">
                  <div className="flex min-w-0 flex-1 items-start gap-3">
                    <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-[6px] border border-border bg-muted/30">
                      {provider.id === "github" ? <span className="text-xs font-bold">GH</span> : <span className="text-base font-bold text-blue-600">G</span>}
                    </div>
                    <div className="min-w-0">
                      <div className="flex flex-wrap items-center gap-2">
                        <h3 className="text-sm font-semibold">{provider.name}</h3>
                        <span className={linked ? "rounded-full bg-emerald-500/10 px-2 py-0.5 text-[10px] font-bold text-emerald-600" : "rounded-full bg-muted px-2 py-0.5 text-[10px] font-bold text-muted-foreground"}>
                          {linked ? "Linked" : "Not linked"}
                        </span>
                      </div>
                      <p className="mt-1 text-xs text-muted-foreground">{provider.description}</p>
                      {linked && (
                        <div className="mt-2 text-[11px] text-muted-foreground">
                          <p className="truncate">Provider email: {link.provider_email || "Not shared"}</p>
                          {link.linked_at && <p>Linked {new Date(link.linked_at).toLocaleString()}</p>}
                        </div>
                      )}
                    </div>
                  </div>
                  {linked ? (
                    <Button
                      variant="outline"
                      disabled={busy}
                      onClick={() => setConfirmProvider(provider.id)}
                    >
                      <Unlink />
                      Unlink
                    </Button>
                  ) : (
                    <Button
                      disabled={busy}
                      onClick={() => startMutation.mutate(provider.id)}
                    >
                      {startMutation.isPending && startMutation.variables === provider.id ? <Loader2 className="animate-spin" /> : <Link2 />}
                      Link {provider.name}
                    </Button>
                  )}
                </div>
                {confirmProvider === provider.id && (
                  <div className="flex flex-col gap-3 border-t border-border bg-amber-500/5 px-4 py-3 sm:flex-row sm:items-center sm:justify-between sm:px-5">
                    <p className="text-xs font-medium">
                      Unlink {provider.name}? You can still sign in with your username and password.
                    </p>
                    <div className="flex gap-2">
                      <Button size="sm" variant="ghost" onClick={() => setConfirmProvider(null)}>Cancel</Button>
                      <Button
                        size="sm"
                        variant="destructive"
                        disabled={unlinkMutation.isPending}
                        onClick={() => unlinkMutation.mutate(provider.id)}
                      >
                        {unlinkMutation.isPending ? <Loader2 className="animate-spin" /> : <Unlink />}
                        Confirm unlink
                      </Button>
                    </div>
                  </div>
                )}
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}
