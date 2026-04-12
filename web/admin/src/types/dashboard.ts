// =====================================================
// Dashboard Types - Type definitions สำหรับหน้า Dashboard
// ใช้กับ Summary Cards, Charts, และ Real-time data
// =====================================================

/**
 * SummaryCardData - ข้อมูลสำหรับ Summary Card แต่ละใบ
 * แสดงตัวเลขสำคัญ + % เปลี่ยนแปลง + ไอคอน
 * Animation: ตัวเลขนับขึ้น + card เด้งขึ้นทีละใบ (stagger)
 */
export interface SummaryCardData {
  /** ป้ายกำกับ เช่น 'ยอดฝากวันนี้' */
  label: string
  /** ค่าปัจจุบัน */
  value: number
  /** ค่าเมื่อวาน (ใช้คำนวณ % change) */
  previousValue?: number
  /** รูปแบบการแสดงผลตัวเลข */
  format: 'currency' | 'number' | 'compact'
  /** ชื่อ icon (ใช้กับ Lucide icons) */
  icon: string
  /** สีของ card - ใช้ gradient ตาม brand design */
  color: 'indigo' | 'green' | 'orange' | 'red' | 'purple'
}

/**
 * ChartDataPoint - จุดข้อมูลสำหรับกราฟ
 * ใช้กับ Recharts (Line, Bar, Area charts)
 * แสดงแนวโน้ม 7 วันในหน้า Dashboard
 */
export interface ChartDataPoint {
  /** วันที่ (ISO format) */
  date: string
  /** ยอดฝากรวม (บาท) */
  deposits: number
  /** ยอดถอนรวม (บาท) */
  withdrawals: number
  /** รายได้ (ค่าธรรมเนียม - commission) */
  revenue: number
}

/**
 * DashboardSummary - ข้อมูลสรุปทั้งหมดของหน้า Dashboard
 * Polling ทุก 5 วินาทีเพื่อ real-time update
 * ใช้ React Query สำหรับ caching + refetch
 */
export interface DashboardSummary {
  /** ยอดฝากรวมวันนี้ (บาท) */
  totalDepositsToday: number
  /** ยอดถอนรวมวันนี้ (บาท) */
  totalWithdrawalsToday: number
  /** รายได้รวมวันนี้ (บาท) */
  revenueToday: number
  /** จำนวนร้านค้าที่ active */
  activeMerchants: number
  /** จำนวนรายการถอนที่รออนุมัติ */
  pendingWithdrawals: number
  /** ข้อมูลกราฟแนวโน้ม 7 วัน */
  depositsTrend: ChartDataPoint[]
}
