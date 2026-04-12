"use client"

// =====================================================
// Avatar Component - รูปโปรไฟล์ผู้ใช้ (User Avatar)
// =====================================================
// ใช้ @radix-ui/react-avatar สำหรับ image loading + fallback
// Avatar: container วงกลม
// AvatarImage: รูปภาพจริง (แสดงเมื่อโหลดสำเร็จ)
// AvatarFallback: ตัวอักษรย่อ (แสดงเมื่อไม่มีรูป/โหลดไม่ได้)
// =====================================================

import * as React from "react"
import * as AvatarPrimitive from "@radix-ui/react-avatar"

import { cn } from "@/lib/utils"

/**
 * Avatar - Container หลักของ avatar (วงกลม)
 * ขนาดเริ่มต้น: 40x40px, overflow hidden เพื่อครอปรูป
 */
const Avatar = React.forwardRef<
  React.ElementRef<typeof AvatarPrimitive.Root>,
  React.ComponentPropsWithoutRef<typeof AvatarPrimitive.Root>
>(({ className, ...props }, ref) => (
  <AvatarPrimitive.Root
    ref={ref}
    className={cn(
      "relative flex h-10 w-10 shrink-0 overflow-hidden rounded-full",
      className
    )}
    {...props}
  />
))
Avatar.displayName = AvatarPrimitive.Root.displayName

/**
 * AvatarImage - รูปภาพ avatar
 * aspect-square เพื่อให้เป็นสี่เหลี่ยมจัตุรัส + cover
 */
const AvatarImage = React.forwardRef<
  React.ElementRef<typeof AvatarPrimitive.Image>,
  React.ComponentPropsWithoutRef<typeof AvatarPrimitive.Image>
>(({ className, ...props }, ref) => (
  <AvatarPrimitive.Image
    ref={ref}
    className={cn("aspect-square h-full w-full", className)}
    {...props}
  />
))
AvatarImage.displayName = AvatarPrimitive.Image.displayName

/**
 * AvatarFallback - ข้อความ fallback เมื่อไม่มีรูป
 * แสดงตัวอักษรย่อ (initials) ของผู้ใช้
 * พื้นหลัง muted + center text
 */
const AvatarFallback = React.forwardRef<
  React.ElementRef<typeof AvatarPrimitive.Fallback>,
  React.ComponentPropsWithoutRef<typeof AvatarPrimitive.Fallback>
>(({ className, ...props }, ref) => (
  <AvatarPrimitive.Fallback
    ref={ref}
    className={cn(
      "flex h-full w-full items-center justify-center rounded-full bg-muted",
      className
    )}
    {...props}
  />
))
AvatarFallback.displayName = AvatarPrimitive.Fallback.displayName

export { Avatar, AvatarImage, AvatarFallback }
