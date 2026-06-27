import { useEffect, useState } from 'react'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { Cloud, Shield, ShieldCheck, Users, Zap } from 'lucide-react'

import bgLogin from '../../assets/image.png'
import bgLoginDark from '../../assets/login-dark.png'
import LanguageSwitcher from '@/components/layout/language-switcher'
import ThemeSwitcher from '@/components/layout/theme-switcher'

interface AuthLayoutProps {
  children: ReactNode
}

export default function AuthLayout({ children }: AuthLayoutProps) {
  const { t, i18n: i18nInstance } = useTranslation('auth')

  const [theme, setTheme] = useState<'light' | 'dark'>(() => {
    if (typeof document !== 'undefined') {
      return document.documentElement.classList.contains('dark') ? 'dark' : 'light'
    }
    return 'light'
  })

  useEffect(() => {
    const observer = new MutationObserver(() => {
      const isDark = document.documentElement.classList.contains('dark')
      setTheme(isDark ? 'dark' : 'light')
    })

    observer.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ['class'],
    })

    return () => observer.disconnect()
  }, [])

  return (
    <div className="relative min-h-screen w-full overflow-hidden bg-[#e9eef7] text-foreground transition-colors duration-500 ease-[cubic-bezier(0.22,1,0.36,1)] dark:bg-[#0b1220]">
      {/* Decoupled Light Background Illustration Layer */}
      <div
        className="absolute inset-0 z-0 bg-cover bg-center bg-no-repeat transition-opacity duration-500 ease-[cubic-bezier(0.22,1,0.36,1)]"
        style={{
          backgroundImage: `url(${bgLogin})`,
          opacity: theme === 'light' ? 1 : 0,
        }}
      />

      {/* Decoupled Dark Background Illustration Layer */}
      <div
        className="absolute inset-0 z-0 bg-cover bg-center bg-no-repeat transition-opacity duration-500 ease-[cubic-bezier(0.22,1,0.36,1)]"
        style={{
          backgroundImage: `url(${bgLoginDark})`,
          opacity: theme === 'dark' ? 0.68 : 0,
          filter: 'brightness(0.9) saturate(0.8)',
        }}
      />

      {/* Ambient Blue Gradients to blend layout only in Dark Mode */}
      <div
        className="absolute inset-0 z-0 pointer-events-none transition-opacity duration-500 ease-[cubic-bezier(0.22,1,0.36,1)]"
        style={{
          backgroundImage: `radial-gradient(circle at 20% 20%, rgba(37, 99, 235, 0.14), transparent 32%), radial-gradient(circle at 80% 30%, rgba(14, 165, 233, 0.10), transparent 28%)`,
          opacity: theme === 'dark' ? 1 : 0,
        }}
      />

      <div className="relative z-10 flex min-h-screen w-full flex-col px-5 pb-8 pt-6 sm:px-12 md:px-20 xl:px-37.5 xl:pb-10">
        <header className="flex flex-wrap items-center justify-between gap-4">
          <div className="flex items-center gap-3 text-[#0f172a] dark:text-[#F8FAFC]">
            <Cloud className="h-10 w-10 text-primary dark:text-[#60A5FA]" strokeWidth={2.2} />
            <p className="text-2xl font-bold sm:text-[40px]">
              Aurora <span className="text-primary dark:text-[#60A5FA]">Cloud</span>
            </p>
          </div>

          <div className="flex items-center gap-3">
            <div className="inline-flex items-center gap-2 rounded-md border border-[#d7e1f0] bg-white/90 px-4 py-2 text-sm font-semibold text-[#5f6e86] shadow-sm backdrop-blur dark:border-slate-700/30 dark:bg-[#0f172a]/70 dark:text-[#CBD5E1] dark:shadow-[0_8px_24px_rgba(0,0,0,0.22)]">
              <ShieldCheck className="h-4 w-4 text-[#5f6e86] dark:text-[#CBD5E1]" />
              {t('socInfo')}
            </div>
          </div>
        </header>

        <main className="mt-8 grid flex-1 gap-8 xl:grid-cols-[minmax(0,1fr)_540px] xl:gap-12">
          <section className="flex flex-col">
            <div className="max-w-140">
              <div className="mt-10 space-y-6 sm:mt-20">
                <h1 className="m-0 text-[19px] leading-[1.2] text-[#0f172a] sm:text-[25px] xl:text-[29px] dark:text-[#F8FAFC]">
                  <span className="font-semibold">{t('headingStart')}</span>
                  <span className="font-semibold text-primary dark:text-[#60A5FA]">{t('headingEnd')}</span>
                </h1>
                <p className="max-w-120 pt-3 text-[14px] leading-relaxed text-[#5f6e86] sm:text-[15px] xl:text-[16px] dark:text-[#CBD5E1]">
                  {t('subHeading')}
                </p>
              </div>

              <div className="mt-10 divide-y divide-[#dfe6f2] backdrop-blur sm:mt-20 sm:px-4 dark:divide-slate-700/20">
                <div className="flex items-center gap-6 py-6">
                  <div className="flex h-14 w-14 shrink-0 items-center justify-center rounded-2xl border border-[#dbe6f7] bg-[#eef5ff] text-primary shadow-sm sm:h-20 sm:w-20 dark:border-slate-700/20 dark:bg-[#0f172a]/70 dark:text-[#60A5FA]">
                    <Shield className="size-6 sm:size-10" strokeWidth={1.8} />
                  </div>
                  <div className="space-y-1">
                    <p className="text-[20px] font-bold text-[#0f172a] dark:text-[#F8FAFC]">{t('feature1Title')}</p>
                    <p className="text-base leading-relaxed text-[#5f6e86] dark:text-[#CBD5E1]">
                      {t('feature1Desc')}
                    </p>
                  </div>
                </div>

                <div className="flex items-center gap-6 py-6">
                  <div className="flex h-14 w-14 shrink-0 items-center justify-center rounded-2xl border border-[#dbe6f7] bg-[#eef5ff] text-primary shadow-sm sm:h-20 sm:w-20 dark:border-slate-700/20 dark:bg-[#0f172a]/70 dark:text-[#60A5FA]">
                    <Zap className="size-6 sm:size-10" strokeWidth={1.8} />
                  </div>
                  <div className="space-y-1">
                    <p className="text-[20px] font-bold text-[#0f172a] dark:text-[#F8FAFC]">{t('feature2Title')}</p>
                    <p className="text-base leading-relaxed text-[#5f6e86] dark:text-[#CBD5E1]">
                      {t('feature2Desc')}
                    </p>
                  </div>
                </div>

                <div className="flex items-center gap-6 py-6">
                  <div className="flex h-14 w-14 shrink-0 items-center justify-center rounded-2xl border border-[#dbe6f7] bg-[#eef5ff] text-primary shadow-sm sm:h-20 sm:w-20 dark:border-slate-700/20 dark:bg-[#0f172a]/70 dark:text-[#60A5FA]">
                    <Users className="size-6 sm:size-10" strokeWidth={1.8} />
                  </div>
                  <div className="space-y-1">
                    <p className="text-[20px] font-bold text-[#0f172a] dark:text-[#F8FAFC]">{t('feature3Title')}</p>
                    <p className="text-base leading-relaxed text-[#5f6e86] dark:text-[#CBD5E1]">
                      {t('feature3Desc')}
                    </p>
                  </div>
                </div>
              </div>
            </div>

            <div className="mt-8 md:mt-10 xl:mt-auto">
              <div className="inline-flex w-fit items-center gap-3 rounded-xl border border-[#dfe8f4] bg-white px-4 py-3 shadow-sm dark:border-slate-700/20 dark:bg-[#0f172a]/70">
                <Users className="h-5 w-5 text-primary dark:text-[#60A5FA]" />
                <span className="text-sm font-medium text-[#5f6e86] dark:text-[#CBD5E1]">
                  {i18nInstance.language === 'vi' ? (
                    <>
                      Được sử dụng bởi hơn <span className="font-bold text-primary dark:text-[#60A5FA]">100+</span> tổ chức trong vận hành
                    </>
                  ) : (
                    <>
                      Used by <span className="font-bold text-primary dark:text-[#60A5FA]">100+</span> organizations in Production
                    </>
                  )}
                </span>
              </div>
            </div>
          </section>

          <section className="flex flex-col justify-center">
            {children}
          </section>
        </main>
      </div>

      <div className="fixed bottom-6 right-6 z-50 flex items-center gap-3">
        <ThemeSwitcher className="h-11 w-11 rounded-full border border-sky-200/80 bg-white/90 text-sky-700 shadow-[0_10px_30px_-18px_rgba(14,116,144,0.7)] transition-all duration-300 hover:scale-[1.03] hover:border-sky-300 hover:bg-sky-50 hover:shadow-[0_14px_34px_-18px_rgba(2,132,199,0.65)] active:scale-95 dark:border-slate-700/40 dark:bg-slate-900/90 dark:text-slate-300 dark:shadow-[0_8px_24px_rgba(0,0,0,0.22)] dark:hover:border-slate-600 dark:hover:bg-slate-800" />
        <LanguageSwitcher />
      </div>
    </div>
  )
}
