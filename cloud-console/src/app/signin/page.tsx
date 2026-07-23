"use client";

import React, { useState, useCallback, useEffect, useRef, Suspense } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { Toaster } from "@/components/ui/sonner";
import { useUserSession } from "@/hooks/useUserSession";

// [COMMENT]: Import i18n hooks phục vụ đa ngôn ngữ toàn hệ thống
import { useTranslation, type Language } from "@/lib/i18n";
import { useTheme, type ThemeMode } from "@/context/ThemeContext";

// [COMMENT]: Import API module zone catalog
import { fetchZoneCatalog, type ZoneCatalogItem } from "@/lib/api/zone";

// [COMMENT]: Import các form component con — mỗi form tự quản lý state nội bộ
import SignInForm from "./signin-form";
import SignUpForm from "./signup-form";

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

// [COMMENT]: Component chính điều phối layout xác thực với horizontal slide animation
function AuthPageContent() {
  const searchParams = useSearchParams();
  const router = useRouter();
  const requestedReturnTo = searchParams.get("return_to") || "";
  // [COMMENT]: Chỉ chấp nhận callback nội bộ cố định; giá trị tuyệt đối/protocol-relative không thể thành open redirect.
  const returnTo =
    requestedReturnTo.startsWith("/billing/authorize?") && !requestedReturnTo.startsWith("//")
      ? requestedReturnTo
      : "/";

  // [COMMENT]: Kiểm tra trạng thái phiên đăng nhập hiện tại
  const { authenticated, loading } = useUserSession();

  // [COMMENT]: Chuyển hướng người dùng về trang dashboard nếu đã đăng nhập thành công
  useEffect(() => {
    if (!loading && authenticated) {
      router.push(returnTo);
    }
  }, [loading, authenticated, returnTo, router]);

  // [COMMENT]: Móc nối dịch thuật đa ngôn ngữ toàn cục
  const { lang, setLang, t } = useTranslation();

  // [COMMENT]: Mode điều khiển form nào đang hiển thị (signin / signup)
  const [mode, setMode] = useState<"signin" | "signup">("signin");

  // [COMMENT]: Lấy state theme và hàm cập nhật từ ThemeProvider toàn cục
  const { theme, setTheme } = useTheme();

  // [COMMENT]: Danh sách active zones lấy từ ACR edge
  const [zones, setZones] = useState<ZoneCatalogItem[]>([]);
  const [selectedZoneCode, setSelectedZoneCode] = useState("");

  // [COMMENT]: Ref tới flex container chứa 2 form panel — dùng cho slide animation
  const sliderRef = useRef<HTMLDivElement>(null);

  // [COMMENT]: Chiều cao card được tính động theo panel đang active — tránh khoảng trắng thừa
  const [containerHeight, setContainerHeight] = useState<number>(0);

  // =========================================================================
  // EFFECTS: Khởi tạo và đồng bộ trạng thái
  // =========================================================================

  // [COMMENT]: Đồng bộ mode từ URL query params khi trang được load lần đầu
  useEffect(() => {
    if (searchParams) {
      const modeParam = searchParams.get("mode");
      if (modeParam === "signup") {
        setMode("signup");
      } else {
        setMode("signin");
      }
    }
  }, [searchParams]);

  // [COMMENT]: Lắng nghe sự kiện phím Back/Forward của trình duyệt để đổi mode
  useEffect(() => {
    const handlePopState = () => {
      if (typeof window !== "undefined") {
        const params = new URLSearchParams(window.location.search);
        const modeParam = params.get("mode");
        setMode(modeParam === "signup" ? "signup" : "signin");
      }
    };
    window.addEventListener("popstate", handlePopState);
    return () => window.removeEventListener("popstate", handlePopState);
  }, []);

  // [COMMENT]: Tra cứu danh sách active zones từ ACR edge khi mount
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
    return () => { active = false; };
  }, []);



  // =========================================================================
  // HEIGHT OBSERVER: Theo dõi chiều cao panel đang active
  // =========================================================================
  // [COMMENT]: Dùng ResizeObserver để tự động cập nhật chiều cao card khi nội dung panel thay đổi
  // (ví dụ: password strength indicators xuất hiện, hoặc signup chuyển step verify-email → verified)
  useEffect(() => {
    if (!sliderRef.current) return;

    const activeIndex = mode === "signin" ? 0 : 1;
    const activePanel = sliderRef.current.children[activeIndex] as HTMLElement;
    if (!activePanel) return;

    // [COMMENT]: Đo chiều cao ngay lập tức trước khi ResizeObserver kịp fire
    const updateHeight = () => {
      setContainerHeight(activePanel.scrollHeight);
    };
    updateHeight();

    const observer = new ResizeObserver(updateHeight);
    observer.observe(activePanel);

    return () => observer.disconnect();
  }, [mode]);

  // =========================================================================
  // CALLBACKS: Chuyển đổi mode và theme
  // =========================================================================

  // [COMMENT]: Chuyển mode với pushState (zero-reload) — animation được trigger bởi CSS transition
  const switchMode = useCallback((newMode: "signin" | "signup") => {
    if (typeof window !== "undefined") {
      window.history.pushState(
        null,
        "",
        newMode === "signin" ? "/signin" : "/signin?mode=signup"
      );
    }
    setMode(newMode);
  }, []);

  // [COMMENT]: Thay đổi theme toàn cục thông qua ThemeProvider
  const handleThemeChange = useCallback((newTheme: string) => {
    setTheme(newTheme as ThemeMode);
  }, [setTheme]);

  // =========================================================================
  // RENDER
  // =========================================================================

  // [COMMENT]: Kiểm tra trạng thái phiên làm việc sau khi khởi tạo toàn bộ hooks để tránh vi phạm Rules of Hooks
  if (loading || authenticated) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-[#F8FAFC] dark:bg-[#0B0F19]">
        <div className="h-8 w-8 animate-spin rounded-full border-2 border-[#2563EB]/30 border-t-[#2563EB]" />
      </div>
    );
  }

  return (
    <div className="flex min-h-screen flex-col bg-[#F8FAFC] dark:bg-[#0B0F19]">
      <div className="flex flex-1 items-center justify-center px-4 py-12">
        <div className="mx-auto w-full max-w-[420px] space-y-4 transition-all duration-300">
          {/* ===== LOGO & BRANDING ===== */}
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

          {/* ===== SLIDING CARD CONTAINER ===== */}
          {/* [COMMENT]: Card bọc ngoài có overflow hidden để tạo hiệu ứng slide.
              Height được animate mượt mà nhờ transition-[height] + ResizeObserver tracking.
              Khi mode thay đổi, flex container bên trong trượt ngang bằng translateX. */}
          <div
            className="rounded-xl border border-[#E5E7EB] dark:border-slate-800 bg-white dark:bg-slate-900/50 overflow-hidden transition-[height] duration-500 ease-[cubic-bezier(0.4,0,0.2,1)]"
            style={{ height: containerHeight > 0 ? `${containerHeight}px` : "auto" }}
          >
            {/* [COMMENT]: Flex row chứa 2 panel — mỗi panel chiếm 100% width.
                Transform translateX(-100%) để trượt sang panel signup.
                Cubic-bezier easing tạo cảm giác mượt tự nhiên. */}
            <div
              ref={sliderRef}
              className="flex items-start transition-transform duration-500 ease-[cubic-bezier(0.4,0,0.2,1)]"
              style={{
                transform: mode === "signup" ? "translateX(-100%)" : "translateX(0)",
              }}
            >
              {/* [COMMENT]: Panel 1 — Sign In Form */}
              <div className="w-full shrink-0">
                <div className="px-7 py-6">
                  <SignInForm
                    zones={zones}
                    selectedZoneCode={selectedZoneCode}
                    onZoneChange={setSelectedZoneCode}
                    onSwitchToSignUp={() => switchMode("signup")}
                  />
                </div>
              </div>

              {/* [COMMENT]: Panel 2 — Sign Up Form */}
              <div className="w-full shrink-0">
                <div className="px-7 py-6">
                  <SignUpForm
                    onSwitchToSignIn={() => switchMode("signin")}
                  />
                </div>
              </div>
            </div>
          </div>

          {/* ===== DOCUMENTATION & SUPPORT LINKS ===== */}
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

      {/* ===== FOOTER ===== */}
      {/* [COMMENT]: Footer chứa status, version, zone selector, theme selector, language selector */}
      <div className="w-full py-4 border-t border-[#E5E7EB] dark:border-slate-800 bg-white dark:bg-slate-950/60 text-xs text-muted-foreground flex items-center justify-center gap-4 mt-auto select-none">
        {/* [COMMENT]: System status indicator */}
        <span className="flex items-center gap-1.5 font-semibold text-emerald-600 dark:text-emerald-500">
          <span className="relative flex h-2 w-2">
            <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-emerald-400 opacity-75" />
            <span className="relative inline-flex h-2 w-2 rounded-full bg-emerald-500" />
          </span>
          {t.auth.operational}
        </span>

        <span className="text-slate-300 dark:text-slate-800">|</span>
        <span className="font-mono text-slate-500 dark:text-slate-400">v1.0.0</span>

        {/* [COMMENT]: Zone selector — chỉ hiển thị ở mode signin */}
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

        {/* [COMMENT]: Theme selector — đồng bộ với localStorage key "theme" */}
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

        {/* [COMMENT]: Language selector — đa ngôn ngữ toàn hệ thống */}
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

// [COMMENT]: Entry point — Suspense boundary cho useSearchParams() (Next.js 16 yêu cầu)
export default function SignInPage() {
  return (
    <Suspense fallback={
      <div className="flex min-h-screen items-center justify-center bg-[#F8FAFC] dark:bg-[#0B0F19]">
        <div className="h-8 w-8 animate-spin rounded-full border-2 border-[#2563EB]/30 border-t-[#2563EB]" />
      </div>
    }>
      <AuthPageContent />
      <Toaster />
    </Suspense>
  );
}
