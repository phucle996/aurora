import type { Metadata } from "next";
import { TooltipProvider } from "@/components/ui/tooltip";
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
        <TooltipProvider>{children}</TooltipProvider>
      </body>
    </html>
  );
}
