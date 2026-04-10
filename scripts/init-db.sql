-- =============================================================================
-- RichPayment - Complete Database Schema (สคีมาฐานข้อมูลทั้งหมด)
-- PostgreSQL 16+
-- =============================================================================
--
-- สคริปต์นี้สร้าง database schema ทั้งหมดสำหรับระบบ RichPayment
-- This script creates the complete database schema for the RichPayment platform
--
-- ระบบ Payment Gateway SaaS ที่มี 4 roles หลัก:
-- Payment Gateway SaaS system with 4 main roles:
--   Admin    = ผู้ดูแลระบบ, ควบคุมทั้งหมด, RBAC permissions
--              System owner, full control, role-based access control
--   Agent    = ตัวแทนจำหน่าย, white-label, รับ commission
--              Middleman, resells as white-label, earns commission
--   Merchant = ร้านค้าที่ใช้ระบบรับชำระเงิน
--              Business using the payment service
--   Partner  = เจ้าของบัญชีธนาคาร, รับ commission ต่อ transaction
--              Bank account owner, earns commission per transaction
--
-- ปริมาณข้อมูล / Data volume:
--   ~100M THB/day, 100K-1M transactions/day
--   Partitioned tables retain 3 months, then archived
--
-- ลำดับ hierarchy:
--   Partner -> Agent -> Merchant -> DepositOrder / Withdrawal
--   Wallet -> WalletLedger
--   Commission -> CommissionDailySummary
--
-- ตารางที่ใช้ Partitioning (แบ่งตามเดือน):
-- Tables using monthly range partitioning:
--   - deposit_orders       (รายการฝากเงิน)
--   - wallet_ledger        (บันทึกการเปลี่ยนแปลง balance)
--   - withdrawals          (คำขอถอนเงิน)
--   - commissions          (บันทึกการแบ่ง fee)
--   - sms_messages         (SMS จากธนาคาร)
--   - slip_verifications   (การตรวจสอบสลิป)
--   - audit_logs           (บันทึกทุก action ในระบบ)
--   - webhook_deliveries   (บันทึกการส่ง webhook)
--
-- วิธีใช้ / Usage:
--   psql -U richpayment -d richpayment -f scripts/init-db.sql
--   หรือ mount เข้า docker-entrypoint-initdb.d/
-- =============================================================================

-- ===========================================================================
-- 1. EXTENSIONS - เปิดใช้ extension ที่จำเป็น
--    Enable required PostgreSQL extensions
-- ===========================================================================

-- pgcrypto: ใช้สำหรับ gen_random_uuid() และ encryption functions
-- pgcrypto: Provides gen_random_uuid() and encryption functions
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- uuid-ossp: ใช้สำหรับ uuid_generate_v4() (backward compatibility)
-- uuid-ossp: Provides uuid_generate_v4() for backward compatibility
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- ===========================================================================
-- 2. ENUM TYPES - กำหนด enum สำหรับสถานะต่างๆ
--    Define enum types for status fields
-- ===========================================================================

-- สถานะของ Admin: active (ใช้งานได้), suspended (ระงับชั่วคราว), deleted (ลบแล้ว)
-- Admin status: active (can login), suspended (temporarily blocked), deleted (soft-deleted)
CREATE TYPE admin_status AS ENUM ('active', 'suspended', 'deleted');

-- สถานะของ Merchant: pending (รอ approve), active, suspended, deleted
-- Merchant status: pending (awaiting approval), active, suspended, deleted
CREATE TYPE merchant_status AS ENUM ('pending', 'active', 'suspended', 'deleted');

-- สถานะของ Agent: active, suspended, deleted
-- Agent status: active (operational), suspended, deleted
CREATE TYPE agent_status AS ENUM ('active', 'suspended', 'deleted');

-- สถานะของ Partner: active, suspended, deleted
-- Partner status: active (operational), suspended, deleted
CREATE TYPE partner_status AS ENUM ('active', 'suspended', 'deleted');

-- สถานะของ Deposit Order: pending -> matched -> completed / expired / failed / cancelled
-- Deposit order lifecycle status
CREATE TYPE order_status AS ENUM ('pending', 'matched', 'completed', 'expired', 'failed', 'cancelled');

-- วิธีที่ deposit ถูก match: sms (SMS ธนาคาร), email, slip (สลิปที่อัปโหลด)
-- How the deposit was detected and matched
CREATE TYPE matched_by_type AS ENUM ('sms', 'email', 'slip');

-- สถานะของ Withdrawal: pending -> approved/rejected -> completed/failed
-- Withdrawal request lifecycle status
CREATE TYPE withdrawal_status AS ENUM ('pending', 'approved', 'rejected', 'completed', 'failed');

-- ประเภทปลายทางของ Withdrawal: bank (โอนธนาคาร), promptpay (พร้อมเพย์)
-- Withdrawal destination type
CREATE TYPE withdrawal_dest_type AS ENUM ('bank', 'promptpay');

-- ประเภทเจ้าของ Wallet: merchant, agent, partner, system
-- Wallet owner type (polymorphic)
CREATE TYPE owner_type AS ENUM ('merchant', 'agent', 'partner', 'system');

-- ประเภทรายการใน Ledger: แต่ละ type บอกว่า balance เปลี่ยนเพราะอะไร
-- Ledger entry type: describes the reason for the balance change
CREATE TYPE ledger_entry_type AS ENUM (
    'deposit_credit',           -- เงินเข้าจาก deposit / Deposit credited to wallet
    'withdrawal_debit',         -- เงินออกจาก withdrawal / Withdrawal debited from wallet
    'withdrawal_hold',          -- กัน balance สำหรับ withdrawal ที่รอ approve / Balance held for pending withdrawal
    'withdrawal_release',       -- ปลดล็อค balance เมื่อ withdrawal ถูก reject/fail / Balance released on rejection
    'fee_debit',                -- หัก fee จาก merchant / Fee deducted from merchant
    'commission_credit',        -- เพิ่ม commission ให้ agent/partner / Commission credited to agent/partner
    'commission_payout_debit',  -- หัก commission ตอน agent/partner ถอน / Commission payout debit
    'adjustment'                -- ปรับ balance ด้วยมือ (admin) / Manual admin adjustment
);

-- ประเภท transaction สำหรับ commission: deposit หรือ withdrawal
-- Transaction type for commission records
CREATE TYPE transaction_type AS ENUM ('deposit', 'withdrawal');


-- ===========================================================================
-- 3. TABLES - สร้างตารางหลักทั้งหมด
--    Create all tables
-- ===========================================================================

