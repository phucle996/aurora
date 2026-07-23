"use client";

import { Suspense, useEffect, useRef, useState } from "react";
import { useSearchParams } from "next/navigation";
import { fetchJSON } from "@/lib/api/fetcher";

function BillingAuthorizeContent() {
  const searchParams = useSearchParams();
  const started = useRef(false);
  const [error, setError] = useState<string | null>(null);
  const state = searchParams.get("state") || "";
  const codeChallenge = searchParams.get("code_challenge") || "";

  useEffect(() => {
    if (started.current) return;
    started.current = true;

    if (!/^[A-Za-z0-9_-]{32,128}$/.test(state) || !/^[A-Za-z0-9_-]{43}$/.test(codeChallenge)) {
      setError("Invalid Cost Console authorization request.");
      return;
    }

    // [COMMENT]: POST same-origin khiến browser tự gửi IAM Trinity; raw cookie không bao giờ rời Cloud origin.
    fetchJSON<{ data: { redirect_url: string } }>("/api/v1/auth/domain-sessions/billing", {
      method: "POST",
      body: { state, code_challenge: codeChallenge },
    })
      .then((response) => window.location.replace(response.data.redirect_url))
      .catch((reason: unknown) => {
        const message =
          typeof reason === "object" && reason !== null && "message" in reason
            ? String(reason.message)
            : "IAM session is required.";
        setError(message);
      });
  }, [codeChallenge, state]);

  return (
    <main className="flex min-h-screen items-center justify-center bg-slate-950 p-6 text-slate-200">
      <div className="w-full max-w-md rounded-lg border border-slate-800 bg-slate-900 p-8 text-center">
        {error ? (
          <>
            <h1 className="text-lg font-semibold text-white">Unable to authorize Cost Console</h1>
            <p className="mt-3 text-sm text-rose-400">{error}</p>
            <a
              className="mt-6 inline-flex rounded bg-blue-600 px-4 py-2 text-sm font-semibold text-white"
              href={`/signin?return_to=${encodeURIComponent(`/billing/authorize?state=${state}&code_challenge=${codeChallenge}`)}`}
            >
              Sign in to Aurora Cloud
            </a>
          </>
        ) : (
          <>
            <div className="mx-auto h-8 w-8 animate-spin rounded-full border-2 border-blue-500/30 border-t-blue-500" />
            <p className="mt-4 text-sm text-slate-400">Authorizing Cost Console…</p>
          </>
        )}
      </div>
    </main>
  );
}

export default function BillingAuthorizePage() {
  return (
    <Suspense>
      <BillingAuthorizeContent />
    </Suspense>
  );
}
