"use client";

import React from "react";
import { Sun, Moon } from "lucide-react";
import { type ThemeMode } from "@/context/ThemeContext";

interface ThemeSwitcherProps {
  theme: ThemeMode;
  setTheme: (theme: ThemeMode) => void;
}

export function ThemeSwitcher({ theme, setTheme }: ThemeSwitcherProps) {
  return (
    <button
      onClick={() => {
        if (theme === "light") {
          setTheme("dark");
        } else if (theme === "dark") {
          setTheme("light");
        } else {
          const isSystemDark = typeof window !== "undefined" && window.matchMedia("(prefers-color-scheme: dark)").matches;
          setTheme(isSystemDark ? "light" : "dark");
        }
      }}
      className="p-2 rounded-lg hover:bg-slate-100 dark:hover:bg-sidebar-console-hover text-slate-500 dark:text-slate-400 hover:text-slate-900 dark:hover:text-white cursor-pointer transition-colors"
      title={theme === "light" ? "Switch to Dark Mode" : "Switch to Light Mode"}
    >
      {theme === "light" || (theme === "system" && typeof window !== "undefined" && !window.matchMedia("(prefers-color-scheme: dark)").matches) ? (
        <Moon className="h-4 w-4" />
      ) : (
        <Sun className="h-4 w-4" />
      )}
    </button>
  );
}
