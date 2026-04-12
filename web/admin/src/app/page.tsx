// =====================================================
// Root Page Redirect - หน้าแรกของเว็บ
// Redirect ไป /dashboard (AuthProvider จะตรวจ login ให้)
// =====================================================

import { redirect } from 'next/navigation'

/**
 * Home - Redirect ไป /dashboard
 * ถ้ายังไม่ login → AuthProvider จะ redirect ไป /login อัตโนมัติ
 * ถ้า login แล้ว → จะเข้าหน้า dashboard ตรงๆ
 */
export default function Home() {
  redirect('/dashboard')
}
