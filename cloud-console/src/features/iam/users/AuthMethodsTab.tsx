"use client";

import React from "react";
import { Link2, Loader2 } from "lucide-react";
import { useQuery } from "@tanstack/react-query";

import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";
import {
  getUserAuthMethods,
  type ExternalIdentitySummary,
} from "@/features/iam/users-api";
import { useConsoleQueryScope } from "@/shared/query/scope";

import type { ExtendedUser } from "./UserTable";

interface AuthMethodsTabProps {
  selectedUser: ExtendedUser;
}

function formatTimestamp(value?: string | null) {
  if (!value) return "Never";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "Unknown" : date.toLocaleString();
}

export function AuthMethodsTab({ selectedUser }: AuthMethodsTabProps) {
  const scope = useConsoleQueryScope();
  const { data, isLoading, error } = useQuery({
    queryKey: [...scope, "iam", "user-auth-methods", selectedUser.id],
    queryFn: ({ signal }) => getUserAuthMethods(selectedUser.id, signal),
    enabled: !!selectedUser.id,
    retry: false,
  });

  const renderExternalIdentity = (summary: ExternalIdentitySummary) => {
    const linked = summary.state === "linked";
    return (
      <div className="rounded-md border border-border/40 px-3 py-2.5">
        <div className="flex items-center justify-between gap-3">
          <div className="flex min-w-0 items-center gap-2">
            <Link2 className={cn(
              "h-3.5 w-3.5 shrink-0",
              linked ? "text-emerald-600" : "text-muted-foreground"
            )} />
            <div className="min-w-0">
              <div className="font-semibold capitalize text-foreground">{summary.provider}</div>
              <div className="truncate text-[10px] text-muted-foreground">
                {summary.provider_email || "No linked provider email"}
              </div>
            </div>
          </div>
          <Badge
            variant="outline"
            className={cn(
              "shrink-0 text-[9px] uppercase tracking-wider",
              linked
                ? "border-emerald-500/20 bg-emerald-500/10 text-emerald-600"
                : "border-border/60 text-muted-foreground"
            )}
          >
            {summary.state.replace("_", " ")}
          </Badge>
        </div>
        {summary.state !== "not_linked" && (
          <div className="mt-2 grid grid-cols-2 gap-2 border-t border-border/30 pt-2 text-[10px]">
            <div>
              <div className="text-muted-foreground">Linked</div>
              <div className="font-medium text-foreground">{formatTimestamp(summary.linked_at)}</div>
            </div>
            <div>
              <div className="text-muted-foreground">Last login</div>
              <div className="font-medium text-foreground">{formatTimestamp(summary.last_login_at)}</div>
            </div>
          </div>
        )}
      </div>
    );
  };

  if (isLoading) {
    return (
      <div className="flex items-center justify-center gap-2 py-12 text-[11px] text-muted-foreground">
        <Loader2 className="h-4 w-4 animate-spin" />
        Loading authentication methods…
      </div>
    );
  }

  if (error || !data) {
    return (
      <div className="rounded-md border border-amber-500/20 bg-amber-500/5 px-3 py-3 text-[11px] text-amber-700 dark:text-amber-300">
        Authentication method details are unavailable for this user.
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-4 text-xs select-none">
      <div>
        <div className="font-semibold text-foreground">Account authentication</div>
        <p className="mt-1 text-[10px] leading-normal text-muted-foreground">
          The Aurora account email is the account identifier. Provider emails are separate linked identity metadata.
        </p>
      </div>

      <div className="flex justify-between items-center border-b border-border/30 py-2 gap-4">
        <span className="text-[11px] font-semibold text-muted-foreground">Account email</span>
        <span className="truncate text-right font-semibold text-foreground">{data.account_identifier_email}</span>
      </div>
      <div className="flex justify-between items-center border-b border-border/30 py-2 gap-4">
        <span className="text-[11px] font-semibold text-muted-foreground">Password</span>
        <Badge
          variant="outline"
          className={data.password_set
            ? "border-emerald-500/20 bg-emerald-500/10 text-emerald-600"
            : "border-red-500/20 bg-red-500/10 text-red-600"}
        >
          {data.password_set ? "Set" : "Missing"}
        </Badge>
      </div>

      <div className="flex flex-col gap-2">
        {renderExternalIdentity(data.google)}
        {renderExternalIdentity(data.github)}
      </div>
    </div>
  );
}
