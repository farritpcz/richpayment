// =====================================================
// Dashboard Home Page - หน้าแดชบอร์ดหลัก
// แสดงภาพรวมระบบ: ยอดฝาก ยอดถอน รายได้ ร้านค้า รออนุมัติ
// Summary cards (5 ใบ) + Area chart (7 วัน)
// ใช้ React Query + polling 5 วินาที + fallback mock data
// =====================================================

'use client'

import { useQuery } from '@tanstack/react-query'
import {
  ArrowDownToLine,
  ArrowUpFromLine,
  DollarSign,
  Store,
  Clock,
} from 'lucide-react'
import { SummaryCard } from '@/components/dashboard/summary-card'
import { ChartArea } from '@/components/dashboard/chart-area'
import { useTranslation } from '@/i18n'

// =====================================================
// Mock Data - ข้อมูลจำลองสำหรับ development
// จะถูกแทนที่ด้วย API จริงเมื่อ backend พร้อม
// =====================================================

/**
 * mockSummary - ข้อมูลสรุปจำลอง
 * ใช้แสดงบน summary cards 5 ใบ
 */
const mockSummary = {
  deposits_today: 1_245_800,
  deposits_yesterday: 1_102_300,
  withdrawals_today: 892_500,
  withdrawals_yesterday: 945_200,
  revenue_today: 35_330,
  revenue_yesterday: 31_070,
  active_merchants: 48,
  active_merchants_yesterday: 45,
  pending_withdrawals: 12,
  pending_withdrawals_yesterday: 8,
}

/**
 * mockChartData - ข้อมูลกราฟ 7 วันจำลอง
 * แสดงแนวโน้มยอดฝาก/ถอน/รายได้
 */
const mockChartData = [
  { date: '4 เม.ย.', deposits: 980000, withdrawals: 720000, revenue: 28000 },
  { date: '5 เม.ย.', deposits: 1102300, withdrawals: 845000, revenue: 31000 },
  { date: '6 เม.ย.', deposits: 1050000, withdrawals: 900000, revenue: 29500 },
  { date: '7 เม.ย.', deposits: 1180000, withdrawals: 780000, revenue: 33200 },
  { date: '8 เม.ย.', deposits: 1320000, withdrawals: 945200, revenue: 34800 },
  { date: '9 เม.ย.', deposits: 1150000, withdrawals: 870000, revenue: 32100 },
  { date: '10 เม.ย.', deposits: 1245800, withdrawals: 892500, revenue: 35330 },
]

/**
 * fetchDashboardData - ดึงข้อมูล dashboard จาก API
 * ถ้า API ยังไม่พร้อมจะ fallback ไปใช้ mock data
 */
async function fetchDashboardData() {
  try {
    const res = await fetch('/api/proxy/admin/dashboard/summary', {
      credentials: 'include',
    })
    if (!res.ok) throw new Error('API not available')
    return await res.json()
  } catch {
    // Fallback to mock data สำหรับ development
    return { summary: mockSummary, chart: mockChartData }
  }
}

/**
 * DashboardPage - Component หน้าแดชบอร์ดหลัก
 * - 5 Summary Cards แสดงข้อมูลสรุป (grid 2 cols mobile, 5 cols desktop)
 * - Area Chart แสดงแนวโน้ม 7 วัน
 * - Polling ทุก 5 วินาที ผ่าน React Query refetchInterval
 */
export default function DashboardPage() {
  const { t } = useTranslation()

  // ===== Fetch data with polling / ดึงข้อมูลพร้อม polling 5 วิ =====
  const { data } = useQuery({
    queryKey: ['dashboard-summary'],
    queryFn: fetchDashboardData,
    refetchInterval: 5000, // Polling ทุก 5 วินาที / Poll every 5 seconds
  })

  // ใช้ข้อมูลจาก API หรือ fallback mock / Use API data or mock fallback
  const summary = data?.summary ?? mockSummary
  const chartData = data?.chart ?? mockChartData

  return (
    <div className="space-y-6">
      {/* ===== Page Header / หัวข้อหน้า ===== */}
      <div>
        <h1 className="text-2xl font-bold text-white">{t('dashboard.title')}</h1>
      </div>

      {/* ===== Summary Cards / การ์ดสรุป 5 ใบ ===== */}
      <div className="grid grid-cols-2 md:grid-cols-3 lg:grid-cols-5 gap-4">
        {/* การ์ด 1: ยอดฝากวันนี้ / Today's deposits */}
        <SummaryCard
          label={t('dashboard.deposits_today')}
          value={summary.deposits_today}
          previousValue={summary.deposits_yesterday}
          format="currency"
          icon={ArrowDownToLine}
          color="indigo"
          index={0}
        />

        {/* การ์ด 2: ยอดถอนวันนี้ / Today's withdrawals */}
        <SummaryCard
          label={t('dashboard.withdrawals_today')}
          value={summary.withdrawals_today}
          previousValue={summary.withdrawals_yesterday}
          format="currency"
          icon={ArrowUpFromLine}
          color="purple"
          index={1}
        />

        {/* การ์ด 3: รายได้วันนี้ / Today's revenue */}
        <SummaryCard
          label={t('dashboard.revenue_today')}
          value={summary.revenue_today}
          previousValue={summary.revenue_yesterday}
          format="currency"
          icon={DollarSign}
          color="green"
          index={2}
        />

        {/* การ์ด 4: ร้านค้าที่ใช้งาน / Active merchants */}
        <SummaryCard
          label={t('dashboard.active_merchants')}
          value={summary.active_merchants}
          previousValue={summary.active_merchants_yesterday}
          format="number"
          icon={Store}
          color="orange"
          index={3}
        />

        {/* การ์ด 5: รอการอนุมัติ / Pending withdrawals */}
        <SummaryCard
          label={t('dashboard.pending_withdrawals')}
          value={summary.pending_withdrawals}
          previousValue={summary.pending_withdrawals_yesterday}
          format="number"
          icon={Clock}
          color="red"
          index={4}
        />
      </div>

      {/* ===== Area Chart / กราฟแนวโน้ม 7 วัน ===== */}
      <ChartArea
        data={chartData}
        title={t('dashboard.weekly_trend')}
      />
    </div>
  )
}
