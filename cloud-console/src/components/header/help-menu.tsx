"use client";

import React, { useState, useRef, useEffect } from "react";
import { HelpCircle } from "lucide-react";

export function HelpMenu() {
  const [helpOpen, setHelpOpen] = useState(false);
  const helpRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const handleClickOutside = (e: MouseEvent) => {
      if (helpRef.current && !helpRef.current.contains(e.target as Node)) {
        setHelpOpen(false);
      }
    };
    document.addEventListener("mousedown", handleClickOutside);
    return () => document.removeEventListener("mousedown", handleClickOutside);
  }, []);

  return (
    <div ref={helpRef} className="relative">
      <button
        onClick={() => setHelpOpen(!helpOpen)}
        className="p-2 rounded-lg hover:bg-slate-100 dark:hover:bg-sidebar-console-hover text-slate-500 dark:text-slate-400 hover:text-slate-900 dark:hover:text-white cursor-pointer transition-colors"
      >
        <HelpCircle className="h-4 w-4" />
      </button>

      {helpOpen && (
        <div className="absolute top-[110%] right-0 w-48 bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-lg shadow-xl py-1.5 z-50 animate-in fade-in slide-in-from-top-1 duration-100">
          {["Documentation", "API Reference", "Release Notes", "Technical Support"].map((item) => (
            <button
              key={item}
              className="w-full text-left px-3 py-1.5 text-xs font-semibold hover:bg-slate-50 dark:hover:bg-slate-800/50 cursor-pointer text-slate-700 dark:text-slate-300"
              onClick={() => setHelpOpen(false)}
            >
              {item}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
