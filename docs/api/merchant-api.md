# RichPayment Merchant API Documentation

<!-- =======================================================================
     RichPayment Merchant-Facing API Reference

     This document covers every public endpoint available to merchants
     integrating with the RichPayment payment gateway.

     Base URL: https://api.richpayment.co/api/v1

     NOTE (หมายเหตุ):
     - ภาษาหลักของเอกสารนี้เป็นภาษาอังกฤษ แต่มีหมายเหตุภาษาไทยเพิ่มเติม
       เพื่อช่วยเหลือ Merchant ไทยในการเข้าใจรายละเอียดที่สำคัญ
     - เอกสารนี้อ้างอิงจาก source code โดยตรง (gateway service handlers,
       middleware, และ models)
     ======================================================================= -->

## Table of Contents

<!-- สารบัญ: ลิงก์ไปยังแต่ละส่วนของเอกสาร -->

1. [Overview](#overview)
2. [Base URL & Environments](#base-url--environments)
3. [Authentication](#authentication)
4. [Rate Limiting](#rate-limiting)
5. [Emergency Freeze](#emergency-freeze)
6. [Endpoints](#endpoints)
   - [Create Deposit](#create-deposit)
   - [Check Deposit Status](#check-deposit-status)
   - [Create Withdrawal](#create-withdrawal)
   - [Check Withdrawal Status](#check-withdrawal-status)
   - [Get Wallet Balance](#get-wallet-balance)
   - [Health Check](#health-check)
7. [Webhook Notifications](#webhook-notifications)
8. [Error Codes](#error-codes)
9. [Order Status Lifecycle](#order-status-lifecycle)
10. [Supported Banks](#supported-banks)

---

## Overview

<!-- ภาพรวมของ RichPayment API -->

RichPayment is a payment gateway SaaS platform designed for the Thai market. The
Merchant API allows businesses to:

- **Accept deposits** via bank transfer with PromptPay QR codes
  <!-- รับฝากเงินผ่านการโอนธนาคาร พร้อม QR Code PromptPay -->
- **Process withdrawals** to Thai bank accounts
  <!-- ถอนเงินไปยังบัญชีธนาคารไทย -->
- **Query wallet balance** in real time
  <!-- สอบถามยอดคงเหลือใน wallet แบบ real-time -->
- **Receive webhook notifications** when transaction statuses change
  <!-- รับการแจ้งเตือนผ่าน webhook เมื่อสถานะรายการเปลี่ยน -->

### Architecture Note

<!-- หมายเหตุสถาปัตยกรรม: gateway เป็นจุดเข้าเดียว ไม่มี business logic -->

The gateway API is a thin proxy layer. It validates your request and forwards it
to the appropriate internal microservice:

| Request Type | Internal Service  | Port |
|-------------|-------------------|------|
| Deposits    | order-service     | 8083 |
| Withdrawals | withdrawal-service| 8085 |
| Wallet      | wallet-service    | 8084 |

All responses are forwarded back to the merchant without modification.

---

## Base URL & Environments

<!-- URL ฐานสำหรับแต่ละ environment -->

| Environment | Base URL                              |
|------------|---------------------------------------|
| Production | `https://api.richpayment.co/api/v1`   |
| Sandbox    | `https://sandbox.richpayment.co/api/v1` |

All API requests must use HTTPS. HTTP requests will be rejected.

<!-- ทุกคำขอ API ต้องใช้ HTTPS เท่านั้น คำขอ HTTP จะถูกปฏิเสธ -->

---

## Authentication

<!-- ============================================================
     การยืนยันตัวตน (Authentication)

     ระบบ RichPayment ใช้ 2 ชั้นในการยืนยันตัวตน:
     1. API Key - ระบุว่าเป็น Merchant ใด
     2. HMAC-SHA256 Signature - พิสูจน์ว่า request ไม่ถูกแก้ไข
     ============================================================ -->

Every API request requires three authentication headers:

| Header          | Description                                         |
|-----------------|-----------------------------------------------------|
| `X-API-Key`     | Your merchant API key (issued at registration)      |
| `X-Timestamp`   | Current Unix timestamp (seconds since epoch)        |
| `X-Signature`   | HMAC-SHA256 signature of the request                |

### How API Keys Work

<!-- วิธีการทำงานของ API Key -->

- Your API key is generated when your merchant account is created
- The key is shown **only once** at creation time -- store it securely
  <!-- API Key จะแสดงเพียงครั้งเดียวตอนสร้าง merchant กรุณาเก็บรักษาไว้อย่างดี -->
- The key prefix (first 8 characters) is used for quick identification
- If compromised, request a key rotation from your admin/agent
  <!-- หาก API Key ถูกเปิดเผย ให้ติดต่อ admin/agent เพื่อ rotate key ใหม่ -->

### HMAC-SHA256 Signature

<!-- ============================================================
     การสร้าง Signature

     Signature ช่วยป้องกันการแก้ไข request ระหว่างทาง (man-in-the-middle)
     โดยใช้ HMAC-SHA256 ร่วมกับ secret ที่แชร์กันระหว่าง merchant กับระบบ
     ============================================================ -->

The signature is computed as:

```
signature = HMAC-SHA256(hmac_secret, timestamp + method + path + body)
```

**Components:**

| Component   | Description                                      | Example                          |
|-------------|--------------------------------------------------|----------------------------------|
| `hmac_secret` | Your HMAC secret (provided at registration)   | `whsec_abc123...`                |
| `timestamp` | Same value as `X-Timestamp` header               | `1700000000`                     |
| `method`    | HTTP method in uppercase                          | `POST`                           |
| `path`      | Request path (without query string)               | `/api/v1/deposits`               |
| `body`      | Raw request body (empty string for GET requests)  | `{"merchant_order_id":"ORD-1"}`  |

### Timestamp Validation

<!-- การตรวจสอบ Timestamp -->

The timestamp must be within **5 minutes** (300 seconds) of the server's current
time. Requests with timestamps older or newer than this window will be rejected
with error code `EXPIRED_TIMESTAMP`.

<!-- Timestamp ต้องอยู่ในช่วง 5 นาที จากเวลาปัจจุบันของ server
     หากเกินกว่านี้จะถูกปฏิเสธด้วย error code EXPIRED_TIMESTAMP -->

### Code Examples

#### Go

```go
// buildSignature creates the HMAC-SHA256 signature for a RichPayment API request.
//
// Parameters:
//   - secret:    your HMAC secret key (from merchant registration)
//   - timestamp: Unix timestamp as a string (same as X-Timestamp header)
//   - method:    HTTP method in uppercase (e.g. "POST", "GET")
//   - path:      request path without query string (e.g. "/api/v1/deposits")
//   - body:      raw request body as string (empty string for GET requests)
//
// Returns the hex-encoded HMAC-SHA256 signature.
func buildSignature(secret, timestamp, method, path, body string) string {
    // Concatenate all components into the signing message.
    // Order matters: timestamp + method + path + body
    message := timestamp + method + path + body

    // Create HMAC-SHA256 hash using the merchant's secret key.
    mac := hmac.New(sha256.New, []byte(secret))
    mac.Write([]byte(message))

    // Return the hex-encoded digest for use in the X-Signature header.
    return hex.EncodeToString(mac.Sum(nil))
}
```

#### Python

```python
import hmac
import hashlib
import time
import requests
import json

def create_signature(secret: str, timestamp: str, method: str, path: str, body: str) -> str:
    """
    Create HMAC-SHA256 signature for RichPayment API authentication.

    สร้าง signature สำหรับยืนยันตัวตนกับ RichPayment API

    Args:
        secret:    HMAC secret key (จาก merchant registration)
        timestamp: Unix timestamp เป็น string
        method:    HTTP method เช่น "POST", "GET"
        path:      request path เช่น "/api/v1/deposits"
        body:      request body (string ว่างสำหรับ GET)

    Returns:
        Hex-encoded HMAC-SHA256 signature
    """
    # Concatenate signing components in the correct order.
    message = timestamp + method + path + body

    # Compute HMAC-SHA256 and return as hex string.
    return hmac.new(
        secret.encode('utf-8'),
        message.encode('utf-8'),
        hashlib.sha256
    ).hexdigest()


# --- Example: Create a deposit order ---
# ตัวอย่าง: สร้างรายการฝากเงิน

api_key = "rpay_your_api_key_here"
hmac_secret = "whsec_your_secret_here"

timestamp = str(int(time.time()))
method = "POST"
path = "/api/v1/deposits"
body = json.dumps({
    "merchant_order_id": "ORD-20240101-001",
    "customer_name": "สมชาย ใจดี",
    "customer_bank_code": "KBANK",
    "amount": "1000.00",
    "currency": "THB",
    "callback_url": "https://yoursite.com/webhook/richpayment"
})

signature = create_signature(hmac_secret, timestamp, method, path, body)

# Send the authenticated request.
response = requests.post(
    f"https://api.richpayment.co{path}",
    headers={
        "Content-Type": "application/json",
        "X-API-Key": api_key,
        "X-Timestamp": timestamp,
        "X-Signature": signature,
    },
    data=body,
)

print(response.json())
```

#### Node.js

```javascript
const crypto = require('crypto');
const axios = require('axios');

/**
 * Create HMAC-SHA256 signature for RichPayment API authentication.
 *
 * สร้าง signature สำหรับยืนยันตัวตนกับ RichPayment API
 *
 * @param {string} secret    - HMAC secret key
 * @param {string} timestamp - Unix timestamp as string
 * @param {string} method    - HTTP method (e.g. "POST")
 * @param {string} path      - Request path (e.g. "/api/v1/deposits")
 * @param {string} body      - Request body (empty string for GET)
 * @returns {string} Hex-encoded HMAC-SHA256 signature
 */
function createSignature(secret, timestamp, method, path, body) {
  // Concatenate all components in the correct order.
  const message = timestamp + method + path + body;

  // Compute and return HMAC-SHA256 as hex string.
  return crypto
    .createHmac('sha256', secret)
    .update(message)
    .digest('hex');
}

// --- Example usage ---
const apiKey = 'rpay_your_api_key_here';
const hmacSecret = 'whsec_your_secret_here';
const timestamp = Math.floor(Date.now() / 1000).toString();
const method = 'POST';
const path = '/api/v1/deposits';
const body = JSON.stringify({
  merchant_order_id: 'ORD-20240101-001',
  customer_name: 'สมชาย ใจดี',
  customer_bank_code: 'KBANK',
  amount: '1000.00',
  currency: 'THB',
  callback_url: 'https://yoursite.com/webhook/richpayment',
});

const signature = createSignature(hmacSecret, timestamp, method, path, body);

axios.post(`https://api.richpayment.co${path}`, body, {
  headers: {
    'Content-Type': 'application/json',
    'X-API-Key': apiKey,
    'X-Timestamp': timestamp,
    'X-Signature': signature,
  },
}).then(res => console.log(res.data));
```

#### PHP

```php
<?php
/**
 * Create HMAC-SHA256 signature for RichPayment API authentication.
 *
 * สร้าง signature สำหรับยืนยันตัวตนกับ RichPayment API
 *
 * @param string $secret    HMAC secret key
 * @param string $timestamp Unix timestamp as string
 * @param string $method    HTTP method (e.g. "POST")
 * @param string $path      Request path (e.g. "/api/v1/deposits")
 * @param string $body      Request body (empty string for GET)
 * @return string Hex-encoded HMAC-SHA256 signature
 */
function createSignature(
    string $secret,
    string $timestamp,
    string $method,
    string $path,
    string $body
): string {
    // Concatenate all signing components in order.
    $message = $timestamp . $method . $path . $body;

    // Compute HMAC-SHA256 and return as hex string.
    return hash_hmac('sha256', $message, $secret);
}

// --- Example usage ---
$apiKey     = 'rpay_your_api_key_here';
$hmacSecret = 'whsec_your_secret_here';
$timestamp  = (string) time();
$method     = 'POST';
$path       = '/api/v1/deposits';
$body       = json_encode([
    'merchant_order_id' => 'ORD-20240101-001',
    'customer_name'     => 'สมชาย ใจดี',
    'customer_bank_code'=> 'KBANK',
    'amount'            => '1000.00',
    'currency'          => 'THB',
    'callback_url'      => 'https://yoursite.com/webhook/richpayment',
]);

$signature = createSignature($hmacSecret, $timestamp, $method, $path, $body);

// Send the authenticated request using cURL.
$ch = curl_init("https://api.richpayment.co{$path}");
curl_setopt_array($ch, [
    CURLOPT_POST           => true,
    CURLOPT_POSTFIELDS     => $body,
    CURLOPT_RETURNTRANSFER => true,
    CURLOPT_HTTPHEADER     => [
        'Content-Type: application/json',
        "X-API-Key: {$apiKey}",
        "X-Timestamp: {$timestamp}",
        "X-Signature: {$signature}",
    ],
]);

$response = curl_exec($ch);
curl_close($ch);

echo $response;
```

---

## Rate Limiting

<!-- ============================================================
     การจำกัดอัตราการเรียก API (Rate Limiting)

     ระบบใช้ Redis-based rate limiter เพื่อป้องกันการใช้งานมากเกินไป
     ============================================================ -->

| Parameter       | Value                            |
|-----------------|----------------------------------|
| **Limit**       | 100 requests per minute          |
| **Key**         | Your `X-API-Key` (falls back to IP address) |
| **Window**      | 1 minute (sliding window)        |
| **Storage**     | Redis-based counter              |

When you exceed the rate limit, the API returns:

- **HTTP Status:** `429 Too Many Requests`
- **Error Code:** `RATE_LIMITED`
- **Header:** `Retry-After: 60` (seconds until the window resets)

<!-- เมื่อเกินอัตราที่กำหนด API จะส่ง HTTP 429 กลับมา
     พร้อม header Retry-After บอกจำนวนวินาทีที่ต้องรอ -->

```json
{
  "success": false,
  "error": "too many requests, please try again later",
  "code": "RATE_LIMITED"
}
```

**Best Practice:** Implement exponential back-off in your client when you
receive a 429 response.

<!-- แนะนำ: ใช้ exponential back-off เมื่อได้รับ 429 -->

---

## Emergency Freeze

<!-- ============================================================
     ระบบหยุดฉุกเฉิน (Emergency Freeze)

     เมื่อ admin เปิดโหมด freeze ระบบจะปฏิเสธทุก request ที่เป็น
     POST/PUT/DELETE แต่ยังอนุญาต GET requests
     ============================================================ -->

The system may be placed in **emergency freeze** mode by administrators during
maintenance or security incidents. When frozen:

- **GET requests** (status checks, balance queries) continue to work normally
- **POST requests** (create deposit, create withdrawal) are rejected

Frozen response:

```json
{
  "success": false,
  "error": "system is temporarily frozen for maintenance",
  "code": "SYSTEM_FROZEN"
}
```

**HTTP Status:** `503 Service Unavailable`

<!-- ในโหมด freeze: GET ใช้ได้ปกติ แต่ POST จะถูกปฏิเสธด้วย HTTP 503 -->

---

## Endpoints

### Standard Response Envelope

<!-- ============================================================
     โครงสร้างการตอบกลับมาตรฐาน

     ทุก response จะอยู่ในรูปแบบ envelope เดียวกัน เพื่อให้ parse ง่าย
     ============================================================ -->

All API responses follow a consistent JSON envelope:

**Success:**
```json
{
  "success": true,
  "data": { ... }
}
```

**Error:**
```json
{
  "success": false,
  "error": "human-readable error message",
  "code": "MACHINE_READABLE_CODE"
}
```

---

### Create Deposit

<!-- ============================================================
     สร้างรายการฝากเงิน

     Merchant เรียก endpoint นี้เพื่อสร้าง deposit order
     ระบบจะเลือกบัญชีธนาคาร, สร้าง QR Code PromptPay, และรอให้
     ลูกค้าโอนเงิน เมื่อตรวจพบเงินเข้า ระบบจะส่ง webhook แจ้ง merchant
     ============================================================ -->

Creates a new deposit order. The system assigns a receiving bank account,
generates a PromptPay QR code, and waits for the customer to transfer funds.

**Endpoint:** `POST /api/v1/deposits`

**Request Headers:**

| Header         | Required | Description                     |
|----------------|----------|---------------------------------|
| `Content-Type` | Yes      | `application/json`              |
| `X-API-Key`    | Yes      | Your merchant API key           |
| `X-Timestamp`  | Yes      | Current Unix timestamp          |
| `X-Signature`  | Yes      | HMAC-SHA256 signature           |

**Request Body:**

| Field               | Type    | Required | Description                                          |
|---------------------|---------|----------|------------------------------------------------------|
| `merchant_order_id` | string  | Yes      | Your unique order reference for idempotency           |
| `customer_name`     | string  | No       | End customer's name (ชื่อลูกค้า)                      |
| `customer_bank_code`| string  | No       | Customer's bank code (e.g. `KBANK`, `SCB`)           |
| `amount`            | string  | Yes      | Deposit amount as decimal string (must be > 0)       |
| `currency`          | string  | No       | ISO 4217 currency code (default: `THB`)              |
| `callback_url`      | string  | No       | Webhook URL for this specific order                  |

<!-- หมายเหตุ:
     - merchant_order_id: ใช้สำหรับ idempotency ป้องกันสร้างรายการซ้ำ
     - amount: ต้องเป็นจำนวนเงินที่มากกว่า 0
     - ระบบอาจปรับ amount เล็กน้อย (เช่น เพิ่มสตางค์) เพื่อให้ยอดไม่ซ้ำกัน
       ทำให้จับคู่ SMS ได้แม่นยำขึ้น (duplicate_amount_strategy = unique_amount)
-->

**Example Request:**

```bash
curl -X POST https://api.richpayment.co/api/v1/deposits \
  -H "Content-Type: application/json" \
  -H "X-API-Key: rpay_live_abc123def456" \
  -H "X-Timestamp: 1700000000" \
  -H "X-Signature: a1b2c3d4e5f6..." \
  -d '{
    "merchant_order_id": "ORD-20240101-001",
    "customer_name": "สมชาย ใจดี",
    "customer_bank_code": "KBANK",
    "amount": "1000.00",
    "currency": "THB",
    "callback_url": "https://yoursite.com/webhook/richpayment"
  }'
```

**Success Response (201 Created):**

```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "merchant_order_id": "ORD-20240101-001",
  "amount": "1000.25",
  "currency": "THB",
  "status": "pending",
  "qr_payload": "00020101021229370016A000000677010111...",
  "expires_at": "2024-01-01T00:05:00Z",
  "created_at": "2024-01-01T00:00:00Z"
}
```

<!-- หมายเหตุ:
     - amount อาจต่างจากที่ส่งมา เพราะระบบปรับยอดให้ไม่ซ้ำ (adjusted_amount)
     - qr_payload: ใช้สร้าง QR Code ให้ลูกค้าสแกน
     - expires_at: หมดอายุตามค่า deposit_timeout_sec ของ merchant (default 5 นาที)
-->

**Response Fields:**

| Field               | Type   | Description                                           |
|---------------------|--------|-------------------------------------------------------|
| `id`                | string | Platform-generated order UUID                         |
| `merchant_order_id` | string | Your reference echoed back                            |
| `amount`            | string | Actual amount (may be adjusted for uniqueness)        |
| `currency`          | string | Currency code                                         |
| `status`            | string | Order status: `pending`                               |
| `qr_payload`        | string | PromptPay QR payload for customer to scan             |
| `expires_at`        | string | ISO 8601 timestamp when the order expires             |
| `created_at`        | string | ISO 8601 timestamp when the order was created         |

**Error Responses:**

| HTTP Status | Code             | Description                          |
|-------------|------------------|--------------------------------------|
| 400         | `INVALID_BODY`   | Request body is not valid JSON       |
| 400         | `MISSING_FIELD`  | `merchant_order_id` is missing       |
| 400         | `INVALID_AMOUNT` | Amount is zero or negative           |
| 502         | `UPSTREAM_ERROR` | Internal order-service is unreachable|

---

### Check Deposit Status

<!-- ============================================================
     ตรวจสอบสถานะการฝากเงิน

     ใช้ ID ที่ได้จาก Create Deposit เพื่อตรวจสอบสถานะปัจจุบัน
     ============================================================ -->

Retrieves the current status of a deposit order.

**Endpoint:** `GET /api/v1/deposits/{id}`

**Path Parameters:**

| Parameter | Type   | Description                         |
|-----------|--------|-------------------------------------|
| `id`      | string | Deposit order UUID (from create)    |

<!-- id ต้องเป็น UUID format ที่ถูกต้อง -->

**Example Request:**

```bash
curl -X GET https://api.richpayment.co/api/v1/deposits/550e8400-e29b-41d4-a716-446655440000 \
  -H "X-API-Key: rpay_live_abc123def456" \
  -H "X-Timestamp: 1700000000" \
  -H "X-Signature: a1b2c3d4e5f6..."
```

**Success Response (200 OK):**

```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "merchant_order_id": "ORD-20240101-001",
  "amount": "1000.25",
  "currency": "THB",
  "status": "completed",
  "qr_payload": "00020101021229370016A000000677010111...",
  "expires_at": "2024-01-01T00:05:00Z",
  "created_at": "2024-01-01T00:00:00Z"
}
```

**Error Responses:**

| HTTP Status | Code             | Description                           |
|-------------|------------------|---------------------------------------|
| 400         | `INVALID_ID`     | ID is not a valid UUID format         |
| 502         | `UPSTREAM_ERROR` | Internal order-service is unreachable |

---

### Create Withdrawal

<!-- ============================================================
     สร้างรายการถอนเงิน

     Merchant เรียก endpoint นี้เพื่อถอนเงินจาก wallet ไปยัง
     บัญชีธนาคารปลายทาง ระบบจะ:
     1. ตรวจสอบยอดคงเหลือ
     2. Hold ยอดเงินใน wallet
     3. รอ admin อนุมัติ
     4. โอนเงินจริง
     ============================================================ -->

Creates a new withdrawal request. The system verifies sufficient balance,
places a hold on the funds, and submits the withdrawal for admin approval.

**Endpoint:** `POST /api/v1/withdrawals`

**Request Body:**

| Field                | Type   | Required | Description                                   |
|----------------------|--------|----------|-----------------------------------------------|
| `merchant_order_id`  | string | Yes      | Your unique order reference                   |
| `amount`             | string | Yes      | Withdrawal amount (must be > 0)               |
| `currency`           | string | No       | ISO 4217 currency code (default: `THB`)       |
| `beneficiary_name`   | string | No       | Recipient's name (ชื่อผู้รับ)                  |
| `beneficiary_account`| string | Yes      | Destination bank account number               |
| `beneficiary_bank`   | string | No       | Destination bank code (e.g. `KBANK`, `SCB`)   |
| `callback_url`       | string | No       | Webhook URL for status updates                |

<!-- หมายเหตุ:
     - beneficiary_account เป็นฟิลด์บังคับ ต้องระบุเลขบัญชีปลายทาง
     - ระบบจะตรวจสอบ daily_withdrawal_limit ของ merchant ด้วย
     - withdrawal ต้องรอ admin approve ก่อนจึงจะโอนเงินจริง
-->

**Example Request:**

```bash
curl -X POST https://api.richpayment.co/api/v1/withdrawals \
  -H "Content-Type: application/json" \
  -H "X-API-Key: rpay_live_abc123def456" \
  -H "X-Timestamp: 1700000000" \
  -H "X-Signature: a1b2c3d4e5f6..." \
  -d '{
    "merchant_order_id": "WD-20240101-001",
    "amount": "5000.00",
    "currency": "THB",
    "beneficiary_name": "สมหญิง รักไทย",
    "beneficiary_account": "1234567890",
    "beneficiary_bank": "KBANK",
    "callback_url": "https://yoursite.com/webhook/withdrawal"
  }'
```

**Success Response (201 Created):**

```json
{
  "id": "660e8400-e29b-41d4-a716-446655440000",
  "merchant_order_id": "WD-20240101-001",
  "amount": "5000.00",
  "currency": "THB",
  "status": "pending",
  "created_at": "2024-01-01T00:00:00Z"
}
```

<!-- หมายเหตุ:
     - status เริ่มต้นเป็น "pending" เสมอ
     - ยอดเงินจะถูก hold ใน wallet ทันที (ไม่สามารถใช้ยอดนี้ทำรายการอื่นได้)
     - ต้องรอ admin approve ก่อน ถึงจะเปลี่ยนเป็น "approved" -> "completed"
-->

**Response Fields:**

| Field               | Type   | Description                                |
|---------------------|--------|--------------------------------------------|
| `id`                | string | Platform-generated withdrawal UUID         |
| `merchant_order_id` | string | Your reference echoed back                 |
| `amount`            | string | Withdrawal amount                          |
| `currency`          | string | Currency code                              |
| `status`            | string | Initial status: `pending`                  |
| `created_at`        | string | ISO 8601 creation timestamp                |

**Error Responses:**

| HTTP Status | Code             | Description                              |
|-------------|------------------|------------------------------------------|
| 400         | `INVALID_BODY`   | Request body is not valid JSON           |
| 400         | `MISSING_FIELD`  | Required field is missing                |
| 400         | `INVALID_AMOUNT` | Amount is zero or negative               |
| 502         | `UPSTREAM_ERROR` | Internal withdrawal-service unreachable  |

---

### Check Withdrawal Status

<!-- ตรวจสอบสถานะการถอนเงิน -->

Retrieves the current status of a withdrawal.

**Endpoint:** `GET /api/v1/withdrawals/{id}`

**Path Parameters:**

| Parameter | Type   | Description                        |
|-----------|--------|------------------------------------|
| `id`      | string | Withdrawal UUID (from create)      |

**Example Request:**

```bash
curl -X GET https://api.richpayment.co/api/v1/withdrawals/660e8400-e29b-41d4-a716-446655440000 \
  -H "X-API-Key: rpay_live_abc123def456" \
  -H "X-Timestamp: 1700000000" \
  -H "X-Signature: a1b2c3d4e5f6..."
```

**Success Response (200 OK):**

```json
{
  "id": "660e8400-e29b-41d4-a716-446655440000",
  "merchant_order_id": "WD-20240101-001",
  "amount": "5000.00",
  "fee_amount": "50.00",
  "net_amount": "4950.00",
  "currency": "THB",
  "status": "completed",
  "dest_type": "bank",
  "dest_details": "{\"bank\":\"KBANK\",\"account\":\"1234567890\",\"name\":\"สมหญิง รักไทย\"}",
  "transfer_ref": "KBANK-TXN-20240101-123456",
  "completed_at": "2024-01-01T01:30:00Z",
  "created_at": "2024-01-01T00:00:00Z"
}
```

<!-- หมายเหตุ:
     - fee_amount: ค่าธรรมเนียมถอนเงิน คำนวณจาก withdraw_fee_pct ของ merchant
     - net_amount: จำนวนเงินที่โอนจริง = amount - fee_amount
     - transfer_ref: เลขอ้างอิงจากธนาคาร จะมีเมื่อ status = completed
-->

**Error Responses:**

| HTTP Status | Code             | Description                              |
|-------------|------------------|------------------------------------------|
| 400         | `INVALID_ID`     | ID is not a valid UUID format            |
| 502         | `UPSTREAM_ERROR` | Internal withdrawal-service unreachable  |

---

### Get Wallet Balance

<!-- ============================================================
     สอบถามยอดคงเหลือใน Wallet

     แสดงยอดเงินคงเหลือทั้งหมด, ยอดที่ถูก hold, และยอดที่ใช้ได้
     ============================================================ -->

Returns the merchant's current wallet balance, including held amounts
and available balance.

**Endpoint:** `GET /api/v1/wallet/balance`

**Query Parameters:**

| Parameter  | Type   | Required | Default | Description                    |
|------------|--------|----------|---------|--------------------------------|
| `currency` | string | No       | `THB`   | ISO 4217 currency code         |

**Example Request:**

```bash
curl -X GET "https://api.richpayment.co/api/v1/wallet/balance?currency=THB" \
  -H "X-API-Key: rpay_live_abc123def456" \
  -H "X-Timestamp: 1700000000" \
  -H "X-Signature: a1b2c3d4e5f6..."
```

**Success Response (200 OK):**

```json
{
  "currency": "THB",
  "balance": "150000.50",
  "hold_balance": "5000.00",
  "available": "145000.50"
}
```

**Response Fields:**

| Field          | Type   | Description                                          |
|----------------|--------|------------------------------------------------------|
| `currency`     | string | Currency code                                        |
| `balance`      | string | Total balance including held funds (ยอดรวม)           |
| `hold_balance` | string | Funds reserved for pending withdrawals (ยอดที่ถูก hold)|
| `available`    | string | Spendable balance: `balance - hold_balance` (ยอดที่ใช้ได้)|

<!-- หมายเหตุ:
     - balance = ยอดรวมทั้งหมด
     - hold_balance = ยอดที่ถูก hold จาก withdrawal ที่ pending อยู่
     - available = ยอดที่สามารถถอนได้ = balance - hold_balance
-->

**Error Responses:**

| HTTP Status | Code             | Description                           |
|-------------|------------------|---------------------------------------|
| 502         | `UPSTREAM_ERROR` | Internal wallet-service is unreachable|

---

### Health Check

<!-- Health Check - ตรวจสอบสถานะของ gateway service -->

A public endpoint (no authentication required) for monitoring.

**Endpoint:** `GET /healthz`

**Response (200 OK):**

```json
{
  "success": true,
  "data": {
    "status": "ok",
    "service": "gateway-api"
  }
}
```

---

## Webhook Notifications

<!-- ============================================================
     การแจ้งเตือนผ่าน Webhook

     ระบบจะส่ง HTTP POST ไปยัง callback_url ของ merchant
     เมื่อสถานะของ deposit/withdrawal เปลี่ยน

     ดูรายละเอียดเพิ่มเติมที่ webhook.md
     ============================================================ -->

When a deposit or withdrawal changes status, the system sends a signed
HTTP POST to your configured webhook URL.

### Webhook Headers

| Header                 | Description                                    |
|------------------------|------------------------------------------------|
| `Content-Type`         | `application/json`                             |
| `X-Webhook-Signature`  | HMAC-SHA256 signature of the payload           |
| `X-Webhook-Timestamp`  | Unix timestamp when the webhook was signed     |
| `X-Webhook-ID`         | Unique UUID for this delivery attempt          |

### Signature Verification

<!-- การตรวจสอบ webhook signature -->

The signature is computed as:

```
signature = HMAC-SHA256(webhook_secret, "{timestamp}.{body}")
```

Where:
- `webhook_secret` is your merchant's webhook secret
- `timestamp` is the value of `X-Webhook-Timestamp`
- `body` is the raw request body

**Always verify the signature before processing the webhook.**

<!-- ต้องตรวจสอบ signature ทุกครั้งก่อนประมวลผล webhook -->

### Retry Policy

<!-- นโยบายการ retry webhook -->

| Attempt | Delay After Failure | Total Elapsed |
|---------|---------------------|---------------|
| 1       | Immediate           | 0s            |
| 2       | 10 seconds          | 10s           |
| 3       | 30 seconds          | 40s           |
| 4       | 90 seconds          | ~2 minutes    |
| 5       | 270 seconds         | ~7 minutes    |

- **Max attempts:** 5
- **Timeout per attempt:** 10 seconds
- **Success criteria:** HTTP 2xx response from your server
- After 5 failed attempts, the webhook is marked as exhausted and an admin
  alert is triggered

<!-- หากส่ง webhook ไม่สำเร็จทั้ง 5 ครั้ง ระบบจะแจ้ง admin ผ่าน Telegram -->

> For full webhook documentation with code examples, see **[webhook.md](webhook.md)**.

---

## Error Codes

<!-- ============================================================
     รหัสข้อผิดพลาดทั้งหมด

     ดูรายละเอียดเพิ่มเติมที่ errors.md
     ============================================================ -->

### Authentication Errors

| Code                | HTTP Status | Description                                          |
|---------------------|-------------|------------------------------------------------------|
| `MISSING_API_KEY`   | 401         | `X-API-Key` header not provided                      |
| `MISSING_TIMESTAMP` | 401         | `X-Timestamp` header not provided                    |
| `MISSING_SIGNATURE` | 401         | `X-Signature` header not provided                    |
| `INVALID_TIMESTAMP` | 401         | `X-Timestamp` is not a valid Unix timestamp          |
| `EXPIRED_TIMESTAMP` | 401         | Timestamp is more than 5 minutes from server time    |

### Rate Limiting Errors

| Code           | HTTP Status | Description                                         |
|----------------|-------------|-----------------------------------------------------|
| `RATE_LIMITED`  | 429         | Exceeded 100 requests per minute                   |

### System Errors

| Code             | HTTP Status | Description                                        |
|------------------|-------------|----------------------------------------------------|
| `SYSTEM_FROZEN`  | 503         | System is in emergency freeze mode                 |
| `UPSTREAM_ERROR` | 502         | An internal service is unreachable                 |

### Validation Errors

| Code             | HTTP Status | Description                                        |
|------------------|-------------|----------------------------------------------------|
| `INVALID_BODY`   | 400         | Request body is not valid JSON                     |
| `MISSING_FIELD`  | 400         | A required field is missing                        |
| `INVALID_AMOUNT` | 400         | Amount is zero or negative                         |
| `INVALID_ID`     | 400         | ID parameter is not a valid UUID                   |

### IP Whitelist Errors

| Code             | HTTP Status | Description                                        |
|------------------|-------------|----------------------------------------------------|
| `INVALID_IP`     | 403         | Cannot determine client IP address                 |
| `IP_NOT_ALLOWED` | 403         | Client IP is not in the merchant's whitelist       |

> For the complete error reference, see **[errors.md](errors.md)**.

---

## Order Status Lifecycle

<!-- ============================================================
     วงจรชีวิตของสถานะ Order

     Deposit และ Withdrawal มีสถานะต่างกัน แต่ทั้งคู่เริ่มจาก pending
     ============================================================ -->

### Deposit Order Statuses

<!-- สถานะของรายการฝากเงิน -->

```
pending -> matched -> completed    (สำเร็จ - เงินเข้า wallet แล้ว)
pending -> expired                 (หมดอายุ - ลูกค้าไม่โอนภายในเวลาที่กำหนด)
pending -> cancelled               (ยกเลิก - โดย merchant หรือ admin)
matched -> failed                  (ล้มเหลว - เกิดข้อผิดพลาดหลังจับคู่)
```

| Status      | Description                                                        |
|-------------|--------------------------------------------------------------------|
| `pending`   | Waiting for customer to transfer funds                             |
| `matched`   | Incoming bank transaction matched (SMS, email, or slip)            |
| `completed` | Settlement complete, wallet credited (เงินเข้า wallet แล้ว)         |
| `expired`   | No matching transaction found before the deadline                  |
| `failed`    | Processing error after matching (ต้องให้ admin ตรวจสอบ)             |
| `cancelled` | Order cancelled before matching                                    |

### Deposit Matching Methods

<!-- วิธีการจับคู่เงินฝาก -->

| Method  | Description                                      |
|---------|--------------------------------------------------|
| `sms`   | Matched via incoming bank SMS notification       |
| `email` | Matched via incoming bank email notification     |
| `slip`  | Matched via uploaded bank transfer slip (image)  |

### Withdrawal Statuses

<!-- สถานะของรายการถอนเงิน -->

```
pending -> approved -> completed   (สำเร็จ - โอนเงินแล้ว)
pending -> rejected                (ถูกปฏิเสธ - admin ไม่อนุมัติ เงินคืน wallet)
approved -> completed              (สำเร็จ - โอนเงินจริงแล้ว)
approved -> failed                 (ล้มเหลว - โอนเงินไม่สำเร็จ เงินคืน wallet)
```

| Status      | Description                                                        |
|-------------|--------------------------------------------------------------------|
| `pending`   | Created, balance held, awaiting admin approval                     |
| `approved`  | Admin approved, awaiting bank transfer execution                   |
| `rejected`  | Admin rejected, held balance released back (เงินคืน wallet)        |
| `completed` | Bank transfer confirmed (โอนเงินสำเร็จ)                            |
| `failed`    | Bank transfer failed, held balance released (เงินคืน wallet)       |

---

## Supported Banks

<!-- ============================================================
     ธนาคารที่รองรับ

     รายการธนาคารที่สามารถใช้กับระบบ RichPayment
     ============================================================ -->

| Bank Code | Bank Name (English)         | Bank Name (Thai)            |
|-----------|-----------------------------|-----------------------------|
| `KBANK`   | Kasikorn Bank               | ธนาคารกสิกรไทย               |
| `SCB`     | Siam Commercial Bank        | ธนาคารไทยพาณิชย์              |
| `BBL`     | Bangkok Bank                | ธนาคารกรุงเทพ                |
| `KTB`     | Krungthai Bank              | ธนาคารกรุงไทย                |
| `BAY`     | Bank of Ayudhya (Krungsri)  | ธนาคารกรุงศรีอยุธยา           |
| `TMB`     | TMBThanachart Bank (ttb)    | ธนาคารทหารไทยธนชาต           |
| `GSB`     | Government Savings Bank     | ธนาคารออมสิน                 |
| `BAAC`    | Bank for Agriculture (BAAC) | ธนาคาร ธ.ก.ส.               |

> Bank codes are used in `customer_bank_code` (deposits) and
> `beneficiary_bank` (withdrawals).

<!-- รหัสธนาคารใช้ในฟิลด์ customer_bank_code (ฝาก) และ beneficiary_bank (ถอน) -->

---

## Fee Structure

<!-- ============================================================
     โครงสร้างค่าธรรมเนียม

     ค่าธรรมเนียมถูกกำหนดต่อ merchant โดย admin
     Commission จะถูกแบ่งตามลำดับ: System -> Agent -> Partner
     ============================================================ -->

Fees are configured per merchant by the admin:

| Fee Type             | Field               | Description                               |
|----------------------|---------------------|-------------------------------------------|
| Deposit fee          | `deposit_fee_pct`   | % of deposit amount (e.g. `0.03` = 3%)   |
| Withdrawal fee (THB) | `withdrawal_fee_pct`| % of withdrawal amount                   |

### Fee Calculation Example

<!-- ตัวอย่างการคำนวณค่าธรรมเนียม -->

For a deposit of 10,000 THB with `deposit_fee_pct = 0.03` (3%):

```
Fee      = 10,000 x 0.03 = 300 THB
Net      = 10,000 - 300   = 9,700 THB (credited to merchant wallet)
```

<!-- ค่าธรรมเนียม = 300 บาท, ยอดสุทธิ = 9,700 บาท (เข้า wallet ของ merchant) -->

The fee is then split among the commission hierarchy:
- **System** retains the remainder after agent and partner shares
- **Agent** receives their configured `commission_pct` of the fee
- **Partner** receives their configured `commission_pct` of the fee

---

*Last updated: 2026-04-10*
*RichPayment API v1*
