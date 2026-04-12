// =====================================================
// Auth Provider - จัดการ Authentication State
// ตรวจสอบ session ตอนโหลดหน้า + redirect ถ้าไม่ได้ login
// Session timeout: 1 ชั่วโมง (ตั้งค่าที่ cookie maxAge)
// =====================================================

'use client'

import { createContext, useContext, useEffect, useState, useCallback, type ReactNode } from 'react'
import { useRouter, usePathname } from 'next/navigation'
import type { User } from '@/types/auth'
import { validateSession, logout as logoutFn } from '@/lib/auth'

/** Interface สำหรับ Auth Context value */
interface AuthContextValue {
  /** ข้อมูล user ที่ login อยู่ (null = ยังไม่ได้ login หรือกำลังโหลด) */
  user: User | null
  /** true ขณะกำลังตรวจสอบ session (แสดง loading UI) */
  isLoading: boolean
  /** ฟังก์ชัน logout - ลบ session + redirect ไป login */
  logout: () => Promise<void>
}

const AuthContext = createContext<AuthContextValue | null>(null)

/**
 * AuthProvider - ครอบ app เพื่อจัดการ authentication
 *
 * Flow:
 * 1. โหลดหน้า -> เรียก validateSession() ตรวจสอบ cookie
 * 2. ถ้า session valid -> เก็บ user data ใน state
 * 3. ถ้า session invalid -> redirect ไป /login
 * 4. หน้า /login ไม่ต้องตรวจสอบ session
 */
export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<User | null>(null)
  const [isLoading, setIsLoading] = useState(true)
  const router = useRouter()
  const pathname = usePathname()

  useEffect(() => {
    // ข้าม validation ในหน้า login (ไม่งั้น redirect วนลูป)
    if (pathname === '/login') {
      setIsLoading(false)
      return
    }

    // ตรวจสอบ session กับ backend ผ่าน BFF
    validateSession().then((u) => {
      if (u) {
        setUser(u)
      } else {
        // Session หมดอายุหรือไม่มี cookie -> ไป login
        router.replace('/login')
      }
      setIsLoading(false)
    })
  }, [pathname, router])

  /** Logout - เรียก API ลบ session + clear state + redirect */
  const logout = useCallback(async () => {
    await logoutFn()
    setUser(null)
    router.replace('/login')
  }, [router])

  return (
    <AuthContext.Provider value={{ user, isLoading, logout }}>
      {children}
    </AuthContext.Provider>
  )
}

/**
 * useAuth - Hook สำหรับเข้าถึง auth state ใน component
 * ต้องอยู่ภายใต้ AuthProvider เสมอ
 *
 * ตัวอย่างการใช้:
 *   const { user, isLoading, logout } = useAuth()
 *   if (isLoading) return <Loading />
 *   if (!user) return null // จะ redirect อัตโนมัติ
 */
export function useAuth() {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error('useAuth must be used within AuthProvider')
  return ctx
}
