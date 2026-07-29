"use client";

import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { Loader2, Save } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { updateMyProfile, type UpdateProfileInput } from "@/features/settings/api";
import { useUserSession } from "@/session/use-session";

export function PersonalizationScreen() {
  const { profile, refreshSession } = useUserSession();
  const [form, setForm] = useState<UpdateProfileInput>({
    fullname: profile?.fullname ?? "",
    phone: profile?.phone ?? "",
    address: profile?.address ?? "",
    avatar_url: profile?.avatar_url ?? "",
    bio: profile?.bio ?? "",
    locale: profile?.locale ?? "en-US",
    timezone: profile?.timezone ?? "UTC",
  });

  const mutation = useMutation({
    mutationFn: updateMyProfile,
    onSuccess: async () => {
      await refreshSession();
      toast.success("Profile updated");
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : "Profile could not be updated");
    },
  });

  return (
    <form
      className="space-y-5"
      onSubmit={(event) => {
        event.preventDefault();
        mutation.mutate(form);
      }}
    >
      <section className="overflow-hidden rounded-[8px] border border-border bg-card">
        <div className="border-b border-border px-4 py-4 sm:px-5">
          <h2 className="text-sm font-semibold">Account identity</h2>
          <p className="mt-1 text-xs text-muted-foreground">
            Username and account email are permanent identifiers. Password recovery remains the only password-change flow.
          </p>
        </div>
        <div className="grid gap-4 p-4 sm:grid-cols-2 sm:p-5">
          <div className="space-y-2">
            <Label htmlFor="settings-username">Username</Label>
            <Input id="settings-username" value={profile?.username ?? ""} disabled />
          </div>
          <div className="space-y-2">
            <Label htmlFor="settings-account-email">Account email</Label>
            <Input id="settings-account-email" value={profile?.account_email ?? ""} disabled />
          </div>
        </div>
      </section>

      <section className="overflow-hidden rounded-[8px] border border-border bg-card">
        <div className="border-b border-border px-4 py-4 sm:px-5">
          <h2 className="text-sm font-semibold">Personalization</h2>
          <p className="mt-1 text-xs text-muted-foreground">Update the profile fields shown across Aurora Console.</p>
        </div>
        <div className="grid gap-4 p-4 sm:grid-cols-2 sm:p-5">
          <div className="space-y-2">
            <Label htmlFor="settings-fullname">Full name</Label>
            <Input
              id="settings-fullname"
              required
              maxLength={120}
              value={form.fullname}
              onChange={(event) => setForm((current) => ({ ...current, fullname: event.target.value }))}
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="settings-phone">Phone</Label>
            <Input
              id="settings-phone"
              type="tel"
              inputMode="tel"
              placeholder="+84901234567"
              maxLength={16}
              pattern="^\+[1-9][0-9]{6,14}$"
              value={form.phone}
              onChange={(event) => setForm((current) => ({ ...current, phone: event.target.value }))}
            />
          </div>
          <div className="space-y-2 sm:col-span-2">
            <Label htmlFor="settings-address">Address</Label>
            <Input
              id="settings-address"
              maxLength={500}
              value={form.address}
              onChange={(event) => setForm((current) => ({ ...current, address: event.target.value }))}
            />
          </div>
          <div className="space-y-2 sm:col-span-2">
            <Label htmlFor="settings-avatar">Avatar URL</Label>
            <Input
              id="settings-avatar"
              type="url"
              inputMode="url"
              placeholder="https://cdn.example.com/avatar.png"
              maxLength={2048}
              value={form.avatar_url}
              onChange={(event) => setForm((current) => ({ ...current, avatar_url: event.target.value }))}
            />
            <p className="text-[11px] text-muted-foreground">HTTPS URL without query parameters or fragments.</p>
          </div>
          <div className="space-y-2 sm:col-span-2">
            <Label htmlFor="settings-bio">Bio</Label>
            <Textarea
              id="settings-bio"
              rows={3}
              maxLength={500}
              value={form.bio}
              onChange={(event) => setForm((current) => ({ ...current, bio: event.target.value }))}
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="settings-locale">Language</Label>
            <select
              id="settings-locale"
              className="h-9 w-full rounded-lg border border-input bg-background px-2.5 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50"
              value={form.locale}
              onChange={(event) => setForm((current) => ({ ...current, locale: event.target.value }))}
            >
              <option value="en-US">English (United States)</option>
              <option value="vi-VN">Tiếng Việt</option>
            </select>
          </div>
          <div className="space-y-2">
            <Label htmlFor="settings-timezone">Timezone</Label>
            <select
              id="settings-timezone"
              className="h-9 w-full rounded-lg border border-input bg-background px-2.5 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50"
              value={form.timezone}
              onChange={(event) => setForm((current) => ({ ...current, timezone: event.target.value }))}
            >
              {!["UTC", "Asia/Ho_Chi_Minh", "Asia/Singapore", "Asia/Tokyo", "Europe/London", "America/New_York"].includes(form.timezone) && (
                <option value={form.timezone}>{form.timezone}</option>
              )}
              <option value="UTC">UTC</option>
              <option value="Asia/Ho_Chi_Minh">Asia/Ho Chi Minh</option>
              <option value="Asia/Singapore">Asia/Singapore</option>
              <option value="Asia/Tokyo">Asia/Tokyo</option>
              <option value="Europe/London">Europe/London</option>
              <option value="America/New_York">America/New York</option>
            </select>
          </div>
        </div>
        <div className="flex justify-end border-t border-border bg-muted/20 px-4 py-3 sm:px-5">
          <Button type="submit" disabled={mutation.isPending}>
            {mutation.isPending ? <Loader2 className="animate-spin" /> : <Save />}
            Save changes
          </Button>
        </div>
      </section>
    </form>
  );
}
