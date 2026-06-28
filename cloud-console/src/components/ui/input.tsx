import * as React from "react"
import { Input as InputPrimitive } from "@base-ui/react/input"

import { cn } from "@/lib/utils"

function Input({ className, type, ...props }: React.ComponentProps<"input">) {
  return (
    <InputPrimitive
      type={type}
      data-slot="input"
      className={cn(
        /* [COMMENT]: Kiểu dáng ô input được tinh chỉnh: viền mảnh 1px #D1D5DB, focus border màu xanh dương #2563EB và ring xanh dương mờ */
        "h-9 w-full min-w-0 rounded-lg border border-slate-300 bg-white px-3 py-1.5 text-sm transition-all outline-none file:inline-flex file:h-6 file:border-0 file:bg-transparent file:text-sm file:font-medium file:text-foreground placeholder:text-slate-400 focus-visible:border-blue-600 focus-visible:ring-4 focus-visible:ring-blue-600/15 disabled:pointer-events-none disabled:cursor-not-allowed disabled:bg-slate-50 disabled:opacity-50 aria-invalid:border-destructive aria-invalid:ring-4 aria-invalid:ring-destructive/20 md:text-sm dark:border-slate-800 dark:bg-slate-950/40 dark:placeholder:text-slate-500 dark:focus-visible:border-blue-500 dark:focus-visible:ring-blue-500/20 dark:disabled:bg-slate-900 dark:aria-invalid:border-destructive/50 dark:aria-invalid:ring-destructive/40",
        className
      )}
      {...props}
    />
  )
}

export { Input }
