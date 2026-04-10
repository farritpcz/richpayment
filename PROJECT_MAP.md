# 🗺️ RichPayment Project Map
# แผนที่โปรเจค — ดูภาพรวมทั้งหมดได้ที่นี่

> **สถิติ:** 171 ไฟล์ | 110 Go files | ~27,700 บรรทัด | 12 microservices

---

## 📁 โครงสร้างโฟลเดอร์ (อ่านจากบนลงล่าง)

```
richpayment/
│
├── 📦 pkg/                          # ← SHARED LIBRARY (ทุก service ใช้ร่วมกัน)
│   ├── config/config.go             #    อ่าน env vars (DB, Redis, NATS URLs)
│   ├── crypto/                      #    เข้ารหัส/ถอดรหัส
│   │   ├── aes.go                   #    AES-256-GCM encryption
│   │   ├── hash.go                  #    bcrypt password + SHA-256
│   │   └── hmac.go                  #    HMAC-SHA256 signing/verify
│   ├── database/                    #    เชื่อมต่อ DB
│   │   ├── postgres.go              #    PostgreSQL connection pool
│   │   └── redis.go                 #    Redis client + distributed lock
│   ├── errors/errors.go             #    Error types มาตรฐาน (AppError)
│   ├── httpclient/                  #    🔑 HTTP client สำหรับ service-to-service
│   │   └── internal_client.go       #    Auto-sign requests ด้วย HMAC
│   ├── logger/logger.go             #    Structured logging (slog)
│   ├── middleware/                   #    🔒 Middleware ที่ใช้ร่วมกัน
│   │   ├── internal_auth.go         #    ✅ ป้องกัน service-to-service (HMAC)
│   │   ├── logsanitizer.go          #    ✅ Mask sensitive data ใน logs
│   │   ├── recovery.go              #    Panic recovery
│   │   └── service_acl.go           #    ✅ ใครเรียกใครได้ (ACL)
│   └── models/                      #    Domain models (structs)
│       ├── commission.go            #    Commission, DailySummary
│       ├── order.go                 #    DepositOrder
│       ├── user.go                  #    Admin, Merchant, Agent, Partner
│       ├── wallet.go                #    Wallet, WalletLedger
│       └── withdrawal.go            #    Withdrawal
│
├── 🔌 services/                     # ← 12 MICROSERVICES
│   │
│   ├── 🌐 gateway/ (port 8080)     # API Gateway — จุดเข้าเดียว
│   │   ├── cmd/main.go              #   Entry point
│   │   ├── internal/handler/        #   HTTP handlers (deposit, withdrawal, wallet)
│   │   ├── internal/middleware/      #   API key auth, rate limit, IP whitelist,
│   │   │   ├── apikey.go            #   session, freeze, response helpers
│   │   │   ├── ratelimit.go         #   🔒 Redis-based 100 req/min
│   │   │   ├── ipwhitelist.go       #   🔒 CIDR whitelist
│   │   │   ├── freeze.go            #   🔒 Emergency freeze switch
│   │   │   └── session.go           #   🍪 Secure cookie + IP binding
│   │   └── internal/router/         #   Route definitions
│   │
│   ├── 🔐 auth/ (port 8081)        # Authentication — login/session/2FA
│   │   ├── internal/service/
│   │   │   ├── auth.go              #   Login + session + 🔒IP rate limit
│   │   │   ├── rbac.go              #   21 permissions × 5 roles (bitmask)
│   │   │   └── totp.go              #   Google Authenticator (RFC 6238)
│   │   ├── internal/handler/        #   POST /auth/login, /logout, /validate
│   │   └── internal/model/types.go  #   User, Session, Permission types
│   │
│   ├── 👤 user/ (port 8082)        # User Management — CRUD ทุก role
│   │   ├── internal/service/        #   AdminService, MerchantService,
│   │   │   ├── admin.go             #   AgentService, PartnerService
│   │   │   ├── merchant.go          #   สร้าง API key + HMAC secret
│   │   │   ├── agent.go             #   Commission rates
│   │   │   └── partner.go           #   Partner hierarchy
│   │   └── internal/handler/        #   17 CRUD endpoints
│   │
│   ├── 📋 order/ (port 8083)       # Deposit Orders — สร้าง/จัดการฝากเงิน
│   │   ├── internal/service/
│   │   │   ├── deposit.go           #   ⭐ สร้าง order + QR + เลือกบัญชี
│   │   │   ├── matcher.go           #   ⭐ จับคู่ SMS กับ order
│   │   │   ├── qr.go               #   PromptPay QR generator
│   │   │   └── timeout.go           #   Auto-expire pending orders
│   │   └── internal/repository/     #   PostgreSQL queries
│   │
│   ├── 💰 wallet/ (port 8084)      # Wallet — กระเป๋าเงิน + บัญชีแยกประเภท
│   │   ├── internal/service/
│   │   │   └── wallet.go            #   🔒 Credit/Debit/Hold/Release
│   │   │                            #   (atomic transactions + distributed lock)
│   │   └── internal/repository/     #   🔒 SELECT FOR UPDATE + idempotency
│   │
│   ├── 💸 withdrawal/ (port 8085)  # Withdrawals — ถอนเงิน
│   │   ├── internal/service/
│   │   │   └── withdrawal.go        #   Create→Approve→Complete flow
│   │   └── internal/handler/        #   6 endpoints
│   │
│   ├── 📱 parser/ (port 8086)      # SMS Parser — อ่าน SMS ธนาคาร
│   │   ├── internal/banks/          #   Bank-specific parsers
│   │   │   ├── kbank.go             #   กสิกร (Thai + English format)
│   │   │   └── scb.go              #   ไทยพาณิชย์
│   │   ├── internal/service/
│   │   │   ├── parser.go            #   🛡️ 12-step processing pipeline
│   │   │   ├── antispoof.go         #   🛡️ Confidence scoring (5 signals)
│   │   │   └── anomaly.go           #   🛡️ Rate anomaly detection
│   │   └── internal/repository/     #   SMS storage + order matching
│   │
│   ├── 🔔 notification/ (port 8087)# Webhooks + Alerts
│   │   ├── internal/service/
│   │   │   ├── webhook.go           #   HMAC-signed webhooks + retry
│   │   │   ├── telegram.go          #   Telegram security alerts
│   │   │   └── retry.go             #   Exponential backoff worker
│   │   └── internal/handler/        #   POST /internal/webhook/send
│   │
│   ├── 💹 commission/ (port 8088)  # Commission — แบ่ง fee 3 ทาง
│   │   ├── internal/service/
│   │   │   ├── calculator.go        #   System + Agent + Partner split
│   │   │   └── aggregator.go        #   Daily/Monthly summaries
│   │   └── internal/repository/     #   Commission + wallet credit
│   │
│   ├── 🏦 bank/ (port 8089)        # Bank Accounts — จัดการบัญชีรับเงิน
│   │   ├── internal/service/
│   │   │   ├── pool.go              #   เลือกบัญชี + auto-switch
│   │   │   ├── monitor.go           #   Daily counters + utilization
│   │   │   └── transfer.go          #   โอนเงินไปบัญชีพัก
│   │   └── internal/handler/        #   13 endpoints
│   │
│   ├── 🤖 telegram/ (port 8090)    # Telegram Bot — ตรวจสลิป
│   │   ├── internal/service/
│   │   │   ├── bot.go               #   Long-polling + photo handler
│   │   │   ├── slip.go              #   9-step slip verification
│   │   │   └── alert.go             #   Security alert sender
│   │   └── internal/easyslip/       #   EasySlip API client
│   │
│   └── ⏰ scheduler/ (port 8091)   # Cron Jobs — งานตั้งเวลา
│       ├── internal/service/
│       │   ├── cron.go              #   5 scheduled jobs
│       │   ├── partition.go         #   PostgreSQL partition management
│       │   ├── archive.go           #   Data archival (pg_dump + gzip)
│       │   └── summary.go           #   Commission aggregation
│       └── internal/handler/        #   Manual trigger + status
│
├── 📡 proto/                        # gRPC Proto definitions (12 files)
│   ├── common.proto                 #   Shared types (Money, Pagination)
│   ├── auth.proto                   #   Login, Logout, ValidateSession
│   ├── wallet.proto                 #   GetBalance, Credit, Debit, Hold
│   ├── order.proto                  #   CreateDeposit, MatchSMS
│   └── ... (+ 8 more)              #   withdrawal, commission, bank, etc.
│
├── 📄 docs/api/                     # API Documentation (4 files)
│   ├── merchant-api.md              #   Merchant-facing API reference
│   ├── admin-api.md                 #   Admin back-office API
│   ├── webhook.md                   #   Webhook format + verification
│   └── errors.md                    #   Error code reference
│
├── 🧪 tests/integration/           # Integration Tests (14 test cases)
│   ├── deposit_flow_test.go         #   Deposit → Match → Wallet → Webhook
│   ├── withdrawal_flow_test.go      #   Create → Approve → Complete
│   ├── slip_flow_test.go            #   Slip → Verify → Match
│   └── commission_flow_test.go      #   3-way split + aggregation
│
├── 🐳 Docker & DevOps
│   ├── Dockerfile                   #   Multi-stage build (distroless)
│   ├── docker-compose.yml           #   Production (12 services + infra)
│   ├── docker-compose.dev.yml       #   Dev (infra only)
│   ├── .dockerignore                #   Docker build exclusions
│   ├── Makefile                     #   Dev commands (make run-*, test, build)
│   ├── .air.toml                    #   Hot-reload config
│   ├── .env.dev                     #   Dev environment variables
│   └── scripts/
│       ├── init-db.sql              #   DB schema (20 tables + partitions)
│       └── dev-run-all.sh           #   Run all services locally
│
├── 🗄️ migrations/                  # PostgreSQL migrations
│   └── 001_initial_schema.sql       #   Initial schema
│
└── 📋 Config
    ├── go.work                      #   Go workspace (links pkg + 12 services)
    ├── go.work.sum                  #   Workspace checksum
    └── PROJECT_MAP.md               #   ← คุณกำลังอ่านอยู่ตรงนี้!
```

