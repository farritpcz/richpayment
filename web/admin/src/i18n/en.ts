// =====================================================
// English Dictionary - พจนานุกรมภาษาอังกฤษ
// Must match the exact same structure as th.ts
// ใช้กับ i18n system สำหรับสลับภาษา TH/EN
// =====================================================

const en = {
  // === Common - General labels used across the app ===
  common: {
    appName: 'RichPayment Admin',
    login: 'Login',
    logout: 'Logout',
    save: 'Save',
    cancel: 'Cancel',
    confirm: 'Confirm',
    delete: 'Delete',
    edit: 'Edit',
    create: 'Create',
    search: 'Search',
    filter: 'Filter',
    export: 'Export',
    loading: 'Loading...',
    noData: 'No data',
    error: 'An error occurred',
    success: 'Success',
    pending: 'Pending',
    completed: 'Completed',
    failed: 'Failed',
    active: 'Active',
    suspended: 'Suspended',
    all: 'All',
  },

  // === Authentication - Login page and 2FA ===
  auth: {
    email: 'Email',
    password: 'Password',
    totpCode: '2FA Code (6 digits)',
    loginTitle: 'Admin Login',
    loginSubtitle: 'RichPayment Management System',
    loginButton: 'Login',
    loggingIn: 'Logging in...',
    invalidCredentials: 'Invalid email or password',
    accountLocked: 'Account temporarily locked. Please try again later.',
    totpRequired: 'Please enter your 2FA code',
    sessionExpired: 'Session expired. Please login again.',
  },

  // === Sidebar - Left navigation menu ===
  sidebar: {
    dashboard: 'Dashboard',
    merchants: 'Merchants',
    agents: 'Agents',
    partners: 'Partners',
    withdrawals: 'Withdrawals',
    bankAccounts: 'Bank Accounts',
    commission: 'Commission',
    auditLog: 'Audit Log',
    settings: 'Settings',
    admins: 'Administrators',
    overview: 'Overview',
    management: 'Management',
    finance: 'Finance',
    reports: 'Reports',
    system: 'System',
  },

  // === Header - Top navigation bar ===
  header: {
    emergencyFreeze: 'Emergency Freeze',
    notifications: 'Notifications',
    theme: 'Toggle Theme',
    language: 'Change Language',
    profile: 'Profile',
  },

  // === Dashboard - Main dashboard page ===
  dashboard: {
    title: 'Dashboard',
    depositsToday: 'Deposits Today',
    withdrawalsToday: 'Withdrawals Today',
    revenueToday: 'Revenue Today',
    activeMerchants: 'Active Merchants',
    pendingWithdrawals: 'Pending Approval',
    trendChart: '7-Day Trend',
    recentTransactions: 'Recent Transactions',
    comparedToYesterday: 'Compared to yesterday',
  },

  // === Status Badges - Various statuses (soft-colored pill badges) ===
  status: {
    pending: 'Pending',
    completed: 'Completed',
    failed: 'Failed',
    expired: 'Expired',
    approved: 'Approved',
    rejected: 'Rejected',
    active: 'Active',
    suspended: 'Suspended',
    deleted: 'Deleted',
  },
} as const

export default en
