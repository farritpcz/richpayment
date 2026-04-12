// =====================================================
// Sidebar - แถบเมนูด้านซ้าย (Collapsible)
// Main navigation sidebar for admin dashboard
// รองรับ RBAC: ซ่อนเมนูที่ user ไม่มีสิทธิ์เข้าถึง
// Collapsible: 240px ↔ 64px with smooth transition
// =====================================================

'use client'

import Link from 'next/link'
import { usePathname } from 'next/navigation'
import {
  LayoutDashboard,
  Store,
  Users,
  Building2,
  Wallet,
  Landmark,
  Coins,
  FileText,
  Shield,
  Settings,
  ChevronLeft,
  ChevronRight,
  type LucideIcon,
} from 'lucide-react'
import { cn } from '@/lib/utils'
import { usePermissions } from '@/hooks/use-permissions'
import { PERM } from '@/lib/permissions'
import { useTranslation } from '@/i18n'

/**
 * MenuItem - โครงสร้างข้อมูลแต่ละรายการเมนู
 */
interface MenuItem {
  /** i18n key สำหรับชื่อเมนู */
  labelKey: string
  /** URL path ที่จะ navigate ไป */
  href: string
  /** lucide-react icon component */
  icon: LucideIcon
  /** สิทธิ์ที่ต้องมีเพื่อเห็นเมนูนี้ (bitmask) */
  requiredPermission: number
  /** จำนวน badge (optional, เช่น pending count) */
  badge?: number
}

/**
 * MenuGroup - กลุ่มเมนู
 */
interface MenuGroup {
  /** i18n key สำหรับชื่อกลุ่ม */
  labelKey: string
  /** รายการเมนูในกลุ่ม */
  items: MenuItem[]
}

/**
 * menuGroups - กำหนดโครงสร้างเมนูทั้งหมด
 * แบ่งเป็นกลุ่ม: ภาพรวม, จัดการ, การเงิน, รายงาน, ระบบ
 */
const menuGroups: MenuGroup[] = [
  {
    labelKey: 'sidebar.overview',
    items: [
      { labelKey: 'sidebar.dashboard', href: '/dashboard', icon: LayoutDashboard, requiredPermission: PERM.VIEW_ORDERS },
    ],
  },
  {
    labelKey: 'sidebar.management',
    items: [
      { labelKey: 'sidebar.merchants', href: '/merchants', icon: Store, requiredPermission: PERM.VIEW_MERCHANTS },
      { labelKey: 'nav.agents', href: '/agents', icon: Users, requiredPermission: PERM.VIEW_AGENTS },
      { labelKey: 'nav.partners', href: '/partners', icon: Building2, requiredPermission: PERM.VIEW_PARTNERS },
      { labelKey: 'nav.admins', href: '/admins', icon: Shield, requiredPermission: PERM.MANAGE_ADMINS },
    ],
  },
  {
    labelKey: 'nav.finance',
    items: [
      { labelKey: 'nav.withdrawals', href: '/withdrawals', icon: Wallet, requiredPermission: PERM.APPROVE_WITHDRAWALS },
      { labelKey: 'nav.bank_accounts', href: '/bank-accounts', icon: Landmark, requiredPermission: PERM.VIEW_BANK_ACCOUNTS },
      { labelKey: 'nav.commission', href: '/commission', icon: Coins, requiredPermission: PERM.MANAGE_COMMISSIONS },
    ],
  },
  {
    labelKey: 'nav.reports',
    items: [
      { labelKey: 'nav.audit_log', href: '/audit-log', icon: FileText, requiredPermission: PERM.VIEW_AUDIT_LOGS },
      { labelKey: 'nav.report', href: '/reports', icon: FileText, requiredPermission: PERM.VIEW_REPORTS },
    ],
  },
  {
    labelKey: 'nav.system',
    items: [
      { labelKey: 'nav.settings', href: '/settings', icon: Settings, requiredPermission: PERM.MANAGE_SETTINGS },
    ],
  },
]

/**
 * SidebarProps - Props ของ Sidebar component
 */
interface SidebarProps {
  /** ย่อ sidebar หรือไม่ / Whether sidebar is collapsed */
  collapsed: boolean
  /** ฟังก์ชันสลับ collapse / Toggle collapse callback */
  onToggle: () => void
}

/**
 * Sidebar - Component แถบเมนูด้านซ้าย
 * - พื้นหลังเข้ม (#0c0f24) เข้มกว่าพื้นหลังหลัก 1 ระดับ
 * - โลโก้ด้านบน (ซ่อน text เมื่อ collapsed)
 * - เมนูแบ่งกลุ่ม + RBAC filter
 * - Active state: bg-indigo-500/10, text-indigo-400, left border glow
 * - ปุ่ม collapse ด้านล่าง
 * - Smooth width transition: 240px ↔ 64px
 */
