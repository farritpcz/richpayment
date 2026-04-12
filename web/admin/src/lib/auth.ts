// =====================================================
// Auth Functions - ฟังก์ชันจัดการ Authentication
// เรียก API routes ผ่าน BFF (Next.js API Routes)
// ไม่เรียก backend โดยตรง เพื่อป้องกัน CORS + ซ่อน session
// =====================================================

import { apiFetch } from './api-client'
import type { LoginRequest, LoginResponse, User } from '@/types/auth'

/**
 * login - เข้าสู่ระบบ
 * ส่ง email + password + totp_code (ถ้ามี) ไป BFF
 * BFF จะ proxy ไปยัง auth-service แล้วตั้ง HttpOnly cookie
 *
 * @param data - ข้อมูล login (email, password, totp_code?)
 * @returns LoginResponse พร้อม user data หรือ requires_totp flag
 */
export async function login(data: LoginRequest): Promise<LoginResponse> {
  return apiFetch<LoginResponse>('/api/auth/login', {
    method: 'POST',
    body: JSON.stringify(data),
  })
}

/**
 * logout - ออกจากระบบ
 * เรียก BFF ให้ลบ session จาก backend + clear cookie
 */
export async function logout(): Promise<void> {
  await apiFetch('/api/auth/logout', { method: 'POST' })
}

/**
 * validateSession - ตรวจสอบว่า session ยังใช้ได้หรือไม่
 * เรียกตอนโหลดหน้าเพื่อเช็ค cookie ที่มีอยู่
 * ถ้า session หมดอายุจะ return null (ไม่ throw error)
 *
 * @returns User object ถ้า session ยังใช้ได้, null ถ้าหมดอายุ
 */
export async function validateSession(): Promise<User | null> {
  try {
    return await apiFetch<User>('/api/auth/validate', { method: 'POST' })
  } catch {
    return null
  }
}
