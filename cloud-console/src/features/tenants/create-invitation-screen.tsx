"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { ArrowLeft, Copy, Send } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { NativeSelect } from "@/components/ui/native-select";
import {
	createTenantInvitation,
	listTenantRoles,
	type TenantInvitation,
	type TenantRole,
} from "@/features/tenants/api";
import { useUserSession } from "@/session/use-session";

export function CreateTenantInvitationScreen() {
	const router = useRouter();
	const { renderContext } = useUserSession();
	const [roles, setRoles] = useState<TenantRole[]>([]);
	const [identifier, setIdentifier] = useState("");
	const [roleID, setRoleID] = useState("");
	const [result, setResult] = useState<TenantInvitation | null>(null);
	const [loading, setLoading] = useState(true);
	const [submitting, setSubmitting] = useState(false);

	useEffect(() => {
		const controller = new AbortController();
		void listTenantRoles(controller.signal)
			.then((items) => {
				setRoles(items);
				setRoleID(items[0]?.id ?? "");
			})
			.catch((error) => toast.error(error instanceof Error ? error.message : "Cannot load tenant roles."))
			.finally(() => setLoading(false));
		return () => controller.abort();
	}, []);

	if (renderContext?.kind !== "tenant") {
		return <Card><CardHeader><CardTitle>Tenant context required</CardTitle><CardDescription>Switch to a tenant before inviting a member.</CardDescription></CardHeader></Card>;
	}

	return (
		<div className="mx-auto max-w-2xl space-y-5">
		<Button variant="ghost" size="sm" onClick={() => router.push("/tenant")}><ArrowLeft /> Tenant console</Button>
		<Card>
			<CardHeader>
				<CardTitle>Invite a tenant member</CardTitle>
				<CardDescription>The account must already exist. The link expires in six hours and is shown only once.</CardDescription>
			</CardHeader>
			<CardContent>
				<form className="space-y-4" onSubmit={async (event) => {
					event.preventDefault();
					if (!identifier.trim() || !roleID) return;
					setSubmitting(true);
					try {
						setResult(await createTenantInvitation(identifier.trim().toLowerCase(), roleID));
						toast.success("Invitation created.");
					} catch (error) {
						toast.error(error instanceof Error ? error.message : "Cannot create invitation.");
					} finally {
						setSubmitting(false);
					}
				}}>
					<div className="space-y-2">
						<Label htmlFor="tenant-invite-identifier">Username or account email</Label>
						<Input id="tenant-invite-identifier" value={identifier} onChange={(event) => setIdentifier(event.target.value)} maxLength={320} disabled={submitting} />
					</div>
					<div className="space-y-2">
						<Label htmlFor="tenant-invite-role">Tenant role</Label>
						<NativeSelect id="tenant-invite-role" value={roleID} onChange={(event) => setRoleID(event.target.value)} disabled={loading || submitting}>
							{roles.map((role) => <option key={role.id} value={role.id}>{role.name} (level {role.role_level})</option>)}
						</NativeSelect>
					</div>
					<Button type="submit" disabled={loading || submitting || !identifier.trim() || !roleID}><Send />{submitting ? "Creating…" : "Create invitation"}</Button>
				</form>
				{result && <div className="mt-5 space-y-2 rounded-md border p-4">
					<p className="text-sm font-medium">One-time join link</p>
					<p className="break-all font-mono text-xs text-muted-foreground">{`${window.location.origin}${result.join_link}`}</p>
					<Button variant="outline" size="sm" onClick={async () => {
						await navigator.clipboard.writeText(`${window.location.origin}${result.join_link}`);
						toast.success("Invitation link copied.");
					}}><Copy /> Copy link</Button>
				</div>}
			</CardContent>
		</Card>
		</div>
	);
}
