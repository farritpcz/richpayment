// =====================================================
// Query Provider - จัดการ server state ด้วย React Query
// ใช้ TanStack React Query สำหรับ caching, refetching, polling
// Dashboard ใช้ polling ทุก 5 วินาทีสำหรับ real-time data
// =====================================================

'use client'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useState, type ReactNode } from 'react'

/**
 * QueryProvider - ครอบ app เพื่อให้ใช้ useQuery/useMutation ได้
 * สร้าง QueryClient ใน useState เพื่อป้องกัน re-create ตอน re-render
 *
 * Default options:
 * - staleTime: 5000ms (5 วินาที) - ข้อมูลถือว่า fresh 5 วิ (เหมาะกับ real-time polling)
 * - retry: 1 - retry ครั้งเดียวเมื่อ fail
 * - refetchOnWindowFocus: false - ไม่ refetch ตอนกลับมาที่ tab (ใช้ polling แทน)
 */
export function QueryProvider({ children }: { children: ReactNode }) {
  const [client] = useState(() => new QueryClient({
    defaultOptions: {
      queries: {
        staleTime: 5000,           // 5 วินาที - เหมาะกับ real-time polling
        retry: 1,                  // retry 1 ครั้ง ไม่ต้อง retry เยอะ
        refetchOnWindowFocus: false, // ปิด auto-refetch ตอน focus กลับมา
      },
    },
  }))

  return (
    <QueryClientProvider client={client}>
      {children}
    </QueryClientProvider>
  )
}
