// =====================================================
// EmptyState - แสดงเมื่อไม่มีข้อมูล
// Empty state component with icon, text, and optional action
// ใช้แสดงเมื่อตาราง/รายการยังไม่มีข้อมูล
// =====================================================

import { type LucideIcon } from 'lucide-react'
import { Button } from '@/components/ui/button'

/**
 * EmptyStateProps - Props ของ EmptyState
 */
interface EmptyStateProps {
  /** ไอคอน lucide-react / Icon to display */
  icon: LucideIcon
  /** หัวข้อ / Title text */
  title: string
  /** คำอธิบาย / Description text */
  description: string
  /** ปุ่ม action (optional) / Optional action button */
  action?: {
    /** ข้อความบนปุ่ม / Button label */
    label: string
    /** ฟังก์ชันเมื่อกด / Click handler */
    onClick: () => void
  }
}

/**
 * EmptyState - Component แสดงเมื่อไม่มีข้อมูล
 * Layout: ไอคอนใหญ่ (48px) + หัวข้อ + คำอธิบาย + ปุ่ม (optional)
 * อยู่ตรงกลาง พร้อม padding เพียงพอ
 */
export function EmptyState({ icon: Icon, title, description, action }: EmptyStateProps) {
  return (
    <div className="flex flex-col items-center justify-center py-16 px-4 text-center">
      {/* ===== Icon / ไอคอน (ใหญ่ สีจาง) ===== */}
      <div className="w-12 h-12 rounded-full bg-white/5 flex items-center justify-center mb-4">
        <Icon className="w-6 h-6 text-white/30" />
      </div>

      {/* ===== Title / หัวข้อ ===== */}
      <h3 className="text-lg font-semibold text-white/80 mb-1">
        {title}
      </h3>

      {/* ===== Description / คำอธิบาย ===== */}
      <p className="text-sm text-white/40 max-w-sm mb-6">
        {description}
      </p>

      {/* ===== Action Button / ปุ่ม action (optional) ===== */}
      {action && (
        <Button
          onClick={action.onClick}
          className="bg-indigo-600 hover:bg-indigo-700 text-white"
        >
          {action.label}
        </Button>
      )}
    </div>
  )
}
