# RichPayment Webhook Documentation

<!-- =======================================================================
     RichPayment Webhook Delivery Reference

     This document covers the complete webhook notification system, including
     payload format, signature verification, retry policy, and best practices.

     NOTE (หมายเหตุ):
     - ภาษาหลักของเอกสารนี้เป็นภาษาอังกฤษ แต่มีหมายเหตุภาษาไทยเพิ่มเติม
       เพื่อช่วยเหลือ Merchant ไทยในการเข้าใจรายละเอียดที่สำคัญ
     - เอกสารนี้อ้างอิงจาก source code โดยตรง (notification service webhook.go)
     - Webhook ใช้ HMAC-SHA256 ในการลงลายเซ็นทุก payload เพื่อความปลอดภัย
     ======================================================================= -->

## Table of Contents

<!-- สารบัญ: ลิงก์ไปยังแต่ละส่วนของเอกสาร -->

1. [Overview](#overview)
2. [Webhook Headers](#webhook-headers)
3. [Payload Format](#payload-format)
   - [Deposit Events](#deposit-events)
   - [Withdrawal Events](#withdrawal-events)
4. [Signature Verification](#signature-verification)
   - [Go](#go)
   - [Python](#python)
   - [Node.js](#nodejs)
   - [PHP](#php)
5. [Retry Policy](#retry-policy)
6. [Best Practices](#best-practices)
7. [Testing Webhooks](#testing-webhooks)

---

## Overview

<!-- ============================================================
     ภาพรวมของระบบ Webhook

     เมื่อสถานะของ deposit หรือ withdrawal เปลี่ยน ระบบจะส่ง
     HTTP POST ไปยัง webhook_url ที่ merchant กำหนดไว้
     โดยทุก request จะมีลายเซ็น HMAC-SHA256 เพื่อให้ merchant
     ตรวจสอบความถูกต้องของ payload ได้
     ============================================================ -->

When a deposit or withdrawal transaction changes status, the RichPayment
notification service sends a signed HTTP POST request to your configured
webhook URL. This allows your system to react to payment events in real time
without polling the API.

**Key characteristics:**

- **Delivery method:** HTTP POST with JSON body
  <!-- วิธีส่ง: HTTP POST พร้อม JSON body -->
- **Signing algorithm:** HMAC-SHA256 on every payload
  <!-- ใช้ HMAC-SHA256 ลงลายเซ็นทุก payload -->
- **Retry on failure:** Up to 5 attempts with exponential backoff
  <!-- retry สูงสุด 5 ครั้ง พร้อม exponential backoff -->
- **Timeout per attempt:** 10 seconds
  <!-- timeout ต่อครั้ง 10 วินาที -->
- **Success criteria:** Your server must respond with HTTP 2xx
  <!-- ต้องตอบกลับด้วย HTTP 2xx ถึงจะถือว่าสำเร็จ -->

### Webhook Flow

<!-- ขั้นตอนการทำงานของ webhook -->

```
1. Transaction status changes (e.g. deposit matched -> completed)
   สถานะรายการเปลี่ยน (เช่น deposit matched -> completed)

2. Notification service builds signed JSON payload
   ระบบสร้าง JSON payload พร้อมลายเซ็น

3. HTTP POST to your webhook_url with signature headers
   ส่ง HTTP POST ไปยัง webhook_url พร้อม headers ลายเซ็น

4. Your server verifies signature and processes the event
   Server ของคุณตรวจสอบลายเซ็นและประมวลผล event

5. Respond with HTTP 2xx to acknowledge receipt
   ตอบกลับด้วย HTTP 2xx เพื่อยืนยันการรับ

6. If non-2xx or timeout: system retries with exponential backoff
   หากไม่ใช่ 2xx หรือ timeout: ระบบจะ retry ตาม exponential backoff
```

---

## Webhook Headers

<!-- ============================================================
     Headers ที่ส่งมาพร้อมกับทุก Webhook Request

     ทุก webhook request จะมี headers เหล่านี้เสมอ
     Merchant ต้องใช้ X-Webhook-Signature และ X-Webhook-Timestamp
     ในการตรวจสอบความถูกต้อง
     ============================================================ -->

Every webhook delivery includes the following HTTP headers:

| Header                 | Description                                                   |
|------------------------|---------------------------------------------------------------|
| `Content-Type`         | Always `application/json`                                     |
| `X-Webhook-Signature`  | HMAC-SHA256 hex digest of the payload (ลายเซ็น HMAC-SHA256)   |
| `X-Webhook-Timestamp`  | Unix timestamp (seconds) when the signature was created       |
| `X-Webhook-ID`         | Unique UUID for this specific delivery attempt (ใช้ track แต่ละครั้ง) |

<!-- หมายเหตุ:
     - X-Webhook-ID: เปลี่ยนทุกครั้งที่ retry แม้จะเป็น event เดียวกัน
       ใช้สำหรับ debug/log ว่าเป็นการส่งครั้งที่เท่าไหร่
     - X-Webhook-Timestamp: ใช้ร่วมกับ signature ในการตรวจสอบ
       ควรตรวจว่า timestamp ไม่เก่าเกินไป เพื่อป้องกัน replay attack
     - X-Webhook-Signature: ต้องตรวจสอบทุกครั้งก่อนประมวลผล payload
-->

---

## Payload Format

<!-- ============================================================
     รูปแบบ Payload ของ Webhook

     Payload แบ่งเป็น 2 ประเภทหลัก:
     1. Deposit events - เกี่ยวกับรายการฝากเงิน
     2. Withdrawal events - เกี่ยวกับรายการถอนเงิน

     ทุก payload มีฟิลด์ร่วมกัน:
     - event: ชนิดของ event
     - timestamp: เวลาที่ event เกิด
     - data: ข้อมูลรายละเอียดของ event
     ============================================================ -->

All webhook payloads share a common envelope structure:

```json
{
  "event": "event_type",
  "timestamp": "2024-01-01T00:00:00Z",
  "data": { ... }
}
```

**Common Fields:**

| Field       | Type   | Description                                              |
|-------------|--------|----------------------------------------------------------|
| `event`     | string | Event type identifier (ชนิดของ event)                     |
| `timestamp` | string | ISO 8601 timestamp when the event occurred               |
| `data`      | object | Event-specific payload data (ข้อมูลเฉพาะของแต่ละ event)  |

---

### Deposit Events

<!-- ============================================================
     Events ที่เกี่ยวกับรายการฝากเงิน

     Deposit มีหลายสถานะ: pending -> matched -> completed
                           pending -> expired
                           pending -> cancelled
                           matched -> failed

     ระบบจะส่ง webhook ทุกครั้งที่สถานะเปลี่ยน
     ============================================================ -->

#### `deposit.matched`

<!-- เงินฝากถูกจับคู่สำเร็จ: ระบบตรวจพบเงินเข้าที่ตรงกับ order -->

Sent when an incoming bank transaction matches a pending deposit order.
The system detected a bank transfer (via SMS, email, or slip upload) that
corresponds to this deposit order.

<!-- ส่งเมื่อระบบจับคู่เงินเข้ากับ deposit order สำเร็จ (ผ่าน SMS, email, หรือ slip) -->

```json
{
  "event": "deposit.matched",
  "timestamp": "2024-01-01T00:02:30Z",
  "data": {
    "id": "550e8400-e29b-41d4-a716-446655440000",
    "merchant_order_id": "ORD-20240101-001",
    "amount": "1000.25",
    "currency": "THB",
    "status": "matched",
    "match_method": "sms",
    "matched_at": "2024-01-01T00:02:30Z",
    "created_at": "2024-01-01T00:00:00Z"
  }
}
```

<!-- หมายเหตุ:
     - match_method: วิธีที่ใช้จับคู่ ("sms", "email", หรือ "slip")
     - amount: อาจต่างจากที่ merchant ส่งมา เพราะระบบอาจปรับยอดให้ไม่ซ้ำ
     - matched_at: เวลาที่จับคู่สำเร็จ
-->

**Data Fields:**

| Field               | Type   | Description                                           |
|---------------------|--------|-------------------------------------------------------|
| `id`                | string | Platform-generated deposit order UUID                 |
| `merchant_order_id` | string | Your reference ID echoed back                         |
| `amount`            | string | Deposit amount (may differ from requested amount)     |
| `currency`          | string | ISO 4217 currency code (e.g. `THB`)                  |
| `status`            | string | `matched`                                             |
| `match_method`      | string | How the deposit was matched: `sms`, `email`, or `slip`|
| `matched_at`        | string | ISO 8601 timestamp of the match                      |
| `created_at`        | string | ISO 8601 timestamp when order was created             |

---

#### `deposit.completed`

<!-- เงินฝากสำเร็จ: settlement เสร็จสิ้น เงินเข้า wallet ของ merchant แล้ว -->

Sent when the deposit has been fully settled and the merchant's wallet has
been credited. This is the final success state.

<!-- ส่งเมื่อ settlement เสร็จสิ้น เงินเข้า wallet ของ merchant แล้ว
     นี่คือสถานะสุดท้ายที่แสดงว่าฝากเงินสำเร็จ -->

```json
{
  "event": "deposit.completed",
  "timestamp": "2024-01-01T00:03:00Z",
  "data": {
    "id": "550e8400-e29b-41d4-a716-446655440000",
    "merchant_order_id": "ORD-20240101-001",
    "amount": "1000.25",
    "fee_amount": "30.01",
    "net_amount": "970.24",
    "currency": "THB",
    "status": "completed",
    "match_method": "sms",
    "completed_at": "2024-01-01T00:03:00Z",
    "created_at": "2024-01-01T00:00:00Z"
  }
}
```

<!-- หมายเหตุ:
     - fee_amount: ค่าธรรมเนียม คำนวณจาก deposit_fee_pct ของ merchant
     - net_amount: ยอดที่เข้า wallet จริง = amount - fee_amount
     - สำคัญ: ใช้ net_amount เป็นยอดเงินที่ merchant ได้รับจริง
-->

**Data Fields:**

| Field               | Type   | Description                                           |
|---------------------|--------|-------------------------------------------------------|
| `id`                | string | Platform-generated deposit order UUID                 |
| `merchant_order_id` | string | Your reference ID echoed back                         |
| `amount`            | string | Total deposit amount                                  |
| `fee_amount`        | string | Platform fee deducted (ค่าธรรมเนียม)                   |
| `net_amount`        | string | Amount credited to wallet: amount - fee (ยอดสุทธิ)     |
| `currency`          | string | ISO 4217 currency code                                |
| `status`            | string | `completed`                                           |
| `match_method`      | string | How the deposit was matched: `sms`, `email`, or `slip`|
| `completed_at`      | string | ISO 8601 timestamp of completion                      |
| `created_at`        | string | ISO 8601 timestamp when order was created             |

---

#### `deposit.expired`

<!-- Deposit หมดอายุ: ลูกค้าไม่โอนเงินภายในเวลาที่กำหนด -->

Sent when a deposit order expires because no matching bank transfer was
detected before the deadline.

<!-- ส่งเมื่อ deposit order หมดอายุ เพราะไม่พบเงินโอนเข้าที่ตรงกัน
     ภายในเวลาที่กำหนด (ค่า default คือ 5 นาที ตาม deposit_timeout_sec) -->

```json
{
  "event": "deposit.expired",
  "timestamp": "2024-01-01T00:05:00Z",
  "data": {
    "id": "550e8400-e29b-41d4-a716-446655440000",
    "merchant_order_id": "ORD-20240101-001",
    "amount": "1000.25",
    "currency": "THB",
    "status": "expired",
    "expired_at": "2024-01-01T00:05:00Z",
    "created_at": "2024-01-01T00:00:00Z"
  }
}
```

<!-- หมายเหตุ:
     - expired_at: เวลาที่ order หมดอายุ
     - Merchant สามารถสร้าง deposit order ใหม่ได้ หากลูกค้ายังต้องการจ่าย
     - ระยะเวลา timeout ตั้งค่าได้ต่อ merchant (deposit_timeout_sec)
-->

**Data Fields:**

| Field               | Type   | Description                                           |
|---------------------|--------|-------------------------------------------------------|
| `id`                | string | Platform-generated deposit order UUID                 |
| `merchant_order_id` | string | Your reference ID echoed back                         |
| `amount`            | string | Requested deposit amount                              |
| `currency`          | string | ISO 4217 currency code                                |
| `status`            | string | `expired`                                             |
| `expired_at`        | string | ISO 8601 timestamp when the order expired             |
| `created_at`        | string | ISO 8601 timestamp when order was created             |

---

#### `deposit.failed`

<!-- Deposit ล้มเหลว: เกิดข้อผิดพลาดหลังจากจับคู่เงินเข้าสำเร็จ -->

Sent when a deposit fails after being matched. This is a rare error condition
that requires admin investigation.

<!-- ส่งเมื่อ deposit ล้มเหลวหลังจากจับคู่เงินเข้าสำเร็จ
     เหตุการณ์นี้เกิดขึ้นน้อย ต้องให้ admin ตรวจสอบ -->

```json
{
  "event": "deposit.failed",
  "timestamp": "2024-01-01T00:03:00Z",
  "data": {
    "id": "550e8400-e29b-41d4-a716-446655440000",
    "merchant_order_id": "ORD-20240101-001",
    "amount": "1000.25",
    "currency": "THB",
    "status": "failed",
    "reason": "settlement processing error",
    "failed_at": "2024-01-01T00:03:00Z",
    "created_at": "2024-01-01T00:00:00Z"
  }
}
```

<!-- หมายเหตุ:
     - reason: สาเหตุของความล้มเหลว
     - สถานะนี้ต้องให้ admin เข้ามาตรวจสอบและแก้ไข
     - Merchant ไม่ต้องทำอะไร ให้รอ admin ดำเนินการ
-->

**Data Fields:**

| Field               | Type   | Description                                           |
|---------------------|--------|-------------------------------------------------------|
| `id`                | string | Platform-generated deposit order UUID                 |
| `merchant_order_id` | string | Your reference ID echoed back                         |
| `amount`            | string | Deposit amount                                        |
| `currency`          | string | ISO 4217 currency code                                |
| `status`            | string | `failed`                                              |
| `reason`            | string | Human-readable failure reason (สาเหตุที่ล้มเหลว)      |
| `failed_at`         | string | ISO 8601 timestamp of the failure                     |
| `created_at`        | string | ISO 8601 timestamp when order was created             |

---

#### `deposit.cancelled`

<!-- Deposit ถูกยกเลิก: โดย merchant หรือ admin ก่อนที่จะมีการจับคู่เงินเข้า -->

Sent when a deposit order is cancelled before any matching bank transfer is
found. Cancellation can be initiated by the merchant or by an admin.

<!-- ส่งเมื่อ deposit order ถูกยกเลิกก่อนจับคู่เงินเข้า
     การยกเลิกอาจมาจาก merchant เอง หรือจาก admin -->

```json
{
  "event": "deposit.cancelled",
  "timestamp": "2024-01-01T00:01:30Z",
  "data": {
    "id": "550e8400-e29b-41d4-a716-446655440000",
    "merchant_order_id": "ORD-20240101-001",
    "amount": "1000.25",
    "currency": "THB",
    "status": "cancelled",
    "cancelled_at": "2024-01-01T00:01:30Z",
    "created_at": "2024-01-01T00:00:00Z"
  }
}
```

**Data Fields:**

| Field               | Type   | Description                                           |
|---------------------|--------|-------------------------------------------------------|
| `id`                | string | Platform-generated deposit order UUID                 |
| `merchant_order_id` | string | Your reference ID echoed back                         |
| `amount`            | string | Requested deposit amount                              |
| `currency`          | string | ISO 4217 currency code                                |
| `status`            | string | `cancelled`                                           |
| `cancelled_at`      | string | ISO 8601 timestamp of cancellation                    |
| `created_at`        | string | ISO 8601 timestamp when order was created             |

---

### Withdrawal Events

<!-- ============================================================
     Events ที่เกี่ยวกับรายการถอนเงิน

     Withdrawal มีหลายสถานะ:
     pending -> approved -> completed (สำเร็จ)
     pending -> rejected (ปฏิเสธ เงินคืน wallet)
     approved -> failed (ล้มเหลว เงินคืน wallet)

     ระบบจะส่ง webhook ทุกครั้งที่สถานะเปลี่ยน
     ============================================================ -->

#### `withdrawal.approved`

<!-- Withdrawal ได้รับอนุมัติจาก admin พร้อมโอนเงินจริง -->

Sent when an admin approves a pending withdrawal. The bank transfer will
be executed next.

<!-- ส่งเมื่อ admin อนุมัติ withdrawal ที่รอดำเนินการ
     ขั้นตอนถัดไปคือโอนเงินจริงไปยังบัญชีปลายทาง -->

```json
{
  "event": "withdrawal.approved",
  "timestamp": "2024-01-01T01:00:00Z",
  "data": {
    "id": "660e8400-e29b-41d4-a716-446655440000",
    "merchant_order_id": "WD-20240101-001",
    "amount": "5000.00",
    "currency": "THB",
    "status": "approved",
    "approved_at": "2024-01-01T01:00:00Z",
    "created_at": "2024-01-01T00:00:00Z"
  }
}
```

<!-- หมายเหตุ:
     - approved_at: เวลาที่ admin อนุมัติ
     - หลังจากอนุมัติ ระบบจะดำเนินการโอนเงินจริง
     - ยอดเงินยังคงถูก hold ใน wallet จนกว่าจะโอนสำเร็จหรือล้มเหลว
-->

**Data Fields:**

| Field               | Type   | Description                                           |
|---------------------|--------|-------------------------------------------------------|
| `id`                | string | Platform-generated withdrawal UUID                    |
| `merchant_order_id` | string | Your reference ID echoed back                         |
| `amount`            | string | Withdrawal amount                                     |
| `currency`          | string | ISO 4217 currency code                                |
| `status`            | string | `approved`                                            |
| `approved_at`       | string | ISO 8601 timestamp of approval                        |
| `created_at`        | string | ISO 8601 timestamp when withdrawal was created        |

---

#### `withdrawal.completed`

<!-- Withdrawal สำเร็จ: โอนเงินไปยังบัญชีปลายทางเรียบร้อยแล้ว -->

Sent when the bank transfer for a withdrawal is confirmed successful. This
is the final success state.

<!-- ส่งเมื่อการโอนเงินไปยังบัญชีปลายทางสำเร็จ
     นี่คือสถานะสุดท้ายที่แสดงว่าถอนเงินสำเร็จ -->

```json
{
  "event": "withdrawal.completed",
  "timestamp": "2024-01-01T01:30:00Z",
  "data": {
    "id": "660e8400-e29b-41d4-a716-446655440000",
    "merchant_order_id": "WD-20240101-001",
    "amount": "5000.00",
    "fee_amount": "50.00",
    "net_amount": "4950.00",
    "currency": "THB",
    "status": "completed",
    "transfer_ref": "KBANK-TXN-20240101-123456",
    "completed_at": "2024-01-01T01:30:00Z",
    "created_at": "2024-01-01T00:00:00Z"
  }
}
```

<!-- หมายเหตุ:
     - fee_amount: ค่าธรรมเนียมถอนเงิน (คำนวณจาก withdrawal_fee_pct)
     - net_amount: จำนวนเงินที่โอนจริง = amount - fee_amount
     - transfer_ref: เลขอ้างอิงจากธนาคาร ใช้สำหรับตรวจสอบกับธนาคาร
     - สำคัญ: ใช้ transfer_ref เพื่อ reconcile กับ statement ธนาคาร
-->

**Data Fields:**

| Field               | Type   | Description                                                |
|---------------------|--------|------------------------------------------------------------|
| `id`                | string | Platform-generated withdrawal UUID                         |
| `merchant_order_id` | string | Your reference ID echoed back                              |
| `amount`            | string | Total withdrawal amount                                    |
| `fee_amount`        | string | Platform fee deducted (ค่าธรรมเนียมถอนเงิน)                 |
| `net_amount`        | string | Amount actually transferred: amount - fee (ยอดโอนจริง)      |
| `currency`          | string | ISO 4217 currency code                                     |
| `status`            | string | `completed`                                                |
| `transfer_ref`      | string | Bank transfer reference number (เลขอ้างอิงธนาคาร)          |
| `completed_at`      | string | ISO 8601 timestamp of completion                           |
| `created_at`        | string | ISO 8601 timestamp when withdrawal was created             |

---

#### `withdrawal.rejected`

<!-- Withdrawal ถูกปฏิเสธ: admin ไม่อนุมัติ เงินถูกคืนกลับ wallet -->

Sent when an admin rejects a pending withdrawal. The held balance is
released back to the merchant's wallet.

<!-- ส่งเมื่อ admin ปฏิเสธ withdrawal ยอดเงินที่ถูก hold จะคืนกลับ wallet -->

```json
{
  "event": "withdrawal.rejected",
  "timestamp": "2024-01-01T01:00:00Z",
  "data": {
    "id": "660e8400-e29b-41d4-a716-446655440000",
    "merchant_order_id": "WD-20240101-001",
    "amount": "5000.00",
    "currency": "THB",
    "status": "rejected",
    "reason": "suspicious activity detected",
    "rejected_at": "2024-01-01T01:00:00Z",
    "created_at": "2024-01-01T00:00:00Z"
  }
}
```

<!-- หมายเหตุ:
     - reason: สาเหตุที่ admin ปฏิเสธ
     - เงินที่ถูก hold จะคืนกลับ wallet ทันทีเมื่อ status เปลี่ยนเป็น rejected
     - Merchant สามารถสร้าง withdrawal ใหม่ได้หากต้องการ
-->

**Data Fields:**

| Field               | Type   | Description                                           |
|---------------------|--------|-------------------------------------------------------|
| `id`                | string | Platform-generated withdrawal UUID                    |
| `merchant_order_id` | string | Your reference ID echoed back                         |
| `amount`            | string | Withdrawal amount (returned to wallet)                |
| `currency`          | string | ISO 4217 currency code                                |
| `status`            | string | `rejected`                                            |
| `reason`            | string | Rejection reason from admin (สาเหตุที่ปฏิเสธ)          |
| `rejected_at`       | string | ISO 8601 timestamp of rejection                       |
| `created_at`        | string | ISO 8601 timestamp when withdrawal was created        |

---

#### `withdrawal.failed`

<!-- Withdrawal ล้มเหลว: โอนเงินไม่สำเร็จ เงินถูกคืนกลับ wallet -->

Sent when a bank transfer fails after the withdrawal was approved. The held
balance is released back to the merchant's wallet.

<!-- ส่งเมื่อการโอนเงินล้มเหลวหลังจากที่ withdrawal ถูกอนุมัติแล้ว
     เงินที่ถูก hold จะคืนกลับ wallet -->

```json
{
  "event": "withdrawal.failed",
  "timestamp": "2024-01-01T01:30:00Z",
  "data": {
    "id": "660e8400-e29b-41d4-a716-446655440000",
    "merchant_order_id": "WD-20240101-001",
    "amount": "5000.00",
    "currency": "THB",
    "status": "failed",
    "reason": "invalid destination account number",
    "failed_at": "2024-01-01T01:30:00Z",
    "created_at": "2024-01-01T00:00:00Z"
  }
}
```

<!-- หมายเหตุ:
     - reason: สาเหตุที่โอนเงินล้มเหลว (เช่น เลขบัญชีปลายทางไม่ถูกต้อง)
     - เงินที่ถูก hold จะคืนกลับ wallet เมื่อ status เปลี่ยนเป็น failed
     - Merchant ควรตรวจสอบข้อมูลบัญชีปลายทางก่อนสร้าง withdrawal ใหม่
-->

**Data Fields:**

| Field               | Type   | Description                                           |
|---------------------|--------|-------------------------------------------------------|
| `id`                | string | Platform-generated withdrawal UUID                    |
| `merchant_order_id` | string | Your reference ID echoed back                         |
| `amount`            | string | Withdrawal amount (returned to wallet)                |
| `currency`          | string | ISO 4217 currency code                                |
| `status`            | string | `failed`                                              |
| `reason`            | string | Failure reason (สาเหตุที่ล้มเหลว)                      |
| `failed_at`         | string | ISO 8601 timestamp of the failure                     |
| `created_at`        | string | ISO 8601 timestamp when withdrawal was created        |

---

## Signature Verification

<!-- ============================================================
     การตรวจสอบลายเซ็น Webhook (Signature Verification)

     ทุก webhook request จะมีลายเซ็น HMAC-SHA256 ที่คำนวณจาก:
       signature = HMAC-SHA256(webhook_secret, "{timestamp}.{body}")

     โดย:
     - webhook_secret: secret key ของ merchant (ได้รับตอนสร้าง merchant)
     - timestamp: ค่าจาก header X-Webhook-Timestamp
     - body: raw request body (JSON)

     วิธีนี้คล้ายกับ Stripe Webhook Signatures
     ============================================================ -->

**You must verify the webhook signature before processing any payload.**
This prevents attackers from spoofing webhook deliveries.

<!-- ต้องตรวจสอบลายเซ็นทุกครั้งก่อนประมวลผล payload
     เพื่อป้องกันผู้ไม่หวังดีปลอม webhook -->

### Signature Algorithm

The signature is computed as:

```
signature = HMAC-SHA256(webhook_secret, "{timestamp}.{body}")
```

**Components:**

| Component        | Source                           | Description                              |
|------------------|----------------------------------|------------------------------------------|
| `webhook_secret` | Merchant registration            | Your HMAC secret key for webhooks        |
| `timestamp`      | `X-Webhook-Timestamp` header     | Unix epoch seconds when signed           |
| `body`           | Raw HTTP request body            | Complete JSON body (ไม่ต้อง parse ก่อน)   |

<!-- หมายเหตุสำคัญ:
     - ใช้ raw body ตรงๆ ห้าม parse แล้ว re-serialize เพราะจะได้ผลลัพธ์ต่างกัน
     - message ที่ใช้ sign คือ "{timestamp}.{body}" (มีจุดคั่นระหว่าง timestamp กับ body)
     - เปรียบเทียบ signature แบบ constant-time เพื่อป้องกัน timing attack
-->

### Verification Steps

<!-- ขั้นตอนการตรวจสอบลายเซ็น -->

1. Extract `X-Webhook-Signature` and `X-Webhook-Timestamp` from the headers
   <!-- ดึง signature และ timestamp จาก headers -->
2. Read the raw request body (do NOT parse and re-serialize JSON)
   <!-- อ่าน raw body ห้าม parse แล้ว JSON.stringify ใหม่ -->
3. Construct the signing message: `"{timestamp}.{body}"`
   <!-- สร้าง message สำหรับ sign: "{timestamp}.{body}" -->
4. Compute HMAC-SHA256 of the message using your `webhook_secret`
   <!-- คำนวณ HMAC-SHA256 ของ message โดยใช้ webhook_secret -->
5. Compare the computed signature with `X-Webhook-Signature` using constant-time comparison
   <!-- เปรียบเทียบ signature แบบ constant-time -->
6. Optionally verify that the timestamp is recent (within 5 minutes)
   <!-- ตรวจสอบว่า timestamp ไม่เก่าเกิน 5 นาที (แนะนำ) -->

---

### Go

```go
package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"time"
)

// VerifyWebhookSignature validates the HMAC-SHA256 signature of an incoming
// RichPayment webhook request. It reads the raw body and computes the expected
// signature, comparing it in constant time to prevent timing attacks.
//
// ตรวจสอบลายเซ็น HMAC-SHA256 ของ webhook request จาก RichPayment
// อ่าน raw body และคำนวณ signature ที่คาดหวัง เปรียบเทียบแบบ constant-time
//
// Parameters:
//   - r:             the incoming HTTP request from RichPayment
//   - webhookSecret: your merchant's webhook secret key
//
// Returns:
//   - body: the raw request body bytes (use this for JSON unmarshalling)
//   - err:  nil if the signature is valid, an error otherwise
func VerifyWebhookSignature(r *http.Request, webhookSecret string) ([]byte, error) {
	// Extract required headers from the webhook request.
	// ดึง headers ที่จำเป็นจาก webhook request
	signature := r.Header.Get("X-Webhook-Signature")
	timestamp := r.Header.Get("X-Webhook-Timestamp")

	// Validate that both headers are present.
	// ตรวจสอบว่ามี headers ทั้งสองตัว
	if signature == "" || timestamp == "" {
		return nil, fmt.Errorf("missing webhook signature or timestamp header")
	}

	// Parse the timestamp and check that it is recent (within 5 minutes).
	// This prevents replay attacks where an attacker re-sends an old webhook.
	// ตรวจสอบว่า timestamp ไม่เก่าเกิน 5 นาที เพื่อป้องกัน replay attack
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid timestamp format: %w", err)
	}
	if math.Abs(float64(time.Now().Unix()-ts)) > 300 {
		return nil, fmt.Errorf("webhook timestamp too old or too far in the future")
	}

	// Read the raw request body. Do NOT parse and re-serialize the JSON,
	// because whitespace or key ordering changes would break the signature.
	// อ่าน raw body ตรงๆ ห้าม parse แล้ว re-serialize เพราะจะเปลี่ยน signature
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, fmt.Errorf("read request body: %w", err)
	}

	// Construct the canonical signing string: "{timestamp}.{body}".
	// This is the same format used by RichPayment's notification service.
	// สร้าง message สำหรับ sign ในรูปแบบ "{timestamp}.{body}"
	message := timestamp + "." + string(body)

	// Compute the expected HMAC-SHA256 signature.
	// คำนวณ HMAC-SHA256 signature ที่คาดหวัง
	mac := hmac.New(sha256.New, []byte(webhookSecret))
	mac.Write([]byte(message))
	expectedSignature := hex.EncodeToString(mac.Sum(nil))

	// Use constant-time comparison to prevent timing attacks.
	// An attacker could measure response time differences to guess the
	// signature byte-by-byte if we used regular string comparison.
	// ใช้ hmac.Equal เพื่อป้องกัน timing attack
	if !hmac.Equal([]byte(signature), []byte(expectedSignature)) {
		return nil, fmt.Errorf("webhook signature mismatch")
	}

	return body, nil
}

// Example usage in an HTTP handler:
//
// ตัวอย่างการใช้งานใน HTTP handler:
//
//   func webhookHandler(w http.ResponseWriter, r *http.Request) {
//       body, err := VerifyWebhookSignature(r, "whsec_your_secret_here")
//       if err != nil {
//           http.Error(w, "invalid signature", http.StatusUnauthorized)
//           return
//       }
//
//       var event WebhookEvent
//       if err := json.Unmarshal(body, &event); err != nil {
//           http.Error(w, "invalid JSON", http.StatusBadRequest)
//           return
//       }
//
//       // Process the event based on event.Event type.
//       // ประมวลผล event ตามชนิดของ event.Event
//       switch event.Event {
//       case "deposit.completed":
//           // Handle completed deposit
//       case "withdrawal.completed":
//           // Handle completed withdrawal
//       }
//
//       w.WriteHeader(http.StatusOK)
//   }
```

---

### Python

```python
import hmac
import hashlib
import time
import json
from flask import Flask, request, jsonify

app = Flask(__name__)

# Your webhook secret from merchant registration.
# webhook secret ที่ได้รับตอนสร้าง merchant
WEBHOOK_SECRET = "whsec_your_secret_here"

# Maximum age (seconds) for webhook timestamps to prevent replay attacks.
# อายุสูงสุด (วินาที) ของ timestamp เพื่อป้องกัน replay attack
MAX_TIMESTAMP_AGE = 300  # 5 minutes / 5 นาที


def verify_webhook_signature(payload: bytes, signature: str, timestamp: str, secret: str) -> bool:
    """
    Verify the HMAC-SHA256 signature of a RichPayment webhook.

    ตรวจสอบลายเซ็น HMAC-SHA256 ของ webhook จาก RichPayment

    Args:
        payload:   Raw request body bytes (raw body ที่ยังไม่ parse)
        signature: Value of X-Webhook-Signature header
        timestamp: Value of X-Webhook-Timestamp header
        secret:    Your merchant's webhook secret key

    Returns:
        True if the signature is valid, False otherwise.
        True หากลายเซ็นถูกต้อง, False หากไม่ถูกต้อง
    """
    # Check that the timestamp is recent to prevent replay attacks.
    # ตรวจสอบว่า timestamp ไม่เก่าเกิน 5 นาที
    try:
        ts = int(timestamp)
    except ValueError:
        return False

    if abs(time.time() - ts) > MAX_TIMESTAMP_AGE:
        return False

    # Construct the signing message: "{timestamp}.{body}".
    # สร้าง message สำหรับ sign: "{timestamp}.{body}"
    message = f"{timestamp}.{payload.decode('utf-8')}"

    # Compute the expected HMAC-SHA256 signature.
    # คำนวณ HMAC-SHA256 signature ที่คาดหวัง
    expected = hmac.new(
        secret.encode("utf-8"),
        message.encode("utf-8"),
        hashlib.sha256
    ).hexdigest()

    # Use constant-time comparison (hmac.compare_digest) to prevent
    # timing attacks.
    # ใช้ hmac.compare_digest เพื่อป้องกัน timing attack
    return hmac.compare_digest(signature, expected)


@app.route("/webhook/richpayment", methods=["POST"])
def handle_webhook():
    """
    Webhook endpoint for receiving RichPayment notifications.

    Endpoint สำหรับรับ webhook จาก RichPayment
    """
    # Read the raw body before Flask parses it.
    # อ่าน raw body ก่อนที่ Flask จะ parse
    raw_body = request.get_data()

    # Extract signature headers.
    # ดึง headers สำหรับตรวจสอบลายเซ็น
    signature = request.headers.get("X-Webhook-Signature", "")
    timestamp = request.headers.get("X-Webhook-Timestamp", "")
    webhook_id = request.headers.get("X-Webhook-ID", "")

    # Verify the webhook signature before processing.
    # ตรวจสอบลายเซ็นก่อนประมวลผล
    if not verify_webhook_signature(raw_body, signature, timestamp, WEBHOOK_SECRET):
        return jsonify({"error": "invalid signature"}), 401

    # Parse the validated payload.
    # Parse payload ที่ผ่านการตรวจสอบแล้ว
    event = json.loads(raw_body)
    event_type = event.get("event")
    data = event.get("data", {})

    # Process by event type.
    # ประมวลผลตามชนิด event
    if event_type == "deposit.completed":
        # Credit user's account with data["net_amount"].
        # เติมเงินให้ลูกค้าด้วย data["net_amount"]
        print(f"Deposit completed: {data['merchant_order_id']} amount={data['net_amount']}")

    elif event_type == "withdrawal.completed":
        # Mark withdrawal as done in your system.
        # อัปเดตสถานะถอนเงินในระบบของคุณ
        print(f"Withdrawal completed: {data['merchant_order_id']} ref={data['transfer_ref']}")

    elif event_type == "deposit.expired":
        # Handle expired deposit - notify customer or retry.
        # จัดการ deposit หมดอายุ - แจ้งลูกค้าหรือสร้างใหม่
        print(f"Deposit expired: {data['merchant_order_id']}")

    # Always return 200 to acknowledge receipt.
    # ตอบ 200 เสมอ เพื่อยืนยันว่ารับ webhook สำเร็จ
    return jsonify({"status": "ok"}), 200
```

---

### Node.js

```javascript
const crypto = require('crypto');
const express = require('express');

const app = express();

// Your webhook secret from merchant registration.
// webhook secret ที่ได้รับตอนสร้าง merchant
const WEBHOOK_SECRET = 'whsec_your_secret_here';

// Maximum age (seconds) for webhook timestamps to prevent replay attacks.
// อายุสูงสุด (วินาที) ของ timestamp เพื่อป้องกัน replay attack
const MAX_TIMESTAMP_AGE = 300; // 5 minutes / 5 นาที

/**
 * Verify the HMAC-SHA256 signature of a RichPayment webhook.
 *
 * ตรวจสอบลายเซ็น HMAC-SHA256 ของ webhook จาก RichPayment
 *
 * @param {Buffer} payload   - Raw request body
 * @param {string} signature - Value of X-Webhook-Signature header
 * @param {string} timestamp - Value of X-Webhook-Timestamp header
 * @param {string} secret    - Your merchant's webhook secret key
 * @returns {boolean} True if signature is valid
 */
function verifyWebhookSignature(payload, signature, timestamp, secret) {
  // Check timestamp freshness to prevent replay attacks.
  // ตรวจสอบว่า timestamp ไม่เก่าเกิน 5 นาที
  const ts = parseInt(timestamp, 10);
  if (isNaN(ts)) return false;

  const now = Math.floor(Date.now() / 1000);
  if (Math.abs(now - ts) > MAX_TIMESTAMP_AGE) return false;

  // Construct the signing message: "{timestamp}.{body}".
  // สร้าง message สำหรับ sign: "{timestamp}.{body}"
  const message = `${timestamp}.${payload.toString('utf-8')}`;

  // Compute the expected HMAC-SHA256 signature.
  // คำนวณ HMAC-SHA256 signature ที่คาดหวัง
  const expected = crypto
    .createHmac('sha256', secret)
    .update(message)
    .digest('hex');

  // Use timing-safe comparison to prevent timing attacks.
  // ใช้ timingSafeEqual เพื่อป้องกัน timing attack
  try {
    return crypto.timingSafeEqual(
      Buffer.from(signature, 'utf-8'),
      Buffer.from(expected, 'utf-8')
    );
  } catch {
    // If lengths differ, timingSafeEqual throws -- signature is invalid.
    // หากความยาวต่างกัน ถือว่า signature ไม่ถูกต้อง
    return false;
  }
}

// IMPORTANT: Use express.raw() to get the raw body as a Buffer.
// express.json() would parse the body and re-serializing would break the signature.
// สำคัญ: ใช้ express.raw() เพื่อให้ได้ raw body
// ห้ามใช้ express.json() เพราะ re-serialize จะทำให้ signature ผิด
app.post('/webhook/richpayment', express.raw({ type: 'application/json' }), (req, res) => {
  // Extract signature headers.
  // ดึง headers สำหรับตรวจสอบลายเซ็น
  const signature = req.headers['x-webhook-signature'] || '';
  const timestamp = req.headers['x-webhook-timestamp'] || '';
  const webhookId = req.headers['x-webhook-id'] || '';

  // Verify the webhook signature before processing.
  // ตรวจสอบลายเซ็นก่อนประมวลผล
  if (!verifyWebhookSignature(req.body, signature, timestamp, WEBHOOK_SECRET)) {
    console.error('Invalid webhook signature', { webhookId });
    return res.status(401).json({ error: 'invalid signature' });
  }

  // Parse the validated payload.
  // Parse payload ที่ผ่านการตรวจสอบแล้ว
  const event = JSON.parse(req.body.toString('utf-8'));
  const { event: eventType, data } = event;

  // Process by event type.
  // ประมวลผลตามชนิด event
  switch (eventType) {
    case 'deposit.completed':
      // Credit user's account with data.net_amount.
      // เติมเงินให้ลูกค้าด้วย data.net_amount
      console.log(`Deposit completed: ${data.merchant_order_id} amount=${data.net_amount}`);
      break;

    case 'withdrawal.completed':
      // Mark withdrawal as done in your system.
      // อัปเดตสถานะถอนเงินในระบบของคุณ
      console.log(`Withdrawal completed: ${data.merchant_order_id} ref=${data.transfer_ref}`);
      break;

    case 'deposit.expired':
      // Handle expired deposit.
      // จัดการ deposit หมดอายุ
      console.log(`Deposit expired: ${data.merchant_order_id}`);
      break;

    default:
      console.log(`Unhandled event: ${eventType}`);
  }

  // Always return 200 to acknowledge receipt.
  // ตอบ 200 เสมอ เพื่อยืนยันว่ารับ webhook สำเร็จ
  res.status(200).json({ status: 'ok' });
});

app.listen(3000, () => console.log('Webhook server running on port 3000'));
```

---

### PHP

```php
<?php
/**
 * RichPayment Webhook Verification and Handling
 *
 * ตรวจสอบและจัดการ webhook จาก RichPayment
 *
 * This script demonstrates how to receive, verify, and process
 * webhook notifications from the RichPayment payment gateway.
 */

// Your webhook secret from merchant registration.
// webhook secret ที่ได้รับตอนสร้าง merchant
$webhookSecret = 'whsec_your_secret_here';

// Maximum age (seconds) for webhook timestamps to prevent replay attacks.
// อายุสูงสุด (วินาที) ของ timestamp เพื่อป้องกัน replay attack
$maxTimestampAge = 300; // 5 minutes / 5 นาที

/**
 * Verify the HMAC-SHA256 signature of a RichPayment webhook.
 *
 * ตรวจสอบลายเซ็น HMAC-SHA256 ของ webhook จาก RichPayment
 *
 * @param string $payload   Raw request body
 * @param string $signature Value of X-Webhook-Signature header
 * @param string $timestamp Value of X-Webhook-Timestamp header
 * @param string $secret    Your merchant's webhook secret key
 * @param int    $maxAge    Maximum timestamp age in seconds (default: 300)
 * @return bool True if signature is valid, false otherwise
 */
function verifyWebhookSignature(
    string $payload,
    string $signature,
    string $timestamp,
    string $secret,
    int $maxAge = 300
): bool {
    // Check timestamp freshness to prevent replay attacks.
    // ตรวจสอบว่า timestamp ไม่เก่าเกิน $maxAge วินาที
    if (!is_numeric($timestamp)) {
        return false;
    }

    $ts = (int) $timestamp;
    if (abs(time() - $ts) > $maxAge) {
        return false;
    }

    // Construct the signing message: "{timestamp}.{body}".
    // สร้าง message สำหรับ sign: "{timestamp}.{body}"
    $message = $timestamp . '.' . $payload;

    // Compute the expected HMAC-SHA256 signature.
    // คำนวณ HMAC-SHA256 signature ที่คาดหวัง
    $expected = hash_hmac('sha256', $message, $secret);

    // Use timing-safe comparison to prevent timing attacks.
    // ใช้ hash_equals เพื่อป้องกัน timing attack
    return hash_equals($expected, $signature);
}

// =========================================================================
// Read and verify the incoming webhook
// อ่านและตรวจสอบ webhook ที่เข้ามา
// =========================================================================

// Read the raw request body. php://input gives us the unparsed body.
// อ่าน raw body จาก php://input (body ที่ยังไม่ถูก parse)
$rawBody = file_get_contents('php://input');

// Extract signature headers from the request.
// ดึง headers สำหรับตรวจสอบลายเซ็น
$signature = $_SERVER['HTTP_X_WEBHOOK_SIGNATURE'] ?? '';
$timestamp = $_SERVER['HTTP_X_WEBHOOK_TIMESTAMP'] ?? '';
$webhookId = $_SERVER['HTTP_X_WEBHOOK_ID'] ?? '';

// Verify the signature before doing anything else.
// ตรวจสอบลายเซ็นก่อนทำอะไรอื่น
if (!verifyWebhookSignature($rawBody, $signature, $timestamp, $webhookSecret, $maxTimestampAge)) {
    http_response_code(401);
    echo json_encode(['error' => 'invalid signature']);
    exit;
}

// Parse the validated JSON payload.
// Parse payload ที่ผ่านการตรวจสอบแล้ว
$event = json_decode($rawBody, true);
$eventType = $event['event'] ?? '';
$data = $event['data'] ?? [];

// Process by event type.
// ประมวลผลตามชนิด event
switch ($eventType) {
    case 'deposit.completed':
        // Credit user's account with $data['net_amount'].
        // เติมเงินให้ลูกค้าด้วย $data['net_amount']
        error_log("Deposit completed: {$data['merchant_order_id']} amount={$data['net_amount']}");
        break;

    case 'withdrawal.completed':
        // Mark withdrawal as done in your system.
        // อัปเดตสถานะถอนเงินในระบบของคุณ
        error_log("Withdrawal completed: {$data['merchant_order_id']} ref={$data['transfer_ref']}");
        break;

    case 'deposit.expired':
        // Handle expired deposit - notify customer or create new order.
        // จัดการ deposit หมดอายุ - แจ้งลูกค้าหรือสร้าง order ใหม่
        error_log("Deposit expired: {$data['merchant_order_id']}");
        break;

    case 'withdrawal.rejected':
        // Handle rejected withdrawal - held balance returned to wallet.
        // จัดการ withdrawal ที่ถูกปฏิเสธ - เงินคืน wallet
        error_log("Withdrawal rejected: {$data['merchant_order_id']} reason={$data['reason']}");
        break;

    default:
        error_log("Unhandled webhook event: {$eventType}");
}

// Always respond with 200 to acknowledge receipt.
// ตอบ 200 เสมอ เพื่อยืนยันว่ารับ webhook สำเร็จ
http_response_code(200);
echo json_encode(['status' => 'ok']);
```

---

## Retry Policy

<!-- ============================================================
     นโยบายการ Retry Webhook

     เมื่อ webhook ส่งไม่สำเร็จ (non-2xx response หรือ timeout)
     ระบบจะ retry ตาม exponential backoff ดังนี้:
     - ครั้งที่ 1: ส่งทันที
     - ครั้งที่ 2: รอ 10 วินาที
     - ครั้งที่ 3: รอ 30 วินาที
     - ครั้งที่ 4: รอ 90 วินาที
     - ครั้งที่ 5: รอ 270 วินาที

     หากล้มเหลวทั้ง 5 ครั้ง webhook จะถูก mark เป็น exhausted
     และระบบจะแจ้ง admin ผ่าน Telegram
     ============================================================ -->

When a webhook delivery fails (non-2xx response or timeout), the system
retries with **exponential backoff**. The retry schedule is:

| Attempt | Delay Before This Attempt | Total Elapsed Time | Description                                |
|---------|---------------------------|--------------------|--------------------------------------------|
| 1       | Immediate                 | 0s                 | First attempt, sent right away (ส่งทันที)   |
| 2       | 10 seconds                | ~10s               | Second attempt (รอ 10 วินาที)               |
| 3       | 30 seconds                | ~40s               | Third attempt (รอ 30 วินาที)                |
| 4       | 90 seconds                | ~2 minutes         | Fourth attempt (รอ 90 วินาที)               |
| 5       | 270 seconds               | ~7 minutes         | Fifth and final attempt (รอ 270 วินาที)     |

<!-- หมายเหตุ: delay progression คือ 10s, 30s, 90s, 270s (x3 ทุกครั้ง)
     ใน source code มี 810s ด้วย แต่จะไม่ถูกใช้เพราะ maxAttempts = 5 -->

### Retry Details

<!-- รายละเอียดเพิ่มเติมเกี่ยวกับการ retry -->

- **Max attempts:** 5 (1 initial + 4 retries)
  <!-- จำนวนครั้งสูงสุด: 5 ครั้ง (1 ครั้งแรก + 4 retries) -->
- **Timeout per attempt:** 10 seconds -- if your server doesn't respond within
  10 seconds, it counts as a failure
  <!-- timeout ต่อครั้ง: 10 วินาที หากไม่ตอบภายใน 10 วินาที ถือว่าล้มเหลว -->
- **Success criteria:** HTTP status code 200-299 (any 2xx response)
  <!-- เงื่อนไขสำเร็จ: HTTP status code 200-299 -->
- **Failure criteria:** HTTP status code outside 200-299, network error, or timeout
  <!-- เงื่อนไขล้มเหลว: HTTP status ที่ไม่ใช่ 2xx, network error, หรือ timeout -->
- **Storage:** Retry state is persisted in Redis with 24-hour TTL
  <!-- การจัดเก็บ: สถานะ retry เก็บใน Redis พร้อม TTL 24 ชั่วโมง -->

### After All Retries Exhausted

<!-- เมื่อ retry ครบทุกครั้งแล้วยังล้มเหลว -->

When all 5 attempts fail:

1. The webhook is marked as **exhausted** in Redis (30-day retention)
   <!-- webhook ถูก mark เป็น exhausted ใน Redis (เก็บ 30 วัน) -->
2. A **Telegram alert** is sent to the admin operations group
   <!-- ส่ง Telegram alert ไปยังกลุ่ม admin -->
3. No further automatic retries will be attempted
   <!-- ไม่มีการ retry อัตโนมัติอีก -->
4. Manual re-delivery can be triggered by an admin if needed
   <!-- Admin สามารถ trigger ส่งใหม่ได้ด้วยตนเอง -->

---

## Best Practices

<!-- ============================================================
     แนวทางปฏิบัติที่ดีสำหรับ Webhook Integration

     คำแนะนำเพื่อให้ระบบ webhook ทำงานได้อย่างมั่นคงและปลอดภัย
     ============================================================ -->

### 1. Always Verify Signatures

<!-- ตรวจสอบลายเซ็นทุกครั้ง -->

**Never process a webhook without verifying the signature.** This is your
primary defense against spoofed requests. Use constant-time comparison
functions (e.g. `hmac.Equal` in Go, `hmac.compare_digest` in Python,
`crypto.timingSafeEqual` in Node.js, `hash_equals` in PHP) to prevent
timing attacks.

<!-- ห้ามประมวลผล webhook โดยไม่ตรวจสอบลายเซ็น
     ใช้ constant-time comparison เพื่อป้องกัน timing attack -->

### 2. Implement Idempotency

<!-- ทำระบบ Idempotency เพื่อป้องกันการประมวลผลซ้ำ -->

Webhooks may be delivered more than once (e.g. if your server returned 200
but our system didn't receive the acknowledgement due to a network issue).
Use the combination of `event` + `data.id` + `data.status` as an idempotency
key to prevent duplicate processing.

<!-- Webhook อาจถูกส่งซ้ำ ใช้ event + data.id + data.status เป็น key
     เพื่อป้องกันการประมวลผลซ้ำ -->

```python
# Example idempotency check (Python with Redis)
# ตัวอย่าง idempotency check (Python กับ Redis)

import redis

r = redis.Redis()

def is_duplicate(event_type: str, order_id: str, status: str) -> bool:
    """
    Check if this webhook event has already been processed.
    ตรวจสอบว่า webhook event นี้ถูกประมวลผลไปแล้วหรือยัง
    """
    # Build a unique key from event type, order ID, and status.
    # สร้าง key จาก event type, order ID, และ status
    idempotency_key = f"webhook_processed:{event_type}:{order_id}:{status}"

    # Use SETNX (SET if Not eXists) for atomic check-and-set.
    # ใช้ SETNX เพื่อ check-and-set แบบ atomic
    was_set = r.setnx(idempotency_key, "1")

    if was_set:
        # First time seeing this event - set a 24-hour TTL.
        # เจอ event นี้เป็นครั้งแรก ตั้ง TTL 24 ชั่วโมง
        r.expire(idempotency_key, 86400)
        return False  # Not a duplicate / ไม่ซ้ำ

    return True  # Duplicate / ซ้ำ
```

### 3. Respond Quickly (Within 10 Seconds)

<!-- ตอบกลับให้เร็ว ภายใน 10 วินาที -->

Your webhook endpoint has a **10-second timeout**. If your server takes
longer than this, the delivery will be treated as failed and a retry will
be scheduled.

**Recommended approach:** Acknowledge the webhook immediately (return HTTP 200),
then process the event asynchronously in a background job/queue.

<!-- แนะนำ: ตอบ HTTP 200 ทันที แล้วประมวลผลแบบ asynchronous ใน background job -->

```python
# Good pattern: acknowledge first, process later.
# แนวทางที่ดี: ตอบรับก่อน แล้วประมวลผลทีหลัง

@app.route("/webhook/richpayment", methods=["POST"])
def handle_webhook():
    # ... signature verification ...

    # Enqueue the event for background processing.
    # ส่ง event เข้า queue สำหรับประมวลผลใน background
    background_queue.enqueue(process_webhook_event, event)

    # Respond immediately with 200.
    # ตอบ 200 ทันที
    return jsonify({"status": "ok"}), 200
```

### 4. Handle All Event Types

<!-- จัดการทุก event type -->

Even if you only care about `deposit.completed`, implement a handler that
gracefully ignores unknown event types. New event types may be added in
the future without breaking changes.

<!-- แม้สนใจแค่ deposit.completed ก็ควร handle event type ที่ไม่รู้จักด้วย
     เพราะอาจมี event type ใหม่ในอนาคต -->

```python
# Handle known events, gracefully ignore unknown ones.
# จัดการ event ที่รู้จัก, ข้ามที่ไม่รู้จักอย่างสง่างาม

event_handlers = {
    "deposit.matched": handle_deposit_matched,
    "deposit.completed": handle_deposit_completed,
    "deposit.expired": handle_deposit_expired,
    "deposit.failed": handle_deposit_failed,
    "deposit.cancelled": handle_deposit_cancelled,
    "withdrawal.approved": handle_withdrawal_approved,
    "withdrawal.completed": handle_withdrawal_completed,
    "withdrawal.rejected": handle_withdrawal_rejected,
    "withdrawal.failed": handle_withdrawal_failed,
}

handler = event_handlers.get(event_type)
if handler:
    handler(data)
else:
    # Log but don't fail - new event types may appear.
    # Log แต่ไม่ fail - อาจมี event type ใหม่เพิ่มมา
    logger.info(f"Ignoring unknown webhook event: {event_type}")
```

### 5. Check Timestamp Freshness

<!-- ตรวจสอบความใหม่ของ timestamp -->

In addition to signature verification, check that the `X-Webhook-Timestamp`
is within an acceptable window (recommended: 5 minutes). This prevents
replay attacks where an attacker re-sends a previously captured webhook.

<!-- นอกจากตรวจสอบลายเซ็นแล้ว ควรตรวจว่า timestamp ไม่เก่าเกิน 5 นาที
     เพื่อป้องกัน replay attack -->

### 6. Use HTTPS for Your Webhook Endpoint

<!-- ใช้ HTTPS สำหรับ webhook endpoint ของคุณ -->

Always serve your webhook endpoint over HTTPS to protect the payload
and signature in transit. The RichPayment system will refuse to send
webhooks to plain HTTP URLs.

<!-- ใช้ HTTPS เสมอ เพื่อปกป้อง payload และ signature ระหว่างส่ง
     ระบบ RichPayment จะไม่ส่ง webhook ไปยัง HTTP URL ธรรมดา -->

### 7. Log Everything

<!-- Log ทุกอย่าง -->

Log every webhook delivery including the `X-Webhook-ID`, event type, order ID,
and processing result. This makes debugging much easier when something goes
wrong.

<!-- Log ทุก webhook delivery รวมถึง X-Webhook-ID, event type, order ID,
     และผลการประมวลผล เพื่อให้ debug ง่ายขึ้น -->

### 8. Return Appropriate Status Codes

<!-- ตอบ HTTP status code ที่เหมาะสม -->

| Your Response Code | RichPayment Behavior                              |
|--------------------|----------------------------------------------------|
| 200-299            | Success, no retry (สำเร็จ ไม่ retry)                |
| 400-499            | Failure, will retry (ล้มเหลว จะ retry)              |
| 500-599            | Failure, will retry (ล้มเหลว จะ retry)              |
| Timeout (>10s)     | Failure, will retry (timeout จะ retry)              |

<!-- หมายเหตุ: ทุก non-2xx response จะถูก retry ไม่ว่าจะเป็น 4xx หรือ 5xx -->

---

## Testing Webhooks

<!-- ============================================================
     การทดสอบ Webhook

     คำแนะนำสำหรับทดสอบ webhook integration ในสภาพแวดล้อม sandbox
     ============================================================ -->

### Sandbox Environment

<!-- ทดสอบใน Sandbox -->

Use the sandbox environment (`https://sandbox.richpayment.co/api/v1`) to test
your webhook integration. Sandbox webhooks use the same format and signing
algorithm as production.

<!-- ใช้ sandbox environment สำหรับทดสอบ webhook integration
     Sandbox ใช้รูปแบบและ algorithm เดียวกับ production -->

### Local Development with Tunnels

<!-- พัฒนาบน localhost ด้วย tunnel -->

For local development, use a tunnel service to expose your local webhook
endpoint:

```bash
# Using ngrok to expose local port 3000:
# ใช้ ngrok เพื่อ expose local port 3000:
ngrok http 3000

# Then set your webhook URL to:
# แล้วตั้ง webhook URL เป็น:
# https://abc123.ngrok.io/webhook/richpayment
```

### Manual Signature Verification Test

<!-- ทดสอบตรวจสอบลายเซ็นด้วยตนเอง -->

You can generate a test signature manually to verify your implementation:

```bash
# Generate a test signature using OpenSSL.
# สร้าง test signature ด้วย OpenSSL

SECRET="whsec_your_secret_here"
TIMESTAMP="1700000000"
BODY='{"event":"deposit.completed","timestamp":"2024-01-01T00:03:00Z","data":{"id":"550e8400-e29b-41d4-a716-446655440000","merchant_order_id":"ORD-20240101-001","amount":"1000.25","status":"completed"}}'

# The signing message is: "{timestamp}.{body}"
# Message สำหรับ sign คือ: "{timestamp}.{body}"
echo -n "${TIMESTAMP}.${BODY}" | openssl dgst -sha256 -hmac "${SECRET}"
```

---

*Last updated: 2026-04-10*
*RichPayment Webhook Documentation v1*
