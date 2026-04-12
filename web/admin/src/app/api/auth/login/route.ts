// =====================================================
// Login API Route - BFF proxy สำหรับ login
// Next.js API Route -> auth-service backend
// ตั้ง HttpOnly cookie เพื่อเก็บ session ID อย่างปลอดภัย
// Session timeout: 1 ชั่วโมง (maxAge: 3600)
// =====================================================

import { NextRequest, NextResponse } from 'next/server'

/** URL ของ auth-service backend (ใน Docker network หรือ localhost) */
const AUTH_SERVICE_URL = process.env.AUTH_SERVICE_URL || 'http://localhost:8081'

/**
 * POST /api/auth/login
 * รับ email + password + totp_code (optional) จาก client
 * ส่งต่อไปยัง auth-service แล้วตั้ง session cookie
 *
 * Flow:
 * 1. Client ส่ง { email, password } -> BFF
 * 2. BFF เติม user_type: 'admin' แล้วส่งไป auth-service
 * 3. auth-service ตอบ { user, session_id } หรือ { requires_totp: true }
 * 4. BFF ตั้ง HttpOnly cookie 'richpay_session' แล้วส่ง response กลับ
 */
export async function POST(req: NextRequest) {
  try {
    const body = await req.json()

    // เรียก auth-service โดยตรง (internal network)
    // เติม user_type: 'admin' เพื่อบอก backend ว่ามาจาก admin dashboard
    const res = await fetch(`${AUTH_SERVICE_URL}/auth/login`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ ...body, user_type: 'admin' }),
    })

    const data = await res.json()

    // ถ้า login ไม่สำเร็จ ส่ง error กลับไปเลย
    if (!res.ok) {
      return NextResponse.json(data, { status: res.status })
    }

    // Login สำเร็จ - ตั้ง session cookie
    const response = NextResponse.json(data)
    response.cookies.set('richpay_session', data.session_id || '', {
      httpOnly: true,                                  // ป้องกัน XSS อ่าน cookie
      secure: process.env.NODE_ENV === 'production',   // HTTPS only ใน production
      sameSite: 'lax',                                 // ป้องกัน CSRF
      maxAge: 3600,                                    // หมดอายุ 1 ชั่วโมง
      path: '/',                                       // ใช้ได้ทุก path
    })

    return response
  } catch {
    // auth-service ล่มหรือ network error
    return NextResponse.json({ error: 'Service unavailable' }, { status: 503 })
  }
}
