// =====================================================
// StatusBadge - แสดงสถานะเป็น pill badge สีต่างๆ
// Status pill badge with color mapping
// รองรับทั้งภาษาไทยและอังกฤษ
// =====================================================

import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'

/**
 * StatusBadgeProps - Props ของ StatusBadge
 */
interface StatusBadgeProps {
  /** สถานะที่จะแสดง (ภาษาไทยหรืออังกฤษ) / Status string */
  status: string
  /** ขนาด / Size variant */
  size?: 'sm' | 'md'
}

/**
 * statusColorMap - แผนที่สีตามสถานะ
 * รองรับทั้งภาษาไทยและอังกฤษ
 * สี: bg อ่อน + text เข้ม เพื่ออ่านง่ายทั้ง dark/light mode
 */
const statusColorMap: Record<string, string> = {
  // สีเหลือง - รอดำเนินการ / Yellow - Pending
  pending: 'bg-yellow-500/10 text-yellow-500 border-yellow-500/20',
  'รอดำเนินการ': 'bg-yellow-500/10 text-yellow-500 border-yellow-500/20',

  // สีเขียว - เสร็จสิ้น / Green - Completed
  completed: 'bg-emerald-500/10 text-emerald-500 border-emerald-500/20',
  'เสร็จสิ้น': 'bg-emerald-500/10 text-emerald-500 border-emerald-500/20',

  // สีแดง - ล้มเหลว / Red - Failed
  failed: 'bg-red-500/10 text-red-500 border-red-500/20',
  'ล้มเหลว': 'bg-red-500/10 text-red-500 border-red-500/20',

  // สีเทา - หมดอายุ / Gray - Expired
  expired: 'bg-gray-500/10 text-gray-500 border-gray-500/20',
  'หมดอายุ': 'bg-gray-500/10 text-gray-500 border-gray-500/20',

  // สีน้ำเงิน - อนุมัติ / Blue - Approved
  approved: 'bg-blue-500/10 text-blue-500 border-blue-500/20',
  'อนุมัติ': 'bg-blue-500/10 text-blue-500 border-blue-500/20',

  // สีแดง - ปฏิเสธ / Red - Rejected
  rejected: 'bg-red-500/10 text-red-500 border-red-500/20',
  'ปฏิเสธ': 'bg-red-500/10 text-red-500 border-red-500/20',

  // สีเขียว - ใช้งาน / Green - Active
  active: 'bg-emerald-500/10 text-emerald-500 border-emerald-500/20',
  'ใช้งาน': 'bg-emerald-500/10 text-emerald-500 border-emerald-500/20',

  // สีส้ม - ระงับ / Orange - Suspended
  suspended: 'bg-orange-500/10 text-orange-500 border-orange-500/20',
  'ระงับ': 'bg-orange-500/10 text-orange-500 border-orange-500/20',
}

/**
 * StatusBadge - Component แสดง pill badge สถานะ
 * แสดงเป็นวงรี (rounded-full) สีอ่อนตามสถานะ
 * รองรับ 2 ขนาด: sm (เล็ก) และ md (ปกติ)
 */
export function StatusBadge({ status, size = 'md' }: StatusBadgeProps) {
  // หาสีจาก map (lowercase) หรือใช้ default สีเทา
  const colorClass = statusColorMap[status.toLowerCase()] || 'bg-gray-500/10 text-gray-500 border-gray-500/20'

  return (
    <Badge
      variant="outline"
      className={cn(
        'rounded-full font-medium border',
        colorClass,
        // ขนาด / Size
        size === 'sm' ? 'text-xs px-2 py-0.5' : 'text-sm px-3 py-1'
      )}
    >
      {status}
    </Badge>
  )
}
