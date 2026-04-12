// =====================================================
// AnimatedNumber - ตัวเลขเคลื่อนไหว (Counter Animation)
// Animates from 0 to target value on mount
// ใช้ requestAnimationFrame + easeOutExpo easing
// รองรับ format: currency (฿), number, compact (K/M)
// =====================================================

'use client'

import { useEffect, useRef, useState } from 'react'
import { formatCurrency, formatCompact } from '@/lib/utils'

/**
 * AnimatedNumberProps - Props ของ AnimatedNumber
 */
interface AnimatedNumberProps {
  /** ค่าเป้าหมาย / Target value to animate to */
  value: number
  /** ระยะเวลา animation (ms) / Animation duration, default 1000ms */
  duration?: number
  /** รูปแบบแสดงผล / Display format */
  format?: 'currency' | 'number' | 'compact'
}

/**
 * easeOutExpo - Easing function สำหรับ animation
 * เริ่มเร็ว ค่อยๆ ช้าลงจนหยุด (ดูเป็นธรรมชาติ)
 * @param t - progress 0-1
 * @returns eased progress 0-1
 */
function easeOutExpo(t: number): number {
  return t === 1 ? 1 : 1 - Math.pow(2, -10 * t)
}

/**
 * formatValue - แปลงตัวเลขตาม format ที่กำหนด
 * @param value - ตัวเลขที่จะ format
 * @param format - รูปแบบ (currency/number/compact)
 * @returns string ที่ format แล้ว
 */
function formatValue(value: number, format: 'currency' | 'number' | 'compact'): string {
  switch (format) {
    case 'currency':
      return formatCurrency(value)
    case 'compact':
      return formatCompact(value)
    case 'number':
    default:
      return new Intl.NumberFormat('th-TH').format(Math.round(value))
  }
}

/**
 * AnimatedNumber - Component ตัวเลขเคลื่อนไหว
 * นับจาก 0 ไปหาค่าเป้าหมาย ด้วย easeOutExpo easing
 * ใช้แสดงบน Summary Cards ในแดชบอร์ด
 */
export function AnimatedNumber({
  value,
  duration = 1000,
  format = 'number',
}: AnimatedNumberProps) {
  /** ค่าที่แสดงบนหน้าจอ (เปลี่ยนทุก frame) */
  const [displayValue, setDisplayValue] = useState(0)
  /** เก็บ animation frame ID สำหรับ cleanup */
  const frameRef = useRef<number>(0)
  /** เวลาเริ่มต้น animation */
  const startTimeRef = useRef<number>(0)

  useEffect(() => {
    // ตั้งเวลาเริ่มต้น / Record start time
    startTimeRef.current = performance.now()

    /**
     * tick - อัพเดทค่าทุก frame
     * คำนวณ progress จากเวลาที่ผ่านไป แล้ว apply easing
     */
    function tick(now: number) {
      const elapsed = now - startTimeRef.current
      const progress = Math.min(elapsed / duration, 1)
      const easedProgress = easeOutExpo(progress)

      // คำนวณค่าปัจจุบันจาก eased progress
      setDisplayValue(easedProgress * value)

      // ถ้ายังไม่ถึง 100% → animate ต่อ
      if (progress < 1) {
        frameRef.current = requestAnimationFrame(tick)
      }
    }

    // เริ่ม animation / Start animation loop
    frameRef.current = requestAnimationFrame(tick)

    // Cleanup ตอน unmount หรือ value เปลี่ยน
    return () => cancelAnimationFrame(frameRef.current)
  }, [value, duration])

  return <span>{formatValue(displayValue, format)}</span>
}
