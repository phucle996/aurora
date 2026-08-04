import type { Metadata } from "next";
import { TooltipProvider } from "@/components/ui/tooltip";
import { I18nProvider } from "@/lib/i18n";
import { Toaster } from "@/components/ui/sonner";
import { UserSessionProvider } from "@/session/provider";
import { ThemeProvider } from "@/context/ThemeContext";
import { RealtimeProviderWrapper } from "@/realtime/provider";
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
          <I18nProvider>
            <UserSessionProvider>
              <RealtimeProviderWrapper>
                <ThemeProvider>
                  <TooltipProvider>
                    {children}
                    <Toaster />
                  </TooltipProvider>
                </ThemeProvider>
              </RealtimeProviderWrapper>
            </UserSessionProvider>
          </I18nProvider>
        </QueryProvider>
      </body>
    </html>
  );
}
