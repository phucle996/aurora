"use client"

import { useTheme } from "next-themes"
import { Toaster as Sonner, type ToasterProps } from "sonner"
import { CircleCheckIcon, InfoIcon, TriangleAlertIcon, OctagonXIcon, Loader2Icon } from "lucide-react"

const Toaster = ({ ...props }: ToasterProps) => {
  const { theme = "system" } = useTheme()

  return (
    <Sonner
      theme={theme as ToasterProps["theme"]}
      className="toaster group"
      icons={{
        success: (
          <CircleCheckIcon className="size-4 text-[var(--success-icon)]" />
        ),
        info: (
          <InfoIcon className="size-4 text-[var(--info-icon)]" />
        ),
        warning: (
          <TriangleAlertIcon className="size-4 text-[var(--warning-icon)]" />
        ),
        error: (
          <OctagonXIcon className="size-4 text-[var(--error-icon)]" />
        ),
        loading: (
          <Loader2Icon className="size-4 animate-spin text-blue-500" />
        ),
      }}
      style={
        {
          "--normal-bg": "var(--popover)",
          "--normal-text": "var(--popover-foreground)",
          "--normal-border": "var(--border)",
          "--border-radius": "var(--radius)",

          /* [COMMENT]: Định cấu hình các màu sắc riêng biệt cho từng trạng thái Toast */
          "--success-bg": "var(--success-bg)",
          "--success-border": "var(--success-border)",
          "--success-text": "var(--success-text)",

          "--error-bg": "var(--error-bg)",
          "--error-border": "var(--error-border)",
          "--error-text": "var(--error-text)",

          "--warning-bg": "var(--warning-bg)",
          "--warning-border": "var(--warning-border)",
          "--warning-text": "var(--warning-text)",

          "--info-bg": "var(--info-bg)",
          "--info-border": "var(--info-border)",
          "--info-text": "var(--info-text)",
        } as React.CSSProperties
      }
      toastOptions={{
        classNames: {
          toast: "cn-toast",
        },
      }}
      {...props}
    />
  )
}

export { Toaster }
