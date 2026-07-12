import type { Metadata } from "next";
import { TooltipProvider } from "@/components/ui/tooltip";
import { I18nProvider } from "@/lib/i18n";
import { Toaster } from "@/components/ui/sonner";
import { UserSessionProvider } from "@/hooks/UserSessionProvider";
import { ThemeProvider } from "@/context/ThemeContext";
import { WorkspaceProvider } from "@/context/WorkspaceContext";
import { WorkspaceInitializer } from "@/components/workspace-initializer";
import { RealtimeProviderWrapper } from "@/context/RealtimeContext";
import QueryProvider from "@/components/providers/QueryProvider";
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
        <QueryProvider>
          {/* [COMMENT]: WorkspaceProvider nằm trong UserSessionProvider để downstream component
              có thể đọc cả session và workspace context qua hook tương ứng */}
          <I18nProvider>
            <UserSessionProvider>
              <WorkspaceProvider>
                {/* [COMMENT]: WorkspaceInitializer là side-effect bridge — không render UI,
                    lắng nghe session change và tự động init/clear workspace catalog */}
                <WorkspaceInitializer />
                <RealtimeProviderWrapper>
                  <ThemeProvider>
                    <TooltipProvider>
                      {children}
                      <Toaster />
                    </TooltipProvider>
                  </ThemeProvider>
                </RealtimeProviderWrapper>
              </WorkspaceProvider>
            </UserSessionProvider>
          </I18nProvider>
        </QueryProvider>
      </body>
    </html>
  );
}

