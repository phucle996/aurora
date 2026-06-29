"use client";

import React, { useState, useCallback, useEffect } from "react";
import { toast } from "sonner";

// [COMMENT]: Import i18n hooks phục vụ đa ngôn ngữ toàn hệ thống
import { useTranslation } from "@/lib/i18n";

// [COMMENT]: Import shadcn components
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

// [COMMENT]: Import API module đăng ký
import { authAPI } from "@/lib/api/auth";

// [COMMENT]: Import icons từ lucide-react
import { ChevronLeft, ChevronRight } from "lucide-react";

// [COMMENT]: Icon hiển thị trạng thái live check password constraints
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

// [COMMENT]: Hàm chuyển đổi số điện thoại thô sang định dạng E.164 (ví dụ: 0346287974 -> +84346287974)
// Hàm này sẽ tự động thêm mã quốc gia Việt Nam (+84) làm mặc định nếu số điện thoại bắt đầu bằng 0.
const formatPhoneToE164 = (rawPhone: string) => {
  let cleaned = rawPhone.replace(/[^\d+]/g, "");
  if (!cleaned) return "";

  if (cleaned.startsWith("+")) {
    return cleaned;
  }

  if (cleaned.startsWith("0")) {
    return "+84" + cleaned.slice(1);
  }

  if (cleaned.startsWith("84") && cleaned.length === 11) {
    return "+" + cleaned;
  }

  return "+" + cleaned;
};

// [COMMENT]: Props interface cho SignUpForm — chỉ cần callback chuyển mode
export interface SignUpFormProps {
  onSwitchToSignIn: () => void;
}

