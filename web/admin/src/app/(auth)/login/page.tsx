// =====================================================
// Login Page - หน้าเข้าสู่ระบบ
// Fullscreen login with particle background animation
// รองรับ 2FA (TOTP) flow: login → show totp → verify
// Glassmorphism card design + fade-in animation
// =====================================================

'use client'

import { useState } from 'react'
import { useRouter } from 'next/navigation'
import { ParticleBackground } from '@/components/login/particle-bg'
import { LoginForm } from '@/components/login/login-form'
import { login } from '@/lib/auth'
import { useTranslation } from '@/i18n'
import type { LoginRequest } from '@/types/auth'

/**
 * LoginPage - หน้าล็อกอินหลัก
 * Flow:
 * 1. ผู้ใช้กรอก email + password → submit
 * 2. ถ้า backend ตอบ requires_totp → แสดงช่อง OTP
 * 3. ผู้ใช้กรอก OTP → submit อีกครั้ง
 * 4. login สำเร็จ → redirect ไป /dashboard (cookie ถูกตั้งโดย BFF)
 */
export default function LoginPage() {
  const { t } = useTranslation()
  const router = useRouter()

  /** แสดงช่อง TOTP หรือไม่ / Whether TOTP field is visible */
  const [showTotp, setShowTotp] = useState(false)
  /** กำลังโหลด / Loading state */
  const [isLoading, setIsLoading] = useState(false)
  /** ข้อความ error / Error message */
  const [error, setError] = useState<string | null>(null)

  /**
   * handleSubmit - จัดการเมื่อกด submit ฟอร์ม
   * เรียก lib/auth.login() แล้วตรวจผลลัพธ์
   * ถ้า requires_totp → แสดงช่อง OTP
   * ถ้าสำเร็จ → redirect ไป /dashboard
   */
  const handleSubmit = async (data: { email: string; password: string; totp_code?: string }) => {
    setError(null)
    setIsLoading(true)

    try {
      const loginData: LoginRequest = {
        email: data.email,
        password: data.password,
        totp_code: data.totp_code || undefined,
        user_type: 'admin',
      }

      const result = await login(loginData)

      if (result.requires_totp) {
        // ต้องกรอก TOTP → แสดงช่อง OTP / Show TOTP input
        setShowTotp(true)
      } else if (result.user) {
        // login สำเร็จ → ไปหน้า dashboard / Redirect on success
        router.push('/dashboard')
      }
    } catch (err) {
      // แสดง error / Show error message
      setError(err instanceof Error ? err.message : t('auth.invalidCredentials'))
    } finally {
      setIsLoading(false)
    }
  }

  return (
    <div className="relative min-h-screen flex items-center justify-center overflow-hidden bg-[#0a0d1f]">
      {/* ===== Particle Background / พื้นหลังอนุภาค ===== */}
      <div className="absolute inset-0">
        <ParticleBackground />
      </div>

      {/* ===== Login Card / การ์ดล็อกอิน (Glassmorphism) ===== */}
      <div className="relative z-10 w-full max-w-md px-4 animate-fade-in">
        <div className="backdrop-blur-xl bg-white/5 border border-white/10 rounded-2xl p-8 shadow-2xl">
          {/* ===== Logo + Title / โลโก้และชื่อระบบ ===== */}
          <div className="text-center mb-8">
            {/* Logo icon - วงกลม gradient indigo → purple */}
            <div className="mx-auto w-16 h-16 rounded-2xl bg-gradient-to-br from-indigo-500 to-purple-600 flex items-center justify-center mb-4 shadow-lg shadow-indigo-500/25">
              <svg
                className="w-8 h-8 text-white"
                fill="none"
                viewBox="0 0 24 24"
                stroke="currentColor"
                strokeWidth={2}
              >
                <path strokeLinecap="round" strokeLinejoin="round" d="M12 15v2m-6 4h12a2 2 0 002-2v-6a2 2 0 00-2-2H6a2 2 0 00-2 2v6a2 2 0 002 2zm10-10V7a4 4 0 00-8 0v4h8z" />
              </svg>
            </div>
            {/* ชื่อระบบ / System name */}
            <h1 className="text-2xl font-bold text-white">
              {t('auth.loginTitle')}
            </h1>
            {/* คำอธิบาย / Subtitle */}
            <p className="text-white/50 text-sm mt-1">
              {t('auth.loginSubtitle')}
            </p>
          </div>

          {/* ===== Login Form / ฟอร์มล็อกอิน ===== */}
          <LoginForm
            showTotp={showTotp}
            isLoading={isLoading}
            error={error}
            onSubmit={handleSubmit}
          />
        </div>
      </div>
    </div>
  )
}
