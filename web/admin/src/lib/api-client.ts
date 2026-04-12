// =====================================================
// API Client - HTTP client สำหรับเรียก Backend API
// ใช้ fetch พื้นฐาน + error handling + auto-redirect on 401
// =====================================================

/**
 * ApiError - Custom error class สำหรับ API errors
 * เก็บ HTTP status code และ error code จาก backend
 * ใช้แยกประเภท error เพื่อแสดง UI ที่เหมาะสม
 */
export class ApiError extends Error {
  constructor(
    /** HTTP status code เช่น 400, 401, 403, 500 */
    public status: number,
    /** ข้อความ error ที่อ่านได้ */
    message: string,
    /** Error code จาก backend เช่น 'INSUFFICIENT_BALANCE', 'TOTP_REQUIRED' */
    public code?: string
  ) {
    super(message)
    this.name = 'ApiError'
  }
}

/**
 * apiFetch - Wrapper ครอบ fetch สำหรับเรียก API ผ่าน BFF proxy
 * - ส่ง credentials (cookie) อัตโนมัติ
 * - ถ้าได้ 401 จะ redirect ไปหน้า login
 * - Parse JSON response อัตโนมัติ
 * - Throw ApiError เมื่อ response ไม่สำเร็จ
 *
 * @param path - API path เช่น '/api/auth/login' หรือ '/api/proxy/merchants'
 * @param options - fetch options (method, body, headers, etc.)
 * @returns Parsed JSON response
 * @throws {ApiError} เมื่อ HTTP status ไม่ใช่ 2xx
 */
export async function apiFetch<T>(path: string, options?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    // ส่ง cookie (richpay_session) ไปด้วยทุกครั้ง
    credentials: 'include',
    headers: { 'Content-Type': 'application/json', ...options?.headers },
    ...options,
  })

  // 401 Unauthorized - session หมดอายุหรือไม่ได้ login
  // Redirect ไปหน้า login อัตโนมัติ (เฉพาะ client-side)
  if (res.status === 401) {
    if (typeof window !== 'undefined') {
      window.location.href = '/login'
    }
    throw new ApiError(401, 'Unauthorized')
  }

  // Response ไม่สำเร็จ - parse error body แล้ว throw
  if (!res.ok) {
    const body = await res.json().catch(() => ({}))
    throw new ApiError(res.status, body.error || body.message || 'Request failed', body.code)
  }

  return res.json()
}
