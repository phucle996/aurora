"use client";

import React, { useState, useCallback, useEffect, Suspense } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { toast } from "sonner";
import { Toaster } from "@/components/ui/sonner";

// [COMMENT]: Import i18n hooks phục vụ đa ngôn ngữ toàn hệ thống
import { useTranslation, type Language } from "@/lib/i18n";

// [COMMENT]: Import shadcn components — enterprise-grade UI primitives
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Checkbox } from "@/components/ui/checkbox";
import { Separator } from "@/components/ui/separator";

// [COMMENT]: Import các API và security modules đã port từ codebase cũ
import { authAPI, type LoginRequest } from "@/lib/api/auth";
import { fetchZoneCatalog, type ZoneCatalogItem } from "@/lib/api/zone";
import { ensureDevicePublicKey, DeviceKeyUnsupportedError } from "@/lib/security/deviceKey";

// [COMMENT]: Icon SVG nhỏ gọn cho Logo thương hiệu
function AuroraLogo() {
  return (
    <svg width="32" height="32" viewBox="0 0 32 32" fill="none" xmlns="http://www.w3.org/2000/svg">
      <rect width="32" height="32" rx="8" fill="#2563EB" />
      <path
        d="M16 7L23 23H9L16 7Z"
        fill="white"
        stroke="white"
        strokeWidth="1.5"
        strokeLinejoin="round"
      />
    </svg>
  );
}

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

function CircleIcon({ active }: { active: boolean }) {
  return (
    <span
      className={`inline-block h-2 w-2 rounded-full border-2 transition-all duration-300 ${active
        ? "border-emerald-600 bg-emerald-600 dark:border-emerald-400 dark:bg-emerald-400"
        : "border-slate-300 dark:border-slate-700"
        }`}
    />
  );
}

