"use client";

import { useEffect, useState } from "react";
import Link from "next/link";

import { Button } from "@/components/ui/button";
import { authAPI } from "@/lib/api/auth";

type ActivationSecret = { user_id: string; event_id: string; token: string };

export default function ActivateAccountPage() {
	const [secret, setSecret] = useState<ActivationSecret | null>(null);
	const [state, setState] = useState<"ready" | "submitting" | "success" | "invalid" | "failed">("ready");
	const [message, setMessage] = useState("");

	useEffect(() => {
		// [COMMENT]: Fragment không được gửi tới Envoy/ACR; đọc một lần rồi xóa khỏi address bar/browser history.
		const values = new URLSearchParams(window.location.hash.replace(/^#/, ""));
		const next = {
			user_id: values.get("user_id")?.trim() ?? "",
			event_id: values.get("event_id")?.trim() ?? "",
			token: values.get("token")?.trim() ?? "",
		};
		window.history.replaceState(null, "", window.location.pathname);
		if (!next.user_id || !next.event_id || !next.token) {
			setState("invalid");
			setMessage("This activation link is incomplete or invalid.");
			return;
		}
		setSecret(next);
	}, []);

	async function confirmActivation() {
		if (!secret || state === "submitting") return;
		setState("submitting");
		try {
			await authAPI.verifyAccount(secret);
			setSecret(null);
			setState("success");
			setMessage("Your Aurora account is active. You can now sign in.");
		} catch (error) {
			const apiError = error as { message?: string };
			setSecret(null);
			setState("failed");
			setMessage(apiError.message || "Activation failed. Sign in to request another email.");
		}
	}

	return (
		<main className="flex min-h-screen items-center justify-center bg-background px-4">
			<meta name="referrer" content="no-referrer" />
			<section className="w-full max-w-md space-y-6 rounded-xl border bg-card p-8 text-center shadow-sm">
				<h1 className="text-xl font-semibold">Activate your Aurora account</h1>
				<p className="text-sm text-muted-foreground">
					{message || "Confirm below to activate the account associated with this email link."}
				</p>
				{secret && state !== "invalid" ? (
					<Button className="w-full" disabled={state === "submitting"} onClick={confirmActivation}>
						{state === "submitting" ? "Activating…" : "Confirm activation"}
					</Button>
				) : (
					<Link
						href="/signin"
						className="inline-flex h-9 w-full items-center justify-center rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground"
					>
						Go to sign in
					</Link>
				)}
			</section>
		</main>
	);
}
