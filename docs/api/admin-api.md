# RichPayment Admin API Documentation

<!-- =======================================================================
     RichPayment Admin/Back-Office API Reference

     This document covers all internal and admin-facing endpoints used by
     the admin dashboard, including user management (CRUD for admins,
     merchants, agents, partners), bank account management, authentication,
     and settlement/transfer operations.

     NOTE (หมายเหตุ):
     - Admin API ใช้ session-based authentication ผ่าน cookie
     - ทุก endpoint ต้องผ่าน session auth ยกเว้น login และ healthz
     - RBAC permissions ใช้ bitmask เพื่อควบคุมสิทธิ์การเข้าถึง
     ======================================================================= -->

## Table of Contents

1. [Overview](#overview)
2. [Authentication](#authentication)
3. [RBAC Permissions](#rbac-permissions)
4. [User Management](#user-management)
   - [Admin CRUD](#admin-crud)
   - [Merchant CRUD](#merchant-crud)
   - [Agent CRUD](#agent-crud)
   - [Partner CRUD](#partner-crud)
5. [Bank Account Management](#bank-account-management)
6. [Settlement & Transfer Operations](#settlement--transfer-operations)
7. [Withdrawal Approval](#withdrawal-approval)
8. [Internal Service Endpoints](#internal-service-endpoints)
9. [Error Handling](#error-handling)

---

## Overview

<!-- ============================================================
     ภาพรวม Admin API

     Admin API ประกอบด้วยหลาย service ที่ทำงานร่วมกัน:
     - auth-service: จัดการ login/logout/session
     - user-service: จัดการ CRUD ของ admins, merchants, agents, partners
     - bank-service: จัดการบัญชีธนาคารและการโอนเงิน
     - withdrawal-service: จัดการการอนุมัติ/ปฏิเสธ withdrawal
     ============================================================ -->

The Admin API is used by the back-office dashboard to manage the RichPayment
platform. It spans multiple internal services:

| Service              | Port | Purpose                                     |
|----------------------|------|---------------------------------------------|
| **auth-service**     | 8081 | Login, logout, session management           |
| **user-service**     | 8082 | CRUD for admins, merchants, agents, partners|
| **bank-service**     | 8087 | Bank account pool and transfer management   |
| **withdrawal-service**| 8085| Withdrawal approval/rejection workflow      |

### Service Architecture

<!-- สถาปัตยกรรม: admin dashboard เรียก service ต่างๆ ผ่าน HTTP -->

```
Admin Dashboard (browser)
    |
    +-- auth-service (:8081)       -- Login, session validation
    +-- user-service (:8082)       -- User CRUD operations
    +-- bank-service (:8087)       -- Bank account & transfer management
    +-- withdrawal-service (:8085) -- Withdrawal approval workflow
```

---

## Authentication

<!-- ============================================================
     การยืนยันตัวตน (Admin Authentication)

     Admin ใช้ session-based auth ผ่าน cookie ชื่อ richpay_session
     Session ถูกเก็บใน Redis พร้อม TTL
     ============================================================ -->

Admin authentication uses **session-based cookies** backed by Redis.

### Login

**Endpoint:** `POST /auth/login`
**Service:** auth-service (:8081)

**Request Body:**

| Field      | Type   | Required | Description                                    |
|------------|--------|----------|------------------------------------------------|
| `email`    | string | Yes      | Admin's login email                            |
| `password` | string | Yes      | Admin's password                               |
| `totp_code`| string | No       | 6-digit TOTP code (required if 2FA is enabled) |
| `user_type`| string | No       | `admin` (default), `merchant`, or `agent`      |

<!-- หมายเหตุ:
     - totp_code: ต้องระบุหาก admin เปิดใช้ 2FA (Google Authenticator)
     - user_type: ระบุ "admin" สำหรับ admin login
     - หาก login ผิดติดต่อกัน 5 ครั้ง account จะถูก lock ชั่วคราว
-->

**Example Request:**

```json
{
  "email": "admin@richpayment.co",
  "password": "securePassword123",
  "totp_code": "123456",
  "user_type": "admin"
}
```

**Success Response (200 OK):**

```json
{
  "session_id": "sess_abc123def456",
  "user_id": "550e8400-e29b-41d4-a716-446655440000",
  "email": "admin@richpayment.co",
  "role": "super_admin",
  "role_mask": 4294967295,
  "expires_at": "2024-01-02T00:00:00Z"
}
```

**Error Responses:**

| HTTP Status | Error                           | Cause                          |
|-------------|---------------------------------|--------------------------------|
| 400         | `invalid request body`          | Malformed JSON                 |
| 400         | `email and password are required`| Missing credentials           |
| 401         | `invalid credentials`           | Wrong email or password        |
| 401         | `account is locked`             | Too many failed login attempts |
| 401         | `invalid TOTP code`             | Incorrect 2FA code             |

### Session Cookie

<!-- Cookie ชื่อ richpay_session ถูกใช้ในการยืนยันตัวตน -->

After login, set the session cookie for subsequent requests:

```
Cookie: richpay_session=sess_abc123def456
```

The session is validated against Redis on every request. Sessions contain:
- User ID
- Email
- User type (admin, merchant, agent)
- Role name
- Permission bitmask
- Expiration timestamp

### Logout

**Endpoint:** `POST /auth/logout`

**Request Body:**

```json
{
  "session_id": "sess_abc123def456"
}
```

**Response (200 OK):**

```json
{
  "status": "ok"
}
```

### Validate Session

<!-- ตรวจสอบ session ว่ายังใช้งานได้หรือไม่ -->

**Endpoint:** `POST /auth/validate`

**Request Body:**

```json
{
  "session_id": "sess_abc123def456"
}
```

**Valid Session Response:**

```json
{
  "valid": true,
  "user_id": "550e8400-e29b-41d4-a716-446655440000",
  "email": "admin@richpayment.co",
  "user_type": "admin",
  "role": "super_admin",
  "role_mask": 4294967295
}
```

**Expired/Invalid Session Response:**

```json
{
  "valid": false
}
```

### Session Middleware Errors

<!-- Error จาก session middleware -->

| HTTP Status | Code               | Description                     |
|-------------|--------------------|---------------------------------|
| 401         | `MISSING_SESSION`  | No `richpay_session` cookie     |
| 401         | `INVALID_SESSION`  | Session expired or not found    |
| 500         | `INTERNAL_ERROR`   | Redis connection error          |

---

## RBAC Permissions

<!-- ============================================================
     ระบบสิทธิ์ RBAC (Role-Based Access Control)

     ใช้ bitmask 64-bit เพื่อกำหนดสิทธิ์ของ admin แต่ละคน
     แต่ละ bit แทนสิทธิ์ 1 อย่าง สามารถรวมกันได้
     ============================================================ -->

Admin permissions are encoded as a **64-bit bitmask** (`role_mask` field).
Each bit represents a specific capability.

### Permission Bits

<!-- ตาราง bitmask ของสิทธิ์ -->

| Bit | Value        | Permission Name          | Description (Thai)                        |
|-----|--------------|--------------------------|-------------------------------------------|
| 0   | `1`          | View Merchants           | ดู merchant ได้                            |
| 1   | `2`          | Create Merchants         | สร้าง merchant ได้                         |
| 2   | `4`          | Manage Agents            | จัดการ agent ได้                           |
| 3   | `8`          | Manage Finances          | จัดการการเงิน (อนุมัติ withdrawal ฯลฯ)     |
| 4   | `16`         | Manage Bank Accounts     | จัดการบัญชีธนาคาร                          |
| 5   | `32`         | Manage Admins            | จัดการ admin คนอื่น                        |
| 6   | `64`         | View Reports             | ดูรายงาน                                  |
| 7   | `128`        | System Settings          | ตั้งค่าระบบ (emergency freeze ฯลฯ)         |

### Role Examples

<!-- ตัวอย่างบทบาทและ bitmask -->

| Role Name      | Bitmask    | Hex          | Capabilities                                    |
|----------------|------------|--------------|------------------------------------------------|
| `super_admin`  | all bits=1 | `0xFFFFFFFF` | Full access to everything                       |
| `operator`     | `0b01001011` | `0x4B`     | View merchants + Manage finances + View reports |
| `viewer`       | `0b01000001` | `0x41`     | View merchants + View reports only              |

### Checking Permissions

<!-- วิธีตรวจสอบ permission จาก bitmask -->

To check if a user has a specific permission:

```go
// hasPermission checks whether the admin's role_mask includes the
// specified permission bit.
//
// Parameters:
//   - roleMask:   the admin's combined permission bitmask from the session
//   - permission: the specific permission bit to check
//
// Returns true if the admin has the specified permission.
func hasPermission(roleMask int64, permission int64) bool {
    return roleMask & permission != 0
}

// Example: check if admin can manage finances (bit 3 = value 8)
canManageFinances := hasPermission(session.RoleMask, 8)
```

---

## User Management

<!-- ============================================================
     การจัดการผู้ใช้

     user-service (:8082) จัดการ CRUD ของ admins, merchants, agents, partners
     ทุก endpoint ต้องผ่าน session auth
     ============================================================ -->

All user management endpoints are served by the **user-service** (:8082).

### Admin CRUD

<!-- CRUD สำหรับ Admin -->

#### Create Admin

**Endpoint:** `POST /api/v1/admins`

**Request Body:**

| Field          | Type   | Required | Description                          |
|----------------|--------|----------|--------------------------------------|
| `email`        | string | Yes      | Unique login email                   |
| `password`     | string | Yes      | Plaintext password (hashed server-side) |
| `display_name` | string | Yes      | Name shown in dashboard              |
| `role_mask`    | number | Yes      | Permission bitmask (see RBAC section)|

<!-- หมายเหตุ: password จะถูก hash ด้วย bcrypt บน server -->

**Example:**

```json
{
  "email": "newadmin@richpayment.co",
  "password": "strongPassword!123",
  "display_name": "New Admin",
  "role_mask": 75
}
```

**Response (201 Created):** Returns the created admin object.

#### Get Admin

**Endpoint:** `GET /api/v1/admins/{id}`

**Path Parameter:** `id` (UUID)

**Response (200 OK):**

```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "email": "admin@richpayment.co",
  "display_name": "Main Admin",
  "role_mask": 4294967295,
  "status": "active",
  "created_at": "2024-01-01T00:00:00Z",
  "updated_at": "2024-01-01T00:00:00Z"
}
```

#### List Admins

**Endpoint:** `GET /api/v1/admins?page=1&limit=20`

**Query Parameters:**

| Parameter | Type   | Default | Description         |
|-----------|--------|---------|---------------------|
| `page`    | number | 1       | Page number         |
| `limit`   | number | 20      | Items per page      |

**Response (200 OK):**

```json
{
  "data": [ { ... }, { ... } ],
  "total": 5,
  "page": 1,
  "limit": 20
}
```

#### Update Admin

**Endpoint:** `PUT /api/v1/admins/{id}`

**Request Body (partial update -- only include fields to change):**

| Field          | Type   | Required | Description             |
|----------------|--------|----------|-------------------------|
| `display_name` | string | No       | New display name        |
| `role_mask`    | number | No       | New permission bitmask  |
| `status`       | string | No       | `active`, `suspended`, `deleted` |
| `email`        | string | No       | New email address       |

<!-- หมายเหตุ: partial update -- ส่งเฉพาะ field ที่ต้องการเปลี่ยน -->

**Response (200 OK):**

```json
{
  "status": "updated"
}
```

---

### Merchant CRUD

<!-- CRUD สำหรับ Merchant -->

#### Create Merchant

**Endpoint:** `POST /api/v1/merchants`

**Request Body:**

| Field                    | Type   | Required | Description                              |
|--------------------------|--------|----------|------------------------------------------|
| `name`                   | string | Yes      | Business name (ชื่อกิจการ)                |
| `email`                  | string | Yes      | Contact email                            |
| `webhook_url`            | string | No       | Callback URL for webhooks                |
| `agent_id`               | string | No       | UUID of managing agent                   |
| `deposit_fee_pct`        | string | Yes      | Deposit fee % (e.g. `"0.03"` = 3%)      |
| `withdrawal_fee_pct`     | string | Yes      | Withdrawal fee % (e.g. `"0.01"` = 1%)   |
| `daily_withdrawal_limit` | string | Yes      | Max daily withdrawal (e.g. `"1000000"`)  |

<!-- หมายเหตุ:
     - deposit_fee_pct/withdrawal_fee_pct: ใช้เป็น decimal string
     - daily_withdrawal_limit: วงเงินถอนสูงสุดต่อวัน
     - agent_id: ถ้าไม่ระบุ = direct merchant (ไม่มี agent)
     - API key จะถูกสร้างอัตโนมัติ และแสดงเพียงครั้งเดียวใน response
-->

**Response (201 Created):**

```json
{
  "merchant": {
    "id": "770e8400-e29b-41d4-a716-446655440000",
    "name": "Test Shop",
    "email": "shop@test.com",
    "webhook_url": "https://test.com/webhook",
    "deposit_fee_pct": "0.03",
    "withdrawal_fee_pct": "0.01",
    "daily_withdrawal_limit": "1000000",
    "status": "pending",
    "created_at": "2024-01-01T00:00:00Z"
  },
  "api_key": "rpay_live_abc123def456ghi789jkl012mno345"
}
```

> **IMPORTANT:** The `api_key` is only returned at creation time. Store it
> securely. It cannot be retrieved later.
>
> **สำคัญ:** `api_key` จะแสดงเพียงครั้งเดียวตอนสร้าง กรุณาบันทึกไว้ให้ดี

#### Get Merchant

**Endpoint:** `GET /api/v1/merchants/{id}`

#### List Merchants

**Endpoint:** `GET /api/v1/merchants?page=1&limit=20&agent_id={uuid}`

**Query Parameters:**

| Parameter  | Type   | Default | Description                       |
|------------|--------|---------|-----------------------------------|
| `page`     | number | 1       | Page number                       |
| `limit`    | number | 20      | Items per page                    |
| `agent_id` | string | -       | Filter by agent UUID (optional)   |

<!-- agent_id: กรอง merchant ตาม agent ที่ดูแล -->

#### Update Merchant

**Endpoint:** `PUT /api/v1/merchants/{id}`

**Request Body (partial update):**

| Field         | Type   | Description                              |
|---------------|--------|------------------------------------------|
| `name`        | string | New business name                        |
| `email`       | string | New contact email                        |
| `webhook_url` | string | New webhook URL                          |
| `status`      | string | `pending`, `active`, `suspended`, `deleted` |

#### Revoke/Rotate API Key

<!-- เพิกถอน/หมุนเวียน API Key ของ Merchant -->

**Endpoint:** `POST /api/v1/merchants/{id}/revoke-key`

This endpoint revokes the current API key and generates a new one. It requires
TOTP verification for additional security.

<!-- ต้องใส่ TOTP code เพื่อยืนยันก่อน rotate API key -->

**Request Body:**

```json
{
  "totp_code": "123456"
}
```

**Response (200 OK):**

```json
{
  "new_api_key": "rpay_live_new_key_abc123...",
  "message": "API key has been rotated. Store the new key securely."
}
```

> **WARNING:** The old API key is immediately invalidated. The merchant must
> update their integration immediately.
>
> **คำเตือน:** API key เก่าจะใช้ไม่ได้ทันที merchant ต้อง update integration ทันที

---

### Agent CRUD

<!-- CRUD สำหรับ Agent -->

#### Create Agent

**Endpoint:** `POST /api/v1/agents`

**Request Body:**

| Field           | Type   | Required | Description                            |
|-----------------|--------|----------|----------------------------------------|
| `name`          | string | Yes      | Agent display name                     |
| `email`         | string | Yes      | Login email                            |
| `password`      | string | Yes      | Login password                         |
| `partner_id`    | string | No       | UUID of parent partner                 |
| `commission_pct`| string | Yes      | Commission % (e.g. `"0.30"` = 30%)    |

<!-- หมายเหตุ:
     - commission_pct: เปอร์เซ็นต์ค่า commission ของ agent
       (เช่น 0.30 = agent ได้ 30% ของค่าธรรมเนียม)
     - partner_id: ถ้าระบุ agent จะอยู่ภายใต้ partner นั้น
-->

**Response (201 Created):** Returns the created agent object.

#### Get Agent

**Endpoint:** `GET /api/v1/agents/{id}`

#### List Agents

**Endpoint:** `GET /api/v1/agents?page=1&limit=20&partner_id={uuid}`

**Query Parameters:**

| Parameter    | Type   | Default | Description                        |
|--------------|--------|---------|------------------------------------|
| `page`       | number | 1       | Page number                        |
| `limit`      | number | 20      | Items per page                     |
| `partner_id` | string | -       | Filter by partner UUID (optional)  |

#### Update Agent

**Endpoint:** `PUT /api/v1/agents/{id}`

**Request Body (partial update):**

| Field    | Type   | Description                              |
|----------|--------|------------------------------------------|
| `name`   | string | New display name                         |
| `email`  | string | New email address                        |
| `status` | string | `active`, `suspended`, `deleted`         |

---

### Partner CRUD

<!-- CRUD สำหรับ Partner -->

#### Create Partner

**Endpoint:** `POST /api/v1/partners`

**Request Body:**

| Field           | Type   | Required | Description                            |
|-----------------|--------|----------|----------------------------------------|
| `name`          | string | Yes      | Partner business name                  |
| `email`         | string | Yes      | Login email                            |
| `password`      | string | Yes      | Login password                         |
| `commission_pct`| string | Yes      | Commission % (e.g. `"0.10"` = 10%)    |

<!-- commission_pct: เปอร์เซ็นต์ค่า commission ของ partner
     Partner อยู่ด้านบนสุดของ commission hierarchy -->

**Response (201 Created):** Returns the created partner object.

#### Get Partner

**Endpoint:** `GET /api/v1/partners/{id}`

#### List Partners

**Endpoint:** `GET /api/v1/partners?page=1&limit=20`

#### Update Partner

**Endpoint:** `PUT /api/v1/partners/{id}`

**Request Body (partial update):**

| Field    | Type   | Description                              |
|----------|--------|------------------------------------------|
| `name`   | string | New business name                        |
| `email`  | string | New email address                        |
| `status` | string | `active`, `suspended`, `deleted`         |

---

## Bank Account Management

<!-- ============================================================
     การจัดการบัญชีธนาคาร

     bank-service (:8087) จัดการ pool ของบัญชีธนาคารที่ใช้รับเงิน
     รวมถึงการ auto-switch เมื่อบัญชีถึง limit
     ============================================================ -->

Bank accounts are managed by the **bank-service** (:8087). These accounts
are the receiving bank accounts used by the platform to accept deposits.

### List All Accounts

<!-- แสดงรายการบัญชีธนาคารทั้งหมดพร้อมสถานะ -->

**Endpoint:** `GET /bank/accounts`

Returns all bank accounts with their current status, including capacity
utilization and daily counters.

**Response (200 OK):**

```json
{
  "success": true,
  "data": [
    {
      "id": "880e8400-e29b-41d4-a716-446655440000",
      "bank_code": "KBANK",
      "account_number": "xxx-x-xxxxx-x",
      "account_name": "RichPayment Co Ltd",
      "is_active": true,
      "daily_received": "2500000.00",
      "daily_limit": "5000000.00",
      "utilization_pct": 50.0,
      "remaining_capacity": "2500000.00"
    }
  ]
}
```

### Get Account Status

**Endpoint:** `GET /bank/accounts/{id}/status`

Returns detailed status for a single bank account.

### Select Account (Internal)

<!-- endpoint ภายใน: เลือกบัญชีที่ดีที่สุดสำหรับรับ deposit -->

**Endpoint:** `POST /internal/bank/select-account`

> Internal endpoint -- called by order-service during deposit creation.

**Request Body:**

```json
{
  "merchant_id": "550e8400-e29b-41d4-a716-446655440000"
}
```

### Auto-Switch Account (Internal)

<!-- endpoint ภายใน: ปิดบัญชีที่มีปัญหาและย้ายไปบัญชีอื่น -->

**Endpoint:** `POST /internal/bank/auto-switch`

Disables a bank account and reassigns affected merchants. Called when an
account reaches its daily limit or encounters errors.

**Request Body:**

```json
{
  "bank_account_id": "880e8400-e29b-41d4-a716-446655440000",
  "reason": "daily_limit_reached"
}
```

### Update Account Pool (Internal)

**Endpoint:** `POST /internal/bank/update-pool`

Rebuilds the Redis sorted set for a merchant's bank account pool.

### Daily Counter Management (Internal)

<!-- จัดการ counter รายวัน -->

**Update Daily Received:**
`POST /internal/bank/daily-received`

```json
{
  "bank_account_id": "880e8400-e29b-41d4-a716-446655440000",
  "amount": "5000.00"
}
```

**Reset Daily Counters:**
`POST /internal/bank/reset-counters`

Called by the scheduler at midnight to reset all daily receiving counters.

<!-- scheduler เรียกตอนเที่ยงคืนเพื่อ reset counter รายวัน -->

---

## Settlement & Transfer Operations

<!-- ============================================================
     การโอนเงินและ Settlement

     Admin ใช้ endpoint เหล่านี้เพื่อโอนเงินจากบัญชีรับเงิน
     ไปยังบัญชี holding ที่ได้รับอนุมัติล่วงหน้า
     ============================================================ -->

Transfer operations are used by the finance team to move funds from
receiving bank accounts to pre-approved holding accounts.

### Create Transfer

**Endpoint:** `POST /bank/transfers`

**Request Body:**

| Field            | Type   | Required | Description                           |
|------------------|--------|----------|---------------------------------------|
| `from_account_id`| string | Yes      | Source bank account UUID              |
| `to_holding_id`  | string | Yes      | Pre-approved holding account UUID     |
| `amount`         | string | Yes      | Transfer amount (e.g. `"50000.00"`)   |
| `admin_id`       | string | Yes      | UUID of the admin initiating transfer |

<!-- หมายเหตุ:
     - to_holding_id ต้องเป็นบัญชี holding ที่ผ่านการอนุมัติแล้ว
     - admin_id ใช้เพื่อ audit trail ว่าใครเป็นคนสั่งโอน
-->

**Example:**

```json
{
  "from_account_id": "880e8400-e29b-41d4-a716-446655440000",
  "to_holding_id": "990e8400-e29b-41d4-a716-446655440000",
  "amount": "50000.00",
  "admin_id": "550e8400-e29b-41d4-a716-446655440000"
}
```

**Response (201 Created):**

```json
{
  "success": true,
  "data": {
    "id": "aa0e8400-e29b-41d4-a716-446655440000",
    "from_account_id": "880e8400...",
    "to_holding_id": "990e8400...",
    "amount": "50000.00",
    "status": "pending",
    "admin_id": "550e8400...",
    "created_at": "2024-01-01T00:00:00Z"
  }
}
```

### Complete Transfer

**Endpoint:** `POST /bank/transfers/{id}/complete`

Marks a pending transfer as completed after the bank transfer is confirmed.

**Request Body:**

```json
{
  "reference": "KBANK-TXN-20240101-123456"
}
```

<!-- reference: เลขอ้างอิงจากธนาคาร เมื่อโอนเงินสำเร็จ -->

**Response (200 OK):**

```json
{
  "success": true,
  "data": {
    "status": "completed"
  }
}
```

### List Transfers

**Endpoint:** `GET /bank/transfers?page=1&limit=20`

**Query Parameters:**

| Parameter | Type   | Default | Description         |
|-----------|--------|---------|---------------------|
| `page`    | number | 1       | Page number         |
| `limit`   | number | 20      | Items per page      |

**Response (200 OK):**

```json
{
  "success": true,
  "data": {
    "transfers": [ { ... }, { ... } ],
    "total": 45,
    "page": 1,
    "limit": 20
  }
}
```

### Daily Transfer Summary

**Endpoint:** `GET /bank/transfers/daily-summary?date=2024-01-01`

**Query Parameters:**

| Parameter | Type   | Required | Format       | Description    |
|-----------|--------|----------|--------------|----------------|
| `date`    | string | Yes      | `YYYY-MM-DD` | Target date    |

<!-- date ต้องเป็นรูปแบบ YYYY-MM-DD -->

**Response (200 OK):**

```json
{
  "success": true,
  "data": {
    "date": "2024-01-01",
    "total_transfers": 12,
    "total_amount": "450000.00",
    "completed": 10,
    "pending": 2
  }
}
```

---

## Withdrawal Approval

<!-- ============================================================
     การอนุมัติ/ปฏิเสธ Withdrawal

     Admin ต้อง approve withdrawal ก่อนที่ระบบจะโอนเงินจริง
     Flow: pending -> approved -> completed
                   -> rejected (เงินคืน wallet)
     ============================================================ -->

Withdrawals require admin approval before funds are transferred. The
withdrawal-service manages this approval workflow.

### Withdrawal Lifecycle

```
1. Merchant creates withdrawal (POST /api/v1/withdrawals)
   -> status: pending, wallet balance held
   -> (merchant สร้าง withdrawal, ยอดเงินถูก hold ใน wallet)

2. Admin approves/rejects (internal workflow)
   -> approved: ready for bank transfer
   -> rejected: held balance released back to wallet
   -> (admin อนุมัติหรือปฏิเสธ)

3. Finance executes bank transfer
   -> completed: transfer confirmed, commission recorded
   -> failed: transfer failed, held balance released
   -> (ฝ่ายการเงินโอนเงินจริง)
```

### Internal Service Communication

<!-- การสื่อสารระหว่าง service สำหรับ withdrawal -->

When a withdrawal is created, the withdrawal-service communicates with:

1. **wallet-service** (:8084) -- Check balance, place hold, release, debit
2. **commission-service** (:8086) -- Record fee split on completion

```
withdrawal-service -> wallet-service    (balance check + hold)
                   -> commission-service (fee recording on completion)
```

---

## Internal Service Endpoints

<!-- ============================================================
     Endpoint ภายในระบบ (ไม่ expose ให้ภายนอก)

     endpoint เหล่านี้ใช้สำหรับการสื่อสารระหว่าง service
     ============================================================ -->

These endpoints are for inter-service communication only and should not
be exposed to external traffic.

### Notification Service (:8088)

<!-- service สำหรับส่ง webhook และ Telegram alerts -->

| Method | Endpoint                    | Description                      |
|--------|-----------------------------|----------------------------------|
| POST   | `/internal/webhook/send`    | Send signed webhook to merchant  |
| POST   | `/internal/alert/send`      | Send Telegram alert              |
| GET    | `/healthz`                  | Health check                     |

### Wallet Service (:8084)

<!-- service สำหรับจัดการ wallet -->

| Method | Endpoint                       | Description                      |
|--------|--------------------------------|----------------------------------|
| GET    | `/wallet/balance`              | Query wallet balance             |
| POST   | `/wallet/credit`               | Credit wallet (deposit complete) |
| POST   | `/wallet/debit`                | Debit wallet (withdrawal debit)  |

### Commission Service (:8086)

<!-- service สำหรับบันทึก commission -->

| Method | Endpoint                       | Description                      |
|--------|--------------------------------|----------------------------------|
| POST   | `/commission/record`           | Record fee split for transaction |

---

## Error Handling

<!-- ============================================================
     การจัดการ Error

     ทุก response ใช้ JSON envelope มาตรฐาน
     ============================================================ -->

All admin API endpoints use a standard JSON error format:

```json
{
  "success": false,
  "error": "human-readable error message",
  "code": "MACHINE_READABLE_CODE"
}
```

Or the simpler format used by some services:

```json
{
  "error": "human-readable error message"
}
```

### Common HTTP Status Codes

| Status | Meaning                                                |
|--------|--------------------------------------------------------|
| 200    | Success                                                |
| 201    | Resource created successfully                          |
| 400    | Bad request (invalid input, missing fields)            |
| 401    | Authentication failed (missing/invalid session)        |
| 403    | Forbidden (insufficient permissions)                   |
| 404    | Resource not found                                     |
| 422    | Unprocessable entity (business rule violation)         |
| 429    | Rate limited                                           |
| 500    | Internal server error                                  |
| 502    | Upstream service unreachable                           |
| 503    | System frozen or service unavailable                   |

> For the complete error code reference, see **[errors.md](errors.md)**.

---

*Last updated: 2026-04-10*
*RichPayment Admin API v1*
