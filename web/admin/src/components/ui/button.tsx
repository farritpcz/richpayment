"use client"

// =====================================================
// Button Component - ปุ่มกดพื้นฐาน (Base Button)
// =====================================================
// ใช้ class-variance-authority (CVA) สำหรับจัดการ variants
// รองรับ variant: default, destructive, outline, secondary, ghost, link
// รองรับ size: default, sm, lg, icon
// ใช้ Slot จาก Radix UI สำหรับ asChild pattern
// =====================================================

import * as React from "react"
import { Slot } from "@radix-ui/react-slot"
import { cva, type VariantProps } from "class-variance-authority"

import { cn } from "@/lib/utils"

// =====================================================
// buttonVariants - กำหนดรูปแบบปุ่มทั้งหมด
// ใช้ CVA เพื่อสร้าง variants ที่ type-safe
// base: สไตล์พื้นฐานที่ทุก variant ใช้ร่วมกัน
// variants.variant: กำหนดสีและรูปแบบ (default=primary, destructive=แดง, etc.)
// variants.size: กำหนดขนาด (default=h-10, sm=h-9, lg=h-11, icon=สี่เหลี่ยม)
// =====================================================
const buttonVariants = cva(
  // Base styles - สไตล์พื้นฐานสำหรับทุกปุ่ม
  "inline-flex items-center justify-center whitespace-nowrap rounded-md text-sm font-medium ring-offset-background transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 disabled:pointer-events-none disabled:opacity-50",
  {
    variants: {
      /**
       * variant - รูปแบบสีของปุ่ม
       * default: สี primary (indigo) - ปุ่มหลัก
       * destructive: สีแดง - สำหรับลบ/ยกเลิก
       * outline: ขอบ + พื้นใส - ปุ่มรอง
       * secondary: สี secondary - ปุ่มทางเลือก
       * ghost: ไม่มีขอบ ไม่มีพื้น - ปุ่มแบบเรียบ
       * link: แสดงเป็นลิงก์ - ขีดเส้นใต้เมื่อ hover
       */
      variant: {
        default: "bg-primary text-primary-foreground hover:bg-primary/90",
        destructive:
          "bg-destructive text-destructive-foreground hover:bg-destructive/90",
        outline:
          "border border-input bg-background hover:bg-accent hover:text-accent-foreground",
        secondary:
          "bg-secondary text-secondary-foreground hover:bg-secondary/80",
        ghost: "hover:bg-accent hover:text-accent-foreground",
        link: "text-primary underline-offset-4 hover:underline",
      },
      /**
       * size - ขนาดของปุ่ม
       * default: ขนาดปกติ h-10
       * sm: ขนาดเล็ก h-9 - ใช้ใน table actions
       * lg: ขนาดใหญ่ h-11 - ใช้ใน form submit
       * icon: ปุ่มไอคอน h-10 w-10 - ใช้กับไอคอนเดี่ยว
       */
      size: {
        default: "h-10 px-4 py-2",
        sm: "h-9 rounded-md px-3",
        lg: "h-11 rounded-md px-8",
        icon: "h-10 w-10",
      },
    },
    // ค่าเริ่มต้น - ถ้าไม่ระบุ variant/size จะใช้ค่านี้
    defaultVariants: {
      variant: "default",
      size: "default",
    },
  }
)

// =====================================================
// ButtonProps - กำหนด props ของ Button
// รวม HTML button attributes + CVA variants + asChild
// asChild: เมื่อเป็น true จะ render children แทน <button>
// เหมาะสำหรับใช้กับ Link หรือ component อื่น
// =====================================================
export interface ButtonProps
  extends React.ButtonHTMLAttributes<HTMLButtonElement>,
    VariantProps<typeof buttonVariants> {
  /** asChild - ใช้ Radix Slot pattern เพื่อ render children เป็น root element */
  asChild?: boolean
}

/**
 * Button - ปุ่มกด UI component หลัก
 * รองรับ forwardRef สำหรับ ref access
 * ใช้ Slot เมื่อ asChild=true เพื่อ merge props ลง children
 * ตัวอย่าง: <Button variant="destructive" size="sm">ลบ</Button>
 * ตัวอย่าง asChild: <Button asChild><Link href="/home">Home</Link></Button>
 */
const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(
  ({ className, variant, size, asChild = false, ...props }, ref) => {
    // ถ้า asChild=true ใช้ Slot (merge props ลง children)
    // ถ้า asChild=false ใช้ <button> ปกติ
    const Comp = asChild ? Slot : "button"
    return (
      <Comp
        className={cn(buttonVariants({ variant, size, className }))}
        ref={ref}
        {...props}
      />
    )
  }
)
Button.displayName = "Button"

export { Button, buttonVariants }
