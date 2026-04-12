// =====================================================
// Validate Session API Route - ตรวจสอบว่า session ยัง valid
// เรียกตอนโหลดหน้าเพื่อเช็ค cookie ที่มีอยู่
// ถ้า session หมดอายุจะลบ cookie + return 401
// =====================================================

import { NextRequest, NextResponse } from 'next/server'

/** URL ของ auth-service backend */
const AUTH_SERVICE_URL = process.env.AUTH_SERVICE_URL || 'http://localhost:8081'

/**
 * POST /api/auth/validate
 * ตรวจสอบ session กับ auth-service backend
 *
 * Flow:
 * 1. อ่าน session ID จาก cookie
 * 2. ถ้าไม่มี cookie -> return 401
 * 3. ส่ง session ID ไป auth-service เพื่อ validate
 * 4. ถ้า valid -> return user data (id, email, role, role_mask)
 * 5. ถ้า invalid -> ลบ cookie + return 401
 */
export async function POST(req: NextRequest) {
  const sessionId = req.cookies.get('richpay_session')?.value

  // ไม่มี session cookie -> ยังไม่ได้ login
  if (!sessionId) {
    return NextResponse.json(null, { status: 401 })
  }

  try {
    // ส่ง session ID ไป auth-service เพื่อตรวจสอบ
    const res = await fetch(`${AUTH_SERVICE_URL}/auth/validate`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-Session-ID': sessionId,
      },
    })

    // Session หมดอายุหรือไม่ถูกต้อง
    if (!res.ok) {
      const response = NextResponse.json(null, { status: 401 })
      // ลบ cookie ที่หมดอายุเพื่อไม่ต้องเช็คอีก
      response.cookies.delete('richpay_session')
      return response
    }

    // Session valid - return user data
    const data = await res.json()
    return NextResponse.json(data)
  } catch {
    // auth-service ล่ม - ไม่ลบ cookie เพราะอาจกลับมาได้
    return NextResponse.json(null, { status: 503 })
  }
}
