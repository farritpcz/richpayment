// =====================================================
// LoginForm - ฟอร์มล็อกอินเข้าระบบ
// Login form with react-hook-form + zod validation
// รองรับ 2FA (TOTP) flow, glassmorphism design
// =====================================================

'use client'

import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { Loader2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { useTranslation } from '@/i18n'
import { cn } from '@/lib/utils'

// ===== Zod Schema - กำหนดรูปแบบข้อมูลฟอร์ม =====
/**
 * loginSchema - Validation schema สำหรับฟอร์ม login
 * email: ต้องเป็น email format
 * password: ต้องมีอย่างน้อย 6 ตัวอักษร
 * totp_code: optional, ถ้ามีต้อง 6 หลัก
 */
const loginSchema = z.object({
  email: z.string().min(1, 'กรุณากรอกอีเมล').email('รูปแบบอีเมลไม่ถูกต้อง'),
  password: z.string().min(6, 'รหัสผ่านต้องมีอย่างน้อย 6 ตัวอักษร'),
  totp_code: z.string().optional().refine(
    (val) => !val || /^\d{6}$/.test(val),
    { message: 'รหัส OTP ต้องเป็นตัวเลข 6 หลัก' }
  ),
})

/** LoginFormData - ประเภทข้อมูลฟอร์มจาก zod schema */
type LoginFormData = z.infer<typeof loginSchema>

/**
 * LoginFormProps - Props ของ LoginForm component
 */
interface LoginFormProps {
  /** แสดงช่อง TOTP หรือไม่ / Whether to show TOTP input */
  showTotp: boolean
  /** กำลังโหลดหรือไม่ / Loading state */
  isLoading: boolean
  /** ข้อความ error / Error message to display */
  error: string | null
  /** ฟังก์ชันที่เรียกเมื่อ submit / Form submit handler */
  onSubmit: (data: LoginFormData) => void
}

/**
 * LoginForm - Component ฟอร์มล็อกอิน
 * - ใช้ react-hook-form สำหรับจัดการ state ของฟอร์ม
 * - ใช้ zod สำหรับ validation
 * - แสดงช่อง TOTP เฉพาะเมื่อ showTotp = true
 * - มี loading spinner บนปุ่ม submit
 * - Shake animation เมื่อเกิด error
 * - Glassmorphism card style
 */
export function LoginForm({ showTotp, isLoading, error, onSubmit }: LoginFormProps) {
  const { t } = useTranslation()

  const {
    register,
    handleSubmit,
    formState: { errors },
  } = useForm<LoginFormData>({
    resolver: zodResolver(loginSchema),
    defaultValues: {
      email: '',
      password: '',
      totp_code: '',
    },
  })

  return (
    <form
      onSubmit={handleSubmit(onSubmit)}
      className={cn(
        'space-y-6',
        // Shake animation เมื่อมี error / Shake on error
        error && 'animate-shake'
      )}
    >
      {/* ===== Email Field / ช่องอีเมล ===== */}
      <div className="space-y-2">
        <Label htmlFor="email" className="text-white/80 text-sm">
          {t('auth.email')}
        </Label>
        <Input
          id="email"
          type="email"
          placeholder="admin@richpayment.co"
          autoComplete="email"
          disabled={isLoading}
          className="bg-white/10 border-white/20 text-white placeholder:text-white/40
                     focus:border-indigo-400 focus:ring-indigo-400/20"
          {...register('email')}
        />
        {/* แสดง error ถ้า validation ไม่ผ่าน */}
        {errors.email && (
          <p className="text-red-400 text-xs">{errors.email.message}</p>
        )}
      </div>

      {/* ===== Password Field / ช่องรหัสผ่าน ===== */}
      <div className="space-y-2">
        <Label htmlFor="password" className="text-white/80 text-sm">
          {t('auth.password')}
        </Label>
        <Input
          id="password"
          type="password"
          placeholder="••••••••"
          autoComplete="current-password"
          disabled={isLoading}
          className="bg-white/10 border-white/20 text-white placeholder:text-white/40
                     focus:border-indigo-400 focus:ring-indigo-400/20"
          {...register('password')}
        />
        {errors.password && (
          <p className="text-red-400 text-xs">{errors.password.message}</p>
        )}
      </div>

      {/* ===== TOTP Field / ช่องรหัส OTP (แสดงเฉพาะเมื่อต้อง 2FA) ===== */}
      {showTotp && (
        <div className="space-y-2 animate-fade-in-up">
          <Label htmlFor="totp_code" className="text-white/80 text-sm">
            {t('auth.totpCode')}
          </Label>
          <Input
            id="totp_code"
            type="text"
            inputMode="numeric"
            maxLength={6}
            placeholder="000000"
            autoComplete="one-time-code"
            autoFocus
            disabled={isLoading}
            className="bg-white/10 border-white/20 text-white placeholder:text-white/40
                       focus:border-indigo-400 focus:ring-indigo-400/20
                       text-center text-2xl tracking-[0.5em] font-mono"
            {...register('totp_code')}
          />
          {errors.totp_code && (
            <p className="text-red-400 text-xs">{errors.totp_code.message}</p>
          )}
        </div>
      )}

      {/* ===== Error Message / ข้อความ error ===== */}
      {error && (
        <div className="bg-red-500/10 border border-red-500/20 rounded-lg p-3 text-red-400 text-sm text-center">
          {error}
        </div>
      )}

      {/* ===== Submit Button / ปุ่มเข้าสู่ระบบ ===== */}
      <Button
        type="submit"
        disabled={isLoading}
        className="w-full bg-indigo-600 hover:bg-indigo-700 text-white h-11 text-base
                   transition-all duration-200 disabled:opacity-50"
      >
        {isLoading ? (
          <>
            {/* Spinner animation ขณะโหลด */}
            <Loader2 className="mr-2 h-4 w-4 animate-spin" />
            {t('auth.loggingIn')}
          </>
        ) : (
          t('auth.loginButton')
        )}
      </Button>
    </form>
  )
}
