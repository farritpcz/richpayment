// =====================================================
// Theme Provider - จัดการ Dark/Light mode
// ใช้ next-themes สำหรับ toggle theme
// Default: dark mode (ตาม design spec - Slate BG #0F172A)
// =====================================================

'use client'

import { ThemeProvider as NextThemesProvider } from 'next-themes'
import { type ReactNode } from 'react'

/**
 * ThemeProvider - ครอบ app เพื่อให้ใช้ dark/light mode ได้
 * - attribute="class" ใช้ class strategy (Tailwind dark:)
 * - defaultTheme="dark" เริ่มต้นเป็น dark mode
 * - enableSystem=false ไม่ใช้ system preference
 * - disableTransitionOnChange ป้องกัน flash ตอนสลับ theme
 */
export function ThemeProvider({ children }: { children: ReactNode }) {
  return (
    <NextThemesProvider
      attribute="class"
      defaultTheme="dark"
      enableSystem={false}
      disableTransitionOnChange
    >
      {children}
    </NextThemesProvider>
  )
}
