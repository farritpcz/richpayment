// =====================================================
// Header - แถบด้านบน (Top Navigation Bar)
// แสดง: ปุ่ม menu (mobile), ชื่อหน้า, Emergency Freeze,
// การแจ้งเตือน, Theme toggle, Language toggle, User dropdown
// =====================================================

'use client'

import { Menu, ShieldAlert, Bell, Sun, Moon, User as UserIcon, LogOut } from 'lucide-react'
import { useTheme } from 'next-themes'
import { Button } from '@/components/ui/button'
import { Avatar, AvatarFallback } from '@/components/ui/avatar'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { useAuth } from '@/providers/auth-provider'
import { usePermissions } from '@/hooks/use-permissions'
import { PERM } from '@/lib/permissions'
import { useTranslation } from '@/i18n'

/**
 * HeaderProps - Props ของ Header component
 */
interface HeaderProps {
  /** ฟังก์ชันเปิด mobile sidebar / Open mobile menu callback */
  onMenuClick: () => void
}

/**
 * Header - Component แถบด้านบน
 * - ซ้าย: ปุ่ม menu (mobile) + ชื่อหน้าปัจจุบัน
 * - ขวา: Emergency Freeze, Notification, Theme, Language, User dropdown
 * - Emergency Freeze แสดงเฉพาะ user ที่มีสิทธิ์ EMERGENCY_FREEZE
 */
export function Header({ onMenuClick }: HeaderProps) {
  const { t, locale, setLocale } = useTranslation()
  const { user, logout } = useAuth()
  const { can } = usePermissions()
  const { theme, setTheme } = useTheme()

  /** จำนวนการแจ้งเตือน (mock) / Notification count (mock data) */
  const notificationCount = 3

  /** ตัวอักษรย่อสำหรับ Avatar / Initials for avatar fallback */
  const initials = user?.display_name
    ?.split(' ')
    .map((n) => n[0])
    .join('')
    .toUpperCase()
    .slice(0, 2) || 'AD'

  return (
    <header className="sticky top-0 z-30 flex items-center justify-between h-16 px-4 md:px-6 border-b border-white/5 bg-[#0f172a]/80 backdrop-blur-xl">
      {/* ===== Left Section / ด้านซ้าย ===== */}
      <div className="flex items-center gap-4">
        {/* Mobile menu button (ซ่อนบน desktop) / Hidden on desktop */}
        <Button
          variant="ghost"
          size="icon"
          className="md:hidden text-white/60 hover:text-white"
          onClick={onMenuClick}
          aria-label="Open menu"
        >
          <Menu className="w-5 h-5" />
        </Button>
      </div>

      {/* ===== Right Section / ด้านขวา ===== */}
      <div className="flex items-center gap-2">
        {/* --- Emergency Freeze Button / ปุ่มหยุดระบบฉุกเฉิน --- */}
        {can(PERM.EMERGENCY_FREEZE) && (
          <Button
            variant="destructive"
            size="sm"
            className="hidden sm:flex items-center gap-2 bg-red-600 hover:bg-red-700 text-white"
          >
            <ShieldAlert className="w-4 h-4" />
            <span className="hidden md:inline">{t('header.emergency_freeze')}</span>
          </Button>
        )}

        {/* --- Notification Bell / กระดิ่งแจ้งเตือน --- */}
        <Button variant="ghost" size="icon" className="relative text-white/60 hover:text-white">
          <Bell className="w-5 h-5" />
          {/* Badge จำนวน notification */}
          {notificationCount > 0 && (
            <span className="absolute -top-0.5 -right-0.5 bg-red-500 text-white text-[10px] font-bold rounded-full min-w-[18px] h-[18px] flex items-center justify-center px-1">
              {notificationCount}
            </span>
          )}
        </Button>

        {/* --- Theme Toggle / สลับ Dark/Light mode --- */}
        <Button
          variant="ghost"
          size="icon"
          className="text-white/60 hover:text-white"
          onClick={() => setTheme(theme === 'dark' ? 'light' : 'dark')}
          aria-label="Toggle theme"
        >
          {theme === 'dark' ? (
            <Sun className="w-5 h-5" />
          ) : (
            <Moon className="w-5 h-5" />
          )}
        </Button>

        {/* --- Language Toggle / สลับภาษาไทย-อังกฤษ --- */}
        <Button
          variant="ghost"
          size="sm"
          className="text-white/60 hover:text-white font-medium text-sm px-2"
          onClick={() => setLocale(locale === 'th' ? 'en' : 'th')}
        >
          {locale === 'th' ? 'EN' : 'TH'}
        </Button>

        {/* --- User Dropdown / เมนูผู้ใช้ --- */}
        <DropdownMenu>
          <DropdownMenuTrigger
            className="flex items-center gap-2 px-2 py-1.5 rounded-md text-white/80 hover:text-white hover:bg-white/5 transition-colors"
          >
            <Avatar className="w-8 h-8">
              <AvatarFallback className="bg-indigo-600 text-white text-xs font-semibold">
                {initials}
              </AvatarFallback>
            </Avatar>
            {/* ชื่อผู้ใช้ (ซ่อนบน mobile) */}
            <div className="hidden md:block text-left">
              <p className="text-sm font-medium leading-none">{user?.display_name}</p>
              <p className="text-xs text-white/40 mt-0.5">{user?.role}</p>
            </div>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-48">
            {/* Profile */}
            <DropdownMenuItem className="cursor-pointer">
              <UserIcon className="w-4 h-4 mr-2" />
              {t('header.profile')}
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            {/* Logout */}
            <DropdownMenuItem className="cursor-pointer text-red-500" onClick={() => logout()}>
              <LogOut className="w-4 h-4 mr-2" />
              {t('header.logout')}
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
    </header>
  )
}
