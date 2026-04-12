// =====================================================
// SummaryCard - การ์ดสรุปข้อมูล (Glassmorphism)
// Dashboard summary card with animated number
// แสดงค่าตัวเลข + เปอร์เซ็นต์เปลี่ยนแปลง + ไอคอน
// Stagger animation: เด้งขึ้นทีละใบ
// =====================================================

'use client'

import { type LucideIcon, TrendingUp, TrendingDown } from 'lucide-react'
import { AnimatedNumber } from '@/components/shared/animated-number'
import { cn } from '@/lib/utils'

/**
 * SummaryCardProps - Props ของ SummaryCard
 */
interface SummaryCardProps {
  /** ชื่อการ์ด / Card label */
  label: string
  /** ค่าปัจจุบัน / Current value */
  value: number
  /** ค่าก่อนหน้า (สำหรับคำนวณ % change) / Previous value */
  previousValue?: number
  /** รูปแบบแสดงตัวเลข / Number format */
  format?: 'currency' | 'number' | 'compact'
  /** ไอคอน / Icon component */
  icon: LucideIcon
  /** สีหลัก / Color theme for glow and icon */
  color?: 'indigo' | 'green' | 'orange' | 'red' | 'purple'
  /** ลำดับสำหรับ stagger animation / Index for stagger delay */
  index?: number
}

/**
 * colorConfig - ตั้งค่าสีตาม color prop
 * กำหนดสี icon background, border glow, text
 */
const colorConfig = {
  indigo: {
    iconBg: 'bg-indigo-500/10',
    iconText: 'text-indigo-400',
    borderGlow: 'border-l-indigo-500',
  },
  green: {
    iconBg: 'bg-emerald-500/10',
    iconText: 'text-emerald-400',
    borderGlow: 'border-l-emerald-500',
  },
  orange: {
    iconBg: 'bg-orange-500/10',
    iconText: 'text-orange-400',
    borderGlow: 'border-l-orange-500',
  },
  red: {
    iconBg: 'bg-red-500/10',
    iconText: 'text-red-400',
    borderGlow: 'border-l-red-500',
  },
  purple: {
    iconBg: 'bg-purple-500/10',
    iconText: 'text-purple-400',
    borderGlow: 'border-l-purple-500',
  },
}

/**
 * SummaryCard - Component การ์ดสรุป
 * - Glassmorphism: backdrop-blur + bg-white/5 + border
 * - Left border glow ตาม color prop
 * - ไอคอนบนซ้าย (วงกลมสี) + label ล่าง
 * - ตัวเลขใหญ่ bold + AnimatedNumber
 * - % change badge (เขียว/แดง + ลูกศร)
 * - Stagger animation: fadeInUp delay ตาม index
 */
export function SummaryCard({
  label,
  value,
  previousValue,
  format = 'currency',
  icon: Icon,
  color = 'indigo',
  index = 0,
}: SummaryCardProps) {
  const colors = colorConfig[color]

  // ===== คำนวณ % change / Calculate percentage change =====
  let changePercent: number | null = null
  if (previousValue !== undefined && previousValue !== 0) {
    changePercent = ((value - previousValue) / previousValue) * 100
  }
  const isPositive = changePercent !== null && changePercent >= 0

  return (
    <div
      className={cn(
        // Glassmorphism styling
        'backdrop-blur-xl bg-white/5 border border-white/10 shadow-lg',
        'rounded-xl p-5',
        // Left border glow ตามสี / Color glow on left border
        'border-l-2',
        colors.borderGlow,
        // Stagger animation / เด้งขึ้นทีละใบ
        'opacity-0 animate-fade-in-up'
      )}
      style={{ animationDelay: `${index * 100}ms`, animationFillMode: 'forwards' }}
    >
      {/* ===== Top row: Icon + Change badge ===== */}
      <div className="flex items-start justify-between mb-3">
        {/* Icon วงกลมสี / Colored icon circle */}
        <div className={cn('w-10 h-10 rounded-lg flex items-center justify-center', colors.iconBg)}>
          <Icon className={cn('w-5 h-5', colors.iconText)} />
        </div>

        {/* % Change badge / แสดงเปอร์เซ็นต์เปลี่ยนแปลง */}
        {changePercent !== null && (
          <div
            className={cn(
              'flex items-center gap-1 text-xs font-medium px-2 py-1 rounded-full',
              isPositive
                ? 'bg-emerald-500/10 text-emerald-400'
                : 'bg-red-500/10 text-red-400'
            )}
          >
            {isPositive ? (
              <TrendingUp className="w-3 h-3" />
            ) : (
              <TrendingDown className="w-3 h-3" />
            )}
            {isPositive ? '+' : ''}{changePercent.toFixed(1)}%
          </div>
        )}
      </div>

      {/* ===== Value / ตัวเลขใหญ่ ===== */}
      <div className="text-2xl font-bold text-white mb-1">
        <AnimatedNumber value={value} format={format} />
      </div>

      {/* ===== Label / ชื่อการ์ด ===== */}
      <p className="text-sm text-white/50">{label}</p>
    </div>
  )
}
