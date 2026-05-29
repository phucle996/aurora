import i18n from 'i18next'
import { initReactI18next } from 'react-i18next'
import LanguageDetector from 'i18next-browser-languagedetector'

import en from './locales/en.json'
import vi from './locales/vi.json'
import ja from './locales/ja.json'
import ko from './locales/ko.json'
import zh from './locales/zh.json'

export const SUPPORTED_LANGUAGES = [
  { code: 'en', label: 'English', flag: '🇬🇧' },
  { code: 'vi', label: 'Tiếng Việt', flag: '🇻🇳' },
  { code: 'ja', label: '日本語', flag: '🇯🇵' },
  { code: 'ko', label: '한국어', flag: '🇰🇷' },
  { code: 'zh', label: '中文', flag: '🇨🇳' },
]

i18n
  .use(LanguageDetector)
  .use(initReactI18next)
  .init({
    resources: {
      en: { translation: en },
      vi: { translation: vi },
      ja: { translation: ja },
      ko: { translation: ko },
      zh: { translation: zh },
    },
    fallbackLng: 'en',
    supportedLngs: ['en', 'vi', 'ja', 'ko', 'zh'],
    detection: {
      order: ['localStorage', 'navigator'],
      lookupLocalStorage: 'aurora-runbook-lang',
      caches: ['localStorage'],
    },
    interpolation: { escapeValue: false },
  })

export default i18n
