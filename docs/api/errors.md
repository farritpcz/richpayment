# RichPayment Error Code Reference

<!-- =======================================================================
     RichPayment Complete Error Code Reference

     This document provides a comprehensive reference of all error codes
     used across the RichPayment platform, organized by category with
     HTTP status code mappings and descriptions.

     NOTE (หมายเหตุ):
     - ภาษาหลักของเอกสารนี้เป็นภาษาอังกฤษ แต่มีหมายเหตุภาษาไทยเพิ่มเติม
       เพื่อช่วยเหลือ Merchant ไทยในการเข้าใจรายละเอียดที่สำคัญ
     - Error codes อ้างอิงจาก source code โดยตรง:
       * pkg/errors/errors.go (sentinel errors)
       * services/gateway/internal/middleware/ (auth, rate limit, freeze)
       * services/gateway/internal/handler/ (validation, upstream)
       * services/withdrawal/internal/ (withdrawal-specific errors)
       * services/wallet/internal/ (wallet-specific errors)
       * services/user/internal/ (user management errors)
       * services/bank/internal/ (bank-specific errors)
     ======================================================================= -->

## Table of Contents

<!-- สารบัญ: ลิงก์ไปยังแต่ละหมวดหมู่ของ error codes -->

1. [Error Response Format](#error-response-format)
2. [Authentication Errors](#authentication-errors)
3. [Authorization Errors](#authorization-errors)
4. [Validation Errors](#validation-errors)
5. [Rate Limiting Errors](#rate-limiting-errors)
6. [System Errors](#system-errors)
7. [Wallet Errors](#wallet-errors)
8. [Withdrawal Errors](#withdrawal-errors)
9. [Bank Errors](#bank-errors)
10. [Session Errors](#session-errors)
11. [Resource Errors](#resource-errors)
12. [Quick Reference Table](#quick-reference-table)

---

## Error Response Format

<!-- ============================================================
     รูปแบบ Error Response

     ทุก API response ใช้ JSON envelope มาตรฐาน
     มี 2 รูปแบบ ขึ้นอยู่กับ service ที่เรียกใช้
     ============================================================ -->

All error responses follow one of two standard JSON formats:

### Merchant API Error Format

<!-- รูปแบบ error จาก Merchant API (gateway service) -->

The gateway service and merchant-facing endpoints use this format:

```json
{
  "success": false,
  "error": "human-readable error message",
  "code": "MACHINE_READABLE_CODE"
}
```

<!-- หมายเหตุ:
     - success: เป็น false เสมอสำหรับ error response
     - error: ข้อความอธิบาย error ที่อ่านเข้าใจง่าย (ภาษาอังกฤษ)
     - code: รหัส error แบบ machine-readable ใช้สำหรับ programmatic handling
-->

| Field     | Type    | Description                                              |
|-----------|---------|----------------------------------------------------------|
| `success` | boolean | Always `false` for error responses                       |
| `error`   | string  | Human-readable error description (ข้อความอธิบาย error)   |
| `code`    | string  | Machine-readable error code for programmatic handling    |

### Admin API Error Format

<!-- รูปแบบ error จาก Admin API (internal services) -->

Some internal services use a simpler format:

```json
{
  "error": "human-readable error message"
}
```

### AppError Structure (Internal)

<!-- ============================================================
     โครงสร้าง AppError ภายในระบบ

     ระบบใช้ AppError struct เป็นมาตรฐานสำหรับ error ภายใน
     ประกอบด้วย: Code, Message, HTTPStatus, และ Err (optional wrapped error)
     อ้างอิงจาก pkg/errors/errors.go
     ============================================================ -->

Internally, the platform uses a structured `AppError` type that carries:
- **Code:** Machine-readable error identifier
- **Message:** Human-readable description
- **HTTPStatus:** Corresponding HTTP status code
- **Err:** Optional wrapped error for debugging (not exposed in API responses)

<!-- AppError ภายในระบบประกอบด้วย:
     - Code: รหัส error แบบ machine-readable
     - Message: ข้อความอธิบาย
     - HTTPStatus: HTTP status code ที่สอดคล้อง
     - Err: error ที่ถูก wrap (ไม่แสดงใน API response)
-->

---

## Authentication Errors

<!-- ============================================================
     Error ที่เกี่ยวกับการยืนยันตัวตน (Authentication)

     เกิดขึ้นเมื่อ:
     - ไม่ส่ง header ที่จำเป็น (API Key, Timestamp, Signature)
     - API Key ไม่ถูกต้อง
     - Signature ไม่ตรง
     - Timestamp หมดอายุหรือไม่ถูกรูปแบบ
     - 2FA code ไม่ถูกต้อง
     - Account ถูก lock

     อ้างอิงจาก:
     - services/gateway/internal/middleware/apikey.go
     - pkg/errors/errors.go
     ============================================================ -->

Authentication errors occur when the request cannot be verified as coming
from a legitimate, authorized source.

| Code                | HTTP Status | Message                                              | Thai Note (หมายเหตุภาษาไทย)                     |
|---------------------|-------------|------------------------------------------------------|--------------------------------------------------|
| `MISSING_API_KEY`   | 401         | X-API-Key header is required                         | ไม่ได้ส่ง header X-API-Key มาด้วย                 |
| `MISSING_TIMESTAMP` | 401         | X-Timestamp header is required                       | ไม่ได้ส่ง header X-Timestamp มาด้วย               |
| `MISSING_SIGNATURE` | 401         | X-Signature header is required                       | ไม่ได้ส่ง header X-Signature มาด้วย               |
| `INVALID_TIMESTAMP` | 401         | X-Timestamp must be a unix timestamp                 | Timestamp ไม่ใช่รูปแบบ Unix timestamp ที่ถูกต้อง   |
| `EXPIRED_TIMESTAMP` | 401         | Request timestamp is too old or too far in the future| Timestamp เก่าหรือเร็วเกินกว่า 5 นาทีจากเวลาเซิร์ฟเวอร์ |
| `INVALID_API_KEY`   | 401         | Invalid API key                                      | API Key ไม่ถูกต้อง ไม่พบในระบบ                    |
| `INVALID_SIGNATURE` | 401         | Invalid HMAC signature                               | ลายเซ็น HMAC-SHA256 ไม่ตรงกับที่คำนวณได้          |
| `UNAUTHORIZED`      | 401         | Authentication required                              | ต้องยืนยันตัวตนก่อนเข้าถึง resource นี้           |
| `INVALID_2FA`       | 401         | Invalid 2FA code                                     | รหัส 2FA (TOTP) ไม่ถูกต้อง                       |
| `ACCOUNT_LOCKED`    | 423         | Account is locked                                    | บัญชีถูก lock เนื่องจาก login ผิดหลายครั้ง         |

<!-- หมายเหตุเพิ่มเติม:
     - MISSING_API_KEY/TIMESTAMP/SIGNATURE: ตรวจสอบว่าส่ง headers ครบทุกตัว
     - EXPIRED_TIMESTAMP: ตรวจสอบนาฬิกาของ server ว่าตรงกับเวลาจริง
       Timestamp ต้องอยู่ภายใน +/- 300 วินาที (5 นาที) จากเวลา server
     - INVALID_API_KEY: ตรวจสอบว่า API Key ถูกต้อง หรืออาจถูก revoke ไปแล้ว
     - INVALID_SIGNATURE: ตรวจสอบ:
       1) Secret key ถูกต้อง
       2) ลำดับ signing components ถูกต้อง (timestamp + method + path + body)
       3) ใช้ raw body ไม่ใช่ parsed body
     - ACCOUNT_LOCKED: รอสักครู่หรือติดต่อ admin เพื่อปลด lock
-->

### Common Fixes

<!-- วิธีแก้ไขปัญหาที่พบบ่อย -->

**`EXPIRED_TIMESTAMP`**: Ensure your server clock is synchronized using NTP.
The timestamp must be within 5 minutes (300 seconds) of the RichPayment
server's time.

<!-- ตรวจสอบให้นาฬิกาของ server sync ด้วย NTP
     Timestamp ต้องห่างไม่เกิน 5 นาที (300 วินาที) -->

**`INVALID_SIGNATURE`**: Double-check:
1. Your HMAC secret is correct and hasn't been rotated
2. The signing order is: `timestamp + method + path + body`
3. You're using the raw request body (not re-serialized JSON)
4. The method is uppercase (e.g. `POST`, not `post`)

<!-- ตรวจสอบ:
     1. HMAC secret ถูกต้องและไม่ได้ถูก rotate
     2. ลำดับการ sign: timestamp + method + path + body
     3. ใช้ raw body ไม่ใช่ JSON ที่ parse แล้ว stringify ใหม่
     4. Method เป็นตัวพิมพ์ใหญ่ (เช่น POST ไม่ใช่ post)
-->

---

## Authorization Errors

<!-- ============================================================
     Error ที่เกี่ยวกับการอนุญาต (Authorization)

     เกิดขึ้นเมื่อผู้ใช้ยืนยันตัวตนแล้ว แต่ไม่มีสิทธิ์เข้าถึง resource
     ============================================================ -->

Authorization errors occur when the authenticated user does not have
sufficient permissions to access the requested resource.

| Code             | HTTP Status | Message                                             | Thai Note (หมายเหตุภาษาไทย)                     |
|------------------|-------------|-----------------------------------------------------|--------------------------------------------------|
| `FORBIDDEN`      | 403         | Permission denied                                   | ไม่มีสิทธิ์เข้าถึง resource นี้                    |
| `INVALID_IP`     | 403         | Unable to determine client IP                       | ไม่สามารถระบุ IP ของ client ได้                    |
| `IP_NOT_ALLOWED` | 403         | Your IP address is not allowed                      | IP ไม่อยู่ใน whitelist ของ merchant                |

<!-- หมายเหตุ:
     - FORBIDDEN: admin ไม่มี permission bit ที่จำเป็น (ดู RBAC ใน admin-api.md)
     - INVALID_IP: ระบบไม่สามารถดึง IP จาก request ได้ (อาจเกิดจาก proxy ผิดพลาด)
     - IP_NOT_ALLOWED: IP ของ merchant ไม่อยู่ใน whitelist ที่ตั้งค่าไว้
       ติดต่อ admin เพื่อเพิ่ม IP ใน whitelist
-->

---

## Validation Errors

<!-- ============================================================
     Error ที่เกี่ยวกับ Validation ของ Input

     เกิดขึ้นเมื่อ request body หรือ parameters ไม่ถูกต้อง
     รวมถึง: JSON ผิดรูปแบบ, ฟิลด์ที่จำเป็นขาดหาย,
     ค่าที่ส่งมาไม่ถูกต้อง (เช่น amount <= 0)

     อ้างอิงจาก:
     - services/gateway/internal/handler/ (deposit, withdrawal, wallet)
     - services/user/internal/service/ (admin, merchant, agent, partner)
     - services/bank/internal/handler/bank_handler.go
     ============================================================ -->

Validation errors occur when the request data does not meet the required
format or business rules.

| Code               | HTTP Status | Message                                           | Thai Note (หมายเหตุภาษาไทย)                     |
|--------------------|-------------|---------------------------------------------------|--------------------------------------------------|
| `INVALID_BODY`     | 400         | Invalid request body / not valid JSON              | Request body ไม่ใช่ JSON ที่ถูกต้อง               |
| `MISSING_FIELD`    | 400         | A required field is missing                        | ฟิลด์ที่จำเป็นขาดหาย (เช่น merchant_order_id)    |
| `MISSING_FIELDS`   | 400         | Multiple required fields are missing               | ฟิลด์ที่จำเป็นหลายตัวขาดหาย                      |
| `INVALID_AMOUNT`   | 400         | Amount must be positive / zero or negative         | จำนวนเงินต้องมากกว่า 0                           |
| `INVALID_ID`       | 400         | ID is not a valid UUID format                      | ID ไม่ใช่รูปแบบ UUID ที่ถูกต้อง                    |
| `VALIDATION_ERROR` | 400         | Specific validation failure (see message)          | ข้อมูลไม่ผ่านการ validate (ดูข้อความ error)        |
| `BAD_REQUEST`      | 400         | Invalid request                                    | Request ไม่ถูกต้องโดยทั่วไป                       |

<!-- หมายเหตุ:
     - INVALID_BODY: ตรวจสอบว่า Content-Type เป็น application/json
       และ body เป็น JSON ที่ถูกต้อง
     - MISSING_FIELD: ตรวจสอบว่าส่งฟิลด์ที่จำเป็นครบถ้วน
       ดูรายการฟิลด์ที่จำเป็นใน merchant-api.md สำหรับแต่ละ endpoint
     - INVALID_AMOUNT: amount ต้องเป็นตัวเลขทศนิยมที่มากกว่า 0
       ส่งเป็น string (เช่น "1000.00" ไม่ใช่ 1000.00)
     - INVALID_ID: ID ต้องเป็นรูปแบบ UUID (เช่น "550e8400-e29b-41d4-a716-446655440000")
     - VALIDATION_ERROR: ใช้ใน user-service สำหรับ field-level validation
       เช่น "email is required", "password is required", "name is required"
-->

### Validation Error Details by Endpoint

<!-- รายละเอียด validation error แยกตาม endpoint -->

**Create Deposit (`POST /api/v1/deposits`):**

| Validation Check              | Error Code       | Error Message                         |
|-------------------------------|------------------|---------------------------------------|
| Body is not valid JSON        | `INVALID_BODY`   | invalid request body                  |
| `merchant_order_id` missing   | `MISSING_FIELD`  | merchant_order_id is required         |
| `amount` is zero or negative  | `INVALID_AMOUNT` | amount must be positive               |

<!-- สร้าง deposit: ต้องมี merchant_order_id และ amount > 0 -->

**Create Withdrawal (`POST /api/v1/withdrawals`):**

| Validation Check              | Error Code       | Error Message                         |
|-------------------------------|------------------|---------------------------------------|
| Body is not valid JSON        | `INVALID_BODY`   | invalid request body                  |
| `merchant_order_id` missing   | `MISSING_FIELD`  | merchant_order_id is required         |
| `amount` is zero or negative  | `INVALID_AMOUNT` | amount must be positive               |
| `beneficiary_account` missing | `MISSING_FIELD`  | beneficiary_account is required       |

<!-- สร้าง withdrawal: ต้องมี merchant_order_id, amount > 0, และ beneficiary_account -->

**Create Merchant (`POST /api/v1/merchants`):**

| Validation Check   | Error Code         | Error Message          |
|--------------------|--------------------|------------------------|
| `name` missing     | `VALIDATION_ERROR` | name is required       |
| `email` missing    | `VALIDATION_ERROR` | email is required      |

<!-- สร้าง merchant: ต้องมี name และ email -->

**Create Admin (`POST /api/v1/admins`):**

| Validation Check       | Error Code         | Error Message               |
|------------------------|--------------------|-----------------------------|
| `email` missing        | `VALIDATION_ERROR` | email is required           |
| `password` missing     | `VALIDATION_ERROR` | password is required        |
| `display_name` missing | `VALIDATION_ERROR` | display_name is required    |

<!-- สร้าง admin: ต้องมี email, password, และ display_name -->

---

## Rate Limiting Errors

<!-- ============================================================
     Error ที่เกี่ยวกับ Rate Limiting

     เกิดขึ้นเมื่อเรียก API เกินจำนวนที่กำหนด (100 requests/นาที)
     ระบบใช้ Redis-based sliding window rate limiter
     Key เป็น API Key (หรือ IP address ถ้าไม่มี API Key)

     อ้างอิงจาก:
     - services/gateway/internal/middleware/ratelimit.go
     - pkg/errors/errors.go
     ============================================================ -->

Rate limiting errors occur when you exceed the allowed number of API
requests within a given time window.

| Code          | HTTP Status | Message                                          | Thai Note (หมายเหตุภาษาไทย)                     |
|---------------|-------------|--------------------------------------------------|--------------------------------------------------|
| `RATE_LIMITED`| 429         | Too many requests, please try again later        | เรียก API เกินจำนวนที่กำหนด (100 ครั้ง/นาที)     |

<!-- หมายเหตุ:
     - Limit: 100 requests ต่อนาที ต่อ API Key
     - Window: 1 นาที (sliding window)
     - Key: ใช้ X-API-Key เป็นตัวนับ (fallback เป็น IP address)
     - เมื่อได้รับ 429 ให้ดูค่า header Retry-After (เป็นวินาที)
-->

### Rate Limit Details

<!-- รายละเอียด Rate Limit -->

| Parameter       | Value                                          |
|-----------------|------------------------------------------------|
| **Limit**       | 100 requests per minute                        |
| **Key**         | `X-API-Key` (falls back to client IP address)  |
| **Window**      | 1 minute (sliding window)                      |
| **Storage**     | Redis-based counter                            |
| **Header**      | `Retry-After: 60` (seconds until reset)        |

**Recommended handling:** Implement exponential backoff when receiving
HTTP 429. Wait at least the number of seconds indicated by the
`Retry-After` header before retrying.

<!-- แนะนำ: ใช้ exponential backoff เมื่อได้รับ HTTP 429
     รอตามจำนวนวินาทีใน header Retry-After ก่อน retry -->

### Example Error Response

```json
{
  "success": false,
  "error": "too many requests, please try again later",
  "code": "RATE_LIMITED"
}
```

---

## System Errors

<!-- ============================================================
     Error ที่เกี่ยวกับระบบ (System-Level Errors)

     เกิดขึ้นจากปัญหาภายในระบบ เช่น:
     - ระบบอยู่ในโหมด freeze (maintenance)
     - Internal service ติดต่อไม่ได้
     - ข้อผิดพลาดภายในที่ไม่คาดคิด

     อ้างอิงจาก:
     - services/gateway/internal/middleware/freeze.go
     - services/gateway/internal/handler/ (upstream errors)
     - pkg/errors/errors.go
     ============================================================ -->

System errors indicate platform-level issues that are typically not caused
by the merchant's request itself.

| Code              | HTTP Status | Message                                         | Thai Note (หมายเหตุภาษาไทย)                       |
|-------------------|-------------|--------------------------------------------------|---------------------------------------------------|
| `SYSTEM_FROZEN`   | 503         | System is temporarily frozen for maintenance     | ระบบถูก freeze ชั่วคราว (admin เปิดโหมด maintenance) |
| `EMERGENCY_FREEZE`| 503         | System is frozen                                 | ระบบถูก freeze ฉุกเฉิน                              |
| `UPSTREAM_ERROR`  | 502         | Internal service is unreachable                  | Internal service ติดต่อไม่ได้ (ปัญหาภายใน)          |
| `INTERNAL_ERROR`  | 500         | Internal server error                            | ข้อผิดพลาดภายในระบบที่ไม่คาดคิด                     |

<!-- หมายเหตุ:
     - SYSTEM_FROZEN / EMERGENCY_FREEZE: ระบบอยู่ในโหมด maintenance
       * GET requests (ตรวจสอบสถานะ, ยอดคงเหลือ) ยังใช้ได้ปกติ
       * POST requests (สร้าง deposit, withdrawal) จะถูกปฏิเสธ
       * รอให้ admin ปลด freeze ก่อนส่ง request ใหม่
     - UPSTREAM_ERROR: internal microservice (order-service, withdrawal-service,
       wallet-service) ติดต่อไม่ได้
       * ไม่ใช่ปัญหาจากฝั่ง merchant
       * สามารถ retry ได้หลังจากรอสักครู่
     - INTERNAL_ERROR: ข้อผิดพลาดทั่วไปที่ไม่คาดคิด
       * ติดต่อ support หากเกิดซ้ำ
-->

### Behavior During Emergency Freeze

<!-- พฤติกรรมของระบบขณะ freeze -->

| Request Type     | Behavior During Freeze                         |
|------------------|------------------------------------------------|
| `GET` requests   | Work normally (สถานะปกติ)                       |
| `POST` requests  | Rejected with `SYSTEM_FROZEN` (ถูกปฏิเสธ)      |
| `PUT` requests   | Rejected with `SYSTEM_FROZEN` (ถูกปฏิเสธ)      |
| `DELETE` requests| Rejected with `SYSTEM_FROZEN` (ถูกปฏิเสธ)      |

---

## Wallet Errors

<!-- ============================================================
     Error ที่เกี่ยวกับ Wallet

     เกิดขึ้นเมื่อมีปัญหากับ wallet ของ merchant เช่น:
     - ยอดเงินไม่เพียงพอสำหรับถอน
     - Wallet ไม่พบ
     - Conflict เมื่อมีการแก้ไข wallet พร้อมกัน

     อ้างอิงจาก:
     - pkg/errors/errors.go
     - services/wallet/internal/repository/wallet_repo.go
     - services/wallet/internal/handler/wallet_handler.go
     ============================================================ -->

Wallet errors relate to the merchant's wallet operations, including balance
checks and fund movements.

| Code                 | HTTP Status | Message                                        | Thai Note (หมายเหตุภาษาไทย)                     |
|----------------------|-------------|------------------------------------------------|--------------------------------------------------|
| `INSUFFICIENT_FUNDS` | 400         | Insufficient wallet balance                    | ยอดเงินใน wallet ไม่เพียงพอสำหรับรายการนี้        |
| `NOT_FOUND`          | 404         | Wallet not found                               | ไม่พบ wallet ของ merchant นี้                     |
| `CONFLICT`           | 409         | Wallet version conflict (concurrent update)    | มีการแก้ไข wallet พร้อมกัน (optimistic locking)   |

<!-- หมายเหตุ:
     - INSUFFICIENT_FUNDS: ยอดคงเหลือ (available balance) น้อยกว่าจำนวนเงินที่ต้องการ
       * available = balance - hold_balance
       * ตรวจสอบยอดคงเหลือก่อนสร้าง withdrawal ด้วย GET /api/v1/wallet/balance
     - NOT_FOUND: wallet ยังไม่ถูกสร้าง หรือ merchant ID ไม่ถูกต้อง
     - CONFLICT: เกิดจาก optimistic locking เมื่อมีหลาย request
       แก้ไข wallet พร้อมกัน retry ได้ทันที
-->

### Understanding Available Balance

<!-- ทำความเข้าใจยอดคงเหลือที่ใช้ได้ -->

```
balance         = Total funds in wallet (ยอดรวมใน wallet)
hold_balance    = Funds reserved for pending withdrawals (ยอดที่ถูก hold)
available       = balance - hold_balance (ยอดที่สามารถใช้ได้)
```

<!-- เมื่อสร้าง withdrawal ระบบจะตรวจสอบว่า available >= amount ที่ต้องการถอน
     หาก available < amount จะได้รับ INSUFFICIENT_FUNDS -->

A withdrawal request will fail with `INSUFFICIENT_FUNDS` if the requested
amount exceeds the `available` balance. Check your balance before submitting
a withdrawal using `GET /api/v1/wallet/balance`.

---

## Withdrawal Errors

<!-- ============================================================
     Error ที่เกี่ยวกับการถอนเงิน (Withdrawal)

     เกิดขึ้นเมื่อมีปัญหาเฉพาะกับ withdrawal เช่น:
     - เกินวงเงินถอนรายวัน
     - สถานะไม่ถูกต้องสำหรับ operation ที่ต้องการ
     - ยอดคงเหลือไม่เพียงพอ
     - Withdrawal ID ซ้ำ

     อ้างอิงจาก:
     - services/withdrawal/internal/service/withdrawal.go
     - services/withdrawal/internal/repository/withdrawal_repo.go
     ============================================================ -->

Withdrawal-specific errors occur during the withdrawal lifecycle, from
creation through approval to bank transfer execution.

| Code                     | HTTP Status | Message                                          | Thai Note (หมายเหตุภาษาไทย)                       |
|--------------------------|-------------|--------------------------------------------------|---------------------------------------------------|
| `DAILY_LIMIT_EXCEEDED`   | 400         | Daily withdrawal limit exceeded                  | ถอนเกินวงเงินรายวันที่กำหนด                        |
| `INSUFFICIENT_FUNDS`     | 400         | Insufficient wallet balance for withdrawal       | ยอดใน wallet ไม่เพียงพอสำหรับถอนเงิน               |
| `INVALID_STATUS`         | 422         | Withdrawal is not in the correct status          | สถานะ withdrawal ไม่ถูกต้องสำหรับ operation นี้     |
| `DUPLICATE_WITHDRAWAL`   | 409         | Withdrawal with this ID already exists           | มี withdrawal ที่ใช้ ID นี้อยู่แล้ว                |

<!-- หมายเหตุ:
     - DAILY_LIMIT_EXCEEDED:
       * ทุก merchant มี daily_withdrawal_limit ที่กำหนดโดย admin
       * ผลรวมถอนเงินทั้งหมดในวันนี้ + ยอดใหม่ เกินวงเงินรายวัน
       * ต้องรอวันถัดไป หรือติดต่อ admin เพื่อปรับวงเงิน
     - INSUFFICIENT_FUNDS:
       * available balance < จำนวนเงินที่ต้องการถอน
       * ตรวจสอบยอดคงเหลือก่อนสร้าง withdrawal
     - INVALID_STATUS: เกิดเมื่อพยายามทำ operation ที่ไม่สอดคล้องกับสถานะปัจจุบัน
       * approve ได้เฉพาะ withdrawal ที่เป็น pending เท่านั้น
       * reject ได้เฉพาะ withdrawal ที่เป็น pending เท่านั้น
       * complete ได้เฉพาะ withdrawal ที่เป็น approved เท่านั้น
     - DUPLICATE_WITHDRAWAL: ID ซ้ำ ตรวจสอบว่าไม่ได้สร้าง withdrawal ซ้ำ
-->

### Withdrawal Status Transitions

<!-- การเปลี่ยนสถานะ withdrawal ที่อนุญาต -->

| Current Status | Allowed Transitions                  | Error if Invalid                  |
|----------------|--------------------------------------|-----------------------------------|
| `pending`      | `approved`, `rejected`               | -                                 |
| `approved`     | `completed`, `failed`                | Cannot approve or reject again    |
| `rejected`     | None (final state)                   | Cannot change rejected withdrawal |
| `completed`    | None (final state)                   | Cannot change completed withdrawal|
| `failed`       | None (final state)                   | Cannot change failed withdrawal   |

<!-- สถานะ rejected, completed, failed เป็นสถานะสุดท้าย
     ไม่สามารถเปลี่ยนแปลงได้อีก -->

---

## Bank Errors

<!-- ============================================================
     Error ที่เกี่ยวกับธนาคาร (Bank)

     เกิดขึ้นเมื่อมีปัญหากับบัญชีธนาคาร เช่น:
     - ไม่พบบัญชีที่สามารถรับเงินได้
     - บัญชีถึง limit รายวัน
     - ข้อมูลบัญชีไม่ถูกต้อง

     อ้างอิงจาก:
     - services/bank/internal/handler/bank_handler.go
     - services/bank/internal/service/bank_service.go
     ============================================================ -->

Bank-related errors occur during bank account management and fund transfer
operations.

| Code               | HTTP Status | Message                                           | Thai Note (หมายเหตุภาษาไทย)                     |
|--------------------|-------------|---------------------------------------------------|--------------------------------------------------|
| `INVALID_BODY`     | 400         | Failed to parse request body                      | Request body ไม่ถูกต้อง (ใน bank operations)      |
| `MISSING_FIELD`    | 400         | Required field missing (see message for details)  | ฟิลด์ที่จำเป็นขาดหาย (เช่น bank_account_id)      |
| `NOT_FOUND`        | 404         | Bank account not found                            | ไม่พบบัญชีธนาคารที่ระบุ                          |

<!-- หมายเหตุ:
     - Bank errors ส่วนใหญ่เป็น internal (ใช้ระหว่าง service)
     - Merchant มักจะเห็น bank error ผ่าน UPSTREAM_ERROR แทน
     - ถ้าได้รับ UPSTREAM_ERROR ตอนสร้าง deposit อาจเป็นเพราะ:
       * ไม่มีบัญชีธนาคารที่พร้อมรับเงิน
       * บัญชีธนาคารทั้งหมดถึง limit รายวัน
-->

### Bank-Specific MISSING_FIELD Errors

<!-- ฟิลด์ที่จำเป็นสำหรับแต่ละ bank operation -->

| Operation              | Required Fields                                   |
|------------------------|---------------------------------------------------|
| Select Account         | `merchant_id`                                     |
| Auto-Switch            | `merchant_id`                                     |
| Update Daily Received  | `bank_account_id`, `amount`                       |
| Create Transfer        | `from_account_id`, `to_holding_id`, `admin_id`    |

---

## Session Errors

<!-- ============================================================
     Error ที่เกี่ยวกับ Session (Admin Dashboard)

     เกิดขึ้นเมื่อ session ของ admin หมดอายุหรือไม่ถูกต้อง
     Session ใช้ cookie ชื่อ richpay_session

     อ้างอิงจาก:
     - services/auth/internal/middleware/
     - admin-api.md
     ============================================================ -->

Session errors are specific to the admin dashboard and occur when the
admin's session cookie is missing, expired, or invalid.

| Code              | HTTP Status | Message                                          | Thai Note (หมายเหตุภาษาไทย)                     |
|-------------------|-------------|--------------------------------------------------|--------------------------------------------------|
| `MISSING_SESSION` | 401         | No richpay_session cookie provided               | ไม่พบ cookie richpay_session ใน request           |
| `INVALID_SESSION` | 401         | Session expired or not found in Redis            | Session หมดอายุหรือถูกลบจาก Redis                 |

<!-- หมายเหตุ:
     - Session errors ใช้เฉพาะ admin dashboard
     - Merchant API ใช้ API Key authentication ไม่ใช่ session
     - MISSING_SESSION: ตรวจสอบว่าส่ง cookie richpay_session มาด้วยทุก request
     - INVALID_SESSION: login ใหม่เพื่อรับ session ใหม่
       * Session อาจหมดอายุตามเวลาที่กำหนด
       * Session อาจถูก invalidate เมื่อ admin logout จากที่อื่น
-->

---

## Resource Errors

<!-- ============================================================
     Error ที่เกี่ยวกับ Resource

     เกิดขึ้นเมื่อ resource ที่ร้องขอไม่พบ หรือมี conflict

     อ้างอิงจาก:
     - pkg/errors/errors.go
     - services/user/internal/repository/stub_repo.go
     ============================================================ -->

Resource errors occur when attempting to access or create resources.

| Code                  | HTTP Status | Message                                        | Thai Note (หมายเหตุภาษาไทย)                     |
|-----------------------|-------------|------------------------------------------------|--------------------------------------------------|
| `NOT_FOUND`           | 404         | Resource not found                             | ไม่พบ resource ที่ร้องขอ                          |
| `CONFLICT`            | 409         | Resource conflict                              | Resource ซ้ำหรือขัดแย้ง                           |
| `DUPLICATE_ADMIN`     | 409         | Admin with this ID already exists              | มี admin ที่ใช้ ID นี้อยู่แล้ว                     |
| `DUPLICATE_MERCHANT`  | 409         | Merchant with this ID already exists           | มี merchant ที่ใช้ ID นี้อยู่แล้ว                  |
| `DUPLICATE_AGENT`     | 409         | Agent with this ID already exists              | มี agent ที่ใช้ ID นี้อยู่แล้ว                     |
| `DUPLICATE_PARTNER`   | 409         | Partner with this ID already exists            | มี partner ที่ใช้ ID นี้อยู่แล้ว                   |
| `DUPLICATE_SLIP`      | 409         | This slip has already been used                | สลิปนี้ถูกใช้ยืนยันไปแล้ว (ป้องกันใช้สลิปซ้ำ)     |
| `ORDER_EXPIRED`       | 410         | Deposit order has expired                      | Deposit order หมดอายุแล้ว                        |

<!-- หมายเหตุ:
     - NOT_FOUND: resource ไม่มีในระบบ ตรวจสอบ ID ที่ส่ง
     - DUPLICATE_*: มี resource ที่ใช้ ID เดียวกันอยู่แล้ว
     - DUPLICATE_SLIP: สลิปถูกใช้แล้ว ไม่สามารถใช้ซ้ำได้
       เป็นการป้องกันการทุจริตจากการใช้สลิปเดิมหลายครั้ง
     - ORDER_EXPIRED: deposit order หมดอายุแล้ว
       ต้องสร้าง order ใหม่หากต้องการดำเนินการต่อ
-->

---

## Quick Reference Table

<!-- ============================================================
     ตารางอ้างอิงด่วน: รวม Error Codes ทั้งหมดเรียงตาม HTTP Status

     ตารางนี้รวบรวม error codes ทั้งหมดที่ใช้ในระบบ
     เรียงตาม HTTP status code เพื่อให้ค้นหาง่าย
     ============================================================ -->

Complete list of all error codes sorted by HTTP status code:

### 400 Bad Request

<!-- รวม error codes ที่ return HTTP 400 -->

| Code                     | Message                                     | Category                |
|--------------------------|---------------------------------------------|-------------------------|
| `BAD_REQUEST`            | Invalid request                             | Validation              |
| `INVALID_BODY`           | Invalid request body / not valid JSON       | Validation              |
| `MISSING_FIELD`          | Required field is missing                   | Validation              |
| `MISSING_FIELDS`         | Multiple required fields missing            | Validation              |
| `INVALID_AMOUNT`         | Amount must be positive                     | Validation              |
| `INVALID_ID`             | ID is not a valid UUID format               | Validation              |
| `VALIDATION_ERROR`       | Specific validation failure                 | Validation              |
| `INSUFFICIENT_FUNDS`     | Insufficient wallet balance                 | Wallet                  |
| `DAILY_LIMIT_EXCEEDED`   | Daily withdrawal limit exceeded             | Withdrawal              |

### 401 Unauthorized

<!-- รวม error codes ที่ return HTTP 401 -->

| Code                | Message                                     | Category                |
|---------------------|---------------------------------------------|-------------------------|
| `MISSING_API_KEY`   | X-API-Key header is required                | Authentication          |
| `MISSING_TIMESTAMP` | X-Timestamp header is required              | Authentication          |
| `MISSING_SIGNATURE` | X-Signature header is required              | Authentication          |
| `INVALID_TIMESTAMP` | X-Timestamp must be a unix timestamp        | Authentication          |
| `EXPIRED_TIMESTAMP` | Request timestamp too old or too far future | Authentication          |
| `INVALID_API_KEY`   | Invalid API key                             | Authentication          |
| `INVALID_SIGNATURE` | Invalid HMAC signature                      | Authentication          |
| `UNAUTHORIZED`      | Authentication required                     | Authentication          |
| `INVALID_2FA`       | Invalid 2FA code                            | Authentication          |
| `MISSING_SESSION`   | No session cookie provided                  | Session                 |
| `INVALID_SESSION`   | Session expired or not found                | Session                 |

### 403 Forbidden

<!-- รวม error codes ที่ return HTTP 403 -->

| Code             | Message                                     | Category                |
|------------------|---------------------------------------------|-------------------------|
| `FORBIDDEN`      | Permission denied                           | Authorization           |
| `INVALID_IP`     | Unable to determine client IP               | Authorization           |
| `IP_NOT_ALLOWED` | Your IP address is not allowed              | Authorization           |

### 404 Not Found

<!-- รวม error codes ที่ return HTTP 404 -->

| Code         | Message                                     | Category                |
|--------------|---------------------------------------------|-------------------------|
| `NOT_FOUND`  | Resource not found                          | Resource                |

### 409 Conflict

<!-- รวม error codes ที่ return HTTP 409 -->

| Code                   | Message                                     | Category                |
|------------------------|---------------------------------------------|-------------------------|
| `CONFLICT`             | Resource conflict                           | Resource                |
| `DUPLICATE_ADMIN`      | Admin with this ID already exists           | Resource                |
| `DUPLICATE_MERCHANT`   | Merchant with this ID already exists        | Resource                |
| `DUPLICATE_AGENT`      | Agent with this ID already exists           | Resource                |
| `DUPLICATE_PARTNER`    | Partner with this ID already exists         | Resource                |
| `DUPLICATE_WITHDRAWAL` | Withdrawal with this ID already exists      | Withdrawal              |
| `DUPLICATE_SLIP`       | This slip has already been used             | Resource                |

### 410 Gone

<!-- รวม error codes ที่ return HTTP 410 -->

| Code            | Message                                     | Category                |
|-----------------|---------------------------------------------|-------------------------|
| `ORDER_EXPIRED` | Deposit order has expired                   | Resource                |

### 422 Unprocessable Entity

<!-- รวม error codes ที่ return HTTP 422 -->

| Code             | Message                                     | Category                |
|------------------|---------------------------------------------|-------------------------|
| `INVALID_STATUS` | Resource not in correct status for operation| Withdrawal              |

### 423 Locked

<!-- รวม error codes ที่ return HTTP 423 -->

| Code             | Message                                     | Category                |
|------------------|---------------------------------------------|-------------------------|
| `ACCOUNT_LOCKED` | Account is locked                           | Authentication          |

### 429 Too Many Requests

<!-- รวม error codes ที่ return HTTP 429 -->

| Code          | Message                                     | Category                |
|---------------|---------------------------------------------|-------------------------|
| `RATE_LIMITED` | Too many requests                          | Rate Limiting           |

### 500 Internal Server Error

<!-- รวม error codes ที่ return HTTP 500 -->

| Code             | Message                                     | Category                |
|------------------|---------------------------------------------|-------------------------|
| `INTERNAL_ERROR` | Internal server error                       | System                  |

### 502 Bad Gateway

<!-- รวม error codes ที่ return HTTP 502 -->

| Code             | Message                                     | Category                |
|------------------|---------------------------------------------|-------------------------|
| `UPSTREAM_ERROR` | Internal service is unreachable             | System                  |

### 503 Service Unavailable

<!-- รวม error codes ที่ return HTTP 503 -->

| Code               | Message                                     | Category                |
|--------------------|---------------------------------------------|-------------------------|
| `SYSTEM_FROZEN`    | System temporarily frozen for maintenance   | System                  |
| `EMERGENCY_FREEZE` | System is frozen                            | System                  |

---

## Error Handling Best Practices

<!-- ============================================================
     แนวทางปฏิบัติที่ดีสำหรับการจัดการ Error

     คำแนะนำเพื่อให้ merchant จัดการ error ได้อย่างมีประสิทธิภาพ
     ============================================================ -->

### For Merchants

<!-- สำหรับ Merchant -->

1. **Always check the `code` field** for programmatic error handling. The
   `error` message may change over time, but `code` values are stable.
   <!-- ใช้ `code` สำหรับ programmatic handling เพราะค่าคงที่
        ข้อความ `error` อาจเปลี่ยนได้ในอนาคต -->

2. **Implement retry logic** for transient errors (`UPSTREAM_ERROR`,
   `RATE_LIMITED`, `INTERNAL_ERROR`). Use exponential backoff.
   <!-- ใช้ retry logic สำหรับ error ชั่วคราว (UPSTREAM_ERROR, RATE_LIMITED)
        ใช้ exponential backoff -->

3. **Do not retry** client errors (`INVALID_BODY`, `MISSING_FIELD`,
   `INVALID_AMOUNT`). Fix the request data before retrying.
   <!-- ห้าม retry สำหรับ client error ต้องแก้ request ก่อน -->

4. **Check balance before withdrawals** to avoid `INSUFFICIENT_FUNDS`.
   Use `GET /api/v1/wallet/balance` before submitting.
   <!-- ตรวจสอบยอดคงเหลือก่อนสร้าง withdrawal -->

5. **Monitor for `SYSTEM_FROZEN`** and pause your integration until the
   system is back to normal.
   <!-- เฝ้าดู SYSTEM_FROZEN และหยุดส่ง request จนกว่าระบบจะกลับมาปกติ -->

### Retry Decision Matrix

<!-- ตารางตัดสินใจว่าควร retry หรือไม่ -->

| HTTP Status | Should Retry? | Notes                                               |
|-------------|---------------|-----------------------------------------------------|
| 400         | No            | Fix request data first (แก้ข้อมูล request ก่อน)       |
| 401         | No            | Check authentication (ตรวจสอบ authentication)         |
| 403         | No            | Check permissions/IP (ตรวจสอบสิทธิ์/IP)               |
| 404         | No            | Resource doesn't exist (resource ไม่มีในระบบ)         |
| 409         | No            | Resolve conflict first (แก้ไข conflict ก่อน)          |
| 429         | Yes           | Wait for Retry-After (รอตาม Retry-After header)      |
| 500         | Yes           | With exponential backoff (ใช้ exponential backoff)    |
| 502         | Yes           | With exponential backoff (ใช้ exponential backoff)    |
| 503         | Yes           | Wait for system unfreezing (รอระบบกลับมาปกติ)         |

---

*Last updated: 2026-04-10*
*RichPayment Error Code Reference v1*
