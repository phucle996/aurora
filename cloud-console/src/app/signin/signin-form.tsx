"use client";

import React, { useState, useCallback, useEffect } from "react";
import { useRouter } from "next/navigation";
import { toast } from "sonner";

// [COMMENT]: Import i18n hooks phục vụ đa ngôn ngữ toàn hệ thống
import { useTranslation } from "@/lib/i18n";

// [COMMENT]: Import shadcn components — enterprise-grade UI primitives
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Checkbox } from "@/components/ui/checkbox";
import { Separator } from "@/components/ui/separator";

// [COMMENT]: Import các API và security modules
import { authAPI, type LoginRequest } from "@/lib/api/auth";
import { type ZoneCatalogItem } from "@/lib/api/zone";
import { ensureDevicePublicKey, DeviceKeyUnsupportedError } from "@/lib/security/deviceKey";
import { useUserSession } from "@/hooks/useUserSession";

// [COMMENT]: Icon cho các SSO providers
function MicrosoftIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
      <rect x="1" y="1" width="6.5" height="6.5" fill="#F25022" />
      <rect x="8.5" y="1" width="6.5" height="6.5" fill="#7FBA00" />
      <rect x="1" y="8.5" width="6.5" height="6.5" fill="#00A4EF" />
      <rect x="8.5" y="8.5" width="6.5" height="6.5" fill="#FFB900" />
    </svg>
  );
}

function GoogleIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
      <path d="M15.68 8.18c0-.57-.05-1.12-.15-1.64H8v3.1h4.31a3.68 3.68 0 01-1.6 2.42v2h2.59c1.51-1.39 2.38-3.44 2.38-5.88z" fill="#4285F4" />
      <path d="M8 16c2.16 0 3.97-.72 5.3-1.94l-2.59-2a4.77 4.77 0 01-7.13-2.51H.96v2.06A8 8 0 008 16z" fill="#34A853" />
      <path d="M3.58 9.55a4.8 4.8 0 010-3.1V4.39H.96a8 8 0 000 7.22l2.62-2.06z" fill="#FBBC05" />
      <path d="M8 3.16a4.34 4.34 0 013.07 1.2l2.3-2.3A7.72 7.72 0 008 0 8 8 0 00.96 4.39l2.62 2.06A4.77 4.77 0 018 3.16z" fill="#EA4335" />
    </svg>
  );
}

function GitHubIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 16 16" fill="currentColor">
      <path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.013 8.013 0 0016 8c0-4.42-3.58-8-8-8z" />
    </svg>
  );
}

// [COMMENT]: Props interface cho SignInForm — nhận zones và callback chuyển mode từ page cha
export interface SignInFormProps {
  zones: ZoneCatalogItem[];
  selectedZoneCode: string;
  onZoneChange: (code: string) => void;
  onSwitchToSignUp: () => void;
}

