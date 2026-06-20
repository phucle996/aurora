import Link from "next/link";
import { Metadata } from "next";

// [COMMENT]: Khai báo metadata chuẩn SEO cho trang khôi phục mật khẩu.
export const metadata: Metadata = {
  title: "Reset Password | Aurora Cloud",
  description: "Reset password instructions and security guidelines for Aurora Cloud.",
};

export default function ResetPassword() {
  return (
    <div className="flex flex-col items-center justify-center min-h-screen px-4 bg-gray-50 dark:bg-gray-900">
      {/* [COMMENT]: Khung thẻ Card được bo góc tròn, viền tinh tế và có hiệu ứng đổ bóng mượt mà, hỗ trợ dark mode */}
      <div className="w-full max-w-md p-8 bg-white border border-gray-200 rounded-2xl shadow-sm dark:bg-gray-800 dark:border-gray-700">
        <div className="text-center mb-6">
          {/* [COMMENT]: Icon ổ khóa minh họa cho phần bảo mật khôi phục mật khẩu */}
          <div className="inline-flex items-center justify-center w-12 h-12 mb-4 bg-brand-50 rounded-full dark:bg-brand-900/30">
            <svg
              className="w-6 h-6 text-brand-600 dark:text-brand-400"
              fill="none"
              stroke="currentColor"
              viewBox="0 0 24 24"
              xmlns="http://www.w3.org/2000/svg"
            >
              <path
                strokeLinecap="round"
                strokeLinejoin="round"
                strokeWidth={2}
                d="M12 15v2m-6 4h12a2 2 0 002-2v-6a2 2 0 00-2-2H6a2 2 0 00-2 2v6a2 2 0 002 2zm10-10V7a4 4 0 00-8 0v4h8z"
              />
            </svg>
          </div>
          <h1 className="text-2xl font-bold text-gray-950 dark:text-white">
            Forgot Password?
          </h1>
        </div>

        {/* [COMMENT]: Phần giải thích quy trình an toàn bảo mật, hướng dẫn chạy CLI hoặc liên hệ admin */}
        <div className="space-y-4 text-sm text-gray-600 dark:text-gray-400">
          <p>
            Để đảm bảo an toàn bảo mật trong môi trường Cloud-Native, tính năng tự động khôi phục mật khẩu qua email bị tắt theo cấu hình mặc định.
          </p>
          <div className="p-4 bg-gray-50 border border-gray-150 rounded-lg dark:bg-gray-950 dark:border-gray-850">
            <span className="block font-medium text-xs text-gray-500 uppercase mb-2 dark:text-gray-400">
              Quản trị viên hệ thống (CLI)
            </span>
            <code className="block text-xs font-mono text-brand-600 dark:text-brand-400 break-all select-all">
              aurora-cli iam users reset-password --user &lt;username&gt;
            </code>
          </div>
          <p className="text-xs leading-relaxed text-gray-500 dark:text-gray-500">
            Vui lòng liên hệ với Quản trị viên hệ thống (DevOps / IT Administrator) của doanh nghiệp bạn để được cấp lại mật khẩu hoặc cung cấp mã khôi phục tạm thời.
          </p>
        </div>

        {/* [COMMENT]: Nút bấm quay trở về trang đăng nhập */}
        <div className="mt-6 border-t border-gray-100 pt-6 dark:border-gray-700">
          <Link
            href="/signin"
            className="flex items-center justify-center w-full px-4 py-2.5 text-sm font-semibold text-white bg-brand-500 rounded-lg hover:bg-brand-600 transition-colors text-center"
          >
            Back to Sign In
          </Link>
        </div>
      </div>
    </div>
  );
}
