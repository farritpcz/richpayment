// =====================================================
// Badge Component - ป้ายสถานะ (Status Badge)
// ใช้แสดงสถานะ order, withdrawal, bank account
// Variants: default, secondary, destructive, outline
// =====================================================

import * as React from "react"
import { cva, type VariantProps } from "class-variance-authority"

import { cn } from "@/lib/utils"

/**
 * badgeVariants - กำหนดรูปแบบ Badge
 * แต่ละ variant มีสีพื้นหลังและขอบต่างกัน
 */
const badgeVariants = cva(
  // Base: pill shape, ขนาดเล็ก, font semibold
  "inline-flex items-center rounded-full border px-2.5 py-0.5 text-xs font-semibold transition-colors focus:outline-none focus:ring-2 focus:ring-ring focus:ring-offset-2",
  {
    variants: {
      variant: {
        /** default - ป้าย primary (indigo) */
        default: "border-transparent bg-primary text-primary-foreground hover:bg-primary/80",
        /** secondary - ป้ายรอง */
        secondary: "border-transparent bg-secondary text-secondary-foreground hover:bg-secondary/80",
        /** destructive - ป้ายแดง (error/failed) */
        destructive: "border-transparent bg-destructive text-destructive-foreground hover:bg-destructive/80",
        /** outline - ป้ายมีขอบ ไม่มีพื้นหลัง */
        outline: "text-foreground",
      },
    },
    defaultVariants: {
      variant: "default",
    },
  }
)

/** BadgeProps - สืบทอดจาก HTML div + CVA variants */
export interface BadgeProps
  extends React.HTMLAttributes<HTMLDivElement>,
    VariantProps<typeof badgeVariants> {}

/**
 * Badge - Component ป้ายสถานะ
 * ใช้แสดงสถานะเช่น "completed", "pending", "failed"
 */
function Badge({ className, variant, ...props }: BadgeProps) {
  return (
    <div className={cn(badgeVariants({ variant }), className)} {...props} />
  )
}

export { Badge, badgeVariants }