-- ===========================================================================
-- 3.1 ADMINS - ผู้ดูแลระบบ (System Administrators)
-- จัดการ merchants, agents, partners และตั้งค่าระบบ
-- Manages merchants, agents, partners and system configuration
-- ใช้ bitmask RBAC สำหรับ fine-grained permissions
-- Uses bitmask RBAC for fine-grained permission control
-- ===========================================================================
CREATE TABLE admins (
    -- Primary Key: UUID v4 สร้างอัตโนมัติ / Auto-generated UUID v4
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- email สำหรับ login (ต้องไม่ซ้ำ) / Login email (must be unique)
    email           VARCHAR(255) NOT NULL UNIQUE,
    -- bcrypt hash ของ password (ไม่เก็บ plaintext เด็ดขาด!)
    -- bcrypt hash of password (NEVER store plaintext!)
    password_hash   VARCHAR(255) NOT NULL,
    -- AES-256 encrypted TOTP secret สำหรับ 2FA (Google Authenticator)
    -- AES-256 encrypted TOTP secret for 2FA (Google Authenticator)
    totp_secret_enc BYTEA,
    -- ชื่อที่แสดงใน dashboard / Display name shown in dashboard
    display_name    VARCHAR(100) NOT NULL,
    -- Bitmask สำหรับ RBAC permissions
    -- bit 0 = ViewMerchants, bit 1 = CreateMerchants, bit 2 = ManageAgents, etc.
    role_mask       BIGINT NOT NULL DEFAULT 0,
    -- สถานะ: active/suspended/deleted
    -- Status: active/suspended/deleted
    status          admin_status NOT NULL DEFAULT 'active',
    -- Soft disable: false = ไม่สามารถ login ได้
    -- Soft disable: false = cannot login
    is_active       BOOLEAN NOT NULL DEFAULT true,
    -- ล็อคบัญชีจนถึงเวลานี้ (หลัง failed login 5 ครั้ง)
    -- Account locked until this time (after 5 failed login attempts)
    locked_until    TIMESTAMPTZ,
    -- จำนวนครั้งที่ login ผิดติดต่อกัน (reset เมื่อ login สำเร็จ)
    -- Consecutive failed login attempts counter (resets on success)
    failed_attempts SMALLINT NOT NULL DEFAULT 0,
    -- Array ของ IP ที่อนุญาต (null = อนุญาตทั้งหมด)
    -- Array of allowed IP addresses (null = allow all)
    ip_whitelist    INET[],
    -- เวลาที่สร้าง record (UTC) / Record creation timestamp (UTC)
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- เวลาที่แก้ไขล่าสุด (UTC) / Last modification timestamp (UTC)
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Comment อธิบายตาราง / Table and column comments
COMMENT ON TABLE admins IS 'ตาราง admin ผู้ดูแลระบบ RichPayment - จัดการ merchants, agents, partners / System administrators with RBAC permissions';
COMMENT ON COLUMN admins.id IS 'UUID v4 ที่สร้างอัตโนมัติ / Auto-generated unique admin identifier';
COMMENT ON COLUMN admins.email IS 'email สำหรับ login ต้องไม่ซ้ำ / Login email, must be unique across all admins';
COMMENT ON COLUMN admins.password_hash IS 'bcrypt hash ของ password / bcrypt hashed password, never stored as plaintext';
COMMENT ON COLUMN admins.totp_secret_enc IS 'AES-256 encrypted TOTP secret สำหรับ 2FA / Encrypted TOTP secret for Google Authenticator';
COMMENT ON COLUMN admins.role_mask IS 'Bitmask permissions: bit0=ViewMerchants, bit1=CreateMerchants, bit2=ManageAgents, bit3=ManageFinances';
COMMENT ON COLUMN admins.is_active IS 'Soft disable: false = ไม่สามารถ login / false means account is disabled';
COMMENT ON COLUMN admins.locked_until IS 'ล็อคบัญชีจนถึงเวลานี้หลัง login ผิด 5 ครั้ง / Lock until this time after 5 failed logins';
COMMENT ON COLUMN admins.ip_whitelist IS 'Array ของ IP ที่อนุญาต, null = ทั้งหมด / Allowed IPs, null means allow all';

-- ===========================================================================
-- 3.2 PARTNERS - เจ้าของบัญชีธนาคาร (Bank Account Owners)
-- อยู่บนสุดของ commission hierarchy: Partner -> Agent -> Merchant
-- Top of commission hierarchy: Partner -> Agent -> Merchant
-- รับ commission จากทุก transaction ที่ผ่านบัญชีของตน
-- Earns commission on every transaction through their accounts
-- ===========================================================================
CREATE TABLE partners (
    -- Primary Key: UUID v4 / Unique partner identifier
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- email สำหรับ login / Login email
    email                   VARCHAR(255) NOT NULL UNIQUE,
    -- bcrypt hash ของ password / bcrypt hashed password
    password_hash           VARCHAR(255) NOT NULL,
    -- AES-256 encrypted TOTP secret สำหรับ 2FA / Encrypted TOTP for 2FA
    totp_secret_enc         BYTEA,
    -- ชื่อที่แสดง / Display name shown in dashboard
    display_name            VARCHAR(100) NOT NULL,
    -- เปอร์เซ็นต์ commission จาก deposit (เช่น 0.0100 = 1.00%)
    -- Commission percentage on deposits (e.g. 0.0100 = 1.00%)
    deposit_commission_pct  NUMERIC(5,4) NOT NULL DEFAULT 0,
    -- เปอร์เซ็นต์ commission จาก withdrawal
    -- Commission percentage on withdrawals
    withdraw_commission_pct NUMERIC(5,4) NOT NULL DEFAULT 0,
    -- สถานะ: active/suspended/deleted / Status
    status                  partner_status NOT NULL DEFAULT 'active',
    -- Soft disable / Soft disable flag
    is_active               BOOLEAN NOT NULL DEFAULT true,
    -- ล็อคบัญชีจนถึงเวลานี้ / Lock until this time
    locked_until            TIMESTAMPTZ,
    -- จำนวนครั้งที่ login ผิด / Failed login attempts counter
    failed_attempts         SMALLINT NOT NULL DEFAULT 0,
    -- Admin ที่สร้าง partner นี้ / Which admin created this partner
    created_by_admin_id     UUID REFERENCES admins(id),
    -- timestamps
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE partners IS 'ตาราง Partner - เจ้าของบัญชีธนาคาร อยู่บนสุดของ commission hierarchy / Bank account owners at top of commission chain';
COMMENT ON COLUMN partners.deposit_commission_pct IS 'เปอร์เซ็นต์ commission จาก deposit เช่น 0.0100 = 1.00% / Deposit commission rate';
COMMENT ON COLUMN partners.withdraw_commission_pct IS 'เปอร์เซ็นต์ commission จาก withdrawal / Withdrawal commission rate';

-- ===========================================================================
-- 3.3 AGENTS - ตัวแทนจำหน่ายที่ดูแล merchants (Intermediaries)
-- อยู่ตรงกลาง: Partner -> Agent -> Merchant
-- Middle of hierarchy: Partner -> Agent -> Merchant
-- resell ระบบแบบ white-label พร้อม custom domain, logo, branding
-- Resell the platform as white-label with custom branding
-- ===========================================================================
CREATE TABLE agents (
    -- Primary Key: UUID v4 / Unique agent identifier
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- email สำหรับ login / Login email
    email                   VARCHAR(255) NOT NULL UNIQUE,
    -- bcrypt hash ของ password / bcrypt hashed password
    password_hash           VARCHAR(255) NOT NULL,
    -- AES-256 encrypted TOTP secret สำหรับ 2FA / Encrypted TOTP for 2FA
    totp_secret_enc         BYTEA,
    -- ชื่อบริษัท/แบรนด์ / Agent's company/brand name
    company_name            VARCHAR(200) NOT NULL,
    -- ชื่อที่แสดง / Display name
    display_name            VARCHAR(100) NOT NULL,
    -- ===== White-label branding configuration (การตั้งค่าแบรนด์) =====
    -- Custom domain ของ agent เช่น pay.agentsite.com
    -- Agent's custom domain (e.g. pay.agentsite.com)
    custom_domain           VARCHAR(255),
    -- URL ของ logo / Agent's custom logo URL
    logo_url                VARCHAR(500),
    -- สี primary ของแบรนด์ (hex เช่น #FF5733) / Primary brand color in hex
    brand_color_primary     VARCHAR(7),
    -- สี secondary ของแบรนด์ / Secondary brand color in hex
    brand_color_secondary   VARCHAR(7),
    -- ===== Commission rates (ตั้งโดย admin) =====
    -- เปอร์เซ็นต์ commission จาก deposit (เช่น 0.0100 = 1.00%)
    -- Agent's commission % on deposits
    deposit_commission_pct  NUMERIC(5,4) NOT NULL DEFAULT 0,
    -- เปอร์เซ็นต์ commission จาก withdrawal
    -- Agent's commission % on withdrawals
    withdraw_commission_pct NUMERIC(5,4) NOT NULL DEFAULT 0,
    -- FK ไปหา partner (nullable - บาง agent ไม่มี partner)
    -- Parent partner (NULL = independent agent, no partner)
    partner_id              UUID REFERENCES partners(id),
    -- สถานะ / Status
    status                  agent_status NOT NULL DEFAULT 'active',
    -- Soft disable / Soft disable flag
    is_active               BOOLEAN NOT NULL DEFAULT true,
    -- ล็อคบัญชี / Lock until
    locked_until            TIMESTAMPTZ,
    -- จำนวนครั้งที่ login ผิด / Failed login attempts
    failed_attempts         SMALLINT NOT NULL DEFAULT 0,
    -- Admin ที่สร้าง / Created by admin
    created_by_admin_id     UUID REFERENCES admins(id),
    -- timestamps
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE agents IS 'ตาราง Agent - ตัวแทนจำหน่ายที่ดูแล merchants และ resell แบบ white-label / Intermediaries managing merchants';
COMMENT ON COLUMN agents.partner_id IS 'FK ไปหา partner (nullable) - agent บางคนทำงานอิสระ / Parent partner, NULL if independent';
COMMENT ON COLUMN agents.custom_domain IS 'Custom domain สำหรับ white-label เช่น pay.agentsite.com / White-label custom domain';
COMMENT ON COLUMN agents.deposit_commission_pct IS 'เปอร์เซ็นต์ commission จาก deposit / Agent commission rate on deposits';

-- ===========================================================================
-- 3.4 MERCHANTS - ร้านค้าที่ใช้ระบบรับชำระเงิน (Payment-Accepting Businesses)
-- อยู่ล่างสุด: Partner -> Agent -> Merchant
-- Bottom of hierarchy: Partner -> Agent -> Merchant
-- integrate ผ่าน API (API Key + HMAC-SHA256 signature)
-- Integrate via API with API Key + HMAC-SHA256 signatures
-- ===========================================================================
CREATE TABLE merchants (
    -- Primary Key: UUID v4 / Unique merchant identifier
    id                          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- FK ไปหา agent (nullable - direct merchant ไม่มี agent)
    -- Parent agent (NULL = direct merchant, no agent)
    agent_id                    UUID REFERENCES agents(id),
    -- email สำหรับ login / Login email
    email                       VARCHAR(255) NOT NULL UNIQUE,
    -- bcrypt hash ของ password / bcrypt hashed password
    password_hash               VARCHAR(255) NOT NULL,
    -- AES-256 encrypted TOTP secret สำหรับ 2FA / Encrypted TOTP for 2FA
    totp_secret_enc             BYTEA,
    -- ชื่อบริษัท / Company name
    company_name                VARCHAR(200) NOT NULL,
    -- ชื่อที่แสดง / Display name
    display_name                VARCHAR(100) NOT NULL,
    -- ===== API Authentication (การยืนยันตัวตน API) =====
    -- bcrypt hash ของ API key (rpay_xxxxx...) - key จริงส่งให้แค่ครั้งเดียว
    -- bcrypt hash of full API key - raw key only returned once at creation
    api_key_hash                VARCHAR(255) NOT NULL,
    -- 8 ตัวแรกของ API key สำหรับ quick lookup
    -- First 8 chars of API key for fast lookup
    api_key_prefix              VARCHAR(8) NOT NULL,
    -- AES-256 encrypted HMAC secret สำหรับ sign/verify API requests
    -- AES-256 encrypted HMAC secret for signing/verifying API requests
    hmac_secret_enc             BYTEA NOT NULL,
    -- ===== Webhook Configuration (การตั้งค่า Webhook) =====
    -- URL ที่จะรับ payment callbacks (POST)
    -- URL to receive payment callback notifications (POST)
    webhook_url                 VARCHAR(500),
    -- AES-256 encrypted webhook secret สำหรับ sign webhook payloads
    -- AES-256 encrypted secret for signing webhook payloads
    webhook_secret_enc          BYTEA,
    -- ===== Security (ความปลอดภัย) =====
    -- Array ของ IP ที่อนุญาตเรียก API (null = ทั้งหมด)
    -- Allowed IPs for API calls (null = allow all)
    ip_whitelist                INET[],
    -- ===== Deposit Configuration (การตั้งค่า Deposit) =====
    -- วิธีจัดการ order ที่ยอดเงินซ้ำ: 'unique_amount' หรือ 'time_based'
    -- How to handle same-amount orders: 'unique_amount' or 'time_based'
    duplicate_amount_strategy   VARCHAR(20) NOT NULL DEFAULT 'unique_amount',
    -- เวลา (วินาที) ก่อน order หมดอายุ (default 300 = 5 นาที)
    -- Seconds before unpaid deposit order expires (default 5 minutes)
    deposit_timeout_sec         INT NOT NULL DEFAULT 300,
    -- ===== Fee Rates (อัตรา fee) =====
    -- เปอร์เซ็นต์ fee สำหรับ deposit (เช่น 0.0300 = 3.00%)
    -- Fee percentage charged on deposits (e.g. 0.0300 = 3.00%)
    deposit_fee_pct             NUMERIC(5,4) NOT NULL DEFAULT 0,
    -- เปอร์เซ็นต์ fee สำหรับ withdrawal THB
    -- Fee percentage charged on THB withdrawals
    withdraw_fee_pct            NUMERIC(5,4) NOT NULL DEFAULT 0,
    -- เปอร์เซ็นต์ fee สำหรับ withdrawal USDT
    -- Fee percentage charged on USDT withdrawals
    usdt_withdraw_fee_pct       NUMERIC(5,4) NOT NULL DEFAULT 0,
    -- ===== Withdrawal Limits (วงเงินถอน) =====
    -- วงเงินถอน THB สูงสุดต่อวัน (NULL = ไม่จำกัด)
    -- Maximum THB withdrawal per day (NULL = unlimited)
    daily_withdraw_limit_thb    NUMERIC(14,2),
    -- วงเงินถอน USDT สูงสุดต่อวัน (NULL = ไม่จำกัด)
    -- Maximum USDT withdrawal per day (NULL = unlimited)
    daily_withdraw_limit_usdt   NUMERIC(14,6),
    -- ===== Telegram Integration =====
    -- Telegram group ID สำหรับส่งสลิปตรวจสอบ (1 group ต่อ 1 merchant)
    -- Telegram group ID for slip verification (1 group per merchant)
    telegram_group_id           BIGINT,
    -- สถานะ / Status
    status                      merchant_status NOT NULL DEFAULT 'pending',
    -- Soft disable / Soft disable flag
    is_active                   BOOLEAN NOT NULL DEFAULT true,
    -- ล็อคบัญชี / Lock until
    locked_until                TIMESTAMPTZ,
    -- จำนวนครั้งที่ login ผิด / Failed login attempts
    failed_attempts             SMALLINT NOT NULL DEFAULT 0,
    -- Admin ที่สร้าง / Created by admin
    created_by_admin_id         UUID REFERENCES admins(id),
    -- timestamps
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Index: ค้นหา merchants ของ agent (สำหรับ agent dashboard)
-- Fast lookup by agent (for agent dashboard showing their merchants)
CREATE INDEX idx_merchants_agent ON merchants(agent_id) WHERE agent_id IS NOT NULL;
-- Index: ค้นหา merchant ด้วย API key prefix (ขั้นตอนแรกของ API authentication)
-- Fast API key lookup by prefix (first step of API authentication)
CREATE INDEX idx_merchants_api_prefix ON merchants(api_key_prefix);

COMMENT ON TABLE merchants IS 'ตาราง Merchant - ร้านค้าที่ใช้ระบบ RichPayment รับชำระเงินผ่าน API / Payment-accepting businesses';
COMMENT ON COLUMN merchants.api_key_hash IS 'bcrypt hash ของ API key - key จริงส่งให้ merchant แค่ครั้งเดียวตอนสร้าง / Hash of API key, raw key returned once';
COMMENT ON COLUMN merchants.hmac_secret_enc IS 'AES-256 encrypted HMAC secret สำหรับ sign/verify requests / Encrypted HMAC signing secret';
COMMENT ON COLUMN merchants.duplicate_amount_strategy IS 'วิธีจัดการยอดซ้ำ: unique_amount (เพิ่มสตางค์) หรือ time_based / Same-amount handling strategy';
COMMENT ON COLUMN merchants.deposit_timeout_sec IS 'เวลาหมดอายุของ order (วินาที) default 300 = 5 นาที / Order expiry timeout in seconds';

-- ===========================================================================
-- 3.5 BANK ACCOUNTS - บัญชีธนาคารสำหรับรับเงิน (Receiving Bank Accounts)
-- บัญชีเหล่านี้เป็นของ partner ใช้รับเงินจากลูกค้า
-- These accounts belong to partners, used to receive customer payments
-- ระบบจะ monitor SMS/Email จากบัญชีเหล่านี้เพื่อตรวจจับเงินเข้า
-- System monitors SMS/Email from these accounts to detect incoming payments
-- Auto-switch เมื่อถึง daily limit หรือเกิด error
-- Auto-rotates when daily limit reached or errors detected
-- ===========================================================================
CREATE TABLE bank_accounts (
    -- Primary Key: UUID v4 / Unique bank account identifier
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- FK ไปหา partner ที่เป็นเจ้าของ / Which partner owns this account
    partner_id           UUID NOT NULL REFERENCES partners(id),
    -- รหัสธนาคาร เช่น 'KBANK', 'SCB', 'BBL', 'KTB'
    -- Bank identifier code (e.g. 'KBANK', 'SCB', 'BBL', 'KTB')
    bank_code            VARCHAR(10) NOT NULL,
    -- เลขบัญชีเข้ารหัส AES-256 (ข้อมูล sensitive!)
    -- AES-256 encrypted bank account number (sensitive data!)
    account_number_enc   BYTEA NOT NULL,
    -- ชื่อเจ้าของบัญชี (แสดงให้ลูกค้าเห็นในหน้าชำระเงิน)
    -- Account holder name (shown to customer on payment page)
    account_name         VARCHAR(200) NOT NULL,
    -- 4 หลักท้ายของเลขบัญชี (สำหรับแสดง/ระบุตัว)
    -- Last 4 digits of account number (for display/identification)
    account_number_last4 VARCHAR(4) NOT NULL,
    -- ===== Operational Settings (การตั้งค่าการทำงาน) =====
    -- เปิด/ปิดบัญชี (master switch)
    -- Master on/off switch
    is_active            BOOLEAN NOT NULL DEFAULT true,
    -- วงเงินรับต่อวัน (default 2M THB)
    -- Maximum amount receivable per day (default 2M THB)
    daily_limit_thb      NUMERIC(14,2) NOT NULL DEFAULT 2000000,
    -- ยอดที่รับไปแล้ววันนี้ (reset ตอนเที่ยงคืนโดย scheduler)
    -- Amount already received today (reset daily by scheduler at midnight)
    daily_received_thb   NUMERIC(14,2) NOT NULL DEFAULT 0,
    -- ยอดเงินปัจจุบันในบัญชี (update โดย monitor)
    -- Last known balance in this account (updated by monitor)
    current_balance_thb  NUMERIC(14,2),
    -- เวลาที่ตรวจสอบยอดล่าสุด / When balance was last checked
    last_balance_check   TIMESTAMPTZ,
    -- ===== SMS Parsing Configuration (การตั้งค่า SMS) =====
    -- เบอร์โทรผู้ส่ง SMS ที่คาดหวัง (สำหรับ anti-spoofing)
    -- Expected SMS sender phone numbers (for anti-spoofing validation)
    sms_sender_numbers   VARCHAR(20)[],
    -- จำนวน error ติดต่อกัน (auto-disable เมื่อเกิน max)
    -- Consecutive SMS parsing errors (auto-disable after max_sms_errors)
    sms_error_count      INT NOT NULL DEFAULT 0,
    -- จำนวน error สูงสุดก่อน auto-disable
    -- Max errors before auto-disabling this account
    max_sms_errors       INT NOT NULL DEFAULT 5,
    -- สถานะ: 'active', 'paused', 'limit_reached', 'error', 'disabled'
    -- Status: active, paused (manual), limit_reached, error (SMS errors), disabled (admin)
    status               VARCHAR(20) NOT NULL DEFAULT 'active',
    -- timestamps
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Index: ค้นหาบัญชีของ partner / Find all accounts belonging to a partner
CREATE INDEX idx_bank_accounts_partner ON bank_accounts(partner_id);
-- Index: ค้นหาบัญชีที่ active (สำหรับ account pool selection)
-- Fast lookup for active accounts (account pool selection)
CREATE INDEX idx_bank_accounts_active ON bank_accounts(status) WHERE status = 'active';

COMMENT ON TABLE bank_accounts IS 'บัญชีธนาคารของ partner สำหรับรับเงิน deposit - ระบบ monitor SMS/Email อัตโนมัติ / Partner bank accounts for receiving deposits';
COMMENT ON COLUMN bank_accounts.account_number_enc IS 'เลขบัญชีเข้ารหัส AES-256 - ข้อมูล sensitive ห้ามเก็บ plaintext / AES-256 encrypted, never store plaintext';
COMMENT ON COLUMN bank_accounts.daily_limit_thb IS 'วงเงินรับต่อวัน default 2M THB - auto-switch เมื่อถึง limit / Daily receiving limit, auto-rotation when reached';
COMMENT ON COLUMN bank_accounts.daily_received_thb IS 'ยอดรับวันนี้ - reset ตอนเที่ยงคืนโดย scheduler / Today''s received amount, reset at midnight';
COMMENT ON COLUMN bank_accounts.sms_sender_numbers IS 'เบอร์ผู้ส่ง SMS ที่อนุญาต สำหรับป้องกัน SMS spoofing / Whitelisted SMS senders for anti-spoofing';

-- ===========================================================================
-- 3.6 BANK ACCOUNT <-> MERCHANT MAPPING
-- ตาราง junction ที่ควบคุมว่าบัญชีไหนรับเงินให้ merchant ไหน
-- Junction table controlling which accounts receive payments for which merchants
-- 1 บัญชีรับได้หลาย merchant, 1 merchant ใช้ได้หลายบัญชี
-- Many-to-many: one account serves multiple merchants, one merchant uses multiple accounts
-- ===========================================================================
CREATE TABLE bank_account_merchant_map (
    -- FK ไปหาบัญชีธนาคาร / The bank account
    bank_account_id UUID NOT NULL REFERENCES bank_accounts(id),
    -- FK ไปหา merchant / The merchant it serves
    merchant_id     UUID NOT NULL REFERENCES merchants(id),
    -- ลำดับความสำคัญ (0 = สูงสุด) / Selection priority (0 = highest)
    priority        SMALLINT NOT NULL DEFAULT 0,
    -- เปิด/ปิด mapping นี้ / Enable/disable this specific mapping
    is_active       BOOLEAN NOT NULL DEFAULT true,
    -- timestamps
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Composite primary key
    PRIMARY KEY (bank_account_id, merchant_id)
);

-- Index: ค้นหาบัญชีที่ map กับ merchant (ใช้ตอนสร้าง deposit order)
-- Find accounts mapped to a merchant (used when creating deposit orders)
CREATE INDEX idx_bam_merchant ON bank_account_merchant_map(merchant_id);

COMMENT ON TABLE bank_account_merchant_map IS 'Mapping ระหว่าง bank account กับ merchant - ควบคุมว่าบัญชีไหนรับเงินให้ใคร / Bank account to merchant mapping';
COMMENT ON COLUMN bank_account_merchant_map.priority IS 'ลำดับความสำคัญในการเลือก (0 = เลือกก่อน) / Selection priority (0 = highest, preferred first)';

-- ===========================================================================
-- 3.7 HOLDING ACCOUNTS - บัญชีพักเงิน (Treasury Accounts)
-- เงินจะถูกโอนจากบัญชีรับเงิน (bank_accounts) มาพักที่นี่เพื่อความปลอดภัย
-- Funds consolidated from receiving accounts to here for safety
-- ต้องลงทะเบียนล่วงหน้า - ป้องกันการโอนเงินไปบัญชีที่ไม่ได้ authorize
-- Must be pre-registered - prevents transfers to unauthorized accounts
-- ===========================================================================
CREATE TABLE holding_accounts (
    -- Primary Key: UUID v4 / Unique holding account identifier
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- รหัสธนาคาร / Bank code (e.g. 'KBANK', 'SCB')
    bank_code               VARCHAR(10) NOT NULL,
    -- เลขบัญชีเข้ารหัส / AES-256 encrypted account number
    account_number_enc      BYTEA NOT NULL,
    -- ชื่อเจ้าของบัญชี / Account holder name
    account_name            VARCHAR(200) NOT NULL,
    -- 4 หลักท้าย / Last 4 digits for display
    account_number_last4    VARCHAR(4) NOT NULL,
    -- เปิด/ปิดบัญชี / Enable/disable
    is_active               BOOLEAN NOT NULL DEFAULT true,
    -- Threshold สำหรับ auto-transfer (NULL = manual only)
    -- Auto-transfer when receiving account balance exceeds this (NULL = manual only)
    auto_transfer_threshold NUMERIC(14,2),
    -- timestamps
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE holding_accounts IS 'บัญชีพักเงิน (treasury) - ต้องลงทะเบียนก่อนเท่านั้นที่จะโอนเงินไปได้ / Pre-registered safe accounts for fund consolidation';
COMMENT ON COLUMN holding_accounts.auto_transfer_threshold IS 'Auto-transfer เมื่อบัญชีรับเกินจำนวนนี้ NULL = manual only / Threshold for automatic transfers';

-- ===========================================================================
-- 3.8 WALLETS - กระเป๋าเงินของทุก entity (Balance Tracking)
-- ทุก merchant, agent, partner และ system มี wallet ของตัวเอง
-- Every merchant, agent, partner, and system has its own wallet
-- ใช้ optimistic locking (version column) ป้องกัน race condition
-- Uses optimistic locking (version column) to prevent concurrent update issues
-- ===========================================================================
CREATE TABLE wallets (
    -- Primary Key: UUID v4 / Unique wallet identifier
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- ประเภทเจ้าของ: merchant, agent, partner, system
    -- Owner type: merchant, agent, partner, system
    owner_type   owner_type NOT NULL,
    -- UUID ของเจ้าของ (polymorphic - ไม่ใช้ FK เพราะชี้ไปหลายตาราง)
    -- Owner's ID (polymorphic - no FK because it references multiple tables)
    owner_id     UUID NOT NULL,
    -- สกุลเงิน ISO 4217 / ISO 4217 currency code
    currency     VARCHAR(3) NOT NULL DEFAULT 'THB',
    -- ยอดเงินทั้งหมด (รวม hold) / Total balance including held funds
    balance      NUMERIC(14,2) NOT NULL DEFAULT 0,
    -- ยอดเงินที่ถูก hold (สำหรับ pending withdrawal)
    -- Held balance (reserved for pending withdrawals)
    -- ยอดที่ใช้ได้จริง = balance - hold_balance
    -- Available balance = balance - hold_balance
    hold_balance NUMERIC(14,2) NOT NULL DEFAULT 0,
    -- Optimistic lock version - เพิ่มทุกครั้งที่ update balance
    -- Optimistic lock counter, incremented on every balance update
    -- ใช้ WHERE version = ? เพื่อตรวจจับ concurrent modification
    -- Used with WHERE version = ? to detect concurrent modifications
    version      BIGINT NOT NULL DEFAULT 0,
    -- timestamps
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Unique constraint: แต่ละ owner มี wallet เดียวต่อสกุลเงิน
    -- One wallet per owner per currency
    UNIQUE(owner_type, owner_id, currency)
);

COMMENT ON TABLE wallets IS 'กระเป๋าเงินของทุก entity - ใช้ optimistic locking ป้องกัน race condition / Wallet balances with optimistic locking';
COMMENT ON COLUMN wallets.version IS 'Optimistic lock counter - UPDATE ... WHERE version = ? ตรวจจับ concurrent modification / Prevents double-spend';
COMMENT ON COLUMN wallets.hold_balance IS 'เงินที่ถูกกันไว้ (รอ approve withdrawal) - available = balance - hold_balance / Reserved for pending operations';

-- ===========================================================================
-- 3.9 DEPOSIT ORDERS - รายการฝากเงิน (PARTITIONED BY MONTH)
-- Flow: Merchant สร้าง order via API -> ระบบ return QR code + amount
--    -> ลูกค้าโอนเงิน -> SMS/Email detected -> Order matched -> Webhook sent
-- Partitioned by created_at สำหรับ fast queries และ easy archival
-- ===========================================================================
CREATE TABLE deposit_orders (
    -- Primary Key: UUID v4 (composite กับ created_at เพราะ partitioning)
    -- UUID v4 (composite with created_at required for partitioning)
    id                   UUID NOT NULL DEFAULT gen_random_uuid(),
    -- FK ไปหา merchant ที่สร้าง order / Which merchant created this order
    merchant_id          UUID NOT NULL,
    -- Reference ID ของ merchant เอง (สำหรับ idempotency + reconciliation)
    -- Merchant's own order reference (for idempotency and reconciliation)
    merchant_order_id    VARCHAR(100) NOT NULL,
    -- ===== Customer Information (ข้อมูลลูกค้า) =====
    -- ชื่อลูกค้า (สำหรับ cross-check กับชื่อผู้โอนในสลิป)
    -- Customer name (for cross-checking with slip sender name)
    customer_name        VARCHAR(200),
    -- รหัสธนาคารของลูกค้า / Customer's bank code
    customer_bank_code   VARCHAR(10),
    -- ===== Amount Fields (จำนวนเงิน) =====
    -- จำนวนเงินที่ merchant ขอ / Original amount requested by merchant
    requested_amount     NUMERIC(12,2) NOT NULL,
    -- จำนวนเงินหลังปรับ (เพิ่มสตางค์เพื่อให้ unique สำหรับ matching)
    -- Adjusted amount for uniqueness (e.g. 1000.00 -> 1000.37)
    adjusted_amount      NUMERIC(12,2) NOT NULL,
    -- จำนวนเงินที่ได้รับจริง (fill หลัง match)
    -- Actual amount received (filled after match)
    actual_amount        NUMERIC(12,2),
    -- ค่า fee ที่เก็บ / Fee deducted
    fee_amount           NUMERIC(12,2),
    -- จำนวนเงินสุทธิเข้า wallet (actual - fee)
    -- Net amount credited to wallet (actual - fee)
    net_amount           NUMERIC(12,2),
    -- สกุลเงิน / Currency
    currency             VARCHAR(3) NOT NULL DEFAULT 'THB',
    -- ===== Matching Information (ข้อมูลการ match) =====
    -- บัญชีธนาคารที่ assign ให้รับเงิน order นี้
    -- Bank account assigned to receive this payment
    bank_account_id      UUID,
    -- วิธีที่ match ได้: sms, email, slip (NULL ถ้ายังไม่ match)
    -- How the payment was detected: sms, email, slip (NULL if not matched)
    matched_by           VARCHAR(20),
    -- เวลาที่ match สำเร็จ / When the match occurred
    matched_at           TIMESTAMPTZ,
    -- FK ไปหา sms_messages ถ้า match ด้วย SMS
    -- FK to sms_messages if matched by SMS
    sms_message_id       UUID,
    -- FK ไปหา slip_verifications ถ้า match ด้วย slip
    -- FK to slip_verifications if matched by slip
    slip_verification_id UUID,
    -- สถานะ: pending, matched, completed, expired, failed, cancelled
    -- Status: pending, matched, completed, expired, failed, cancelled
    status               VARCHAR(20) NOT NULL DEFAULT 'pending',
    -- ===== QR Code =====
    -- PromptPay QR payload string ที่ลูกค้าสแกน
    -- PromptPay QR payload string for customer to scan
    qr_payload           TEXT,
    -- ===== Webhook Tracking (ติดตามการส่ง webhook) =====
    -- ส่ง webhook ไป merchant แล้วหรือยัง / Has webhook been sent?
    webhook_sent         BOOLEAN NOT NULL DEFAULT false,
    -- เวลาที่ส่ง webhook สำเร็จ / When webhook was delivered
    webhook_sent_at      TIMESTAMPTZ,
    -- จำนวนครั้งที่พยายามส่ง webhook / Webhook delivery attempts
    webhook_attempts     SMALLINT NOT NULL DEFAULT 0,
    -- ===== Timestamps =====
    -- เวลาหมดอายุ (ถ้าไม่ match ภายในเวลานี้จะเป็น expired)
    -- Expiry deadline (order becomes expired if not matched by this time)
    expires_at           TIMESTAMPTZ NOT NULL,
    -- เวลาสร้าง / Creation time
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- เวลาแก้ไขล่าสุด / Last modification time
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Composite PK required for range partitioning
    -- ต้องใช้ composite PK เพราะ PostgreSQL partitioning
    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);

-- สร้าง partitions เริ่มต้น (เดือนปัจจุบัน + 2 เดือนข้างหน้า)
-- Create initial partitions (current month + 2 months ahead)
CREATE TABLE deposit_orders_2026_04 PARTITION OF deposit_orders
    FOR VALUES FROM ('2026-04-01') TO ('2026-05-01');
CREATE TABLE deposit_orders_2026_05 PARTITION OF deposit_orders
    FOR VALUES FROM ('2026-05-01') TO ('2026-06-01');
CREATE TABLE deposit_orders_2026_06 PARTITION OF deposit_orders
    FOR VALUES FROM ('2026-06-01') TO ('2026-07-01');

-- Index: ค้นหา orders ของ merchant ตาม status (สำหรับ dashboard)
-- Find merchant's orders by status (dashboard listing)
CREATE INDEX idx_do_merchant_status ON deposit_orders(merchant_id, status, created_at DESC);
-- Index: ** HOT PATH ** ค้นหา pending orders ด้วย bank_account_id + amount (สำหรับ SMS matching)
-- ** HOT PATH ** Find pending orders by bank account + amount (SMS matching)
CREATE INDEX idx_do_pending_amount ON deposit_orders(bank_account_id, adjusted_amount, status)
    WHERE status = 'pending';
-- Index: ค้นหา order ด้วย merchant reference / Find by merchant reference
CREATE INDEX idx_do_merchant_order ON deposit_orders(merchant_id, merchant_order_id, created_at DESC);
-- Index: ค้นหา orders ที่หมดอายุ (สำหรับ timeout worker ของ scheduler)
-- Find expired orders (for scheduler's timeout worker)
CREATE INDEX idx_do_expires ON deposit_orders(expires_at) WHERE status = 'pending';

COMMENT ON TABLE deposit_orders IS 'รายการฝากเงิน (partitioned by month) - match กับ bank transactions แล้ว settle / Deposit orders matched to bank transactions';
COMMENT ON COLUMN deposit_orders.adjusted_amount IS 'จำนวนเงินหลังเพิ่มสตางค์ เช่น 1000 -> 1000.37 เพื่อ unique matching / Uniquified amount for matching';
COMMENT ON COLUMN deposit_orders.merchant_order_id IS 'Reference จาก merchant สำหรับ idempotency / Merchant reference for idempotency';

-- ===========================================================================
-- 3.10 WALLET LEDGER - บันทึกทุกการเปลี่ยนแปลง balance (PARTITIONED)
-- Append-only audit log - ทุก credit/debit ถูกบันทึกที่นี่
-- Immutable ledger for every balance change (source of truth for audit)
-- ===========================================================================
CREATE TABLE wallet_ledger (
    -- Auto-incrementing ID / Primary key for ordering
    id             BIGINT GENERATED ALWAYS AS IDENTITY,
    -- FK ไปหา wallet ที่ balance เปลี่ยน / Which wallet was affected
    wallet_id      UUID NOT NULL,
    -- ประเภทรายการ (deposit_credit, withdrawal_debit, fee_debit, etc.)
    -- Entry type describing the balance change reason
    entry_type     VARCHAR(30) NOT NULL,
    -- ประเภท entity ที่ trigger: 'deposit_order', 'withdrawal', 'commission', 'admin_adjustment'
    -- Source entity type that triggered this entry
    reference_type VARCHAR(30),
    -- UUID ของ entity ที่ trigger / Source entity ID
    reference_id   UUID,
    -- จำนวนเงิน: + = เงินเข้า, - = เงินออก
    -- Amount: positive = credit, negative = debit
    amount         NUMERIC(14,2) NOT NULL,
    -- Balance ของ wallet หลังรายการนี้ (สำหรับ audit + reconciliation)
    -- Wallet balance immediately after this entry
    balance_after  NUMERIC(14,2) NOT NULL,
    -- คำอธิบาย (human-readable) / Human-readable description
    description    VARCHAR(500),
    -- timestamp (append-only ไม่มี updated_at)
    -- Creation time (append-only, no updates)
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Composite PK required for partitioning
    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);

-- สร้าง partitions เริ่มต้น / Create initial partitions
CREATE TABLE wallet_ledger_2026_04 PARTITION OF wallet_ledger
    FOR VALUES FROM ('2026-04-01') TO ('2026-05-01');
CREATE TABLE wallet_ledger_2026_05 PARTITION OF wallet_ledger
    FOR VALUES FROM ('2026-05-01') TO ('2026-06-01');
CREATE TABLE wallet_ledger_2026_06 PARTITION OF wallet_ledger
    FOR VALUES FROM ('2026-06-01') TO ('2026-07-01');

-- Index: ดูประวัติ wallet เรียงจากใหม่สุด / Wallet history sorted newest first
CREATE INDEX idx_wl_wallet ON wallet_ledger(wallet_id, created_at DESC);

COMMENT ON TABLE wallet_ledger IS 'Append-only audit log ของทุก balance change - source of truth สำหรับ reconciliation / Immutable ledger entries';
COMMENT ON COLUMN wallet_ledger.balance_after IS 'Snapshot balance หลังรายการนี้ - ช่วย debug balance discrepancy / Balance snapshot for auditing';

-- ===========================================================================
-- 3.11 WITHDRAWALS - คำขอถอนเงิน (PARTITIONED)
-- Flow: Merchant สร้าง -> Admin review -> Approve/Reject
--    -> Admin โอนเงินจริง -> Mark completed
-- Supports THB (bank transfer) and USDT withdrawals
-- ===========================================================================
CREATE TABLE withdrawals (
    -- Primary Key: UUID v4 / Unique withdrawal identifier
    id                   UUID NOT NULL DEFAULT gen_random_uuid(),
    -- FK ไปหา merchant / Which merchant requested
    merchant_id          UUID NOT NULL,
    -- ===== Amount Fields =====
    -- จำนวนเงินที่ขอถอน (gross) / Requested withdrawal amount
    amount               NUMERIC(14,2) NOT NULL,
    -- ค่า fee / Fee charged
    fee_amount           NUMERIC(14,2) NOT NULL DEFAULT 0,
    -- จำนวนเงินสุทธิที่โอน (amount - fee) / Net amount transferred
    net_amount           NUMERIC(14,2) NOT NULL,
    -- สกุลเงิน: 'THB' หรือ 'USDT' / Currency
    currency             VARCHAR(3) NOT NULL DEFAULT 'THB',
    -- ===== Destination (ปลายทาง) =====
    -- ประเภทปลายทาง: 'bank' หรือ 'usdt' / Destination type
    destination_type     VARCHAR(10) NOT NULL,
    -- รหัสธนาคาร (สำหรับ bank transfer) / Bank code for bank transfers
    bank_code            VARCHAR(10),
    -- เลขบัญชีเข้ารหัส / AES-256 encrypted destination account
    account_number_enc   BYTEA,
    -- ชื่อเจ้าของบัญชีปลายทาง / Destination account holder name
    account_name         VARCHAR(200),
    -- USDT wallet address เข้ารหัส / AES-256 encrypted USDT address
    usdt_address_enc     BYTEA,
    -- USDT network: 'TRC20' หรือ 'ERC20' / USDT network
    usdt_network         VARCHAR(10),
    -- ===== Status & Approval =====
    -- สถานะ: pending, approved, processing, completed, rejected
    -- Status: pending, approved, processing, completed, rejected
    status               VARCHAR(20) NOT NULL DEFAULT 'pending',
    -- Admin ที่ approve/reject / Which admin approved/rejected
    approved_by_admin_id UUID,
    -- เวลาที่ approve / When approved
    approved_at          TIMESTAMPTZ,
    -- เวลาที่ complete / When transfer was confirmed
    completed_at         TIMESTAMPTZ,
    -- เหตุผลที่ reject / Reason for rejection
    rejection_reason     VARCHAR(500),
    -- ===== Proof of Transfer (หลักฐานการโอน) =====
    -- URL ของหลักฐาน (screenshot/PDF) / Transfer receipt URL
    transfer_proof_url   VARCHAR(500),
    -- เลข reference จากธนาคาร / Bank transfer reference number
    transfer_reference   VARCHAR(100),
    -- timestamps
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Composite PK required for partitioning
    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);

-- สร้าง partitions เริ่มต้น / Create initial partitions
CREATE TABLE withdrawals_2026_04 PARTITION OF withdrawals
    FOR VALUES FROM ('2026-04-01') TO ('2026-05-01');
CREATE TABLE withdrawals_2026_05 PARTITION OF withdrawals
    FOR VALUES FROM ('2026-05-01') TO ('2026-06-01');
CREATE TABLE withdrawals_2026_06 PARTITION OF withdrawals
    FOR VALUES FROM ('2026-06-01') TO ('2026-07-01');

-- Index: ประวัติ withdrawal ของ merchant / Merchant's withdrawal history
CREATE INDEX idx_wd_merchant ON withdrawals(merchant_id, status, created_at DESC);
-- Index: Admin ดู pending withdrawals ที่รอ approve / Admin view pending for approval
CREATE INDEX idx_wd_status ON withdrawals(status, created_at DESC);

COMMENT ON TABLE withdrawals IS 'คำขอถอนเงิน (partitioned) - ผ่านขั้นตอน approve แล้วโอนจริง / Withdrawal requests with approval workflow';
COMMENT ON COLUMN withdrawals.destination_type IS 'ประเภทปลายทาง: bank = โอนธนาคาร, usdt = crypto / bank or usdt transfer type';

-- ===========================================================================
-- 3.12 COMMISSIONS - บันทึกการแบ่ง fee ต่อ transaction (PARTITIONED)
-- ทุก deposit/withdrawal ที่ complete จะสร้าง commission record
-- Fee split: total_fee = system_share + agent_share + partner_share
-- Snapshot ของ rate % ณ เวลา transaction (สำหรับ audit)
-- ===========================================================================
CREATE TABLE commissions (
    -- Primary Key: UUID v4 / Unique commission record identifier
    id               UUID NOT NULL DEFAULT gen_random_uuid(),
    -- ประเภท transaction: 'deposit' หรือ 'withdrawal'
    -- Transaction type: deposit or withdrawal
    transaction_type VARCHAR(10) NOT NULL,
    -- UUID ของ deposit_order หรือ withdrawal / Source transaction ID
    transaction_id   UUID NOT NULL,
    -- FK ไปหา merchant / Which merchant's transaction
    merchant_id      UUID NOT NULL,
    -- ===== Commission Breakdown (การแบ่ง fee) =====
    -- Fee ทั้งหมดที่เก็บจาก merchant (ก่อนแบ่ง)
    -- Total fee collected from merchant (before splitting)
    total_fee_amount NUMERIC(12,2) NOT NULL,
    -- ส่วนของระบบ (platform) / System's share
    system_amount    NUMERIC(12,2) NOT NULL,
    -- FK ไปหา agent (nullable) / Agent who gets commission
    agent_id         UUID,
    -- ส่วนของ agent / Agent's commission amount
    agent_amount     NUMERIC(12,2) NOT NULL DEFAULT 0,
    -- FK ไปหา partner (nullable) / Partner who gets commission
    partner_id       UUID,
    -- ส่วนของ partner / Partner's commission amount
    partner_amount   NUMERIC(12,2) NOT NULL DEFAULT 0,
    -- ===== Rate Snapshot (snapshot rate ณ เวลา transaction) =====
    -- Merchant fee % ณ เวลา transaction / Merchant fee rate at time of transaction
    merchant_fee_pct NUMERIC(5,4) NOT NULL,
    -- Agent commission % / Agent's commission rate at time of transaction
    agent_pct        NUMERIC(5,4) NOT NULL DEFAULT 0,
    -- Partner commission % / Partner's commission rate at time of transaction
    partner_pct      NUMERIC(5,4) NOT NULL DEFAULT 0,
    -- สกุลเงิน / Currency
    currency         VARCHAR(3) NOT NULL DEFAULT 'THB',
    -- timestamp (สร้างครั้งเดียว ไม่มี update)
    -- Creation time (created once, never updated)
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Composite PK required for partitioning
    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);

-- สร้าง partitions เริ่มต้น / Create initial partitions
CREATE TABLE commissions_2026_04 PARTITION OF commissions
    FOR VALUES FROM ('2026-04-01') TO ('2026-05-01');
CREATE TABLE commissions_2026_05 PARTITION OF commissions
    FOR VALUES FROM ('2026-05-01') TO ('2026-06-01');
CREATE TABLE commissions_2026_06 PARTITION OF commissions
    FOR VALUES FROM ('2026-06-01') TO ('2026-07-01');

-- Index: ดู commission ของ merchant / Merchant's commission history
CREATE INDEX idx_comm_merchant ON commissions(merchant_id, created_at DESC);
-- Index: ดู commission ของ agent (เฉพาะที่มี agent)
-- Agent's commission (only records with an agent)
CREATE INDEX idx_comm_agent ON commissions(agent_id, created_at DESC) WHERE agent_id IS NOT NULL;
-- Index: ดู commission ของ partner (เฉพาะที่มี partner)
-- Partner's commission (only records with a partner)
CREATE INDEX idx_comm_partner ON commissions(partner_id, created_at DESC) WHERE partner_id IS NOT NULL;

COMMENT ON TABLE commissions IS 'บันทึกการแบ่ง fee: system/agent/partner ได้เท่าไหร่จากแต่ละ transaction / Fee split per transaction';
COMMENT ON COLUMN commissions.merchant_fee_pct IS 'Snapshot ของ fee% ณ เวลา transaction - กันกรณี rate เปลี่ยนทีหลัง / Rate snapshot for audit';
COMMENT ON COLUMN commissions.total_fee_amount IS 'Fee ทั้งหมด = system_amount + agent_amount + partner_amount / Total fee before splitting';

-- ===========================================================================
-- 3.13 COMMISSION DAILY SUMMARY - สรุปยอด commission รายวัน (NOT PARTITIONED)
-- Pre-aggregated สำหรับ dashboard queries ที่เร็ว
-- ตารางนี้ไม่ archive - เก็บถาวรตลอด
-- This table is NEVER archived - stays forever for fast dashboard queries
-- ===========================================================================
CREATE TABLE commission_daily_summary (
    -- Auto-incrementing primary key
    id               BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    -- วันที่สรุป (UTC date) / The date this summary covers
    summary_date     DATE NOT NULL,
    -- ประเภทเจ้าของ / Owner type
    owner_type       VARCHAR(10) NOT NULL,
    -- UUID ของเจ้าของ / Owner's ID
    owner_id         UUID NOT NULL,
    -- ประเภท transaction / Transaction type
    transaction_type VARCHAR(10) NOT NULL,
    -- สกุลเงิน / Currency
    currency         VARCHAR(3) NOT NULL DEFAULT 'THB',
    -- จำนวน transaction ทั้งหมดวันนั้น / Total transactions that day
    total_tx_count   INT NOT NULL DEFAULT 0,
    -- ยอดรวม transaction volume / Aggregate transaction volume
    total_volume     NUMERIC(16,2) NOT NULL DEFAULT 0,
    -- ยอดรวม fee / Aggregate fee amount
    total_fee        NUMERIC(14,2) NOT NULL DEFAULT 0,
    -- ยอดรวม commission ที่ owner ได้ / Aggregate commission earned
    total_commission NUMERIC(14,2) NOT NULL DEFAULT 0,
    -- timestamps
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Unique: แต่ละ owner มี summary เดียวต่อวัน/type/currency
    -- One summary per owner per day per transaction type per currency
    UNIQUE(summary_date, owner_type, owner_id, transaction_type, currency)
);

-- Index: Dashboard query - ดู summaries ของ owner เรียงตามวันที่
-- Dashboard query - owner's summaries sorted by date
CREATE INDEX idx_cds_owner ON commission_daily_summary(owner_type, owner_id, summary_date DESC);

COMMENT ON TABLE commission_daily_summary IS 'สรุปยอด commission รายวัน - pre-aggregated ไม่ต้อง scan ทุก row / Pre-aggregated daily commission rollup';

-- ===========================================================================
-- 3.14 SMS MESSAGES - SMS จากธนาคาร (PARTITIONED)
-- ทุก SMS ที่ได้รับจะถูกเก็บไว้สำหรับ audit และ debugging
-- parser-service จะ parse แล้วพยายาม match กับ pending orders
-- Every incoming SMS is stored for audit trail, parsed and matched
-- ===========================================================================
CREATE TABLE sms_messages (
    -- Primary Key: UUID v4 / Unique SMS record identifier
    id               UUID NOT NULL DEFAULT gen_random_uuid(),
    -- เบอร์โทรผู้ส่ง / Phone number that sent this SMS
    sender_number    VARCHAR(20) NOT NULL,
    -- ข้อความ SMS ดิบ (เก็บ original ทั้งหมด) / Full raw SMS text as received
    raw_message      TEXT NOT NULL,
    -- ===== Parsed Data (ข้อมูลที่ parse ได้ - fill โดย parser-service) =====
    -- รหัสธนาคารที่ตรวจพบ / Detected bank code
    bank_code        VARCHAR(10),
    -- จำนวนเงินที่ parse ได้ / Extracted payment amount
    parsed_amount    NUMERIC(12,2),
    -- ชื่อผู้โอน / Extracted sender name
    parsed_sender    VARCHAR(200),
    -- Reference number / Extracted transaction reference
    parsed_ref       VARCHAR(100),
    -- เวลาของ transaction จาก SMS / Extracted transaction timestamp
    parsed_timestamp TIMESTAMPTZ,
    -- ===== Matching (การ match กับ order) =====
    -- บัญชีธนาคารที่ SMS นี้เกี่ยวข้อง / Which bank account this SMS belongs to
    bank_account_id  UUID,
    -- Order ที่ match ได้ (NULL ถ้ายังไม่ match) / Matched deposit order
    matched_order_id UUID,
    -- สถานะ match: pending, matched, unmatched, duplicate, spoofing_suspect
    -- Match status
    match_status     VARCHAR(20) NOT NULL DEFAULT 'pending',
    -- ===== Validation (การตรวจสอบ) =====
    -- ผ่าน anti-spoofing check หรือไม่ / Passed anti-spoofing validation
    is_valid         BOOLEAN,
    -- รายละเอียดการตรวจสอบ / Validation notes
    validation_notes VARCHAR(500),
    -- เวลาที่ระบบได้รับ SMS / When SMS was received by our system
    received_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- timestamps
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Composite PK required for partitioning
    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);

-- สร้าง partitions เริ่มต้น / Create initial partitions
CREATE TABLE sms_messages_2026_04 PARTITION OF sms_messages
    FOR VALUES FROM ('2026-04-01') TO ('2026-05-01');
CREATE TABLE sms_messages_2026_05 PARTITION OF sms_messages
    FOR VALUES FROM ('2026-05-01') TO ('2026-06-01');
CREATE TABLE sms_messages_2026_06 PARTITION OF sms_messages
    FOR VALUES FROM ('2026-06-01') TO ('2026-07-01');

-- Index: ** HOT PATH ** ค้นหา pending SMS ด้วย bank_account_id + amount (สำหรับ matching)
-- ** HOT PATH ** Find pending SMS by bank account + amount (for matching)
CREATE INDEX idx_sms_pending ON sms_messages(bank_account_id, parsed_amount, match_status)
    WHERE match_status = 'pending';

COMMENT ON TABLE sms_messages IS 'SMS จากธนาคาร (partitioned) - เก็บทุก SMS สำหรับ audit trail / Bank SMS messages for audit and matching';
COMMENT ON COLUMN sms_messages.match_status IS 'สถานะ: pending (ยังไม่ process), matched, unmatched, duplicate, spoofing_suspect / Match processing status';
COMMENT ON COLUMN sms_messages.is_valid IS 'ผ่าน anti-spoofing check หรือไม่ / Whether SMS passed anti-spoofing validation';

-- ===========================================================================
-- 3.15 SLIP VERIFICATIONS - การตรวจสอบสลิปผ่าน Telegram (PARTITIONED)
-- Fallback flow เมื่อ SMS/Email ไม่ catch payment ได้
-- Merchant ส่งรูปสลิปใน Telegram group -> Bot ตรวจสอบผ่าน EasySlip API
-- Fallback: merchant sends slip photo to Telegram -> Bot verifies via EasySlip
-- ===========================================================================
CREATE TABLE slip_verifications (
    -- Primary Key: UUID v4 / Unique verification record identifier
    id                  UUID NOT NULL DEFAULT gen_random_uuid(),
    -- FK ไปหา merchant / Which merchant submitted this slip
    merchant_id         UUID NOT NULL,
    -- Telegram group ID ที่ส่งสลิป / Telegram group where slip was submitted
    telegram_group_id   BIGINT NOT NULL,
    -- Telegram message ID (สำหรับ reference/reply)
    -- Telegram message ID (for reference/reply)
    telegram_message_id BIGINT NOT NULL,
    -- ===== Image Data =====
    -- SHA-256 hash ของรูปสลิป (สำหรับตรวจจับ duplicate)
    -- SHA-256 hash of slip image (for duplicate detection)
    image_hash          VARCHAR(64) NOT NULL,
    -- URL/path ของรูปที่เก็บ / Stored image URL
    image_url           VARCHAR(500),
    -- ===== EasySlip API Results (ผลตรวจสอบ) =====
    -- Reference number จาก EasySlip / Transaction reference from EasySlip
    easyslip_ref        VARCHAR(100),
    -- จำนวนเงินจากสลิป / Payment amount from slip
    easyslip_amount     NUMERIC(12,2),
    -- ชื่อผู้โอน / Sender name from slip
    easyslip_sender     VARCHAR(200),
    -- ชื่อผู้รับ / Receiver name from slip
    easyslip_receiver   VARCHAR(200),
    -- เวลาของ transaction / Transaction timestamp from slip
    easyslip_timestamp  TIMESTAMPTZ,
    -- Raw response จาก EasySlip API (JSON สำหรับ debugging)
    -- Full raw EasySlip API response (for debugging)
    easyslip_raw        JSONB,
    -- ===== Matching =====
    -- Order ที่ match ได้ (NULL ถ้าไม่ match) / Matched deposit order
    matched_order_id    UUID,
    -- สถานะ: pending, verified, duplicate_slip, no_match, api_error
    -- Status: pending, verified, duplicate_slip, no_match, api_error
    status              VARCHAR(20) NOT NULL DEFAULT 'pending',
    -- timestamps
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Composite PK required for partitioning
    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);

-- สร้าง partitions เริ่มต้น / Create initial partitions
CREATE TABLE slip_verifications_2026_04 PARTITION OF slip_verifications
    FOR VALUES FROM ('2026-04-01') TO ('2026-05-01');
CREATE TABLE slip_verifications_2026_05 PARTITION OF slip_verifications
    FOR VALUES FROM ('2026-05-01') TO ('2026-06-01');
CREATE TABLE slip_verifications_2026_06 PARTITION OF slip_verifications
    FOR VALUES FROM ('2026-06-01') TO ('2026-07-01');

-- Index: ป้องกัน duplicate slips (รูปเดิมส่งซ้ำ)
-- Prevent duplicate slips (same image submitted twice)
CREATE UNIQUE INDEX idx_slip_hash ON slip_verifications(image_hash, created_at);
-- Index: ป้องกัน reuse ของ transaction ref เดิม
-- Prevent reuse of same transaction reference
CREATE INDEX idx_slip_ref ON slip_verifications(easyslip_ref, created_at) WHERE easyslip_ref IS NOT NULL;

COMMENT ON TABLE slip_verifications IS 'การตรวจสอบสลิปผ่าน Telegram + EasySlip API (partitioned) / Slip verification via Telegram bot';
COMMENT ON COLUMN slip_verifications.image_hash IS 'SHA-256 hash ของรูปสลิป สำหรับตรวจจับ duplicate / Image hash for duplicate detection';

-- ===========================================================================
-- 3.16 FUND TRANSFERS - บันทึกการโอนเงินจากบัญชีรับไปบัญชีพัก
-- Admin โอนเงินจาก partner's bank accounts -> holding accounts เพื่อความปลอดภัย
-- Records money moved from receiving accounts to holding (treasury) accounts
-- ===========================================================================
CREATE TABLE fund_transfers (
    -- Primary Key: UUID v4 / Unique transfer record identifier
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- FK ไปหาบัญชีต้นทาง (บัญชีรับเงิน)
    -- Source: partner's receiving account
    from_bank_account_id  UUID NOT NULL REFERENCES bank_accounts(id),
    -- FK ไปหาบัญชีปลายทาง (ต้อง pre-registered!)
    -- Destination: system's holding account (MUST be pre-registered)
    to_holding_account_id UUID NOT NULL REFERENCES holding_accounts(id),
    -- จำนวนเงิน / Transfer amount
    amount                NUMERIC(14,2) NOT NULL,
    -- ประเภท trigger: 'manual' (admin กด) หรือ 'auto' (threshold triggered)
    -- Trigger type: manual (admin clicked) or auto (threshold triggered)
    trigger_type          VARCHAR(10) NOT NULL,
    -- สถานะ: pending, completed, failed / Status
    status                VARCHAR(20) NOT NULL DEFAULT 'pending',
    -- Admin ที่สั่ง (NULL สำหรับ auto) / Which admin initiated (NULL for auto)
    initiated_by_admin    UUID REFERENCES admins(id),
    -- Bank transfer reference number
    transfer_reference    VARCHAR(100),
    -- Admin notes / Admin notes about this transfer
    notes                 VARCHAR(500),
    -- timestamps
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- เวลาที่โอนสำเร็จ / When transfer was confirmed
    completed_at          TIMESTAMPTZ
);

COMMENT ON TABLE fund_transfers IS 'บันทึกการโอนเงินจากบัญชีรับไปบัญชีพัก - เฉพาะบัญชีที่ register ไว้เท่านั้น / Fund transfers to pre-registered holding accounts';
COMMENT ON COLUMN fund_transfers.trigger_type IS 'manual = admin สั่ง, auto = ยอดเกิน threshold / manual or auto trigger';

-- ===========================================================================
-- 3.17 AUDIT LOGS - บันทึกทุก action ในระบบ (PARTITIONED)
-- ใคร (WHO) ทำอะไร (WHAT) กับ resource ไหน (WHICH) เมื่อไหร่ (WHEN)
-- Tracks WHO did WHAT to WHICH resource and WHEN
-- ใช้สำหรับ security review, compliance, debugging
-- Used for security review, compliance, and debugging
-- ===========================================================================
CREATE TABLE audit_logs (
    -- Auto-incrementing ID
    id              BIGINT GENERATED ALWAYS AS IDENTITY,
    -- ประเภทผู้ทำ: admin, agent, merchant, partner, system
    -- Actor type: admin, agent, merchant, partner, system
    actor_type      VARCHAR(10) NOT NULL,
    -- UUID ของผู้ทำ (NULL สำหรับ system actions)
    -- Actor's user ID (NULL for system actions)
    actor_id        UUID,
    -- สิ่งที่ทำ เช่น 'merchant.create', 'withdrawal.approve', 'emergency.freeze'
    -- What was done (e.g. 'merchant.create', 'withdrawal.approve')
    action          VARCHAR(100) NOT NULL,
    -- ประเภท resource ที่ถูกกระทำ / Resource type affected
    resource_type   VARCHAR(50),
    -- UUID ของ resource / Resource ID
    resource_id     UUID,
    -- IP address ของผู้ทำ / Actor's IP address
    ip_address      INET,
    -- Browser user agent string
    user_agent      VARCHAR(500),
    -- Request data ที่ผ่านการ sanitize แล้ว (ลบ passwords/secrets)
    -- Sanitized request data (passwords/secrets REMOVED)
    request_data    JSONB,
    -- HTTP response status code
    response_status SMALLINT,
    -- timestamps
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Composite PK required for partitioning
    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);

-- สร้าง partitions เริ่มต้น / Create initial partitions
CREATE TABLE audit_logs_2026_04 PARTITION OF audit_logs
    FOR VALUES FROM ('2026-04-01') TO ('2026-05-01');
CREATE TABLE audit_logs_2026_05 PARTITION OF audit_logs
    FOR VALUES FROM ('2026-05-01') TO ('2026-06-01');
CREATE TABLE audit_logs_2026_06 PARTITION OF audit_logs
    FOR VALUES FROM ('2026-06-01') TO ('2026-07-01');

-- Index: ค้นหา actions ของ user / Find actions by a specific user
CREATE INDEX idx_audit_actor ON audit_logs(actor_type, actor_id, created_at DESC);
-- Index: ค้นหา actions บน resource / Find all actions on a specific resource
CREATE INDEX idx_audit_resource ON audit_logs(resource_type, resource_id, created_at DESC);
-- Index: ค้นหาตาม action type / Find all occurrences of a specific action
CREATE INDEX idx_audit_action ON audit_logs(action, created_at DESC);

COMMENT ON TABLE audit_logs IS 'Audit log ของทุก action (partitioned) - WHO did WHAT to WHICH WHEN / Complete action audit trail';
COMMENT ON COLUMN audit_logs.action IS 'Action ที่ทำ เช่น merchant.create, withdrawal.approve / Action performed';
COMMENT ON COLUMN audit_logs.request_data IS 'Request data ที่ sanitize แล้ว - ลบ passwords/secrets / Sanitized request data';

-- ===========================================================================
-- 3.18 SYSTEM CONFIG - การตั้งค่าระบบ (Key-Value Store)
-- ใช้สำหรับ settings ที่เปลี่ยนได้ตอน runtime
-- Global system settings that can be changed at runtime
-- ===========================================================================
CREATE TABLE system_config (
    -- Key: ชื่อ setting / Setting name
    key        VARCHAR(100) PRIMARY KEY,
    -- Value: ค่าเป็น JSON (flexible structure) / Setting value as JSON
    value      JSONB NOT NULL,
    -- Admin ที่แก้ไขล่าสุด / Which admin last changed this
    updated_by UUID,
    -- เวลาที่แก้ไขล่าสุด / Last update time
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ค่า default ของระบบ / Default system configuration
INSERT INTO system_config (key, value) VALUES
    -- Emergency freeze: หยุดทุก deposits, withdrawals, fund transfers ทันที
    -- Emergency freeze: stops ALL deposits, withdrawals, and fund transfers
    ('emergency_freeze', '{"enabled": false}'::jsonb),
    -- Default order expiry: 5 นาที (300 วินาที)
    -- Default deposit order expiry: 5 minutes (300 seconds)
    ('default_deposit_timeout', '{"seconds": 300}'::jsonb),
    -- API rate limits ต่อ merchant
    -- API rate limits per merchant
    ('global_rate_limits', '{"per_minute": 60, "per_day": 10000}'::jsonb);

COMMENT ON TABLE system_config IS 'การตั้งค่าระบบ key-value - เปลี่ยนได้ตอน runtime / Global runtime configuration';
COMMENT ON COLUMN system_config.key IS 'ชื่อ setting เช่น emergency_freeze, default_deposit_timeout / Setting name';
COMMENT ON COLUMN system_config.value IS 'ค่าเป็น JSON เช่น {"enabled": false} / Setting value as flexible JSON';

-- ===========================================================================
-- 3.19 WEBHOOK DELIVERIES - บันทึกการส่ง webhook ให้ merchant (PARTITIONED)
-- เมื่อ deposit match จะส่ง webhook ไปหา merchant callback URL
-- Retry สูงสุด 5 ครั้ง: 10s, 30s, 90s, 270s, 810s (exponential backoff)
-- Tracks webhook delivery attempts with exponential backoff retry
-- ===========================================================================
CREATE TABLE webhook_deliveries (
    -- Primary Key: UUID v4 / Unique delivery record identifier
    id              UUID NOT NULL DEFAULT gen_random_uuid(),
    -- FK ไปหา merchant / Which merchant this webhook is for
    merchant_id     UUID NOT NULL,
    -- FK ไปหา order ที่ trigger webhook / Which order triggered this
    order_id        UUID NOT NULL,
    -- URL ปลายทาง (merchant's webhook_url) / Destination URL
    url             VARCHAR(500) NOT NULL,
    -- Payload ที่ส่ง (JSON body) / Full webhook payload
    payload         JSONB NOT NULL,
    -- HTTP status code ที่ได้รับ / HTTP status from merchant
    response_status SMALLINT,
    -- Response body (สำหรับ debugging) / Response body for debugging
    response_body   TEXT,
    -- ครั้งที่พยายามส่ง (1-5) / Current attempt number
    attempt         SMALLINT NOT NULL DEFAULT 1,
    -- สถานะ: pending, success, failed, exhausted
    -- Status: pending, success (2xx), failed (will retry), exhausted (all retries failed)
    status          VARCHAR(20) NOT NULL DEFAULT 'pending',
    -- เวลา retry ถัดไป / When to retry next
    next_retry_at   TIMESTAMPTZ,
    -- timestamps
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Composite PK required for partitioning
    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);

-- สร้าง partitions เริ่มต้น / Create initial partitions
CREATE TABLE webhook_deliveries_2026_04 PARTITION OF webhook_deliveries
    FOR VALUES FROM ('2026-04-01') TO ('2026-05-01');
CREATE TABLE webhook_deliveries_2026_05 PARTITION OF webhook_deliveries
    FOR VALUES FROM ('2026-05-01') TO ('2026-06-01');
CREATE TABLE webhook_deliveries_2026_06 PARTITION OF webhook_deliveries
    FOR VALUES FROM ('2026-06-01') TO ('2026-07-01');

COMMENT ON TABLE webhook_deliveries IS 'บันทึกการส่ง webhook (partitioned) - retry 5 ครั้ง exponential backoff / Webhook delivery tracking with retries';
COMMENT ON COLUMN webhook_deliveries.status IS 'pending = ยังไม่ส่ง, success = 2xx, failed = จะ retry, exhausted = หมด retry / Delivery status';

-- ===========================================================================
-- 3.20 COMMISSION PAYOUTS - บันทึกการจ่ายเงิน commission ให้ agent/partner
-- Admin สั่งจ่าย commission สำหรับช่วงเวลาที่กำหนด -> โอนเงินจริง -> mark complete
-- Tracks actual money transfers to agents/partners for their earned commissions
-- ===========================================================================
CREATE TABLE commission_payouts (
    -- Primary Key: UUID v4 / Unique payout record identifier
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- ประเภทผู้รับ: 'agent' หรือ 'partner'
    -- Who is being paid: agent or partner
    owner_type         VARCHAR(10) NOT NULL,
    -- UUID ของผู้รับ / Recipient's ID
    owner_id           UUID NOT NULL,
    -- จำนวนเงินที่จ่าย / Total payout amount
    amount             NUMERIC(14,2) NOT NULL,
    -- สกุลเงิน / Currency
    currency           VARCHAR(3) NOT NULL DEFAULT 'THB',
    -- ช่วงเวลาที่ cover: จากวันที่ / Payout covers commissions from this date
    period_from        DATE NOT NULL,
    -- ช่วงเวลาที่ cover: ถึงวันที่ / Payout covers commissions until this date
    period_to          DATE NOT NULL,
    -- Bank transfer reference / Bank transfer reference number
    transfer_reference VARCHAR(100),
    -- Admin ที่สั่งจ่าย / Which admin initiated this payout
    initiated_by_admin UUID REFERENCES admins(id),
    -- สถานะ: pending, completed, cancelled / Status
    status             VARCHAR(20) NOT NULL DEFAULT 'pending',
    -- timestamps
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- เวลาที่จ่ายสำเร็จ / When payout was confirmed
    completed_at       TIMESTAMPTZ
);

-- Index: ค้นหา payouts ของ agent/partner / Find payouts for a specific owner
CREATE INDEX idx_cp_owner ON commission_payouts(owner_type, owner_id, created_at DESC);

COMMENT ON TABLE commission_payouts IS 'บันทึกการจ่าย commission ให้ agent/partner - admin สั่ง แล้วโอนจริง / Commission payout records';
COMMENT ON COLUMN commission_payouts.period_from IS 'ช่วงเวลาที่ payout นี้ cover: ตั้งแต่วันที่ / Payout period start date';
COMMENT ON COLUMN commission_payouts.period_to IS 'ช่วงเวลาที่ payout นี้ cover: ถึงวันที่ / Payout period end date';


-- ===========================================================================
-- 4. TRIGGERS - Auto-update updated_at timestamp
--    ทุกตารางที่มี updated_at จะถูก update อัตโนมัติเมื่อ row ถูกแก้ไข
--    All tables with updated_at get automatic timestamp updates on modification
-- ===========================================================================

-- Function ที่จะ update updated_at ให้เป็นเวลาปัจจุบันอัตโนมัติ
-- Function to automatically set updated_at to current timestamp
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    -- ทุกครั้งที่ row ถูก UPDATE จะ set updated_at เป็น NOW() อัตโนมัติ
    -- Automatically set updated_at to NOW() on every UPDATE
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- สร้าง trigger สำหรับแต่ละตารางที่มี updated_at
-- BEFORE UPDATE: ทำก่อน update จริง เพื่อให้ updated_at ถูกต้อง
-- Create triggers for each table with updated_at column
-- BEFORE UPDATE ensures updated_at is set correctly

-- Admins - auto-update timestamp
CREATE TRIGGER trg_admins_updated_at
    BEFORE UPDATE ON admins
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- Partners - auto-update timestamp
CREATE TRIGGER trg_partners_updated_at
    BEFORE UPDATE ON partners
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- Agents - auto-update timestamp
CREATE TRIGGER trg_agents_updated_at
    BEFORE UPDATE ON agents
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- Merchants - auto-update timestamp
CREATE TRIGGER trg_merchants_updated_at
    BEFORE UPDATE ON merchants
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- Bank Accounts - auto-update timestamp
CREATE TRIGGER trg_bank_accounts_updated_at
    BEFORE UPDATE ON bank_accounts
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- Wallets - auto-update timestamp
CREATE TRIGGER trg_wallets_updated_at
    BEFORE UPDATE ON wallets
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- Commission Daily Summary - auto-update timestamp
CREATE TRIGGER trg_commission_daily_summary_updated_at
    BEFORE UPDATE ON commission_daily_summary
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- System Config - auto-update timestamp
CREATE TRIGGER trg_system_config_updated_at
    BEFORE UPDATE ON system_config
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();


-- ===========================================================================
-- 5. INITIAL DATA - ข้อมูลเริ่มต้น
--    Initial seed data for the system
-- ===========================================================================

-- สร้าง System Wallet สำหรับเก็บ fee ของ platform
-- Create the system wallet for collecting platform fees
-- ใช้ UUID ที่กำหนดไว้ตายตัวเพื่อให้ reference ง่าย
-- Fixed UUID for easy reference across services
INSERT INTO wallets (id, owner_type, owner_id, currency, balance, hold_balance, version)
VALUES (
    '00000000-0000-0000-0000-000000000001',  -- Fixed UUID สำหรับ system wallet
    'system',                                 -- owner_type = system
    '00000000-0000-0000-0000-000000000001',  -- owner_id = system (self-reference)
    'THB',                                    -- สกุลเงิน THB
    0,                                        -- balance เริ่มต้น = 0
    0,                                        -- hold_balance = 0
    0                                         -- version = 0
) ON CONFLICT (owner_type, owner_id, currency) DO NOTHING;
-- ON CONFLICT DO NOTHING: ถ้ามีอยู่แล้ว (re-run script) ไม่ต้องทำอะไร
-- ON CONFLICT DO NOTHING: idempotent - safe to re-run


-- ===========================================================================
-- 6. VERIFICATION - แสดงข้อความยืนยัน
--    Show confirmation message
-- ===========================================================================
DO $$
BEGIN
    RAISE NOTICE '================================================================';
    RAISE NOTICE 'RichPayment database schema initialized successfully!';
    RAISE NOTICE '================================================================';
    RAISE NOTICE 'Extensions: pgcrypto, uuid-ossp';
    RAISE NOTICE '';
    RAISE NOTICE 'Core Tables:';
    RAISE NOTICE '  - admins                  (ผู้ดูแลระบบ)';
    RAISE NOTICE '  - partners                (เจ้าของบัญชีธนาคาร)';
    RAISE NOTICE '  - agents                  (ตัวแทนจำหน่าย)';
    RAISE NOTICE '  - merchants               (ร้านค้า)';
    RAISE NOTICE '  - bank_accounts           (บัญชีธนาคารรับเงิน)';
    RAISE NOTICE '  - bank_account_merchant_map (mapping บัญชี-merchant)';
    RAISE NOTICE '  - holding_accounts        (บัญชีพักเงิน)';
    RAISE NOTICE '  - wallets                 (กระเป๋าเงิน)';
    RAISE NOTICE '';
    RAISE NOTICE 'Partitioned Tables (monthly):';
    RAISE NOTICE '  - deposit_orders          (รายการฝากเงิน)';
    RAISE NOTICE '  - wallet_ledger           (บันทึก balance change)';
    RAISE NOTICE '  - withdrawals             (คำขอถอนเงิน)';
    RAISE NOTICE '  - commissions             (การแบ่ง fee)';
    RAISE NOTICE '  - sms_messages            (SMS จากธนาคาร)';
    RAISE NOTICE '  - slip_verifications      (การตรวจสอบสลิป)';
    RAISE NOTICE '  - audit_logs              (บันทึก action)';
    RAISE NOTICE '  - webhook_deliveries      (การส่ง webhook)';
    RAISE NOTICE '';
    RAISE NOTICE 'Other Tables:';
    RAISE NOTICE '  - commission_daily_summary (สรุป commission รายวัน)';
    RAISE NOTICE '  - fund_transfers          (การโอนเงินไปบัญชีพัก)';
    RAISE NOTICE '  - commission_payouts      (การจ่าย commission)';
    RAISE NOTICE '  - system_config           (ตั้งค่าระบบ)';
    RAISE NOTICE '';
    RAISE NOTICE 'Initial Partitions: 2026_04, 2026_05, 2026_06';
    RAISE NOTICE '================================================================';
END $$;
