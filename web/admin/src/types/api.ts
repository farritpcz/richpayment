// =====================================================
// API Types - Type definitions สำหรับ API Response ทั่วไป
// ใช้เป็น generic types สำหรับทุก endpoint
// =====================================================

/**
 * PaginatedResponse - Response แบบแบ่งหน้า
 * ใช้กับทุก endpoint ที่ return list (merchants, orders, etc.)
 * TanStack Table ใช้ total + offset + limit สำหรับ server-side pagination
 *
 * @template T - ประเภทข้อมูลในแต่ละ row
 */
export interface PaginatedResponse<T> {
  /** รายการข้อมูลในหน้าปัจจุบัน */
  data: T[]
  /** จำนวนข้อมูลทั้งหมด (ใช้คำนวณจำนวนหน้า) */
  total: number
  /** ตำแหน่งเริ่มต้นของหน้าปัจจุบัน (0-based) */
  offset: number
  /** จำนวนข้อมูลต่อหน้า */
  limit: number
}

/**
 * ApiErrorResponse - Response เมื่อ API เกิด error
 * Backend ทุก service ใช้ format เดียวกัน
 */
export interface ApiErrorResponse {
  /** ข้อความ error หลัก */
  error: string
  /** Error code สำหรับ client จัดการ เช่น 'TOTP_REQUIRED', 'INSUFFICIENT_BALANCE' */
  code?: string
  /** รายละเอียด error แยกตาม field (ใช้กับ form validation) */
  details?: Record<string, string>
}
