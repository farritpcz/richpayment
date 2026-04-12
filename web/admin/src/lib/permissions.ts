// =====================================================
// RBAC Permission Constants - ค่าคงที่สิทธิ์การเข้าถึง
// Mirrors: services/auth/internal/service/rbac.go
// ใช้ bitmask เพื่อเก็บสิทธิ์หลายตัวใน field เดียว (role_mask)
// =====================================================

/**
 * PERM - Permission bitmask constants
 * ค่าคงที่สิทธิ์แบบ bitmask ตรงกับ backend Go service
 * แต่ละสิทธิ์ใช้ 1 bit ทำให้เก็บ 21 สิทธิ์ใน integer เดียว
 *
 * ตัวอย่างการใช้:
 *   - Super Admin: role_mask = 0x1FFFFF (ทุก bit เปิด)
 *   - View Only:   role_mask = PERM.VIEW_MERCHANTS | PERM.VIEW_ORDERS
 *   - Finance:     role_mask = PERM.VIEW_ORDERS | PERM.APPROVE_WITHDRAWALS | PERM.VIEW_WALLETS
 */
export const PERM = {
  // === Merchant Management - จัดการร้านค้า ===
  VIEW_MERCHANTS: 1 << 0,       // Bit 0  - ดูรายชื่อร้านค้า
  CREATE_MERCHANTS: 1 << 1,     // Bit 1  - สร้างร้านค้าใหม่
  EDIT_MERCHANTS: 1 << 2,       // Bit 2  - แก้ไขข้อมูลร้านค้า

  // === Agent Management - จัดการตัวแทน ===
  VIEW_AGENTS: 1 << 3,          // Bit 3  - ดูรายชื่อตัวแทน
  CREATE_AGENTS: 1 << 4,        // Bit 4  - สร้างตัวแทนใหม่
  EDIT_AGENTS: 1 << 5,          // Bit 5  - แก้ไขข้อมูลตัวแทน

  // === Order Management - จัดการรายการ ===
  VIEW_ORDERS: 1 << 6,          // Bit 6  - ดูรายการ deposit/withdrawal
  MANAGE_ORDERS: 1 << 7,        // Bit 7  - จัดการรายการ (callback, retry)

  // === Withdrawal & Emergency - ถอนเงินและฉุกเฉิน ===
  APPROVE_WITHDRAWALS: 1 << 8,  // Bit 8  - อนุมัติ/ปฏิเสธการถอนเงิน (ต้อง 2FA)
  EMERGENCY_FREEZE: 1 << 9,     // Bit 9  - หยุดระบบฉุกเฉิน (ปุ่มแดงใน Header)

  // === Wallet Management - จัดการกระเป๋าเงิน ===
  VIEW_WALLETS: 1 << 10,        // Bit 10 - ดูยอดกระเป๋าเงิน
  MANAGE_WALLETS: 1 << 11,      // Bit 11 - โอนเงิน/ปรับยอดกระเป๋า

  // === Audit & Admin - ตรวจสอบและผู้ดูแล ===
  VIEW_AUDIT_LOGS: 1 << 12,     // Bit 12 - ดู audit log (ป้องกันพนักงานโกง)
  MANAGE_ADMINS: 1 << 13,       // Bit 13 - จัดการผู้ดูแลระบบ (สร้าง/แก้สิทธิ์)

  // === Bank Account Management - จัดการบัญชีธนาคาร ===
  VIEW_BANK_ACCOUNTS: 1 << 14,  // Bit 14 - ดูบัญชีธนาคารทั้งหมด
  MANAGE_BANK_ACCOUNTS: 1 << 15,// Bit 15 - เพิ่ม/แก้/ปิดบัญชีธนาคาร

  // === Reports & Settings - รายงานและตั้งค่า ===
  VIEW_REPORTS: 1 << 16,        // Bit 16 - ดูรายงานสรุป
  MANAGE_SETTINGS: 1 << 17,     // Bit 17 - แก้ไขตั้งค่าระบบ

  // === Commission Management - จัดการค่าคอมมิชชัน ===
  MANAGE_COMMISSIONS: 1 << 18,  // Bit 18 - แก้ไข commission rate (ต้อง 2FA)

  // === Partner Management - จัดการพาร์ทเนอร์ ===
  VIEW_PARTNERS: 1 << 19,       // Bit 19 - ดูรายชื่อพาร์ทเนอร์
  MANAGE_PARTNERS: 1 << 20,     // Bit 20 - สร้าง/แก้ไขพาร์ทเนอร์
} as const

/**
 * hasPermission - Check if mask has a specific permission
 * ตรวจสอบว่า user มีสิทธิ์ที่กำหนดหรือไม่
 * ใช้ bitwise AND เพื่อเช็คว่า bit ที่ต้องการเปิดอยู่
 *
 * @param mask - role_mask ของ user (จาก JWT/session)
 * @param perm - สิทธิ์ที่ต้องการตรวจสอบ (จาก PERM constant)
 * @returns true ถ้ามีสิทธิ์
 */
export function hasPermission(mask: number, perm: number): boolean {
  return (mask & perm) === perm
}

/**
 * hasAnyPermission - Check if mask has at least one of the given permissions
 * ตรวจสอบว่า user มีสิทธิ์อย่างน้อย 1 ตัวจากที่กำหนด
 * ใช้สำหรับกรณีที่หลายสิทธิ์สามารถเข้าถึงหน้าเดียวกันได้
 *
 * @param mask - role_mask ของ user
 * @param perms - สิทธิ์ที่ต้องการตรวจสอบ (ส่งได้หลายตัว)
 * @returns true ถ้ามีสิทธิ์อย่างน้อย 1 ตัว
 */
export function hasAnyPermission(mask: number, ...perms: number[]): boolean {
  return perms.some(p => hasPermission(mask, p))
}
