"use client";

import Link from "next/link";
import { AlertCircle, ArrowLeft } from "lucide-react";

import RouteGuard from "@/components/route-guard";

function NewConsumerNoticeContent() {
  return (
    <div className="flex min-h-[calc(100vh-110px)] w-full flex-col gap-6 px-6 pb-10 text-foreground">
      <header className="flex flex-col gap-3 border-b border-border pb-5 md:flex-row md:items-end md:justify-between">
        <div className="flex items-start gap-3">
          <Link
            href="/mail/consumers"
            className="flex size-10 items-center justify-center rounded-lg border border-border bg-muted/20 text-muted-foreground hover:text-foreground hover:bg-muted/50 transition-all mt-0.5"
          >
            <ArrowLeft className="size-5" />
          </Link>
          <div>
            <h1 className="text-xl font-bold tracking-tight">Create New Kafka Consumer</h1>
            <p className="mt-1 text-sm text-muted-foreground">
              Configure a dedicated message consumer stream for automated email dispatch.
            </p>
          </div>
        </div>
      </header>

      {/* Dependency Block Notice Card */}
      <div className="mx-auto max-w-2xl rounded-2xl border border-amber-500/30 bg-amber-500/5 p-8 text-center text-foreground shadow-md">
        <div className="mx-auto flex size-14 items-center justify-center rounded-2xl bg-amber-500/10 border border-amber-500/20 text-amber-500 mb-5">
          <AlertCircle className="size-8" />
        </div>
        <h2 className="text-lg font-bold">Broker Connection Dependency Required</h2>
        <p className="mt-3 text-sm text-muted-foreground leading-relaxed">
          Creating a production consumer requires an encrypted <code className="text-amber-400">source_config_envelope</code> and verified Broker Connection picker. Raw ciphertext input or manual ID entry is prohibited to ensure security and zero credential exposure.
        </p>

        <div className="mt-6 rounded-xl border border-border/60 bg-card p-4 text-left text-xs space-y-2">
          <div className="font-semibold text-foreground">Next Implementation Milestone:</div>
          <ul className="list-disc list-inside text-muted-foreground space-y-1">
            <li>Broker Connection & Encryption Manager contract completion.</li>
            <li>Sender Profile catalog derivation per Zone.</li>
            <li>Interactive Resource Selector for template versions.</li>
          </ul>
        </div>

        <div className="mt-8 flex justify-center gap-3">
          <Link
            href="/mail/consumers"
            className="flex items-center gap-2 rounded-xl bg-slate-800 hover:bg-slate-700 px-5 py-2.5 text-xs font-semibold text-white transition-all"
          >
            <ArrowLeft className="size-4" />
            <span>Back to Consumers List</span>
          </Link>
        </div>
      </div>
    </div>
  );
}

export default function NewConsumerPage() {
  return (
    <RouteGuard requiredKey="email:consumer" requiredAction="create">
      <NewConsumerNoticeContent />
    </RouteGuard>
  );
}
