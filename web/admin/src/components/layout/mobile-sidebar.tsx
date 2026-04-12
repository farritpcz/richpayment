// =====================================================
// MobileSidebar - เมนูด้านซ้ายสำหรับมือถือ
// Uses shadcn Sheet component (slides from left)
// แสดงเมนูเหมือน Sidebar แต่เปิดเต็มจอเมื่อกดปุ่ม menu
// ปิดอัตโนมัติเมื่อกดเลือกเมนู
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
  type LucideIcon,
} from 'lucide-react'
import { Sheet, SheetContent } from '@/components/ui/sheet'
import { cn } from '@/lib/utils'
import { usePermissions } from '@/hooks/use-permissions'
import { PERM } from '@/lib/permissions'
import { useTranslation } from '@/i18n'

/**
 * MobileSidebarProps - Props ของ MobileSidebar
 */
interface MobileSidebarProps {
  /** เปิด Sheet หรือไม่ / Whether the sheet is open */
  open: boolean
  /** ฟังก์ชันเปิด/ปิด / Open state setter */
  onOpenChange: (open: boolean) => void
}

/**
 * MobileMenuItem - โครงสร้างข้อมูลเมนู (เหมือน Sidebar)
 */
interface MobileMenuItem {
  labelKey: string
  href: string
  icon: LucideIcon
  requiredPermission: number
}

/**
 * MobileMenuGroup - กลุ่มเมนู
 */
interface MobileMenuGroup {
  labelKey: string
  items: MobileMenuItem[]
}

/**
 * mobileMenuGroups - เมนูเหมือน Sidebar ทุกประการ
 * แยกไว้เพื่อไม่ต้อง import ข้าม component
 */
const mobileMenuGroups: MobileMenuGroup[] = [
  {
    labelKey: 'nav.overview',
    items: [
      { labelKey: 'nav.dashboard', href: '/dashboard', icon: LayoutDashboard, requiredPermission: PERM.VIEW_ORDERS },
    ],
  },
  {
    labelKey: 'nav.management',
    items: [
      { labelKey: 'nav.merchants', href: '/merchants', icon: Store, requiredPermission: PERM.VIEW_MERCHANTS },
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
 * MobileSidebar - Component เมนูมือถือ
 * เปิดจากด้านซ้ายเป็น Sheet overlay
 * ปิดอัตโนมัติเมื่อกดเลือกเมนู
 */
export function MobileSidebar({ open, onOpenChange }: MobileSidebarProps) {
  const { t } = useTranslation()
  const pathname = usePathname()
  const { can } = usePermissions()

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent side="left" className="w-72 bg-[#0c0f24] border-white/5 p-0">
        {/* ===== Logo / โลโก้ ===== */}
        <div className="flex items-center h-16 px-4 border-b border-white/5">
          <div className="w-8 h-8 rounded-lg bg-gradient-to-br from-indigo-500 to-purple-600 flex items-center justify-center">
            <span className="text-white font-bold text-sm">R</span>
          </div>
          <span className="ml-3 text-white font-semibold text-lg">RichPayment</span>
        </div>

        {/* ===== Menu Groups / กลุ่มเมนู ===== */}
        <nav className="flex-1 overflow-y-auto py-4 px-2 space-y-6">
          {mobileMenuGroups.map((group) => {
            // กรอง item ตาม RBAC / Filter by permission
            const visibleItems = group.items.filter((item) => can(item.requiredPermission))
            if (visibleItems.length === 0) return null

            return (
              <div key={group.labelKey}>
                {/* ชื่อกลุ่ม */}
                <p className="px-3 mb-2 text-xs font-medium text-white/30 uppercase tracking-wider">
                  {t(group.labelKey)}
                </p>

                {/* รายการเมนู */}
                <div className="space-y-1">
                  {visibleItems.map((item) => {
                    const isActive = pathname === item.href || pathname?.startsWith(item.href + '/')
                    const Icon = item.icon

                    return (
                      <Link
                        key={item.href}
                        href={item.href}
                        onClick={() => onOpenChange(false)} // ปิด sheet เมื่อกดเมนู
                        className={cn(
                          'flex items-center gap-3 px-3 py-2.5 rounded-lg',
                          'text-sm font-medium transition-all duration-200',
                          'relative',
                          isActive
                            ? 'bg-indigo-500/10 text-indigo-400'
                            : 'text-white/60 hover:text-white/90 hover:bg-white/5'
                        )}
                      >
                        {/* Active left border glow */}
                        {isActive && (
                          <div className="absolute left-0 top-1/2 -translate-y-1/2 w-0.5 h-6 bg-indigo-400 rounded-r shadow-[0_0_8px_rgba(99,102,241,0.5)]" />
                        )}
                        <Icon className="w-5 h-5 flex-shrink-0" />
                        <span>{t(item.labelKey)}</span>
                      </Link>
                    )
                  })}
                </div>
              </div>
            )
          })}
        </nav>
      </SheetContent>
    </Sheet>
  )
}
