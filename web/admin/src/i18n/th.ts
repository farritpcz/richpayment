// =====================================================
// Thai Dictionary - พจนานุกรมภาษาไทย
// ใช้กับ i18n system สำหรับแปลภาษาทั้ง dashboard
// ครอบคลุมทุกส่วน: auth, sidebar, dashboard, status, etc.
// =====================================================

const th = {
  // === ทั่วไป - Common labels ที่ใช้หลายที่ ===
  common: {
    appName: 'RichPayment Admin',
    login: 'เข้าสู่ระบบ',
    logout: 'ออกจากระบบ',
    save: 'บันทึก',
    cancel: 'ยกเลิก',
    confirm: 'ยืนยัน',
    delete: 'ลบ',
    edit: 'แก้ไข',
    create: 'สร้าง',
    search: 'ค้นหา',
    filter: 'กรอง',
    export: 'ส่งออก',
    loading: 'กำลังโหลด...',
    noData: 'ไม่มีข้อมูล',
    error: 'เกิดข้อผิดพลาด',
    success: 'สำเร็จ',
    pending: 'รอดำเนินการ',
    completed: 'เสร็จสิ้น',
    failed: 'ล้มเหลว',
    active: 'ใช้งาน',
    suspended: 'ระงับ',
    all: 'ทั้งหมด',
  },

  // === Authentication - หน้า Login และ 2FA ===
  auth: {
    email: 'อีเมล',
    password: 'รหัสผ่าน',
    totpCode: 'รหัส 2FA (6 หลัก)',
    loginTitle: 'เข้าสู่ระบบผู้ดูแล',
    loginSubtitle: 'RichPayment Management System',
    loginButton: 'เข้าสู่ระบบ',
    loggingIn: 'กำลังเข้าสู่ระบบ...',
    invalidCredentials: 'อีเมลหรือรหัสผ่านไม่ถูกต้อง',
    accountLocked: 'บัญชีถูกล็อคชั่วคราว กรุณาลองใหม่ภายหลัง',
    totpRequired: 'กรุณาใส่รหัส 2FA',
    sessionExpired: 'เซสชันหมดอายุ กรุณาเข้าสู่ระบบใหม่',
  },

  // === Sidebar - เมนูด้านซ้าย ===
  sidebar: {
    dashboard: 'แดชบอร์ด',
    merchants: 'ร้านค้า',
    agents: 'ตัวแทน',
    partners: 'พาร์ทเนอร์',
    withdrawals: 'การถอนเงิน',
    bankAccounts: 'บัญชีธนาคาร',
    commission: 'ค่าคอมมิชชัน',
    auditLog: 'บันทึกการใช้งาน',
    settings: 'ตั้งค่า',
    admins: 'ผู้ดูแลระบบ',
    overview: 'ภาพรวม',
    management: 'จัดการ',
    finance: 'การเงิน',
    reports: 'รายงาน',
    system: 'ระบบ',
  },

  // === Header - แถบด้านบน ===
  header: {
    emergencyFreeze: 'หยุดฉุกเฉิน',
    notifications: 'การแจ้งเตือน',
    theme: 'เปลี่ยนธีม',
    language: 'เปลี่ยนภาษา',
    profile: 'โปรไฟล์',
  },

  // === Dashboard - หน้าแดชบอร์ดหลัก ===
  dashboard: {
    title: 'แดชบอร์ด',
    depositsToday: 'ยอดฝากวันนี้',
    withdrawalsToday: 'ยอดถอนวันนี้',
    revenueToday: 'รายได้วันนี้',
    activeMerchants: 'ร้านค้าที่ใช้งาน',
    pendingWithdrawals: 'รอการอนุมัติ',
    trendChart: 'แนวโน้ม 7 วัน',
    recentTransactions: 'รายการล่าสุด',
    comparedToYesterday: 'เทียบกับเมื่อวาน',
  },

  // === Status Badges - สถานะต่างๆ (Pill badges สีอ่อน) ===
  status: {
    pending: 'รอดำเนินการ',
    completed: 'เสร็จสิ้น',
    failed: 'ล้มเหลว',
    expired: 'หมดอายุ',
    approved: 'อนุมัติแล้ว',
    rejected: 'ปฏิเสธ',
    active: 'ใช้งาน',
    suspended: 'ระงับ',
    deleted: 'ลบแล้ว',
  },
} as const

export default th
