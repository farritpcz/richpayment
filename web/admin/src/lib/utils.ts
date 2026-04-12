import { clsx, type ClassValue } from "clsx"
import { twMerge } from "tailwind-merge"

// =====================================================
// Utility Functions - ฟังก์ชันช่วยเหลือทั่วไป
// Shared utilities for the Admin Dashboard
// =====================================================

/**
 * cn - Merge Tailwind CSS classes with conflict resolution
 * รวม class ของ Tailwind โดยจัดการ conflict อัตโนมัติ
 * ใช้ clsx สำหรับ conditional classes + twMerge สำหรับ dedup
 */
export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

/**
 * formatCurrency - Format number as Thai Baht (฿)
 * แปลงตัวเลขเป็นรูปแบบสกุลเงินบาท เช่น ฿1,234.56
 * ใช้ Intl.NumberFormat เพื่อรองรับ locale ไทย
 */
export function formatCurrency(amount: number): string {
  return new Intl.NumberFormat('th-TH', { style: 'currency', currency: 'THB' }).format(amount)
}

/**
 * formatDate - Format date/time to Thai locale
 * แปลงวันที่เป็นรูปแบบไทย เช่น 10 เม.ย. 2569 14:30
 * รับได้ทั้ง string (ISO) และ Date object
 */
export function formatDate(date: string | Date): string {
  return new Intl.DateTimeFormat('th-TH', {
    year: 'numeric', month: 'short', day: 'numeric',
    hour: '2-digit', minute: '2-digit'
  }).format(new Date(date))
}

/**
 * formatCompact - Format large numbers in compact notation
 * แปลงตัวเลขใหญ่เป็นรูปแบบย่อ เช่น 1.2M, 500K
 * ใช้แสดงบน Summary Cards ในแดชบอร์ด
 */
export function formatCompact(num: number): string {
  return new Intl.NumberFormat('th-TH', { notation: 'compact', maximumFractionDigits: 1 }).format(num)
}
