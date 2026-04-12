// =====================================================
// Root Layout - โครงสร้างหลักของแอปพลิเคชัน
// ตั้งค่า font (Sarabun), theme, providers ทั้งหมด
// Font: Sarabun สำหรับภาษาไทย+อังกฤษ
// Providers: ThemeProvider, QueryProvider, I18nProvider, AuthProvider
// =====================================================

import type { Metadata } from 'next'
import { Sarabun } from 'next/font/google'
import { ThemeProvider } from '@/providers/theme-provider'
import { QueryProvider } from '@/providers/query-provider'
import { I18nProvider } from '@/providers/i18n-provider'
import { AuthProvider } from '@/providers/auth-provider'
import { cn } from '@/lib/utils'
import './globals.css'

/**
 * sarabun - Font Sarabun จาก Google Fonts
 * ใช้สำหรับทั้งภาษาไทยและอังกฤษ
 * น้ำหนัก: 400 (ปกติ), 500 (กลาง), 600 (หนา), 700 (หนามาก)
 */
const sarabun = Sarabun({
  subsets: ['thai', 'latin'],
  weight: ['400', '500', '600', '700'],
  variable: '--font-sans',
  display: 'swap',
})

/**
 * metadata - ข้อมูล SEO ของเว็บ
 * ชื่อ: RichPayment Admin
 * คำอธิบาย: ระบบจัดการธุรกรรมสำหรับผู้ดูแล
 */
export const metadata: Metadata = {
  title: 'RichPayment Admin',
  description: 'ระบบจัดการธุรกรรม RichPayment สำหรับผู้ดูแลระบบ',
}

/**
 * RootLayout - Layout หลักที่ครอบทุกหน้า
 * ตั้งค่า:
 * 1. HTML lang="th" + suppressHydrationWarning (สำหรับ next-themes)
 * 2. Font Sarabun
 * 3. Providers ซ้อนกัน: Theme → Query → I18n → Auth → Content
 */
export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode
}>) {
  return (
    <html lang="th" className={cn('font-sans', sarabun.variable)} suppressHydrationWarning>
      <body className={cn(sarabun.className, 'antialiased')}>
        {/* ThemeProvider - จัดการ Dark/Light mode */}
        <ThemeProvider>
          {/* QueryProvider - React Query สำหรับ data fetching */}
          <QueryProvider>
            {/* I18nProvider - โหลดภาษาที่เลือกไว้ */}
            <I18nProvider>
              {/* AuthProvider - จัดการ authentication + session */}
              <AuthProvider>
                {children}
              </AuthProvider>
            </I18nProvider>
          </QueryProvider>
        </ThemeProvider>
      </body>
    </html>
  )
}