export function Sidebar({ collapsed, onToggle }: SidebarProps) {
  const { t } = useTranslation()
  const pathname = usePathname()
  const { can } = usePermissions()

  return (
    <aside
      className={cn(
        'fixed left-0 top-0 z-40 h-screen flex flex-col',
        'bg-[#0c0f24] border-r border-white/5',
        'transition-all duration-300 ease-in-out',
        // ความกว้าง: 240px เมื่อเปิด, 64px เมื่อย่อ
        collapsed ? 'w-16' : 'w-60'
      )}
    >
      {/* ===== Logo / โลโก้ด้านบน ===== */}
      <div className="flex items-center h-16 px-4 border-b border-white/5">
        {/* Icon โลโก้ */}
        <div className="w-8 h-8 rounded-lg bg-gradient-to-br from-indigo-500 to-purple-600 flex items-center justify-center flex-shrink-0">
          <span className="text-white font-bold text-sm">R</span>
        </div>
        {/* ชื่อระบบ (ซ่อนเมื่อ collapsed) */}
        {!collapsed && (
          <span className="ml-3 text-white font-semibold text-lg whitespace-nowrap">
            RichPayment
          </span>
        )}
      </div>

      {/* ===== Menu Groups / กลุ่มเมนู ===== */}
      <nav className="flex-1 overflow-y-auto py-4 px-2 space-y-6">
        {menuGroups.map((group) => {
          // กรอง item ตาม RBAC / Filter items by permission
          const visibleItems = group.items.filter((item) => can(item.requiredPermission))
          if (visibleItems.length === 0) return null

          return (
            <div key={group.labelKey}>
              {/* ชื่อกลุ่ม (ซ่อนเมื่อ collapsed) */}
              {!collapsed && (
                <p className="px-3 mb-2 text-xs font-medium text-white/30 uppercase tracking-wider">
                  {t(group.labelKey)}
                </p>
              )}

              {/* รายการเมนู / Menu items */}
              <div className="space-y-1">
                {visibleItems.map((item) => {
                  const isActive = pathname === item.href || pathname?.startsWith(item.href + '/')
                  const Icon = item.icon

                  return (
                    <Link
                      key={item.href}
                      href={item.href}
                      className={cn(
                        'flex items-center gap-3 px-3 py-2.5 rounded-lg',
                        'text-sm font-medium transition-all duration-200',
                        'group relative',
                        isActive
                          ? // Active state - indigo highlight + left border glow
                            'bg-indigo-500/10 text-indigo-400'
                          : // Default state
                            'text-white/60 hover:text-white/90 hover:bg-white/5'
                      )}
                    >
                      {/* Left border glow เมื่อ active */}
                      {isActive && (
                        <div className="absolute left-0 top-1/2 -translate-y-1/2 w-0.5 h-6 bg-indigo-400 rounded-r shadow-[0_0_8px_rgba(99,102,241,0.5)]" />
                      )}

                      {/* Icon */}
                      <Icon className={cn('w-5 h-5 flex-shrink-0', collapsed && 'mx-auto')} />

                      {/* Label (ซ่อนเมื่อ collapsed) */}
                      {!collapsed && (
                        <span className="whitespace-nowrap">{t(item.labelKey)}</span>
                      )}

                      {/* Badge count (ซ่อนเมื่อ collapsed) */}
                      {!collapsed && item.badge !== undefined && item.badge > 0 && (
                        <span className="ml-auto bg-red-500 text-white text-xs rounded-full px-1.5 py-0.5 min-w-[20px] text-center">
                          {item.badge}
                        </span>
                      )}
                    </Link>
                  )
                })}
              </div>
            </div>
          )
        })}
      </nav>

      {/* ===== Collapse Toggle Button / ปุ่มสลับย่อ-ขยาย ===== */}
      <div className="border-t border-white/5 p-2">
        <button
          onClick={onToggle}
          className="w-full flex items-center justify-center p-2.5 rounded-lg
                     text-white/40 hover:text-white/80 hover:bg-white/5
                     transition-colors duration-200"
          aria-label={collapsed ? 'Expand sidebar' : 'Collapse sidebar'}
        >
          {collapsed ? (
            <ChevronRight className="w-5 h-5" />
          ) : (
            <ChevronLeft className="w-5 h-5" />
          )}
        </button>
      </div>
    </aside>
  )
}
