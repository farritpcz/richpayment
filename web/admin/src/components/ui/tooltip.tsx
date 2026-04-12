"use client"

// =====================================================
// Tooltip Component - คำอธิบายเมื่อ hover (Tooltip)
// =====================================================
// ใช้ @radix-ui/react-tooltip สำหรับ accessible tooltips
// TooltipProvider: wrapper ที่จัดการ delay + skip
// Tooltip: root component
// TooltipTrigger: element ที่ hover แล้วแสดง tooltip
// TooltipContent: เนื้อหา tooltip
// =====================================================

import * as React from "react"
import * as TooltipPrimitive from "@radix-ui/react-tooltip"

import { cn } from "@/lib/utils"

/** TooltipProvider - จัดการ delay + skip สำหรับ tooltip group */
const TooltipProvider = TooltipPrimitive.Provider

/** Tooltip - Root component */
const Tooltip = TooltipPrimitive.Root

/** TooltipTrigger - Element ที่ hover แล้วแสดง tooltip */
const TooltipTrigger = TooltipPrimitive.Trigger

/**
 * TooltipContent - เนื้อหา tooltip
 * แสดงเหนือ trigger element (sideOffset=4)
 * มี animation fade+zoom เข้า-ออก
 * พื้นหลังสี popover + เงา + มุมโค้ง
 */
const TooltipContent = React.forwardRef<
  React.ElementRef<typeof TooltipPrimitive.Content>,
  React.ComponentPropsWithoutRef<typeof TooltipPrimitive.Content>
>(({ className, sideOffset = 4, ...props }, ref) => (
  <TooltipPrimitive.Content
    ref={ref}
    sideOffset={sideOffset}
    className={cn(
      "z-50 overflow-hidden rounded-md border bg-popover px-3 py-1.5 text-sm text-popover-foreground shadow-md",
      // Animation: fade+zoom เข้า-ออกตามทิศทาง
      "animate-in fade-in-0 zoom-in-95",
      "data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=closed]:zoom-out-95",
      "data-[side=bottom]:slide-in-from-top-2",
      "data-[side=left]:slide-in-from-right-2",
      "data-[side=right]:slide-in-from-left-2",
      "data-[side=top]:slide-in-from-bottom-2",
      className
    )}
    {...props}
  />
))
TooltipContent.displayName = TooltipPrimitive.Content.displayName

export { Tooltip, TooltipTrigger, TooltipContent, TooltipProvider }
