"use client";
import Checkbox from "@/components/form/input/Checkbox";
import Input from "@/components/form/input/InputField";
import Label from "@/components/form/Label";
import Button from "@/components/ui/button/Button";
import { authAPI } from "@/lib/api/auth";
// [COMMENT]: Import API fetch danh mục Zone phục vụ chọn cụm khu vực khi đăng nhập.
import { fetchZoneCatalog, type ZoneCatalogItem } from "@/lib/api/zone";
import {
  DeviceKeyUnsupportedError,
  ensureDevicePublicKey,
} from "@/lib/security/deviceKey";
import { useUserSession } from "@/hooks/useUserSession";
import type { APIError } from "@/lib/api/fetcher";
import { useRouter } from "next/navigation";
import Link from "next/link";
import React, { useEffect, useRef, useState } from "react";

// [COMMENT]: Hàm helper Client-side dùng để đọc cookie.
function getCookie(name: string): string | null {
  if (typeof document === "undefined") return null;
  const value = `; ${document.cookie}`;
  const parts = value.split(`; ${name}=`);
  if (parts.length === 2) return parts.pop()?.split(";").shift() || null;
  return null;
}

export default function SignInForm() {
  const [showPassword, setShowPassword] = useState(false);
  const [isChecked, setIsChecked] = useState(false);
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [errorMessage, setErrorMessage] = useState("");
  // [COMMENT]: Lưu trữ danh sách Zone hoạt động fetch về từ Edge.
  const [zones, setZones] = useState<ZoneCatalogItem[]>([]);
  // [COMMENT]: Lưu trữ mã code của Zone đang được chọn.
  const [selectedZone, setSelectedZone] = useState<string>("");
  // [COMMENT]: Trạng thái loading khi fetch danh mục Zone từ gateway.
  const [isLoadingZones, setIsLoadingZones] = useState<boolean>(true);
  const submitLockRef = useRef(false);
  const { setAuthenticated, status } = useUserSession();
  const router = useRouter();
  const abortRef = useRef<AbortController | null>(null);

  // [COMMENT]: Tự động chuyển hướng về trang chủ Dashboard nếu người dùng đã được xác thực trước đó.
  useEffect(() => {
    if (status === "authenticated") {
      router.push("/");
    }
  }, [status, router]);

  // [COMMENT]: Tải danh mục Zone động từ Edge Gateway khi mở trang.
  // L1 cache tại Edge đã tối ưu hóa, đảm bảo HA và giảm tải DB tối đa.
  useEffect(() => {
    const fetchZonesController = new AbortController();

    async function loadZones() {
      try {
        const list = await fetchZoneCatalog({ signal: fetchZonesController.signal });
        setZones(list);

        // [COMMENT]: Đọc cookie zone_code cũ trên thiết bị và tự động chọn nếu khớp, cải thiện UX.
        const cachedCode = getCookie("zone_code")?.toLowerCase();
        const matchedZone = list.find((z) => z.code.toLowerCase() === cachedCode);
        if (matchedZone) {
          setSelectedZone(matchedZone.code);
        } else if (list.length > 0) {
          setSelectedZone(list[0].code);
        }
      } catch (err) {
        // [COMMENT]: Fallback an toàn nếu API catalog lỗi, đảm bảo trang login không bị hỏng.
        console.error("Không thể tải danh sách zone:", err);
      } finally {
        setIsLoadingZones(false);
      }
    }

    loadZones();

    return () => {
      fetchZonesController.abort();
    };
  }, []);

  useEffect(() => {
    return () => {
      abortRef.current?.abort();
    };
  }, []);

  // [COMMENT]: Hiển thị màn hình chờ (loading) khi hệ thống đang kiểm tra trạng thái phiên làm việc (status === "unknown").
  if (status === "unknown") {
    return (
      <div className="flex items-center justify-center min-h-screen bg-gray-50 dark:bg-gray-900">
        <div className="flex flex-col items-center space-y-4">
          <div className="w-12 h-12 border-4 border-brand-500 border-t-transparent rounded-full animate-spin"></div>
          <p className="text-sm font-medium text-gray-500 dark:text-gray-400">
            Đang tải thông tin cấu hình...
          </p>
        </div>
      </div>
    );
  }

  const handleSignIn = async (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (isSubmitting || submitLockRef.current) {
      return;
    }

    // [COMMENT]: Cho phép độ dài username ngắn hơn (như "root" có 4 ký tự) để tránh lỗi logic lockout tài khoản quản trị.
    // Chỉ kiểm tra rỗng hoặc độ dài tối thiểu cơ bản của username và mật khẩu để khớp với backend.
    const normalizedUsername = username.trim().toLowerCase();
    const normalizedPassword = password;
    if (normalizedUsername.length < 3 || normalizedPassword.length < 8) {
      setErrorMessage("Thông tin đăng nhập không hợp lệ.");
      return;
    }

    setIsSubmitting(true);
    submitLockRef.current = true;
    setErrorMessage("");

    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;

    let devicePublicKey: string;
    try {
      devicePublicKey = await ensureDevicePublicKey();
    } catch (keyError) {
      setIsSubmitting(false);
      submitLockRef.current = false;
      if (keyError instanceof DeviceKeyUnsupportedError) {
        setErrorMessage(
          "Trình duyệt không hỗ trợ xác thực khóa thiết bị. Vui lòng dùng trình duyệt mới hơn.",
        );
        return;
      }
      setErrorMessage("Không khởi tạo được khóa thiết bị. Vui lòng thử lại.");
      return;
    }

    try {
      await authAPI.login(
        {
          username: normalizedUsername,
          password: normalizedPassword,
          device_public_key: devicePublicKey,
          trust_device: isChecked,
          // [COMMENT]: Truyền zone_code đã chọn từ dropdown xuống DTO login.
          zone_code: selectedZone,
        },
        { signal: controller.signal },
      );
      setAuthenticated({ authenticated: true });
      window.location.href = "/";
    } catch (error) {
      if (controller.signal.aborted) {
        return;
      }
      const apiError = error as APIError;
      if (apiError?.status === 401 || apiError?.status === 403) {
        setErrorMessage("Thông tin đăng nhập không đúng hoặc tài khoản chưa khả dụng.");
        return;
      }
      if (apiError?.status === 429) {
        setErrorMessage("Quá nhiều lần thử. Vui lòng thử lại sau.");
        return;
      }
      if (apiError?.status === 503) {
        setErrorMessage("Hệ thống xác thực tạm thời không khả dụng.");
        return;
      }
      setErrorMessage("Đăng nhập thất bại. Vui lòng thử lại.");
    } finally {
      setIsSubmitting(false);
      submitLockRef.current = false;
    }
  };

  return (
    <div className="flex flex-col flex-1 lg:w-1/2 w-full">
      <div className="flex flex-col justify-center flex-1 w-full max-w-md mx-auto">
        <div>
          <div className="mb-5 sm:mb-8">
            <h1 className="mb-2 font-semibold text-gray-800 text-title-sm dark:text-white/90 sm:text-title-md">
              Sign In
            </h1>
            <p className="text-sm text-gray-500 dark:text-gray-400">
              Enter your username and password to sign in!
            </p>
          </div>
          <div>
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 sm:gap-5">
              <button className="inline-flex items-center justify-center gap-3 py-3 text-sm font-normal text-gray-700 transition-colors bg-gray-100 rounded-lg px-7 hover:bg-gray-200 hover:text-gray-800 dark:bg-white/5 dark:text-white/90 dark:hover:bg-white/10">
                <svg
                  width="20"
                  height="20"
                  viewBox="0 0 20 20"
                  fill="none"
                  xmlns="http://www.w3.org/2000/svg"
                >
                  <path
                    d="M18.7511 10.1944C18.7511 9.47495 18.6915 8.94995 18.5626 8.40552H10.1797V11.6527H15.1003C15.0011 12.4597 14.4654 13.675 13.2749 14.4916L13.2582 14.6003L15.9087 16.6126L16.0924 16.6305C17.7788 15.1041 18.7511 12.8583 18.7511 10.1944Z"
                    fill="#4285F4"
                  />
                  <path
                    d="M10.1788 18.75C12.5895 18.75 14.6133 17.9722 16.0915 16.6305L13.274 14.4916C12.5201 15.0068 11.5081 15.3666 10.1788 15.3666C7.81773 15.3666 5.81379 13.8402 5.09944 11.7305L4.99473 11.7392L2.23868 13.8295L2.20264 13.9277C3.67087 16.786 6.68674 18.75 10.1788 18.75Z"
                    fill="#34A853"
                  />
                  <path
                    d="M5.10014 11.7305C4.91165 11.186 4.80257 10.6027 4.80257 9.99992C4.80257 9.3971 4.91165 8.81379 5.09022 8.26935L5.08523 8.1534L2.29464 6.02954L2.20333 6.0721C1.5982 7.25823 1.25098 8.5902 1.25098 9.99992C1.25098 11.4096 1.5982 12.7415 2.20333 13.9277L5.10014 11.7305Z"
                    fill="#FBBC05"
                  />
                  <path
                    d="M10.1789 4.63331C11.8554 4.63331 12.9864 5.34303 13.6312 5.93612L16.1511 3.525C14.6035 2.11528 12.5895 1.25 10.1789 1.25C6.68676 1.25 3.67088 3.21387 2.20264 6.07218L5.08953 8.26943C5.81381 6.15972 7.81776 4.63331 10.1789 4.63331Z"
                    fill="#EB4335"
                  />
                </svg>
                Sign in with Google
              </button>
              <button className="inline-flex items-center justify-center gap-3 py-3 text-sm font-normal text-gray-700 transition-colors bg-gray-100 rounded-lg px-7 hover:bg-gray-200 hover:text-gray-800 dark:bg-white/5 dark:text-white/90 dark:hover:bg-white/10">
                <svg
                  width="21"
                  className="fill-current"
                  height="20"
                  viewBox="0 0 21 20"
                  fill="none"
                  xmlns="http://www.w3.org/2000/svg"
                >
                  <path d="M15.6705 1.875H18.4272L12.4047 8.75833L19.4897 18.125H13.9422L9.59717 12.4442L4.62554 18.125H1.86721L8.30887 10.7625L1.51221 1.875H7.20054L11.128 7.0675L15.6705 1.875ZM14.703 16.475H16.2305L6.37054 3.43833H4.73137L14.703 16.475Z" />
                </svg>
                Sign in with Microsoft
              </button>
            </div>
            <div className="relative py-3 sm:py-5">
              <div className="absolute inset-0 flex items-center">
                <div className="w-full border-t border-gray-200 dark:border-gray-800"></div>
              </div>
              <div className="relative flex justify-center text-sm">
                <span className="p-2 text-gray-400 bg-white dark:bg-gray-900 sm:px-5 sm:py-2">
                  Or
                </span>
              </div>
            </div>
            <form onSubmit={handleSignIn}>
              <div className="space-y-6">
                <div>
                  <Label>
                    Username <span className="text-error-500">*</span>{" "}
                  </Label>
                  <Input
                    placeholder="your_username"
                    type="text"
                    onChange={(event) => setUsername(event.target.value)}
                  />
                </div>
                <div>
                  <Label>
                    Password <span className="text-error-500">*</span>{" "}
                  </Label>
                  <div className="relative">
                    <Input
                      type={showPassword ? "text" : "password"}
                      placeholder="Enter your password"
                      onChange={(event) => setPassword(event.target.value)}
                    />
                    <span
                      onClick={() => setShowPassword(!showPassword)}
                      className="absolute z-30 -translate-y-1/2 cursor-pointer right-4 top-1/2"
                    >
                      <span className="text-gray-500 dark:text-gray-400 text-xs font-medium select-none">
                        {showPassword ? "Hide" : "Show"}
                      </span>
                    </span>
                  </div>
                </div>
                {/* [COMMENT]: Khung chọn Zone động với globe icon và custom select styling để tăng tính thẩm mỹ */}
                <div>
                  <Label>
                    Zone / Khu vực <span className="text-error-500">*</span>
                  </Label>
                  <div className="relative">
                    <span className="absolute z-30 -translate-y-1/2 left-4 top-1/2">
                      <svg
                        className="text-gray-400 dark:text-gray-500"
                        width="18"
                        height="18"
                        viewBox="0 0 20 20"
                        fill="none"
                        xmlns="http://www.w3.org/2000/svg"
                      >
                        <path
                          d="M10 1.66663C5.39762 1.66663 1.66666 5.39762 1.66666 10C1.66666 14.6023 5.39762 18.3333 10 18.3333C14.6023 18.3333 18.3333 14.6023 18.3333 10C18.3333 5.39762 14.6023 1.66663 10 1.66663ZM10 3.33329C11.1396 4.90829 11.6667 6.94996 11.6667 9.16663H8.33333C8.33333 6.94996 8.86041 4.90829 10 3.33329ZM10 16.6666C8.86041 15.0916 8.33333 13.05 8.33333 10.8333H11.6667C11.6667 13.05 11.1396 15.0916 10 16.6666ZM3.33333 10C3.33333 9.16663 3.66666 8.33329 4.16666 7.5H6.66666C6.66666 8.33329 6.66666 9.16663 6.66666 10C6.66666 10.8333 6.66666 11.6666 6.66666 12.5H4.16666C3.66666 11.667 3.33333 10.8333 3.33333 10ZM10 10.8333H13.3333C13.3333 13.05 12.8063 15.0916 11.6667 16.6666C12.8063 15.0916 13.3333 13.05 13.3333 10.8333ZM15.8333 12.5H13.3333C13.3333 11.6666 13.3333 10.8333 13.3333 10C13.3333 9.16663 13.3333 8.33329 13.3333 7.5H15.8333C16.3333 8.33329 16.6667 9.16663 16.6667 10C16.6667 10.8333 16.3333 11.667 15.8333 12.5ZM15.8333 7.5C15.3333 6.66663 14.6667 5.83329 13.8333 5.33329C14.6667 5.83329 15.3333 6.66663 15.8333 7.5Z"
                          fill="currentColor"
                        />
                      </svg>
                    </span>

                    {isLoadingZones ? (
                      <div className="h-11 w-full flex items-center justify-between rounded-lg border border-gray-300 bg-gray-50 px-11 text-sm dark:border-gray-700 dark:bg-gray-900 text-gray-400">
                        <span>Đang tải danh mục vùng...</span>
                        <div className="w-4 h-4 border-2 border-brand-500 border-t-transparent rounded-full animate-spin"></div>
                      </div>
                    ) : (
                      <select
                        className="h-11 w-full appearance-none rounded-lg border border-gray-300 bg-white px-11 py-2.5 text-sm shadow-theme-xs text-gray-800 focus:border-brand-300 focus:outline-hidden focus:ring-3 focus:ring-brand-500/10 dark:border-gray-700 dark:bg-gray-900 dark:text-white/90 focus:dark:border-brand-800"
                        value={selectedZone}
                        onChange={(e) => setSelectedZone(e.target.value)}
                      >
                        {zones.length === 0 ? (
                          <option value="" disabled>
                            Không có zone nào hoạt động
                          </option>
                        ) : (
                          zones.map((z) => (
                            <option key={z.id} value={z.code}>
                              {z.name} ({z.code.toUpperCase()})
                            </option>
                          ))
                        )}
                      </select>
                    )}

                    <span className="absolute z-30 -translate-y-1/2 pointer-events-none right-4 top-1/2">
                      <svg
                        className="text-gray-400 dark:text-gray-500"
                        width="16"
                        height="16"
                        viewBox="0 0 16 16"
                        fill="none"
                        xmlns="http://www.w3.org/2000/svg"
                      >
                        <path
                          d="M4 6L8 10L12 6"
                          stroke="currentColor"
                          strokeWidth="1.5"
                          strokeLinecap="round"
                          strokeLinejoin="round"
                        />
                      </svg>
                    </span>
                  </div>
                </div>
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-3">
                    <Checkbox checked={isChecked} onChange={setIsChecked} />
                    <span className="block font-normal text-gray-700 text-theme-sm dark:text-gray-400">
                      trusted device in 30 days
                    </span>
                  </div>
                  <Link
                    href="/reset-password"
                    className="text-sm text-brand-500 hover:text-brand-600 dark:text-brand-400"
                  >
                    Forgot password?
                  </Link>
                </div>
                {errorMessage !== "" && (
                  <p className="text-sm text-error-500">{errorMessage}</p>
                )}
                <div>
                  <Button
                    className="w-full"
                    size="sm"
                    type="submit"
                    disabled={isSubmitting}
                  >
                    {isSubmitting ? "Signing in..." : "Sign in"}
                  </Button>
                </div>
              </div>
            </form>

            <div className="mt-5">
              <p className="text-sm font-normal text-center text-gray-700 dark:text-gray-400 sm:text-start">
                Don&apos;t have an account? {""}
                <Link
                  href="/signup"
                  className="text-brand-500 hover:text-brand-600 dark:text-brand-400"
                >
                  Sign Up
                </Link>
              </p>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