export default function SignInForm({
  zones,
  selectedZoneCode,
  onZoneChange,
  onSwitchToSignUp,
}: SignInFormProps) {
  const router = useRouter();
  const { t } = useTranslation();

  // [COMMENT]: State nội bộ cho form đăng nhập — tách biệt khỏi signup form
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [trustDevice, setTrustDevice] = useState(false);
  const [isLoading, setIsLoading] = useState(false);

  // [COMMENT]: Parse username@tenant_domain real-time:
  // nếu username chứa '@', tách thành rawUsername và tenantDomain để hiển thị badge và gửi lên API.
  const atIndex = username.lastIndexOf("@");
  const rawUsername = atIndex > 0 ? username.slice(0, atIndex) : username;
  const tenantDomain = atIndex > 0 ? username.slice(atIndex + 1) : "";

  const { setAuthenticatedSession } = useUserSession();

  // [COMMENT]: Logic xử lý Đăng nhập — validate → generate device key → call API → redirect
  const handleSignIn = useCallback(async (e: React.FormEvent) => {
    e.preventDefault();

    if (!username.trim()) {
      toast.error(t.auth.usernameReq);
      return;
    }
    if (!password.trim()) {
      toast.error(t.auth.passwordReq);
      return;
    }
    if (password.length < 8) {
      toast.error(t.auth.passwordLen);
      return;
    }
    if (!selectedZoneCode) {
      toast.error(t.auth.zoneReq);
      return;
    }

    setIsLoading(true);

    try {
      // [COMMENT]: Sinh Ed25519 device public key cho Trust Device flow
      let devicePublicKey = "";
      try {
        devicePublicKey = await ensureDevicePublicKey();
      } catch (err) {
        if (err instanceof DeviceKeyUnsupportedError) {
          console.warn("[SignIn] Ed25519 not supported, proceeding without device key");
        } else {
          throw err;
        }
      }

      const payload: LoginRequest = {
        // [COMMENT]: Gửi raw username (không có @domain) và tenant_domain riêng biệt.
        // Nếu không có @domain, tenantDomain là chuỗi rỗng — CP sẽ xử lý global login.
        username: rawUsername,
        password,
        device_public_key: devicePublicKey,
        trust_device: trustDevice,
        zone_code: selectedZoneCode,
        tenant_domain: tenantDomain || undefined,
      };

      await authAPI.login(payload);
      // [COMMENT]: Thiết lập trạng thái đăng nhập nhanh không cần gọi lại API check session
      setAuthenticatedSession({ authenticated: true });
      router.push("/");
    } catch (err: unknown) {
      const apiError = err as { status?: number; message?: string };
      const msg = apiError?.message || "An unexpected error occurred. Please try again.";
      toast.error(msg);
    } finally {
      setIsLoading(false);
    }
  }, [username, password, trustDevice, selectedZoneCode, t, router, setAuthenticatedSession]);

  return (
    <div className="space-y-5">
      {/* [COMMENT]: Header đăng nhập */}
      <div className="space-y-1">
        <h2 className="text-base font-semibold text-foreground">{t.auth.signIn}</h2>
        <p className="text-sm text-muted-foreground">{t.auth.subtitleSignIn}</p>
      </div>

      {/* [COMMENT]: Form đăng nhập chính */}
      <form onSubmit={handleSignIn} className="space-y-4" id="signin-form" noValidate>
        <div className="space-y-2">
          <Label htmlFor="signin-username" className="text-sm font-normal text-foreground">
            {t.auth.username}
          </Label>
          <Input
            id="signin-username"
            type="text"
            placeholder={`${t.auth.placeholderUsername} or user@tenant.com`}
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            autoComplete="username"
            disabled={isLoading}
            className="h-11 rounded-[8px] border-slate-300 dark:border-slate-800"
          />
          {/* [COMMENT]: Badge hiển thị tenant context khi user gõ username@tenant_domain */}
          {tenantDomain && (
            <div className="flex items-center gap-1.5 mt-1 animate-in fade-in duration-200">
              <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-[11px] font-medium bg-blue-50 text-blue-700 border border-blue-200 dark:bg-blue-950 dark:text-blue-300 dark:border-blue-800">
                <svg width="10" height="10" viewBox="0 0 10 10" fill="currentColor">
                  <path d="M5 0a5 5 0 100 10A5 5 0 005 0zm0 1.5a1.5 1.5 0 110 3 1.5 1.5 0 010-3zM5 9a4 4 0 01-2.9-1.24C2.35 6.9 3.6 6.25 5 6.25s2.65.65 2.9 1.51A4 4 0 015 9z"/>
                </svg>
                Tenant: <strong>{tenantDomain}</strong>
              </span>
              <span className="text-[11px] text-muted-foreground">Đăng nhập vào tenant context</span>
            </div>
          )}
        </div>

        <div className="space-y-2">
          <div className="flex items-center justify-between">
            <Label htmlFor="signin-password" className="text-sm font-normal text-foreground">
              {t.auth.password}
            </Label>
            <a href="#" className="text-xs text-[#2563EB] hover:text-[#1D4ED8] transition-colors">
              {t.auth.forgotPassword}
            </a>
          </div>
          <Input
            id="signin-password"
            type="password"
            placeholder="••••••••"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoComplete="current-password"
            disabled={isLoading}
            className="h-11 rounded-[8px] border-slate-300 dark:border-slate-800"
          />
        </div>

        <div className="flex items-center gap-2">
          <Checkbox
            id="remember"
            checked={trustDevice}
            onCheckedChange={(checked) => setTrustDevice(checked === true)}
            disabled={isLoading}
          />
          <Label htmlFor="remember" className="text-sm font-normal text-muted-foreground cursor-pointer select-none">
            {t.auth.trustDevice}
          </Label>
        </div>

        <Button
          type="submit"
          className="w-full h-11 bg-[#2563EB] hover:bg-[#1D4ED8] text-white rounded-[8px]"
          disabled={isLoading}
          id="signin-button"
        >
          {isLoading ? (
            <span className="flex items-center gap-2">
              <span className="h-3.5 w-3.5 animate-spin rounded-full border-2 border-white/30 border-t-white" />
              {t.auth.btnSigningIn}
            </span>
          ) : (
            t.auth.btnSignIn
          )}
        </Button>
      </form>

      {/* [COMMENT]: Divider SSO */}
      <div className="relative">
        <Separator />
        <span className="absolute left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2 bg-white px-3 text-xs text-muted-foreground dark:bg-slate-900 dark:text-slate-400">
          {t.auth.orSSO}
        </span>
      </div>

      {/* [COMMENT]: SSO Provider buttons */}
      <div className="grid grid-cols-3 gap-2">
        <Button variant="outline" className="h-9 text-xs font-normal gap-1.5 rounded-[8px]" disabled={isLoading} id="sso-microsoft">
          <MicrosoftIcon /> Microsoft
        </Button>
        <Button variant="outline" className="h-9 text-xs font-normal gap-1.5 rounded-[8px]" disabled={isLoading} id="sso-google">
          <GoogleIcon /> Google
        </Button>
        <Button variant="outline" className="h-9 text-xs font-normal gap-1.5 rounded-[8px]" disabled={isLoading} id="sso-github">
          <GitHubIcon /> GitHub
        </Button>
      </div>

      {/* [COMMENT]: Link chuyển sang đăng ký */}
      <div className="text-center text-xs text-muted-foreground pt-1 select-none">
        {t.auth.noAccount}{" "}
        <button
          onClick={onSwitchToSignUp}
          className="font-medium text-[#2563EB] hover:text-[#1D4ED8] transition-colors cursor-pointer"
        >
          {t.auth.signUp}
        </button>
      </div>
    </div>
  );
}
