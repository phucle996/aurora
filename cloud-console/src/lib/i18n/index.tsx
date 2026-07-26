"use client";

import React, { createContext, useContext, useState, useCallback } from "react";
import en from "./locales/signin/en.json";
import vi from "./locales/signin/vi.json";
import ja from "./locales/signin/ja.json";
import zh from "./locales/signin/zh.json";
import ko from "./locales/signin/ko.json";

// [COMMENT]: Định nghĩa các mã ngôn ngữ được hỗ trợ
export type Language = "en" | "vi" | "ja" | "zh" | "ko";

const locales = { en, vi, ja, zh, ko };

export type TranslationSchema = typeof en;

const I18nContext = createContext<{
  lang: Language;
  setLang: (lang: Language) => void;
  t: TranslationSchema;
} | null>(null);

// [COMMENT]: Nhà cung cấp ngữ cảnh đa ngôn ngữ (i18n) lưu vết lựa chọn qua localStorage
export function I18nProvider({ children }: { children: React.ReactNode }) {
  const [lang, setLangState] = useState<Language>(() => {
    if (typeof window === "undefined") return "en";
    const savedLang = localStorage.getItem("lang") as Language;
    return savedLang && locales[savedLang] ? savedLang : "en";
  });

  const setLang = useCallback((newLang: Language) => {
    if (locales[newLang]) {
      setLangState(newLang);
      localStorage.setItem("lang", newLang);
    }
  }, []);

  const t = locales[lang] || en;

  return (
    <I18nContext.Provider value={{ lang, setLang, t }}>
      {children}
    </I18nContext.Provider>
  );
}

// [COMMENT]: Custom hook để sử dụng tính năng dịch thuật đa ngôn ngữ ở các component con
export function useTranslation() {
  const context = useContext(I18nContext);
  if (!context) {
    throw new Error("useTranslation must be used within an I18nProvider");
  }
  return context;
}
