// =====================================================
// i18n Provider - ระบบสลับภาษา ไทย/อังกฤษ
// ใช้ React Context เก็บ locale + ฟังก์ชัน t() สำหรับแปล
// เก็บค่า locale ลง localStorage เพื่อจำค่าเมื่อ refresh
// =====================================================

'use client'

import { createContext, useContext, useState, useCallback, type ReactNode } from 'react'
import th from './th'
import en from './en'

/** Locale ที่รองรับ - ไทยและอังกฤษ */
type Locale = 'th' | 'en'

/** Type ของ dictionary structure (ใช้ th เป็น reference เพราะเป็นภาษาหลัก) */
type DictionaryShape = {
  [K in keyof typeof th]: {
    [K2 in keyof (typeof th)[K]]: string
  }
}

/** Map locale -> dictionary */
const dictionaries: Record<Locale, DictionaryShape> = { th, en }

/** Interface สำหรับ Context value */
interface I18nContextValue {
  /** ภาษาปัจจุบัน */
  locale: Locale
  /** เปลี่ยนภาษา + บันทึกลง localStorage */
  setLocale: (l: Locale) => void
  /** แปลข้อความจาก key แบบ dot notation เช่น 'sidebar.dashboard' */
  t: (key: string) => string
}

const I18nContext = createContext<I18nContextValue | null>(null)

/**
 * getNestedValue - ดึงค่าจาก object ด้วย dot notation path
 * เช่น getNestedValue(dict, 'sidebar.dashboard') => 'แดชบอร์ด'
 * ถ้าหา key ไม่เจอจะ return key เดิม (fallback)
 *
 * @param obj - dictionary object
 * @param path - dot notation path เช่น 'common.save'
 * @returns ค่าที่หาได้ หรือ path เดิมถ้าหาไม่เจอ
 */
// eslint-disable-next-line @typescript-eslint/no-explicit-any
function getNestedValue(obj: Record<string, unknown>, path: string): string {
  const val = path.split('.').reduce<unknown>((acc, part) => {
    if (acc && typeof acc === 'object' && part in (acc as Record<string, unknown>)) {
      return (acc as Record<string, unknown>)[part]
    }
    return undefined
  }, obj)
  return typeof val === 'string' ? val : path
}

/**
 * I18nProvider - Provider component สำหรับระบบแปลภาษา
 * ครอบ app ทั้งหมดเพื่อให้ทุก component ใช้ t() ได้
 * Default locale: 'th' (ภาษาไทย)
 */
export function I18nProvider({ children }: { children: ReactNode }) {
  // เริ่มต้นด้วยภาษาไทย เป็นค่า default
  const [locale, setLocaleState] = useState<Locale>('th')

  /** เปลี่ยนภาษา + บันทึกลง localStorage เพื่อจำค่า */
  const setLocale = useCallback((l: Locale) => {
    setLocaleState(l)
    if (typeof window !== 'undefined') localStorage.setItem('locale', l)
  }, [])

  /** แปลข้อความจาก key - ใช้ dictionary ของ locale ปัจจุบัน */
  const t = useCallback((key: string) => {
    return getNestedValue(dictionaries[locale] as Record<string, unknown>, key)
  }, [locale])

  return (
    <I18nContext.Provider value={{ locale, setLocale, t }}>
      {children}
    </I18nContext.Provider>
  )
}

/**
 * useTranslation - Hook สำหรับใช้ระบบแปลภาษาใน component
 * ต้องอยู่ภายใต้ I18nProvider เสมอ
 *
 * ตัวอย่างการใช้:
 *   const { t, locale, setLocale } = useTranslation()
 *   <h1>{t('dashboard.title')}</h1>
 *   <button onClick={() => setLocale('en')}>EN</button>
 */
export function useTranslation() {
  const ctx = useContext(I18nContext)
  if (!ctx) throw new Error('useTranslation must be used within I18nProvider')
  return ctx
}

export type { Locale }
