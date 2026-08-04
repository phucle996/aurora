"use client";

import { useEffect } from "react";
import { usePathname, useRouter } from "next/navigation";
import { toast } from "sonner";

import {
  Command,
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command";
import { useUserSession } from "@/session/use-session";
import { billingStartURL, personalConsoleNavigation, tenantConsoleNavigation, type ConsoleKind, type NavigationItem } from "@/shell/navigation";

export function ConsoleCommandPalette({ kind, open, onOpenChange }: { kind: ConsoleKind; open: boolean; onOpenChange: (open: boolean) => void }) {
  const router = useRouter();
  const pathname = usePathname();
  const { checkPermission } = useUserSession();
  const items = kind === "personal"
    ? personalConsoleNavigation(checkPermission)
    : tenantConsoleNavigation(checkPermission);

  useEffect(() => {
    const handleShortcut = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") {
        event.preventDefault();
        onOpenChange(!open);
      }
    };
    window.addEventListener("keydown", handleShortcut);
    return () => window.removeEventListener("keydown", handleShortcut);
  }, [onOpenChange, open]);

  const select = (item: NavigationItem) => {
    onOpenChange(false);
    if (item.external === "billing") {
      const target = billingStartURL();
      if (!target) {
        toast.error("Cost Console is not configured for this deployment.");
        return;
      }
      window.location.assign(target);
      return;
    }
    if (item.path && item.path !== pathname) router.push(item.path);
  };

  return (
    <CommandDialog open={open} onOpenChange={onOpenChange} className="sm:max-w-xl">
      <Command>
        <CommandInput placeholder="Search Console destinations…" />
        <CommandList>
          <CommandEmpty>No permitted destination found.</CommandEmpty>
          <CommandGroup heading="Navigation">
            {items.map((item) => {
              const Icon = item.icon;
              return (
                <CommandItem key={item.id} value={item.label} onSelect={() => select(item)}>
                  <Icon />
                  <span>{item.label}</span>
                </CommandItem>
              );
            })}
          </CommandGroup>
        </CommandList>
      </Command>
    </CommandDialog>
  );
}
