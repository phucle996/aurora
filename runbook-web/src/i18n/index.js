import i18n from 'i18next'
import { initReactI18next } from 'react-i18next'
import LanguageDetector from 'i18next-browser-languagedetector'

import enCommon from './locales/en/common.json'
import enHome from './locales/en/home.json'
import enAuth from './locales/en/auth.json'
import enZone from './locales/en/zone.json'
import enRatelimit from './locales/en/ratelimit.json'

import viCommon from './locales/vi/common.json'
import viHome from './locales/vi/home.json'
import viAuth from './locales/vi/auth.json'
import viZone from './locales/vi/zone.json'
import viRatelimit from './locales/vi/ratelimit.json'

import jaCommon from './locales/ja/common.json'
import jaHome from './locales/ja/home.json'
import jaAuth from './locales/ja/auth.json'
import jaZone from './locales/ja/zone.json'
import jaRatelimit from './locales/ja/ratelimit.json'

import koCommon from './locales/ko/common.json'
import koHome from './locales/ko/home.json'
import koAuth from './locales/ko/auth.json'
import koZone from './locales/ko/zone.json'
import koRatelimit from './locales/ko/ratelimit.json'

import zhCommon from './locales/zh/common.json'
import zhHome from './locales/zh/home.json'
import zhAuth from './locales/zh/auth.json'
import zhZone from './locales/zh/zone.json'
import zhRatelimit from './locales/zh/ratelimit.json'

const merge = (...objs) => Object.assign({}, ...objs)

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
      en: { translation: merge(enCommon, enHome, enAuth, enZone, enRatelimit) },
      vi: { translation: merge(viCommon, viHome, viAuth, viZone, viRatelimit) },
      ja: { translation: merge(jaCommon, jaHome, jaAuth, jaZone, jaRatelimit) },
      ko: { translation: merge(koCommon, koHome, koAuth, koZone, koRatelimit) },
      zh: { translation: merge(zhCommon, zhHome, zhAuth, zhZone, zhRatelimit) },
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
