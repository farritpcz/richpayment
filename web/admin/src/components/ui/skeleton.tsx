// =====================================================
// Skeleton Component - ตัวยึดตำแหน่งขณะโหลด
// =====================================================
// แสดง pulse animation สีเทาเพื่อบอกว่ากำลังโหลดข้อมูล
// ใช้แทน content จริงระหว่างรอ API response
// =====================================================

import { cn } from "@/lib/utils"

/**
 * Skeleton - Loading placeholder
 * แสดง pulse animation สีเทา (muted)
 * ใช้กำหนดขนาดผ่าน className เช่น "h-4 w-[200px]"
 */
function Skeleton({
  className,
  ...props
}: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={cn("animate-pulse rounded-md bg-muted", className)}
      {...props}
    />
  )
}

export { Skeleton }
