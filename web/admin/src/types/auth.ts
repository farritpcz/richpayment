// =====================================================
// Auth Types - Type definitions สำหรับระบบ Authentication
// ใช้ร่วมกับ auth-service backend (Go)
// =====================================================

/**
 * LoginRequest - ข้อมูลที่ส่งไปตอน login
 * totp_code เป็น optional เพราะ flow แรกยังไม่ต้องใส่
 * ถ้า backend ตอบ requires_totp จะแสดง input ให้กรอก
 */
export interface LoginRequest {
  /** อีเมลผู้ดูแลระบบ */
  email: string
  /** รหัสผ่าน */
  password: string
  /** รหัส TOTP 6 หลัก (ใส่เมื่อ backend ร้องขอ) */
  totp_code?: string
  /** ประเภทผู้ใช้ - admin dashboard ส่ง 'admin' เสมอ */
  user_type: 'admin'
}

/**
 * LoginResponse - ข้อมูลที่ได้รับหลัง login สำเร็จ
 * ถ้า requires_totp = true แสดงว่าต้องกรอก 2FA ก่อน
 */
export interface LoginResponse {
  /** ข้อมูล user (มีเมื่อ login สำเร็จทั้งหมด) */
  user: User
  /** true = ต้องกรอก TOTP ก่อนเข้าระบบได้ */
  requires_totp?: boolean
}

/**
 * User - ข้อมูลผู้ใช้ที่ login แล้ว
 * role_mask เป็น bitmask สำหรับตรวจสอบสิทธิ์ RBAC
 * ดูค่า bitmask ได้ที่ lib/permissions.ts
 */
export interface User {
  /** UUID ของ user */
  id: string
  /** อีเมล */
  email: string
  /** ชื่อแสดงผล (ภาษาไทยหรืออังกฤษ) */
  display_name: string
  /** ชื่อ role เช่น 'super_admin', 'finance', 'viewer' */
  role: string
  /** Bitmask สิทธิ์ - ใช้กับ PERM constants ใน lib/permissions.ts */
  role_mask: number
  /** ประเภทผู้ใช้ - admin เสมอสำหรับ dashboard นี้ */
  user_type: 'admin'
}
