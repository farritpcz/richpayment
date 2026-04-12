// =====================================================
// Generic BFF Proxy - Proxy ทุก API request ไปยัง backend services
// ทำหน้าที่เป็น Backend-for-Frontend (BFF) pattern
// - แนบ session ID จาก cookie ไปกับทุก request
// - Route request ไปยัง service ที่ถูกต้องตาม path prefix
// - ป้องกัน client เรียก backend โดยตรง (ซ่อน internal URLs)
// =====================================================

import { NextRequest, NextResponse } from 'next/server'

/**
 * SERVICE_MAP - Map path prefix -> backend service URL
 * ใช้ segment แรกของ path ตัดสินว่าจะส่งไป service ไหน
 *
 * ตัวอย่าง:
 *   /api/proxy/merchants/list -> USER_SERVICE_URL/api/v1/merchants/list
 *   /api/proxy/withdrawals/pending -> WITHDRAWAL_SERVICE_URL/api/v1/withdrawals/pending
 */
const SERVICE_MAP: Record<string, string> = {
  // User Service - จัดการ admin, merchant, agent, partner
  admins: process.env.USER_SERVICE_URL || 'http://localhost:8082',
  merchants: process.env.USER_SERVICE_URL || 'http://localhost:8082',
  agents: process.env.USER_SERVICE_URL || 'http://localhost:8082',
  partners: process.env.USER_SERVICE_URL || 'http://localhost:8082',

  // Withdrawal Service - จัดการการถอนเงิน
  withdrawals: process.env.WITHDRAWAL_SERVICE_URL || 'http://localhost:8085',

  // Commission Service - คำนวณค่าคอมมิชชัน
  commission: process.env.COMMISSION_SERVICE_URL || 'http://localhost:8088',

  // Bank Service - จัดการบัญชีธนาคาร
  bank: process.env.BANK_SERVICE_URL || 'http://localhost:8089',
}

/**
 * proxyRequest - ส่ง request ต่อไปยัง backend service
 *
 * @param req - Next.js request object
 * @param params - Dynamic route params (path segments)
 *
 * Security:
 * - ต้องมี session cookie ถึงจะ proxy ได้
 * - ส่ง X-Session-ID header ไปยัง backend เพื่อ authenticate
 * - ไม่เปิด CORS ให้ client เรียก backend ตรง
 */
async function proxyRequest(req: NextRequest, { params }: { params: { path: string[] } }) {
  // ตรวจสอบ session - ต้อง login ก่อนถึงจะใช้ proxy ได้
  const sessionId = req.cookies.get('richpay_session')?.value
  if (!sessionId) {
    return NextResponse.json({ error: 'Unauthorized' }, { status: 401 })
  }

  const pathSegments = params.path
  const prefix = pathSegments[0] // segment แรกใช้เลือก service
  const serviceURL = SERVICE_MAP[prefix]

  // ไม่รู้จัก prefix -> 404
  if (!serviceURL) {
    return NextResponse.json({ error: 'Unknown service' }, { status: 404 })
  }

  // สร้าง URL ปลายทาง: service URL + /api/v1/ + path segments
  const targetPath = '/api/v1/' + pathSegments.join('/')
  const url = new URL(targetPath, serviceURL)

  // Forward query parameters (เช่น ?offset=0&limit=20&status=pending)
  req.nextUrl.searchParams.forEach((v, k) => url.searchParams.set(k, v))

  try {
    // สร้าง headers สำหรับส่งไป backend
    const headers: Record<string, string> = {
      'Content-Type': 'application/json',
      'X-Session-ID': sessionId, // แนบ session ID เพื่อ authenticate กับ backend
    }

    const fetchOptions: RequestInit = {
      method: req.method,
      headers,
    }

    // ส่ง body สำหรับ POST, PUT, DELETE (GET/HEAD ไม่มี body)
    if (req.method !== 'GET' && req.method !== 'HEAD') {
      fetchOptions.body = await req.text()
    }

    // เรียก backend service
    const res = await fetch(url.toString(), fetchOptions)
    const data = await res.text()

    // ส่ง response กลับไป client ตาม status เดิม
    return new NextResponse(data, {
      status: res.status,
      headers: { 'Content-Type': 'application/json' },
    })
  } catch {
    // Backend service ล่มหรือ network error
    return NextResponse.json({ error: 'Service unavailable' }, { status: 503 })
  }
}

// Export handler สำหรับทุก HTTP method ที่ต้องการ
export const GET = proxyRequest
export const POST = proxyRequest
export const PUT = proxyRequest
export const DELETE = proxyRequest
