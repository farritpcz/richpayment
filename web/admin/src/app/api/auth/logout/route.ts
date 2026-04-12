// =====================================================
// Logout API Route - BFF proxy สำหรับ logout
// แจ้ง auth-service ให้ลบ session + ลบ cookie ฝั่ง client
// =====================================================

import { NextRequest, NextResponse } from 'next/server'

/** URL ของ auth-service backend */
const AUTH_SERVICE_URL = process.env.AUTH_SERVICE_URL || 'http://localhost:8081'

/**
 * POST /api/auth/logout
 * ลบ session จาก backend + ลบ cookie จาก browser
 *
 * Flow:
 * 1. อ่าน session ID จาก cookie
 * 2. แจ้ง auth-service ให้ลบ session (ถ้ามี)
 * 3. ลบ cookie 'richpay_session' จาก browser
 * 4. ถ้า auth-service ล่ม ก็ลบ cookie อยู่ดี (graceful)
 */
export async function POST(req: NextRequest) {
  const sessionId = req.cookies.get('richpay_session')?.value

  if (sessionId) {
    try {
      // แจ้ง auth-service ให้ลบ session จาก Redis/DB
      await fetch(`${AUTH_SERVICE_URL}/auth/logout`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'X-Session-ID': sessionId,
        },
      })
    } catch {
      // Ignore errors - ลบ cookie อยู่ดีแม้ backend ล่ม
      // เพราะ session จะหมดอายุเองใน 1 ชั่วโมง
    }
  }

  // ลบ cookie เสมอไม่ว่า backend จะสำเร็จหรือไม่
  const response = NextResponse.json({ ok: true })
  response.cookies.delete('richpay_session')
  return response
}
