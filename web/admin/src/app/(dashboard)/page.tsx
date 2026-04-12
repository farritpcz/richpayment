// =====================================================
// Dashboard Root Redirect - Redirect ไปหน้า /dashboard
// เมื่อเข้า (dashboard) group root จะ redirect อัตโนมัติ
// =====================================================

import { redirect } from 'next/navigation'

/**
 * DashboardRoot - Redirect ไป /dashboard
 * หน้านี้ไม่แสดงอะไร เพียง redirect เท่านั้น
 */
export default function DashboardRoot() {
  redirect('/dashboard')
}