---

## 🔄 Flow หลัก: ลูกค้าฝากเงิน (Deposit)

```
ลูกค้า                  Merchant              RichPayment
  │                       │                      │
  │  "จ่ายเงิน 500 บาท"  │                      │
  │ ──────────────────► │                      │
  │                       │  POST /deposits     │
  │                       │ ──────────────────► │ gateway (8080)
  │                       │                      │ → order (8083)
  │                       │                      │   → bank (8089) เลือกบัญชี
  │                       │   ◄──── QR Code ──── │   → สร้าง QR PromptPay
  │   ◄── QR Code ──────  │                      │
  │                       │                      │
  │  สแกน QR → โอนเงิน   │                      │
  │  ──────────────────────────────────────────► │ ธนาคาร
  │                       │                      │
  │                       │                      │ SMS จากธนาคาร
  │                       │                      │ → parser (8086) อ่าน SMS
  │                       │                      │   → 🛡️ antispoof ให้คะแนน
  │                       │                      │   → 🛡️ anomaly ตรวจความผิดปกติ
  │                       │                      │   → จับคู่กับ order
  │                       │                      │ → wallet (8084) เพิ่มยอด
  │                       │                      │   → 🔒 SELECT FOR UPDATE
  │                       │                      │ → commission (8088) แบ่ง fee
  │                       │  ◄── Webhook ─────── │ → notification (8087)
  │                       │  (deposit.completed)  │
  │  ◄── "จ่ายสำเร็จ!" ── │                      │
```