function SignInContent() {
  const router = useRouter();
  const searchParams = useSearchParams();

  // [COMMENT]: Móc nối dịch thuật đa ngôn ngữ toàn cục
  const { lang, setLang, t } = useTranslation();

  // [COMMENT]: Quản lý trạng thái form hiển thị: signin (đăng nhập) hoặc signup (đăng ký)
  const [mode, setMode] = useState<"signin" | "signup">("signin");

  // [COMMENT]: Quản lý các bước xác thực email của Đăng ký: form -> verify-email -> verified
  const [signupStep, setSignupStep] = useState<"form" | "verify-email" | "verified">("form");

  // [COMMENT]: State dữ liệu chung
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");

  // [COMMENT]: State Đăng nhập
  const [trustDevice, setTrustDevice] = useState(false);
  const [zones, setZones] = useState<ZoneCatalogItem[]>([]);
  const [selectedZoneCode, setSelectedZoneCode] = useState("");

  // [COMMENT]: State Đăng ký
  const [fullname, setFullname] = useState("");
  const [email, setEmail] = useState("");
  const [phone, setPhone] = useState("");

  const [isLoading, setIsLoading] = useState(false);
  const [timeLeft, setTimeLeft] = useState(59);

  // [COMMENT]: State theme sáng/tối
  const [theme, setTheme] = useState("light");

  // [COMMENT]: Đồng bộ hóa tham số query của URL để quyết định mode hiển thị
  useEffect(() => {
    if (searchParams) {
      const modeParam = searchParams.get("mode");
      if (modeParam === "signup") {
        setMode("signup");
      } else {
        setMode("signin");
      }

      if (searchParams.get("verified") === "true") {
        setSignupStep("verified");
      }
    }
  }, [searchParams]);

  // [COMMENT]: Lắng nghe sự kiện phím Back/Forward của trình duyệt để đổi mode
  useEffect(() => {
    const handlePopState = () => {
      if (typeof window !== "undefined") {
        const params = new URLSearchParams(window.location.search);
        const modeParam = params.get("mode");
        if (modeParam === "signup") {
          setMode("signup");
        } else {
          setMode("signin");
        }
      }
    };
    window.addEventListener("popstate", handlePopState);
    return () => window.removeEventListener("popstate", handlePopState);
  }, []);

  // [COMMENT]: Tra cứu danh sách active zones
  useEffect(() => {
    let active = true;
    fetchZoneCatalog()
      .then((data) => {
        if (active && data) {
          setZones(data);
          if (data.length > 0) {
            setSelectedZoneCode(data[0].code);
          }
        }
      })
      .catch((err) => {
        console.error("[Auth] Failed to fetch active zones list", err);
      });
    return () => {
      active = false;
    };
  }, []);

  // [COMMENT]: Đồng bộ và kích hoạt theme từ localstorage
  useEffect(() => {
    const savedTheme = localStorage.getItem("theme") || "light";
    setTheme(savedTheme);
    if (savedTheme === "dark" || (savedTheme === "system" && window.matchMedia("(prefers-color-scheme: dark)").matches)) {
      document.documentElement.classList.add("dark");
    } else {
      document.documentElement.classList.remove("dark");
    }
  }, []);

  // [COMMENT]: Countdown timer kích hoạt gửi lại email
  useEffect(() => {
    if (mode !== "signup" || signupStep !== "verify-email") return;
    if (timeLeft <= 0) return;

    const timer = setInterval(() => {
      setTimeLeft((prev) => prev - 1);
    }, 1000);

    return () => clearInterval(timer);
  }, [mode, signupStep, timeLeft]);

  // [COMMENT]: Logic thay đổi theme
  const handleThemeChange = useCallback((newTheme: string) => {
    setTheme(newTheme);
    localStorage.setItem("theme", newTheme);
    if (newTheme === "dark" || (newTheme === "system" && window.matchMedia("(prefers-color-scheme: dark)").matches)) {
      document.documentElement.classList.add("dark");
    } else {
      document.documentElement.classList.remove("dark");
    }
  }, []);

  // [COMMENT]: Chuyển đổi qua lại giữa Đăng nhập và Đăng ký không tải lại trang
  const switchMode = useCallback((newMode: "signin" | "signup") => {
    if (typeof window !== "undefined") {
      window.history.pushState(
        null,
        "",
        newMode === "signin" ? "/signin?mode=signin" : "/signin?mode=signup"
      );
    }
    setMode(newMode);
    setPassword("");
  }, []);

  // [COMMENT]: Logic xử lý Đăng nhập
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
        username,
        password,
        device_public_key: devicePublicKey,
        trust_device: trustDevice,
        zone_code: selectedZoneCode,
      };

      await authAPI.login(payload);
      router.push("/");
    } catch (err: unknown) {
      const apiError = err as { status?: number; message?: string };
      const msg = apiError?.message || "An unexpected error occurred. Please try again.";
      toast.error(msg);
    } finally {
      setIsLoading(false);
    }
  }, [username, password, trustDevice, selectedZoneCode, t, router]);

  // [COMMENT]: Live password constraints cho đăng ký
  const hasMinLength = password.length >= 8;
  const hasUppercase = /[A-Z]/.test(password);
  const hasNumber = /[0-9]/.test(password);
  const isPasswordValid = hasMinLength && hasUppercase && hasNumber;

  const getDetectedLocation = () => {
    const browserLang = typeof navigator !== "undefined" ? navigator.language : "en-US";
    const countryCode = browserLang.includes("-") ? browserLang.split("-")[1] : browserLang;
    return countryCode.toUpperCase().substring(0, 2);
  };

  // [COMMENT]: Logic xử lý Đăng ký
  const handleSignUp = useCallback(async (e: React.FormEvent) => {
    e.preventDefault();

    if (!username.trim()) {
      toast.error(t.auth.usernameReq);
      return;
    }
    if (!fullname.trim()) {
      toast.error(t.auth.fullnameReq);
      return;
    }
    if (!email.trim()) {
      toast.error(t.auth.emailReq);
      return;
    }
    if (!password.trim()) {
      toast.error(t.auth.passwordReq);
      return;
    }
    if (!isPasswordValid) {
      toast.error(t.auth.passwordLen);
      return;
    }

    setIsLoading(true);

    try {
      const detectedLocation = getDetectedLocation();
      const detectedTimezone = typeof Intl !== "undefined" ? Intl.DateTimeFormat().resolvedOptions().timeZone : "UTC";

      await authAPI.register({
        username,
        fullname,
        email,
        password,
        phone: phone.trim() || undefined,
        location: detectedLocation,
        timezone: detectedTimezone,
      });

      setSignupStep("verify-email");
      setTimeLeft(59);
    } catch (err: unknown) {
      const apiError = err as { status?: number; message?: string };
      const msg = apiError?.message || "Registration failed. Please try again.";
      toast.error(msg);
    } finally {
      setIsLoading(false);
    }
  }, [username, fullname, email, phone, password, isPasswordValid, t]);

  const handleResendEmail = useCallback(async () => {
    if (timeLeft > 0) return;
    setIsLoading(true);
    try {
      const detectedLocation = getDetectedLocation();
      const detectedTimezone = typeof Intl !== "undefined" ? Intl.DateTimeFormat().resolvedOptions().timeZone : "UTC";

      await authAPI.register({
        username,
        fullname,
        email,
        password,
        phone: phone.trim() || undefined,
        location: detectedLocation,
        timezone: detectedTimezone,
      });
      toast.success("Verification email resent!");
      setTimeLeft(59);
    } catch (err: unknown) {
      const apiError = err as { status?: number; message?: string };
      const msg = apiError?.message || "Resend failed. Please try again.";
      toast.error(msg);
    } finally {
      setIsLoading(false);
    }
  }, [username, fullname, email, phone, password, timeLeft]);

  // [COMMENT]: Dynamic key giúp kích hoạt CSS transition mượt mà giữa các form
  const activeKey = mode === "signup" ? `signup-${signupStep}` : "signin";

  return (
    <div className="flex min-h-screen flex-col bg-[#F8FAFC] dark:bg-[#0B0F19]">
      <div className="flex flex-1 items-center justify-center px-4 py-12">
        <div className="w-full max-w-[480px] space-y-6 transition-all duration-300">
          
          {/* LOGO & BRANDING */}
          <div className="space-y-1">
            <div className="flex items-center gap-3">
              <AuroraLogo />
              <div>
                <h1 className="text-lg font-semibold text-foreground tracking-tight">
                  Aurora Cloud
                </h1>
                <p className="text-xs text-muted-foreground">
                  Cloud Control Plane
                </p>
              </div>
            </div>
          </div>

          {/* DYNAMIC TRANSITION CONTAINER */}
          <div
            key={activeKey}
            className="rounded-xl border border-[#E5E7EB] dark:border-slate-800 bg-white dark:bg-slate-900/50 shadow-none transition-all duration-300 transform opacity-100 scale-100"
          >
            <div className="p-8 space-y-6">

              {/* CHẾ ĐỘ 1: ĐĂNG NHẬP */}
              {mode === "signin" && (
                <div className="space-y-5 animate-in fade-in duration-300">
                  <div className="space-y-1">
                    <h2 className="text-base font-semibold text-foreground">{t.auth.signIn}</h2>
                    <p className="text-sm text-muted-foreground">
                      {t.auth.subtitleSignIn}
                    </p>
                  </div>

                  <form onSubmit={handleSignIn} className="space-y-4" id="signin-form" noValidate>
                    <div className="space-y-2">
                      <Label htmlFor="username" className="text-sm font-normal text-foreground">
                        {t.auth.username}
                      </Label>
                      <Input
                        id="username"
                        type="text"
                        placeholder={t.auth.placeholderUsername}
                        value={username}
                        onChange={(e) => setUsername(e.target.value)}
                        autoComplete="username"
                        disabled={isLoading}
                        className="h-11 rounded-[8px] border-slate-300 dark:border-slate-800"
                      />
                    </div>

                    <div className="space-y-2">
                      <div className="flex items-center justify-between">
                        <Label htmlFor="password" className="text-sm font-normal text-foreground">
                          {t.auth.password}
                        </Label>
                        <a
                          href="#"
                          className="text-xs text-[#2563EB] hover:text-[#1D4ED8] transition-colors"
                        >
                          {t.auth.forgotPassword}
                        </a>
                      </div>
                      <Input
                        id="password"
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

                  <div className="relative">
                    <Separator />
                    <span className="absolute left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2 bg-white px-3 text-xs text-muted-foreground dark:bg-slate-900 dark:text-slate-400">
                      {t.auth.orSSO}
                    </span>
                  </div>

                  <div className="grid grid-cols-3 gap-2">
                    <Button
                      variant="outline"
                      className="h-9 text-xs font-normal gap-1.5 rounded-[8px]"
                      disabled={isLoading}
                      id="sso-microsoft"
                    >
                      <MicrosoftIcon />
                      Microsoft
                    </Button>
                    <Button
                      variant="outline"
                      className="h-9 text-xs font-normal gap-1.5 rounded-[8px]"
                      disabled={isLoading}
                      id="sso-google"
                    >
                      <GoogleIcon />
                      Google
                    </Button>
                    <Button
                      variant="outline"
                      className="h-9 text-xs font-normal gap-1.5 rounded-[8px]"
                      disabled={isLoading}
                      id="sso-github"
                    >
                      <GitHubIcon />
                      GitHub
                    </Button>
                  </div>

                  <div className="text-center text-xs text-muted-foreground pt-1 select-none">
                    {t.auth.noAccount}{" "}
                    <button
                      onClick={() => switchMode("signup")}
                      className="font-medium text-[#2563EB] hover:text-[#1D4ED8] transition-colors cursor-pointer"
                    >
                      {t.auth.signUp}
                    </button>
                  </div>
                </div>
              )}

              {/* CHẾ ĐỘ 2: ĐĂNG KÝ (STAGE 1: FORM) */}
              {mode === "signup" && signupStep === "form" && (
                <div className="space-y-5 animate-in fade-in duration-300">
                  <div className="space-y-1">
                    <h2 className="text-base font-semibold text-foreground">{t.auth.createAuroraIdentity}</h2>
                    <p className="text-sm text-muted-foreground">
                      {t.auth.subtitleSignUpIdentity}
                    </p>
                  </div>

                  <form onSubmit={handleSignUp} className="space-y-5" id="signup-form" noValidate>
                    {/* SECTION 1: Account */}
                    <div className="space-y-3">
                      <span className="text-xs font-semibold text-slate-400 uppercase tracking-wider">
                        {t.auth.secAccount}
                      </span>
                      <div className="border-t border-slate-100 dark:border-slate-800/80 mb-2" />

                      <div className="space-y-2">
                        <Label htmlFor="username" className="text-sm font-normal text-foreground">
                          {t.auth.username}
                        </Label>
                        <Input
                          id="username"
                          type="text"
                          placeholder={t.auth.placeholderUsername}
                          value={username}
                          onChange={(e) => setUsername(e.target.value)}
                          disabled={isLoading}
                          className="h-11 rounded-[8px] border-slate-300 dark:border-slate-800"
                        />
                      </div>

                      <div className="space-y-2">
                        <Label htmlFor="email" className="text-sm font-normal text-foreground">
                          {t.auth.email}
                        </Label>
                        <Input
                          id="email"
                          type="email"
                          placeholder={t.auth.placeholderEmail}
                          value={email}
                          onChange={(e) => setEmail(e.target.value)}
                          disabled={isLoading}
                          className="h-11 rounded-[8px] border-slate-300 dark:border-slate-800"
                        />
                      </div>
                    </div>

                    {/* SECTION 2: Profile & Security */}
                    <div className="space-y-3 pt-2">
                      <span className="text-xs font-semibold text-slate-400 uppercase tracking-wider">
                        {t.auth.secProfile}
                      </span>
                      <div className="border-t border-slate-100 dark:border-slate-800/80 mb-2" />

                      <div className="space-y-2">
                        <Label htmlFor="fullname" className="text-sm font-normal text-foreground">
                          {t.auth.fullname}
                        </Label>
                        <Input
                          id="fullname"
                          type="text"
                          placeholder={t.auth.placeholderFullname}
                          value={fullname}
                          onChange={(e) => setFullname(e.target.value)}
                          disabled={isLoading}
                          className="h-11 rounded-[8px] border-slate-300 dark:border-slate-800"
                        />
                      </div>

                      <div className="space-y-2">
                        <Label htmlFor="phone" className="text-sm font-normal text-foreground">
                          {t.auth.phone}
                        </Label>
                        <Input
                          id="phone"
                          type="tel"
                          placeholder={t.auth.placeholderPhone}
                          value={phone}
                          onChange={(e) => setPhone(e.target.value)}
                          disabled={isLoading}
                          className="h-11 rounded-[8px] border-slate-300 dark:border-slate-800"
                        />
                      </div>

                      <div className="space-y-2">
                        <Label htmlFor="password" className="text-sm font-normal text-foreground">
                          {t.auth.password}
                        </Label>
                        <Input
                          id="password"
                          type="password"
                          placeholder={t.auth.placeholderPassword}
                          value={password}
                          onChange={(e) => setPassword(e.target.value)}
                          disabled={isLoading}
                          className="h-11 rounded-[8px] border-slate-300 dark:border-slate-800"
                        />
                        {password.length > 0 && (
                          <div className="space-y-1.5 pt-1 text-xs select-none">
                            <div className="flex items-center gap-2">
                              <CircleIcon active={hasMinLength} />
                              <span className={hasMinLength ? "text-emerald-600 dark:text-emerald-400 font-medium" : "text-slate-400 dark:text-slate-600"}>
                                {t.auth.pwdAtLeast8}
                              </span>
                            </div>
                            <div className="flex items-center gap-2">
                              <CircleIcon active={hasUppercase} />
                              <span className={hasUppercase ? "text-emerald-600 dark:text-emerald-400 font-medium" : "text-slate-400 dark:text-slate-600"}>
                                {t.auth.pwdOneUppercase}
                              </span>
                            </div>
                            <div className="flex items-center gap-2">
                              <CircleIcon active={hasNumber} />
                              <span className={hasNumber ? "text-emerald-600 dark:text-emerald-400 font-medium" : "text-slate-400 dark:text-slate-600"}>
                                {t.auth.pwdOneNumber}
                              </span>
                            </div>
                          </div>
                        )}
                      </div>
                    </div>

                    <Button
                      type="submit"
                      className="w-full h-11 bg-[#2563EB] hover:bg-[#1D4ED8] text-white rounded-[8px] mt-4 cursor-pointer font-medium"
                      disabled={isLoading}
                      id="signup-button"
                    >
                      {isLoading ? (
                        <span className="flex items-center gap-2">
                          <span className="h-3.5 w-3.5 animate-spin rounded-full border-2 border-white/30 border-t-white" />
                          {t.auth.btnCreatingAccount}
                        </span>
                      ) : (
                        t.auth.btnCreateAccount
                      )}
                    </Button>
                  </form>

                  <div className="h-px bg-slate-100 dark:bg-slate-800 my-1" />

                  <div className="text-center text-xs text-muted-foreground pt-1 select-none">
                    {t.auth.alreadyAccount}{" "}
                    <button
                      onClick={() => switchMode("signin")}
                      className="font-medium text-[#2563EB] hover:text-[#1D4ED8] transition-colors cursor-pointer"
                    >
                      {t.auth.signIn}
                    </button>
                  </div>
                </div>
              )}

              {/* STAGE 2: VERIFY EMAIL */}
              {signupStep === "verify-email" && (
                <div className="text-center space-y-6 py-4 select-none animate-in fade-in duration-300">
                  <div className="mx-auto flex h-12 w-12 items-center justify-center rounded-full bg-blue-50 dark:bg-blue-950/40 text-blue-600 dark:text-blue-400">
                    <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                      <path d="M22 13V6a2 2 0 0 0-2-2H4a2 2 0 0 0-2 2v12c0 1.1.9 2 2 2h9" />
                      <path d="m22 7-8.97 5.7a1.94 1.94 0 0 1-2.06 0L2 7" />
                      <path d="m16 19 2 2 4-4" />
                    </svg>
                  </div>

                  <div className="space-y-2">
                    <h2 className="text-lg font-semibold text-foreground">{t.auth.verifyEmailTitle}</h2>
                    <p className="text-sm text-muted-foreground px-4">
                      {t.auth.verifyEmailSent}
                    </p>
                    <p className="text-sm font-semibold text-foreground break-all">{email}</p>
                    <p className="text-xs text-slate-400 pt-1">
                      {t.auth.verifyEmailInbox}
                    </p>
                  </div>

                  <div className="space-y-3">
                    <a
                      href="https://mail.google.com"
                      target="_blank"
                      rel="noopener noreferrer"
                      className="w-full h-11 bg-[#2563EB] hover:bg-[#1D4ED8] text-white rounded-[8px] flex items-center justify-center font-medium transition-colors"
                    >
                      {t.auth.btnOpenGmail}
                    </a>

                    <div className="text-xs text-muted-foreground">
                      {timeLeft > 0 ? (
                        <span>
                          {t.auth.resendIn} {timeLeft}s
                        </span>
                      ) : (
                        <button
                          onClick={handleResendEmail}
                          disabled={isLoading}
                          className="font-medium text-[#2563EB] hover:text-[#1D4ED8] transition-colors cursor-pointer"
                        >
                          {t.auth.resendEmail}
                        </button>
                      )}
                    </div>
                  </div>

                  <div className="pt-2">
                    <button
                      onClick={() => setSignupStep("form")}
                      className="text-xs font-medium text-slate-500 hover:text-slate-800 dark:hover:text-slate-300 transition-colors cursor-pointer"
                    >
                      {t.auth.useAnotherEmail}
                    </button>
                  </div>
                </div>
              )}

              {/* STAGE 3: VERIFIED SUCCESS */}
              {signupStep === "verified" && (
                <div className="text-center space-y-6 py-4 select-none animate-in fade-in duration-300">
                  <div className="mx-auto flex h-12 w-12 items-center justify-center rounded-full bg-emerald-50 dark:bg-emerald-950/40 text-emerald-600 dark:text-emerald-400 animate-bounce">
                    <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
                      <polyline points="20 6 9 17 4 12" />
                    </svg>
                  </div>

                  <div className="space-y-2">
                    <h2 className="text-lg font-semibold text-foreground">{t.auth.emailVerifiedTitle}</h2>
                    <p className="text-sm text-muted-foreground">
                      {t.auth.emailVerifiedSubtitle}
                    </p>
                  </div>

                  <Button
                    onClick={() => switchMode("signin")}
                    className="w-full h-11 bg-[#2563EB] hover:bg-[#1D4ED8] text-white rounded-[8px] font-medium"
                  >
                    {t.auth.continueSignIn}
                  </Button>
                </div>
              )}

            </div>
          </div>

          {/* DOCUMENTATION & SUPPORT LINKS */}
          <div className="flex items-center justify-center gap-3 text-xs text-muted-foreground font-medium select-none">
            <a href="#" className="hover:text-foreground transition-colors">Documentation</a>
            <span>·</span>
            <a href="#" className="hover:text-foreground transition-colors">Support</a>
            <span>·</span>
            <a href="#" className="hover:text-foreground transition-colors">Privacy</a>
            <span>·</span>
            <a href="#" className="hover:text-foreground transition-colors">Terms</a>
          </div>

        </div>
      </div>

      {/* FOOTER IN RESPONSIVE COLOR SCHEMES */}
      <div className="w-full py-4 border-t border-[#E5E7EB] dark:border-slate-800 bg-white dark:bg-slate-950/60 text-xs text-muted-foreground flex items-center justify-center gap-4 mt-auto select-none">
        <span className="flex items-center gap-1.5 font-semibold text-emerald-600 dark:text-emerald-500">
          <span className="relative flex h-2 w-2">
            <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-emerald-400 opacity-75" />
            <span className="relative inline-flex h-2 w-2 rounded-full bg-emerald-500" />
          </span>
          {t.auth.operational}
        </span>
        <span className="text-slate-300 dark:text-slate-800">|</span>
        <span className="font-mono text-slate-500 dark:text-slate-400">v1.0.0</span>
        
        {zones.length > 0 && mode === "signin" && (
          <>
            <span className="text-slate-300 dark:text-slate-800">|</span>
            <div className="flex items-center gap-1">
              <select
                value={selectedZoneCode}
                onChange={(e) => setSelectedZoneCode(e.target.value)}
                className="bg-transparent text-xs text-slate-600 outline-none hover:border-slate-300 transition-colors cursor-pointer font-medium dark:text-slate-400 dark:hover:border-slate-700"
              >
                {zones.map((z) => (
                  <option key={z.code} value={z.code} className="bg-white text-slate-700 dark:bg-slate-900 dark:text-slate-300">
                    {z.name} ({z.code})
                  </option>
                ))}
              </select>
            </div>
          </>
        )}

        <span className="text-slate-300 dark:text-slate-800">|</span>
        <div className="flex items-center gap-1">
          <select
            value={theme}
            onChange={(e) => handleThemeChange(e.target.value)}
            className="bg-transparent text-xs text-slate-600 outline-none hover:border-slate-300 transition-colors cursor-pointer font-medium dark:text-slate-400 dark:hover:border-slate-700"
          >
            <option value="light" className="bg-white text-slate-700 dark:bg-slate-900 dark:text-slate-300">
              {t.auth.themeLight}
            </option>
            <option value="dark" className="bg-white text-slate-700 dark:bg-slate-900 dark:text-slate-300">
              {t.auth.themeDark}
            </option>
            <option value="system" className="bg-white text-slate-700 dark:bg-slate-900 dark:text-slate-300">
              {t.auth.themeSystem}
            </option>
          </select>
        </div>

        <span className="text-slate-300 dark:text-slate-800">|</span>
        <div className="flex items-center gap-1">
          <select
            value={lang}
            onChange={(e) => setLang(e.target.value as Language)}
            className="bg-transparent text-xs text-slate-600 outline-none hover:border-slate-300 transition-colors cursor-pointer font-medium dark:text-slate-400 dark:hover:border-slate-700"
          >
            <option value="en" className="bg-white text-slate-700 dark:bg-slate-900 dark:text-slate-300">English</option>
            <option value="vi" className="bg-white text-slate-700 dark:bg-slate-900 dark:text-slate-300">Tiếng Việt</option>
            <option value="ja" className="bg-white text-slate-700 dark:bg-slate-900 dark:text-slate-300">日本語</option>
            <option value="zh" className="bg-white text-slate-700 dark:bg-slate-900 dark:text-slate-300">简体中文</option>
            <option value="ko" className="bg-white text-slate-700 dark:bg-slate-900 dark:text-slate-300">한국어</option>
          </select>
        </div>
      </div>
    </div>
  );
}

export default function SignInPage() {
  return (
    <Suspense fallback={
      <div className="flex min-h-screen items-center justify-center bg-[#F8FAFC] dark:bg-[#0B0F19]">
        <div className="h-8 w-8 animate-spin rounded-full border-2 border-[#2563EB]/30 border-t-[#2563EB]" />
      </div>
    }>
      <SignInContent />
      <Toaster />
    </Suspense>
  );
}
