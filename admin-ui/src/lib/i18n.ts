import i18n from 'i18next'
import { initReactI18next } from 'react-i18next'
import HttpBackend from 'i18next-http-backend'
import LanguageDetector from 'i18next-browser-languagedetector'

void i18n
  .use(HttpBackend)
  .use(LanguageDetector)
  .use(initReactI18next)
  .init({
    fallbackLng: 'en',
    supportedLngs: ['vi', 'en', 'zh-CN', 'hi', 'ja', 'ko'],
    detection: {
      order: ['localStorage', 'navigator'],
      lookupLocalStorage: 'adminui-language',
      caches: ['localStorage'],
    },
    ns: ['common', 'auth'],
    defaultNS: 'common',
    backend: {
      loadPath: '/locales/{{lng}}/{{ns}}.json',
    },
    interpolation: {
      escapeValue: false, // React is already safe from XSS
    },
  })

export default i18n
