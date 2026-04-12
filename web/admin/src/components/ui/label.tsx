"use client"

// =====================================================
// Label Component - ป้ายกำกับฟอร์ม (Form Label)
// =====================================================
// ใช้ @radix-ui/react-label เป็น primitive
// เชื่อมโยงกับ input/select โดยอัตโนมัติผ่าน htmlFor
// รองรับ CVA variants สำหรับ error state
// รองรับ peer-disabled state เมื่อ input ถูก disabled
// =====================================================

import * as React from "react"
import * as LabelPrimitive from "@radix-ui/react-label"
import { cva, type VariantProps } from "class-variance-authority"

import { cn } from "@/lib/utils"

// =====================================================
// labelVariants - กำหนดรูปแบบ label
// base: ขนาดตัวอักษร, font weight, leading
// รองรับ peer-disabled: เมื่อ input ที่เชื่อมโยงถูก disabled
// =====================================================
const labelVariants = cva(
  // Base styles - สไตล์พื้นฐาน
  // text-sm: ขนาดตัวอักษรเล็ก
  // font-medium: ตัวหนาปานกลาง
  // leading-none: ไม่มีระยะห่างบรรทัด
  // peer-disabled: เมื่อ input คู่กันถูก disabled จะจาง + ห้ามคลิก
  "text-sm font-medium leading-none peer-disabled:cursor-not-allowed peer-disabled:opacity-70"
)

/**
 * Label - ป้ายกำกับสำหรับ form fields
 * ใช้ Radix UI Label ที่มี accessibility ในตัว
 * เชื่อมโยงกับ input ผ่าน htmlFor prop
 * ตัวอย่าง: <Label htmlFor="email">อีเมล</Label>
 */
const Label = React.forwardRef<
  React.ElementRef<typeof LabelPrimitive.Root>,
  React.ComponentPropsWithoutRef<typeof LabelPrimitive.Root> &
    VariantProps<typeof labelVariants>
>(({ className, ...props }, ref) => (
  <LabelPrimitive.Root
    ref={ref}
    className={cn(labelVariants(), className)}
    {...props}
  />
))
Label.displayName = LabelPrimitive.Root.displayName

export { Label }
