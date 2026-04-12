// =====================================================
// ChartArea - กราฟพื้นที่ (Area Chart) สำหรับแนวโน้ม
// Recharts-based area chart with gradient fill
// แสดง deposits (indigo) และ withdrawals (purple)
// Dark-friendly styling + glassmorphism wrapper
// =====================================================

'use client'

import {
  AreaChart,
  Area,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  ResponsiveContainer,
} from 'recharts'
import { formatCurrency } from '@/lib/utils'

/**
 * ChartDataPoint - โครงสร้างข้อมูลแต่ละจุดบนกราฟ
 */
interface ChartDataPoint {
  /** วันที่ (ใช้แสดงบนแกน X) */
  date: string
  /** ยอดฝาก */
  deposits: number
  /** ยอดถอน */
  withdrawals: number
  /** รายได้ */
  revenue: number
}

/**
 * ChartAreaProps - Props ของ ChartArea
 */
interface ChartAreaProps {
  /** ข้อมูลกราฟ / Chart data array */
  data: ChartDataPoint[]
  /** หัวข้อกราฟ / Chart title */
  title: string
}

/**
 * CustomTooltip - Tooltip แบบกำหนดเอง
 * แสดงข้อมูลเมื่อ hover บนกราฟ
 * Dark-friendly styling
 */
function CustomTooltip({ active, payload, label }: { active?: boolean; payload?: Array<{ name: string; value: number; color: string }>; label?: string }) {
  if (!active || !payload) return null

  return (
    <div className="bg-slate-800/95 backdrop-blur-sm border border-white/10 rounded-lg p-3 shadow-xl">
      {/* วันที่ / Date label */}
      <p className="text-white/60 text-xs mb-2">{label}</p>
      {/* ค่าแต่ละ series */}
      {payload.map((entry, i) => (
        <div key={i} className="flex items-center gap-2 text-sm">
          <div className="w-2 h-2 rounded-full" style={{ backgroundColor: entry.color }} />
          <span className="text-white/70">{entry.name}:</span>
          <span className="text-white font-medium">{formatCurrency(entry.value)}</span>
        </div>
      ))}
    </div>
  )
}

/**
 * ChartArea - Component กราฟพื้นที่
 * - Glassmorphism card wrapper
 * - 2 areas: deposits (indigo gradient) + withdrawals (purple gradient)
 * - Responsive container ปรับขนาดตาม parent
 * - Dark-friendly tooltip + grid
 */
export function ChartArea({ data, title }: ChartAreaProps) {
  return (
    <div className="backdrop-blur-xl bg-white/5 border border-white/10 shadow-lg rounded-xl p-5">
      {/* ===== Chart Title / หัวข้อกราฟ ===== */}
      <h3 className="text-lg font-semibold text-white mb-4">{title}</h3>

      {/* ===== Chart / กราฟ ===== */}
      <div className="h-[300px] w-full">
        <ResponsiveContainer width="100%" height="100%">
          <AreaChart data={data} margin={{ top: 5, right: 5, left: 0, bottom: 5 }}>
            {/* ===== Gradient Definitions / กำหนด gradient สี ===== */}
            <defs>
              {/* Gradient สำหรับ deposits (indigo) */}
              <linearGradient id="gradientDeposits" x1="0" y1="0" x2="0" y2="1">
                <stop offset="0%" stopColor="#6366f1" stopOpacity={0.3} />
                <stop offset="100%" stopColor="#6366f1" stopOpacity={0} />
              </linearGradient>
              {/* Gradient สำหรับ withdrawals (purple) */}
              <linearGradient id="gradientWithdrawals" x1="0" y1="0" x2="0" y2="1">
                <stop offset="0%" stopColor="#8b5cf6" stopOpacity={0.3} />
                <stop offset="100%" stopColor="#8b5cf6" stopOpacity={0} />
              </linearGradient>
            </defs>

            {/* Grid เส้นประ / Dashed grid lines */}
            <CartesianGrid strokeDasharray="3 3" stroke="rgba(255,255,255,0.05)" />

            {/* แกน X - วันที่ */}
            <XAxis
              dataKey="date"
              stroke="rgba(255,255,255,0.3)"
              fontSize={12}
              tickLine={false}
              axisLine={false}
            />

            {/* แกน Y - จำนวนเงิน */}
            <YAxis
              stroke="rgba(255,255,255,0.3)"
              fontSize={12}
              tickLine={false}
              axisLine={false}
              tickFormatter={(val) => `${(val / 1000).toFixed(0)}K`}
            />

            {/* Tooltip แบบกำหนดเอง */}
            <Tooltip content={<CustomTooltip />} />

            {/* ===== Area: Deposits (indigo) / พื้นที่ยอดฝาก ===== */}
            <Area
              type="monotone"
              dataKey="deposits"
              name="ยอดฝาก"
              stroke="#6366f1"
              strokeWidth={2}
              fill="url(#gradientDeposits)"
            />

            {/* ===== Area: Withdrawals (purple) / พื้นที่ยอดถอน ===== */}
            <Area
              type="monotone"
              dataKey="withdrawals"
              name="ยอดถอน"
              stroke="#8b5cf6"
              strokeWidth={2}
              fill="url(#gradientWithdrawals)"
            />
          </AreaChart>
        </ResponsiveContainer>
      </div>
    </div>
  )
}
