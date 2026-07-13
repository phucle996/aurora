"use client";

import React, { useState, useRef, useEffect } from "react";
import { ChevronDown, User, Key, Sliders, LogOut } from "lucide-react";
import { cn } from "@/lib/utils";

interface UserProfileProps {
  profile: any;
  handleLogout: () => void;
}

export function UserProfile({ profile, handleLogout }: UserProfileProps) {
  const [userOpen, setUserOpen] = useState(false);
  const userRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const handleClickOutside = (e: MouseEvent) => {
      if (userRef.current && !userRef.current.contains(e.target as Node)) {
        setUserOpen(false);
      }
    };
    document.addEventListener("mousedown", handleClickOutside);
    return () => document.removeEventListener("mousedown", handleClickOutside);
  }, []);

  return (
    <div ref={userRef} className="relative">
      <button
        onClick={() => setUserOpen(!userOpen)}
        className="flex items-center gap-2.5 px-2 py-1.5 rounded-lg hover:bg-slate-100 dark:hover:bg-sidebar-console-hover cursor-pointer transition-colors text-left"
      >
        {profile?.avatar_url ? (
          <img
            src={profile.avatar_url}
            alt="User Avatar"
            className="h-7 w-7 rounded-full object-cover shrink-0 shadow-inner"
          />
        ) : (
          <div className="h-7 w-7 rounded-full bg-blue-600 shrink-0 text-white flex items-center justify-center font-bold text-xs shadow-inner uppercase">
            {profile?.fullname ? profile.fullname.slice(0, 2) : "US"}
          </div>
        )}
        <div className="hidden xl:flex flex-col select-none">
          <span className="text-xs font-bold text-slate-800 dark:text-slate-200 leading-tight">
            {profile?.fullname || "Loading Profile..."}
          </span>
          <span className="text-[9px] text-slate-400 dark:text-slate-500 font-semibold leading-none">
            {profile?.bio || "Platform Member"}
          </span>
        </div>
        <ChevronDown className="h-3 w-3 text-slate-400 hidden sm:block shrink-0" />
      </button>

      {userOpen && (
        <div className="absolute top-[110%] right-0 w-52 bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-lg shadow-xl py-1.5 z-50 animate-in fade-in slide-in-from-top-1 duration-100">
          <div className="px-3 py-2 border-b border-slate-100 dark:border-slate-800 mb-1 xl:hidden">
            <p className="text-xs font-bold text-slate-800 dark:text-slate-200">
              {profile?.fullname || "Loading Profile..."}
            </p>
            <p className="text-[10px] text-slate-400 dark:text-slate-500">
              {profile?.bio || "Platform Member"}
            </p>
          </div>

          <button
            className="w-full flex items-center gap-2 px-3 py-2 text-xs font-semibold text-slate-700 dark:text-slate-300 hover:bg-slate-50 dark:hover:bg-slate-800/50 cursor-pointer text-left"
            onClick={() => setUserOpen(false)}
          >
            <User className="h-4.5 w-4.5 text-slate-400" />
            <span>My Profile</span>
          </button>
          <button
            className="w-full flex items-center gap-2 px-3 py-2 text-xs font-semibold text-slate-700 dark:text-slate-300 hover:bg-slate-50 dark:hover:bg-slate-800/50 cursor-pointer text-left"
            onClick={() => setUserOpen(false)}
          >
            <Key className="h-4.5 w-4.5 text-slate-400" />
            <span>API Keys</span>
          </button>
          <button
            className="w-full flex items-center gap-2 px-3 py-2 text-xs font-semibold text-slate-700 dark:text-slate-300 hover:bg-slate-50 dark:hover:bg-slate-800/50 cursor-pointer text-left"
            onClick={() => setUserOpen(false)}
          >
            <Sliders className="h-4.5 w-4.5 text-slate-400" />
            <span>Preferences</span>
          </button>

          <div className="h-px bg-slate-100 dark:bg-slate-800 my-1" />

          <button
            className="w-full flex items-center gap-2 px-3 py-2 text-xs font-semibold text-red-600 dark:text-red-400 hover:bg-red-50 dark:hover:bg-red-950/20 cursor-pointer text-left"
            onClick={() => {
              setUserOpen(false);
              handleLogout();
            }}
          >
            <LogOut className="h-4.5 w-4.5" />
            <span>Log Out</span>
          </button>
        </div>
      )}
    </div>
  );
}
