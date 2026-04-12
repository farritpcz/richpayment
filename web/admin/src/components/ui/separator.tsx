"use client"

// =====================================================
// Separator Component - เส้นแบ่ง (Divider Line)
// =====================================================
// ใช้ @radix-ui/react-separator สำหรับ accessible divider
// รองรับ horizontal (เส้นนอน) และ vertical (เส้นตั้ง)
// =====================================================

import * as React from "react"
import * as SeparatorPrimitive from "@radix-ui/react-separator"

import { cn } from "@/lib/utils"

/**
 * Separator - เส้นแบ่งส่วน
 * orientation="horizontal" → เส้นนอน (default)
 * orientation="vertical" → เส้นตั้ง
 * decorative → ไม่แสดงใน accessibility tree
 */
const Separator = React.forwardRef<
  React.ElementRef<typeof SeparatorPrimitive.Root>,
  React.ComponentPropsWithoutRef<typeof SeparatorPrimitive.Root>
>(
  (
    { className, orientation = "horizontal", decorative = true, ...props },
    ref
  ) => (
    <SeparatorPrimitive.Root
      ref={ref}
      decorative={decorative}
      orientation={orientation}
      className={cn(
        "shrink-0 bg-border",
        // เส้นนอน: สูง 1px กว้างเต็ม / เส้นตั้ง: กว้าง 1px สูงเต็ม
        orientation === "horizontal" ? "h-[1px] w-full" : "h-full w-[1px]",
        className
      )}
      {...props}
    />
  )
)
Separator.displayName = SeparatorPrimitive.Root.displayName

export { Separator }