export default function SignUpForm({ onSwitchToSignIn }: SignUpFormProps) {
  const { t } = useTranslation();

  // [COMMENT]: Quản lý các bước xác thực email: form → verify-email → verified
  const [signupStep, setSignupStep] = useState<"form" | "verify-email" | "verified">("form");

  // [COMMENT]: Chia nhỏ form đăng ký thành 2 step để gọn gàng giao diện
  const [formStep, setFormStep] = useState<1 | 2>(1);

  // [COMMENT]: State nội bộ cho form đăng ký — tách biệt khỏi signin form
  const [username, setUsername] = useState("");
  const [email, setEmail] = useState("");
  const [fullname, setFullname] = useState("");
  const [phone, setPhone] = useState("");
  const [password, setPassword] = useState("");
  const [isLoading, setIsLoading] = useState(false);
  const [timeLeft, setTimeLeft] = useState(59);

  // [COMMENT]: Countdown timer kích hoạt gửi lại email xác thực
  useEffect(() => {
    if (signupStep !== "verify-email") return;
    if (timeLeft <= 0) return;

    const timer = setInterval(() => {
      setTimeLeft((prev) => prev - 1);
    }, 1000);

    return () => clearInterval(timer);
  }, [signupStep, timeLeft]);

  // [COMMENT]: Live password constraints — check realtime khi user nhập đúng 100% với backend Go (isStrongPassword)
  const hasMinLength = password.length >= 8;
  const hasLowercase = /[a-z]/.test(password);
  const hasUppercase = /[A-Z]/.test(password);
  const hasNumber = /[0-9]/.test(password);
  const hasSpecial = /[^A-Za-z0-9]/.test(password);
  const isPasswordValid = hasMinLength && hasLowercase && hasUppercase && hasNumber && hasSpecial;

  // [COMMENT]: Lấy location ước lượng từ ngôn ngữ trình duyệt (fallback cho GeoIP phía server)
  const getDetectedLocation = () => {
    const browserLang = typeof navigator !== "undefined" ? navigator.language : "en-US";
    const countryCode = browserLang.includes("-") ? browserLang.split("-")[1] : browserLang;
    return countryCode.toUpperCase().substring(0, 2);
  };

  // [COMMENT]: Tiến hành chuyển đổi sang Step 2 sau khi kiểm tra hợp lệ các trường của Step 1
  const handleNextStep = (e: React.MouseEvent) => {
    e.preventDefault();
    if (!username.trim()) {
      toast.error(t.auth.usernameReq);
      return;
    }
    // [COMMENT]: Kiểm tra độ dài tối thiểu của tên đăng nhập (phải từ 6 ký tự trở lên theo yêu cầu backend)
    if (username.trim().length < 6) {
      toast.error(t.auth.usernameLen);
      return;
    }
    // [COMMENT]: Chặn username chứa '@' — ký tự này được hệ thống dùng làm separator
    // để phân biệt login global (username) và login tenant (username@tenant_domain).
    if (username.includes("@")) {
      toast.error("Username must not contain '@'. Please enter your email in the Email field.");
      return;
    }
    if (!email.trim()) {
      toast.error(t.auth.emailReq);
      return;
    }
    const emailRegex = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;
    if (!emailRegex.test(email.trim())) {
      toast.error("Please enter a valid email address.");
      return;
    }
    setFormStep(2);
  };

  // [COMMENT]: Logic xử lý Đăng ký — validate → call API → chuyển sang verify email step
  const handleSignUp = useCallback(async (e: React.FormEvent) => {
    e.preventDefault();

    // Đối chiếu đầy đủ ở cả 2 bước đề phòng bypass
    if (!username.trim() || !email.trim()) {
      setFormStep(1);
      toast.error("Please complete the account credentials step.");
      return;
    }
    // [COMMENT]: Kiểm tra lại độ dài tối thiểu của tên đăng nhập trước khi submit
    if (username.trim().length < 6) {
      setFormStep(1);
      toast.error(t.auth.usernameLen);
      return;
    }
    if (!fullname.trim()) {
      toast.error(t.auth.fullnameReq);
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

    // [COMMENT]: Định dạng số điện thoại sang chuẩn E.164 trước khi gọi API
    let formattedPhone: string | undefined = undefined;
    if (phone.trim()) {
      formattedPhone = formatPhoneToE164(phone.trim());
      // [COMMENT]: Regex kiểm tra định dạng E.164: bắt đầu bằng + và theo sau bởi 1-14 chữ số
      const phoneRegex = /^\+[1-9]\d{1,14}$/;
      if (!phoneRegex.test(formattedPhone)) {
        toast.error(t.auth.phoneInvalid);
        return;
      }
    }

    setIsLoading(true);

    try {
      const detectedLocation = getDetectedLocation();
      const detectedTimezone = typeof Intl !== "undefined"
        ? Intl.DateTimeFormat().resolvedOptions().timeZone
        : "UTC";

      await authAPI.register({
        username,
        fullname,
        email,
        password,
        phone: formattedPhone,
        location: detectedLocation,
        timezone: detectedTimezone,
      });

      // [COMMENT]: Chuyển sang bước xác thực email sau khi đăng ký thành công
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

  // [COMMENT]: Gửi lại email xác thực — chỉ được phép khi countdown đã hết
  const handleResendEmail = useCallback(async () => {
    if (timeLeft > 0) return;
    setIsLoading(true);

    // [COMMENT]: Định dạng số điện thoại sang chuẩn E.164 khi gửi lại email đăng ký
    let formattedPhone: string | undefined = undefined;
    if (phone.trim()) {
      formattedPhone = formatPhoneToE164(phone.trim());
    }

    try {
      const detectedLocation = getDetectedLocation();
      const detectedTimezone = typeof Intl !== "undefined"
        ? Intl.DateTimeFormat().resolvedOptions().timeZone
        : "UTC";

      await authAPI.register({
        username,
        fullname,
        email,
        password,
        phone: formattedPhone,
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

  // =====================================================
  // STAGE 1: FORM ĐĂNG KÝ
  // =====================================================
  if (signupStep === "form") {
    return (
      <div className="space-y-4">
        {/* Step Header & Indicators */}
        <div className="flex items-center justify-between pb-1 select-none">
          <div className="space-y-0.5">
            <h2 className="text-base font-semibold text-foreground">{t.auth.createAuroraIdentity}</h2>
            <p className="text-xs text-muted-foreground">
              {formStep === 1 ? "Step 1: Account credentials" : "Step 2: Profile & Security"}
            </p>
          </div>
          <div className="flex items-center gap-1.5 shrink-0">
            <div className={`h-1 w-5 rounded-full transition-all duration-300 ${formStep === 1 ? "bg-[#2563EB]" : "bg-slate-200 dark:bg-slate-800"}`} />
            <div className={`h-1 w-5 rounded-full transition-all duration-300 ${formStep === 2 ? "bg-[#2563EB]" : "bg-slate-200 dark:bg-slate-800"}`} />
          </div>
        </div>

        <form onSubmit={handleSignUp} className="space-y-4" id="signup-form" noValidate>
          {formStep === 1 ? (
            /* =====================================================
               STEP 1: ACCOUNT CREDENTIALS
               ===================================================== */
            <div className="space-y-3 animate-in fade-in slide-in-from-right-2 duration-200">
              <div className="space-y-2">
                <Label htmlFor="signup-username" className="text-sm font-normal text-foreground">
                  {t.auth.username}
                </Label>
                <Input
                  id="signup-username"
                  type="text"
                  placeholder={t.auth.placeholderUsername}
                  value={username}
                  onChange={(e) => setUsername(e.target.value)}
                  disabled={isLoading}
                  className="h-11 rounded-[8px] border-slate-300 dark:border-slate-800"
                />
              </div>

              <div className="space-y-2">
                <Label htmlFor="signup-email" className="text-sm font-normal text-foreground">
                  {t.auth.email}
                </Label>
                <Input
                  id="signup-email"
                  type="email"
                  placeholder={t.auth.placeholderEmail}
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  disabled={isLoading}
                  className="h-11 rounded-[8px] border-slate-300 dark:border-slate-800"
                />
              </div>

              <Button
                type="button"
                onClick={handleNextStep}
                className="w-full h-11 bg-[#2563EB] hover:bg-[#1D4ED8] text-white rounded-[8px] mt-4 cursor-pointer font-medium flex items-center justify-center gap-1.5 transition-colors"
              >
                Continue
                <ChevronRight className="h-4 w-4" />
              </Button>
            </div>
          ) : (
            /* =====================================================
               STEP 2: PROFILE & SECURITY
               ===================================================== */
            <div className="space-y-3 animate-in fade-in slide-in-from-right-2 duration-200">
              <div className="space-y-2">
                <Label htmlFor="signup-fullname" className="text-sm font-normal text-foreground">
                  {t.auth.fullname}
                </Label>
                <Input
                  id="signup-fullname"
                  type="text"
                  placeholder={t.auth.placeholderFullname}
                  value={fullname}
                  onChange={(e) => setFullname(e.target.value)}
                  disabled={isLoading}
                  className="h-11 rounded-[8px] border-slate-300 dark:border-slate-800"
                />
              </div>

              <div className="space-y-2">
                <Label htmlFor="signup-phone" className="text-sm font-normal text-foreground">
                  {t.auth.phone}
                </Label>
                <Input
                  id="signup-phone"
                  type="tel"
                  placeholder={t.auth.placeholderPhone}
                  value={phone}
                  onChange={(e) => setPhone(e.target.value)}
                  disabled={isLoading}
                  className="h-11 rounded-[8px] border-slate-300 dark:border-slate-800"
                />
              </div>

              <div className="space-y-2">
                <Label htmlFor="signup-password" className="text-sm font-normal text-foreground">
                  {t.auth.password}
                </Label>
                <Input
                  id="signup-password"
                  type="password"
                  placeholder={t.auth.placeholderPassword}
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  disabled={isLoading}
                  className="h-11 rounded-[8px] border-slate-300 dark:border-slate-800"
                />
                {/* [COMMENT]: Live checklist hiển thị khi user bắt đầu nhập password */}
                {password.length > 0 && (
                  <div className="space-y-1.5 pt-1 text-xs select-none">
                    <div className="flex items-center gap-2">
                      <CircleIcon active={hasMinLength} />
                      <span className={hasMinLength ? "text-emerald-600 dark:text-emerald-400 font-medium" : "text-slate-400 dark:text-slate-600"}>
                        {t.auth.pwdAtLeast8}
                      </span>
                    </div>
                    <div className="flex items-center gap-2">
                      <CircleIcon active={hasLowercase} />
                      <span className={hasLowercase ? "text-emerald-600 dark:text-emerald-400 font-medium" : "text-slate-400 dark:text-slate-600"}>
                        {t.auth.pwdOneLowercase}
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
                    <div className="flex items-center gap-2">
                      <CircleIcon active={hasSpecial} />
                      <span className={hasSpecial ? "text-emerald-600 dark:text-emerald-400 font-medium" : "text-slate-400 dark:text-slate-600"}>
                        {t.auth.pwdOneSpecial}
                      </span>
                    </div>
                  </div>
                )}
              </div>

              <div className="flex gap-3 pt-2">
                <Button
                  type="button"
                  variant="outline"
                  onClick={() => setFormStep(1)}
                  className="flex-1 h-11 border-slate-200 dark:border-slate-800 text-slate-700 dark:text-slate-300 rounded-[8px] cursor-pointer font-medium flex items-center justify-center gap-1.5 transition-colors"
                  disabled={isLoading}
                >
                  <ChevronLeft className="h-4 w-4" />
                  Back
                </Button>
                <Button
                  type="submit"
                  className="flex-[2] h-11 bg-[#2563EB] hover:bg-[#1D4ED8] text-white rounded-[8px] cursor-pointer font-medium flex items-center justify-center"
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
              </div>
            </div>
          )}
        </form>

        <div className="h-px bg-slate-100 dark:bg-slate-800 my-1" />

        {/* [COMMENT]: Link chuyển sang đăng nhập */}
        <div className="text-center text-xs text-muted-foreground pt-1 select-none">
          {t.auth.alreadyAccount}{" "}
          <button
            onClick={onSwitchToSignIn}
            className="font-medium text-[#2563EB] hover:text-[#1D4ED8] transition-colors cursor-pointer"
          >
            {t.auth.signIn}
          </button>
        </div>
      </div>
    );
  }

  // =====================================================
  // STAGE 2: XÁC THỰC EMAIL
  // =====================================================
  if (signupStep === "verify-email") {
    return (
      <div className="text-center space-y-6 py-4 select-none">
        <div className="mx-auto flex h-12 w-12 items-center justify-center rounded-full bg-blue-50 dark:bg-blue-950/40 text-blue-600 dark:text-blue-400">
          <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <path d="M22 13V6a2 2 0 0 0-2-2H4a2 2 0 0 0-2 2v12c0 1.1.9 2 2 2h9" />
            <path d="m22 7-8.97 5.7a1.94 1.94 0 0 1-2.06 0L2 7" />
            <path d="m16 19 2 2 4-4" />
          </svg>
        </div>

        <div className="space-y-2">
          <h2 className="text-lg font-semibold text-foreground">{t.auth.verifyEmailTitle}</h2>
          <p className="text-sm text-muted-foreground px-4">{t.auth.verifyEmailSent}</p>
          <p className="text-sm font-semibold text-foreground break-all">{email}</p>
          <p className="text-xs text-slate-400 pt-1">{t.auth.verifyEmailInbox}</p>
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
              <span>{t.auth.resendIn} {timeLeft}s</span>
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
    );
  }

  // =====================================================
  // STAGE 3: XÁC THỰC THÀNH CÔNG
  // =====================================================
  return (
    <div className="text-center space-y-6 py-4 select-none">
      <div className="mx-auto flex h-12 w-12 items-center justify-center rounded-full bg-emerald-50 dark:bg-emerald-950/40 text-emerald-600 dark:text-emerald-400 animate-bounce">
        <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
          <polyline points="20 6 9 17 4 12" />
        </svg>
      </div>

      <div className="space-y-2">
        <h2 className="text-lg font-semibold text-foreground">{t.auth.emailVerifiedTitle}</h2>
        <p className="text-sm text-muted-foreground">{t.auth.emailVerifiedSubtitle}</p>
      </div>

      <Button
        onClick={onSwitchToSignIn}
        className="w-full h-11 bg-[#2563EB] hover:bg-[#1D4ED8] text-white rounded-[8px] font-medium"
      >
        {t.auth.continueSignIn}
      </Button>
    </div>
  );
}
