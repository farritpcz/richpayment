// =====================================================
// Dashboard Layout - โครงสร้างหลักของหน้า Dashboard
// Auth guard: ตรวจสอบ session ก่อนแสดง content
// Structure: Sidebar (left) + Header + Content (right)
// Responsive: ซ่อน sidebar บน mobile ใช้ Sheet แทน
// =====================================================

'use client'

import { useState, useEffect, type ReactNode } from 'react'
import { useAuth } from '@/providers/auth-provider'
import { Sidebar } from '@/components/layout/sidebar'
import { Header } from '@/components/layout/header'
import { MobileSidebar } from '@/components/layout/mobile-sidebar'
import { Skeleton } from '@/components/ui/skeleton'
import { cn } from '@/lib/utils'

/** Key สำหรับเก็บสถานะ collapsed ใน localStorage */
const SIDEBAR_COLLAPSED_KEY = 'richpayment-sidebar-collapsed'

/**
 * DashboardLayout - Layout หลักที่ครอบทุกหน้าใน (dashboard) group
 *
 * Flow:
 * 1. แสดง skeleton ขณะตรวจสอบ session (isLoading)
 * 2. ถ้าไม่มี user → AuthProvider จะ redirect ไป /login อัตโนมัติ
 * 3. ถ้ามี user → แสดง Sidebar + Header + Content
 *
 * Sidebar:
 * - Desktop: แสดงเสมอ, collapsible (240px ↔ 64px)
 * - Mobile: ซ่อน, ใช้ Sheet (MobileSidebar) แทน
 * - บันทึกสถานะ collapsed ลง localStorage
 */
export default function DashboardLayout({ children }: { children: ReactNode }) {
  const { user, isLoading } = useAuth()

  /** สถานะ sidebar ย่อหรือไม่ / Sidebar collapsed state */
  const [collapsed, setCollapsed] = useState(false)
  /** สถานะ mobile sidebar เปิดหรือไม่ / Mobile sidebar open state */
  const [mobileOpen, setMobileOpen] = useState(false)

  // ===== โหลดสถานะ collapsed จาก localStorage / Restore preference =====
  useEffect(() => {
    const saved = localStorage.getItem(SIDEBAR_COLLAPSED_KEY)
    if (saved === 'true') setCollapsed(true)
  }, [])

  /**
   * toggleCollapsed - สลับสถานะ collapsed + บันทึกลง localStorage
   */
  const toggleCollapsed = () => {
    const next = !collapsed
    setCollapsed(next)
    localStorage.setItem(SIDEBAR_COLLAPSED_KEY, String(next))
  }

  // ===== Loading State / แสดง skeleton ขณะตรวจ session =====
  if (isLoading) {
    return (
      <div className="flex h-screen bg-[#0f172a]">
        {/* Skeleton sidebar */}
        <div className="hidden md:block w-60 bg-[#0c0f24] border-r border-white/5 p-4 space-y-4">
          <Skeleton className="h-8 w-32 bg-white/5" />
          <div className="space-y-2 mt-8">
            {Array.from({ length: 8 }).map((_, i) => (
              <Skeleton key={i} className="h-10 w-full bg-white/5" />
            ))}
          </div>
        </div>
        {/* Skeleton content */}
        <div className="flex-1 p-6 space-y-4">
          <Skeleton className="h-16 w-full bg-white/5" />
          <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
            {Array.from({ length: 6 }).map((_, i) => (
              <Skeleton key={i} className="h-32 bg-white/5" />
            ))}
          </div>
        </div>
      </div>
    )
  }

  // ===== ถ้าไม่มี user จะ redirect (AuthProvider จัดการ) =====
  if (!user) return null

  return (
    <div className="flex h-screen bg-[#0f172a] text-white overflow-hidden">
      {/* ===== Desktop Sidebar (ซ่อนบน mobile) ===== */}
      <div className="hidden md:block">
        <Sidebar collapsed={collapsed} onToggle={toggleCollapsed} />
      </div>

      {/* ===== Mobile Sidebar (Sheet overlay) ===== */}
      <MobileSidebar open={mobileOpen} onOpenChange={setMobileOpen} />

      {/* ===== Main Content Area / พื้นที่เนื้อหาหลัก ===== */}
      <main
        className={cn(
          'flex-1 flex flex-col overflow-hidden',
          'transition-all duration-300 ease-in-out',
          // เลื่อนเนื้อหาตาม sidebar width / Offset by sidebar width
          collapsed ? 'md:ml-16' : 'md:ml-60'
        )}
      >
        {/* Header */}
        <Header onMenuClick={() => setMobileOpen(true)} />

        {/* Content */}
        <div className="flex-1 overflow-auto p-4 md:p-6">
          {children}
        </div>
      </main>
    </div>
  )
}
