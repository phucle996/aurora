import type { Metadata } from "next";
import { TooltipProvider } from "@/components/ui/tooltip";
import { I18nProvider } from "@/lib/i18n";
import { Toaster } from "@/components/ui/sonner";
import { UserSessionProvider } from "@/hooks/UserSessionProvider";
import "@fontsource-variable/inter";
import "./globals.css";

export const metadata: Metadata = {
  title: "Aurora Cloud Console",
  description: "Manage your workloads and infrastructure",
};

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html
      lang="en"
      className="h-full antialiased"
    >
      <body className="min-h-full flex flex-col">
        {/* [COMMENT]: Bọc ứng dụng trong I18nProvider & UserSessionProvider để đồng bộ hoá ngôn ngữ và phiên làm việc */}
        <I18nProvider>
          <UserSessionProvider>
            <TooltipProvider>
              {children}
              <Toaster />
            </TooltipProvider>
          </UserSessionProvider>
        </I18nProvider>
      </body>
    </html>
  );
}
