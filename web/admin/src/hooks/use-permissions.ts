// =====================================================
// usePermissions Hook - ตรวจสอบสิทธิ์ RBAC ของ user
// ใช้ร่วมกับ PERM constants จาก lib/permissions.ts
// ซ่อนเมนู/ปุ่มที่ user ไม่มีสิทธิ์เข้าถึง
// =====================================================

'use client'

import { useAuth } from '@/providers/auth-provider'
import { hasPermission, hasAnyPermission } from '@/lib/permissions'

/**
 * usePermissions - Hook สำหรับตรวจสอบสิทธิ์ RBAC
 * ดึง role_mask จาก user ที่ login แล้วมาเช็คกับ PERM bitmask
 *
 * ตัวอย่างการใช้:
 *   const { can, canAny } = usePermissions()
 *
 *   // ซ่อนเมนูถ้าไม่มีสิทธิ์
 *   {can(PERM.VIEW_MERCHANTS) && <MenuItem>ร้านค้า</MenuItem>}
 *
 *   // แสดงปุ่มถ้ามีสิทธิ์อย่างใดอย่างหนึ่ง
 *   {canAny(PERM.APPROVE_WITHDRAWALS, PERM.MANAGE_ORDERS) && <ActionButton />}
 *
 * @returns Object ที่มี can(), canAny(), และ roleMask
 */
export function usePermissions() {
  const { user } = useAuth()
  // ถ้ายังไม่ได้ login, mask = 0 (ไม่มีสิทธิ์ใดๆ)
  const mask = user?.role_mask ?? 0

  return {
    /** ตรวจสอบว่ามีสิทธิ์ที่กำหนดหรือไม่ */
    can: (perm: number) => hasPermission(mask, perm),
    /** ตรวจสอบว่ามีสิทธิ์อย่างน้อย 1 ตัวจากที่กำหนด */
    canAny: (...perms: number[]) => hasAnyPermission(mask, ...perms),
    /** raw bitmask สำหรับกรณีต้องการเช็คเอง */
    roleMask: mask,
  }
}