---

## 🔒 Security Layers (ชั้นป้องกัน)

```
Internet
  │
  ▼
┌──────────────────────────────────────┐
│ Layer 1: Nginx + SSL/TLS            │ ← HTTPS only
├──────────────────────────────────────┤
│ Layer 2: Rate Limiting              │ ← 100 req/min per API key
├──────────────────────────────────────┤
│ Layer 3: API Key + HMAC Signature   │ ← Merchant auth
├──────────────────────────────────────┤
│ Layer 4: IP Whitelist               │ ← Optional per merchant
├──────────────────────────────────────┤
│ Layer 5: Emergency Freeze           │ ← Admin kill switch
├──────────────────────────────────────┤
│ Layer 6: Internal Service Auth      │ ← HMAC between services
├──────────────────────────────────────┤
│ Layer 7: Service ACL                │ ← Who can call what
├──────────────────────────────────────┤
│ Layer 8: SMS Anti-Spoofing          │ ← Confidence scoring
├──────────────────────────────────────┤
│ Layer 9: Wallet Atomic Locks        │ ← FOR UPDATE + Redis lock
├──────────────────────────────────────┤
│ Layer 10: Session Security          │ ← IP binding + fingerprint
├──────────────────────────────────────┤
│ Layer 11: Log Sanitization          │ ← Mask passwords, keys
└──────────────────────────────────────┘
```

---

## 💰 Commission Flow (ใครได้เงินเท่าไหร่)

```
ลูกค้าฝาก 10,000 บาท (merchant fee = 3%)
  │
  ├── Fee ทั้งหมด = 300 บาท
  │   ├── Agent (30% of fee)   = 90 บาท  → Agent wallet
  │   ├── Partner (10% of fee) = 30 บาท  → Partner wallet
  │   └── System (60% of fee)  = 180 บาท → System wallet
  │
  └── Merchant ได้รับ = 9,700 บาท → Merchant wallet
```

---

## 🚀 Quick Commands

```bash
# Development
make dev-up              # เปิด Postgres + Redis + NATS
make run-gateway         # เปิด gateway พร้อม hot-reload
make run-all             # เปิดทุก service
make test                # รัน unit tests

# Production
docker compose up -d     # เปิดทุกอย่าง
docker compose logs -f   # ดู logs
```
