-- ==========================================================================
-- RichPayment - Initial Database Schema
-- PostgreSQL 16+
--
-- Payment Gateway SaaS system with 4 roles:
--   Admin   = System owner, full control, RBAC
--   Agent   = Middleman, resells as white-label, earns commission
--   Merchant = Business using the payment service
--   Partner  = Bank account owner, earns commission per transaction
--
-- Volume: ~100M THB/day, 100K-1M transactions/day
-- Partitioned tables retain 3 months, then archived
-- ==========================================================================

CREATE EXTENSION IF NOT EXISTS "pgcrypto";  -- For gen_random_uuid()

-- ==========================================================================
-- 1. ADMINS - System administrators with RBAC permissions
-- ==========================================================================
CREATE TABLE admins (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),  -- Unique admin identifier
    email           VARCHAR(255) NOT NULL UNIQUE,                -- Login email, must be unique
    password_hash   VARCHAR(255) NOT NULL,                       -- bcrypt hashed password
    totp_secret_enc BYTEA,                                      -- AES-256 encrypted TOTP secret for 2FA (Google Authenticator)
    display_name    VARCHAR(100) NOT NULL,                       -- Name shown in dashboard
    role_mask       BIGINT NOT NULL DEFAULT 0,                   -- Bitmask for RBAC permissions (bit 0=ViewMerchants, bit 1=CreateMerchants, etc.)
    is_active       BOOLEAN NOT NULL DEFAULT true,               -- Soft disable: false = cannot login
    locked_until    TIMESTAMPTZ,                                 -- Account locked until this time (after 5 failed login attempts)
    failed_attempts SMALLINT NOT NULL DEFAULT 0,                 -- Consecutive failed login attempts counter (resets on success)
    ip_whitelist    INET[],                                      -- Array of allowed IP addresses (null = allow all)
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),          -- Record creation timestamp
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()           -- Last modification timestamp
);

-- ==========================================================================
-- 2. AGENTS - Middlemen who resell the payment gateway as white-label
--    Each agent gets their own branded domain, logo, and colors.
--    Agent earns commission on every transaction made by their merchants.
-- ==========================================================================
CREATE TABLE agents (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),  -- Unique agent identifier
    email                   VARCHAR(255) NOT NULL UNIQUE,                -- Login email
    password_hash           VARCHAR(255) NOT NULL,                       -- bcrypt hashed password
    totp_secret_enc         BYTEA,                                      -- AES-256 encrypted TOTP secret for 2FA
    company_name            VARCHAR(200) NOT NULL,                       -- Agent's company/brand name
    display_name            VARCHAR(100) NOT NULL,                       -- Name shown in dashboard
    -- White-label branding configuration
    custom_domain           VARCHAR(255),                                -- Agent's custom domain (e.g. pay.agentsite.com)
    logo_url                VARCHAR(500),                                -- URL to agent's custom logo image
    brand_color_primary     VARCHAR(7),                                  -- Primary brand color in hex (e.g. #FF5733)
    brand_color_secondary   VARCHAR(7),                                  -- Secondary brand color in hex
    -- Commission rates (% of transaction amount, set by admin)
    deposit_commission_pct  NUMERIC(5,4) NOT NULL DEFAULT 0,             -- Agent's commission % on deposits (e.g. 0.0100 = 1.00%)
    withdraw_commission_pct NUMERIC(5,4) NOT NULL DEFAULT 0,             -- Agent's commission % on withdrawals
    -- Account status
    is_active               BOOLEAN NOT NULL DEFAULT true,               -- Soft disable: false = cannot login
    locked_until            TIMESTAMPTZ,                                 -- Account locked until this time
    failed_attempts         SMALLINT NOT NULL DEFAULT 0,                 -- Failed login attempts counter
    created_by_admin_id     UUID REFERENCES admins(id),                  -- Which admin created this agent
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ==========================================================================
-- 3. MERCHANTS - Businesses that use the payment gateway to accept payments
--    Merchants integrate via API (API Key + HMAC signature).
--    Each merchant has a wallet that accumulates received payments.
-- ==========================================================================
CREATE TABLE merchants (
    id                          UUID PRIMARY KEY DEFAULT gen_random_uuid(),  -- Unique merchant identifier
    agent_id                    UUID REFERENCES agents(id),                  -- Parent agent (NULL = direct merchant, no agent)
    email                       VARCHAR(255) NOT NULL UNIQUE,                -- Login email
    password_hash               VARCHAR(255) NOT NULL,                       -- bcrypt hashed password
    totp_secret_enc             BYTEA,                                      -- AES-256 encrypted TOTP secret for 2FA
    company_name                VARCHAR(200) NOT NULL,                       -- Merchant's company name
    display_name                VARCHAR(100) NOT NULL,                       -- Name shown in dashboard
    -- API authentication
    api_key_hash                VARCHAR(255) NOT NULL,                       -- bcrypt hash of full API key (rpay_xxxxx...)
    api_key_prefix              VARCHAR(8) NOT NULL,                         -- First 8 chars of API key for quick lookup
    hmac_secret_enc             BYTEA NOT NULL,                              -- AES-256 encrypted HMAC secret (used to sign/verify API requests)
    -- Webhook configuration
    webhook_url                 VARCHAR(500),                                -- URL to receive payment callbacks (POST)
    webhook_secret_enc          BYTEA,                                       -- AES-256 encrypted secret for signing webhook payloads
    -- Security
    ip_whitelist                INET[],                                      -- Allowed IPs for API calls (null = allow all)
    -- Deposit configuration
    duplicate_amount_strategy   VARCHAR(20) NOT NULL DEFAULT 'unique_amount', -- How to handle same-amount orders: 'unique_amount' (adjust amount to be unique) or 'time_based' (match by time window)
    deposit_timeout_sec         INT NOT NULL DEFAULT 300,                     -- Seconds before unpaid deposit order expires (default 5 minutes)
    -- Fee rates (% of transaction amount, set by admin for each merchant)
    deposit_fee_pct             NUMERIC(5,4) NOT NULL DEFAULT 0,             -- Fee % charged on deposits (e.g. 0.0300 = 3.00%)
    withdraw_fee_pct            NUMERIC(5,4) NOT NULL DEFAULT 0,             -- Fee % charged on THB withdrawals
    usdt_withdraw_fee_pct       NUMERIC(5,4) NOT NULL DEFAULT 0,             -- Fee % charged on USDT withdrawals
    -- Withdrawal limits (set by admin, NULL = unlimited)
    daily_withdraw_limit_thb    NUMERIC(14,2),                               -- Maximum THB withdrawal per day
    daily_withdraw_limit_usdt   NUMERIC(14,6),                               -- Maximum USDT withdrawal per day
    -- Telegram integration
    telegram_group_id           BIGINT,                                      -- Telegram group ID for slip verification (1 group per merchant)
    -- Account status
    is_active                   BOOLEAN NOT NULL DEFAULT true,               -- Soft disable: false = cannot login or call API
    locked_until                TIMESTAMPTZ,                                 -- Account locked until this time
    failed_attempts             SMALLINT NOT NULL DEFAULT 0,                 -- Failed login attempts counter
    created_by_admin_id         UUID REFERENCES admins(id),                  -- Which admin created this merchant
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                  TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- Fast lookup by agent (for agent dashboard showing their merchants)
CREATE INDEX idx_merchants_agent ON merchants(agent_id) WHERE agent_id IS NOT NULL;
-- Fast API key lookup by prefix (first step of API authentication)
CREATE INDEX idx_merchants_api_prefix ON merchants(api_key_prefix);

-- ==========================================================================
-- 4. PARTNERS - Bank account owners who provide accounts for receiving payments
--    Partners earn commission on every transaction that goes through their accounts.
--    Admin manages partners and sets their commission rates.
-- ==========================================================================
CREATE TABLE partners (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),  -- Unique partner identifier
    email                   VARCHAR(255) NOT NULL UNIQUE,                -- Login email
    password_hash           VARCHAR(255) NOT NULL,                       -- bcrypt hashed password
    totp_secret_enc         BYTEA,                                      -- AES-256 encrypted TOTP secret for 2FA
    display_name            VARCHAR(100) NOT NULL,                       -- Name shown in dashboard
    -- Commission rates (% of transaction amount, set by admin)
    deposit_commission_pct  NUMERIC(5,4) NOT NULL DEFAULT 0,             -- Partner's commission % on deposits
    withdraw_commission_pct NUMERIC(5,4) NOT NULL DEFAULT 0,             -- Partner's commission % on withdrawals
    -- Account status
    is_active               BOOLEAN NOT NULL DEFAULT true,               -- Soft disable
    locked_until            TIMESTAMPTZ,                                 -- Account locked until this time
    failed_attempts         SMALLINT NOT NULL DEFAULT 0,                 -- Failed login attempts counter
    created_by_admin_id     UUID REFERENCES admins(id),                  -- Which admin created this partner
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ==========================================================================
-- 5. BANK ACCOUNTS - Partner's bank accounts used to receive customer payments
--    When a customer makes a payment, the money goes into one of these accounts.
--    The system monitors SMS/Email from these accounts to detect incoming payments.
--    Accounts auto-switch when daily limit is reached or errors detected.
-- ==========================================================================
CREATE TABLE bank_accounts (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),  -- Unique bank account identifier
    partner_id           UUID NOT NULL REFERENCES partners(id),       -- Which partner owns this account
    bank_code            VARCHAR(10) NOT NULL,                        -- Bank identifier code (e.g. 'KBANK', 'SCB', 'BBL', 'KTB')
    account_number_enc   BYTEA NOT NULL,                              -- AES-256 encrypted bank account number (sensitive!)
    account_name         VARCHAR(200) NOT NULL,                       -- Account holder name (shown to customer on payment page)
    account_number_last4 VARCHAR(4) NOT NULL,                         -- Last 4 digits of account number (for display/identification)
    -- Operational settings
    is_active            BOOLEAN NOT NULL DEFAULT true,               -- Master on/off switch
    daily_limit_thb      NUMERIC(14,2) NOT NULL DEFAULT 2000000,      -- Maximum amount this account can receive per day (default 2M THB)
    daily_received_thb   NUMERIC(14,2) NOT NULL DEFAULT 0,            -- Amount already received today (reset daily by scheduler)
    current_balance_thb  NUMERIC(14,2),                               -- Last known balance in this account (updated by monitor)
    last_balance_check   TIMESTAMPTZ,                                 -- When balance was last checked
    -- SMS parsing configuration
    sms_sender_numbers   VARCHAR(20)[],                               -- Expected SMS sender phone numbers for this bank (for anti-spoofing)
    sms_error_count      INT NOT NULL DEFAULT 0,                      -- Consecutive SMS parsing errors (auto-disable after max_sms_errors)
    max_sms_errors       INT NOT NULL DEFAULT 5,                      -- Max errors before auto-disabling this account
    -- Status: 'active' = receiving payments, 'paused' = manually paused,
    --         'limit_reached' = daily limit hit, 'error' = SMS errors exceeded, 'disabled' = admin disabled
    status               VARCHAR(20) NOT NULL DEFAULT 'active',
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- Find all accounts belonging to a partner
CREATE INDEX idx_bank_accounts_partner ON bank_accounts(partner_id);
-- Fast lookup for active accounts (used by account pool selection)
CREATE INDEX idx_bank_accounts_active ON bank_accounts(status) WHERE status = 'active';

-- ==========================================================================
-- 6. BANK ACCOUNT <-> MERCHANT MAPPING
--    Controls which bank accounts can receive payments for which merchants.
--    Admin maps accounts to merchants. One account can serve multiple merchants.
--    Priority determines which account is preferred (lower number = higher priority).
-- ==========================================================================
CREATE TABLE bank_account_merchant_map (
    bank_account_id UUID NOT NULL REFERENCES bank_accounts(id),  -- The bank account
    merchant_id     UUID NOT NULL REFERENCES merchants(id),       -- The merchant it serves
    priority        SMALLINT NOT NULL DEFAULT 0,                  -- Selection priority (0 = highest, 1 = next, etc.)
    is_active       BOOLEAN NOT NULL DEFAULT true,                -- Enable/disable this specific mapping
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (bank_account_id, merchant_id)
);
-- Find all accounts mapped to a merchant (used when creating deposit orders)
CREATE INDEX idx_bam_merchant ON bank_account_merchant_map(merchant_id);

-- ==========================================================================
-- 7. HOLDING ACCOUNTS - Pre-registered safe accounts for fund transfers
--    Money is transferred from receiving accounts to holding accounts for safety.
--    ONLY pre-registered accounts can be transfer destinations (prevents hack attacks).
-- ==========================================================================
CREATE TABLE holding_accounts (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),  -- Unique holding account identifier
    bank_code               VARCHAR(10) NOT NULL,                        -- Bank code (e.g. 'KBANK', 'SCB')
    account_number_enc      BYTEA NOT NULL,                              -- AES-256 encrypted account number
    account_name            VARCHAR(200) NOT NULL,                       -- Account holder name
    account_number_last4    VARCHAR(4) NOT NULL,                         -- Last 4 digits for display
    is_active               BOOLEAN NOT NULL DEFAULT true,               -- Enable/disable this holding account
    auto_transfer_threshold NUMERIC(14,2),                               -- Auto-transfer when receiving account balance exceeds this amount (NULL = manual only)
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ==========================================================================
-- 8. WALLETS - Internal balance tracking for all roles
--    Every merchant, agent, partner, and system has a wallet.
--    Deposits credit the merchant wallet (minus fees).
--    Commission credits agent/partner/system wallets.
--    Withdrawals debit the merchant wallet.
--    Uses optimistic locking (version column) to prevent race conditions.
-- ==========================================================================
CREATE TABLE wallets (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),  -- Unique wallet identifier
    owner_type   VARCHAR(10) NOT NULL,                        -- Who owns this wallet: 'merchant', 'agent', 'partner', 'system'
    owner_id     UUID NOT NULL,                               -- The owner's ID (references merchants.id, agents.id, etc.)
    currency     VARCHAR(3) NOT NULL DEFAULT 'THB',           -- Wallet currency: 'THB', 'USD', 'USDT', etc.
    balance      NUMERIC(14,2) NOT NULL DEFAULT 0,            -- Available balance (can be withdrawn)
    hold_balance NUMERIC(14,2) NOT NULL DEFAULT 0,            -- Held balance (pending withdrawals, cannot be used)
    version      BIGINT NOT NULL DEFAULT 0,                   -- Optimistic lock version (UPDATE ... WHERE version = X, prevents double-spend)
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(owner_type, owner_id, currency)                    -- One wallet per owner per currency
);

-- ==========================================================================
-- 9. DEPOSIT ORDERS - Customer payment requests (PARTITIONED BY MONTH)
--    Flow: Merchant creates order via API -> System returns QR code + amount
--       -> Customer pays -> SMS/Email detected -> Order matched -> Webhook sent
--    Partitioned by created_at for fast queries and easy archival.
-- ==========================================================================
CREATE TABLE deposit_orders (
    id                   UUID NOT NULL DEFAULT gen_random_uuid(),  -- Unique order identifier
    merchant_id          UUID NOT NULL,                            -- Which merchant this order belongs to
    merchant_order_id    VARCHAR(100) NOT NULL,                    -- Merchant's own order/invoice reference
    -- Customer information (sent by merchant, used for slip verification)
    customer_name        VARCHAR(200),                             -- Customer name (for cross-checking with slip sender name)
    customer_bank_code   VARCHAR(10),                              -- Customer's bank code (for cross-checking)
    -- Amount fields
    requested_amount     NUMERIC(12,2) NOT NULL,                   -- Original amount requested by merchant
    adjusted_amount      NUMERIC(12,2) NOT NULL,                   -- Adjusted amount for uniqueness (e.g. 1000.00 -> 1000.01 to avoid collision)
    actual_amount        NUMERIC(12,2),                            -- Actual amount received (filled after match)
    fee_amount           NUMERIC(12,2),                            -- Fee deducted (filled after match)
    net_amount           NUMERIC(12,2),                            -- Amount credited to merchant wallet (actual - fee)
    currency             VARCHAR(3) NOT NULL DEFAULT 'THB',        -- Payment currency
    -- Matching information
    bank_account_id      UUID,                                     -- Which bank account was assigned to receive this payment
    matched_by           VARCHAR(20),                              -- How the payment was detected: 'sms', 'email', 'slip', or NULL if not yet matched
    matched_at           TIMESTAMPTZ,                              -- When the match occurred
    sms_message_id       UUID,                                     -- FK to sms_messages if matched by SMS
    slip_verification_id UUID,                                     -- FK to slip_verifications if matched by slip
    -- Status: 'pending' = waiting for payment, 'matched' = payment detected,
    --         'completed' = fully processed, 'expired' = timeout, 'failed', 'cancelled'
    status               VARCHAR(20) NOT NULL DEFAULT 'pending',
    -- QR code
    qr_payload           TEXT,                                     -- Raw PromptPay QR payload string
    -- Webhook tracking
    webhook_sent         BOOLEAN NOT NULL DEFAULT false,            -- Has webhook been sent to merchant?
    webhook_sent_at      TIMESTAMPTZ,                              -- When webhook was successfully delivered
    webhook_attempts     SMALLINT NOT NULL DEFAULT 0,               -- Number of webhook delivery attempts
    -- Timestamps
    expires_at           TIMESTAMPTZ NOT NULL,                     -- When this order expires (created_at + timeout_sec)
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),        -- Order creation time
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),        -- Last status change time
    PRIMARY KEY (id, created_at)                                   -- Composite PK required for partitioning
) PARTITION BY RANGE (created_at);

-- Create initial partitions (current month + 2 months ahead)
CREATE TABLE deposit_orders_2026_04 PARTITION OF deposit_orders
    FOR VALUES FROM ('2026-04-01') TO ('2026-05-01');
CREATE TABLE deposit_orders_2026_05 PARTITION OF deposit_orders
    FOR VALUES FROM ('2026-05-01') TO ('2026-06-01');
CREATE TABLE deposit_orders_2026_06 PARTITION OF deposit_orders
    FOR VALUES FROM ('2026-06-01') TO ('2026-07-01');

-- Index: Find merchant's orders by status (dashboard listing)
CREATE INDEX idx_do_merchant_status ON deposit_orders(merchant_id, status, created_at DESC);
-- Index: Fast amount matching - find pending orders for a bank account with specific amount (HOT PATH for SMS matching)
CREATE INDEX idx_do_pending_amount ON deposit_orders(bank_account_id, adjusted_amount, status)
    WHERE status = 'pending';
-- Index: Find order by merchant's reference ID
CREATE INDEX idx_do_merchant_order ON deposit_orders(merchant_id, merchant_order_id, created_at DESC);
-- Index: Find expired orders for timeout worker
CREATE INDEX idx_do_expires ON deposit_orders(expires_at) WHERE status = 'pending';

-- ==========================================================================
-- 10. WALLET LEDGER - Immutable log of all wallet balance changes (PARTITIONED)
--     Every credit/debit to any wallet is recorded here.
--     This is the source of truth for wallet balance history and audit.
-- ==========================================================================
CREATE TABLE wallet_ledger (
    id             BIGINT GENERATED ALWAYS AS IDENTITY,        -- Auto-incrementing unique ID
    wallet_id      UUID NOT NULL,                               -- Which wallet was affected
    -- Entry type describes what caused this balance change:
    --   'deposit_credit'          = Money received from customer deposit
    --   'withdrawal_debit'        = Money withdrawn by merchant
    --   'withdrawal_hold'         = Balance moved to hold (pending withdrawal approval)
    --   'withdrawal_release'      = Held balance released back (withdrawal rejected)
    --   'fee_debit'               = Fee deducted from merchant
    --   'commission_credit'       = Commission earned (agent/partner/system)
    --   'commission_payout_debit' = Commission paid out to agent/partner
    --   'adjustment'              = Manual adjustment by admin
    entry_type     VARCHAR(30) NOT NULL,
    reference_type VARCHAR(30),                                 -- What entity caused this: 'deposit_order', 'withdrawal', 'commission', 'admin_adjustment'
    reference_id   UUID,                                        -- ID of the referenced entity
    amount         NUMERIC(14,2) NOT NULL,                      -- Amount changed: positive = credit, negative = debit
    balance_after  NUMERIC(14,2) NOT NULL,                      -- Wallet balance AFTER this entry (for easy balance lookup)
    description    VARCHAR(500),                                -- Human-readable description of this entry
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);

CREATE TABLE wallet_ledger_2026_04 PARTITION OF wallet_ledger
    FOR VALUES FROM ('2026-04-01') TO ('2026-05-01');
CREATE TABLE wallet_ledger_2026_05 PARTITION OF wallet_ledger
    FOR VALUES FROM ('2026-05-01') TO ('2026-06-01');
CREATE TABLE wallet_ledger_2026_06 PARTITION OF wallet_ledger
    FOR VALUES FROM ('2026-06-01') TO ('2026-07-01');

-- Index: Get wallet history sorted by newest first
CREATE INDEX idx_wl_wallet ON wallet_ledger(wallet_id, created_at DESC);

-- ==========================================================================
-- 11. WITHDRAWALS - Merchant withdrawal requests (PARTITIONED)
--     Flow: Merchant creates withdrawal -> Admin reviews -> Admin approves/rejects
--        -> Admin manually transfers money -> Marks as completed
--     Supports both THB (bank transfer) and USDT withdrawals.
-- ==========================================================================
CREATE TABLE withdrawals (
    id                   UUID NOT NULL DEFAULT gen_random_uuid(),  -- Unique withdrawal identifier
    merchant_id          UUID NOT NULL,                            -- Which merchant requested this withdrawal
    -- Amount fields
    amount               NUMERIC(14,2) NOT NULL,                   -- Requested withdrawal amount
    fee_amount           NUMERIC(14,2) NOT NULL DEFAULT 0,         -- Fee charged for this withdrawal
    net_amount           NUMERIC(14,2) NOT NULL,                   -- Actual amount to be transferred (amount - fee)
    currency             VARCHAR(3) NOT NULL DEFAULT 'THB',        -- 'THB' or 'USDT'
    -- Destination: either bank account or USDT wallet
    destination_type     VARCHAR(10) NOT NULL,                     -- 'bank' = bank transfer, 'usdt' = crypto transfer
    bank_code            VARCHAR(10),                              -- Bank code for bank transfers (e.g. 'KBANK')
    account_number_enc   BYTEA,                                    -- AES-256 encrypted destination bank account number
    account_name         VARCHAR(200),                             -- Destination account holder name
    usdt_address_enc     BYTEA,                                    -- AES-256 encrypted USDT wallet address
    usdt_network         VARCHAR(10),                              -- USDT network: 'TRC20', 'ERC20'
    -- Status: 'pending' = waiting for admin, 'approved' = admin approved,
    --         'processing' = admin is transferring, 'completed' = money sent,
    --         'rejected' = admin rejected
    status               VARCHAR(20) NOT NULL DEFAULT 'pending',
    approved_by_admin_id UUID,                                     -- Which admin approved/rejected this
    approved_at          TIMESTAMPTZ,                              -- When it was approved
    completed_at         TIMESTAMPTZ,                              -- When the actual transfer was completed
    rejection_reason     VARCHAR(500),                             -- Reason for rejection (if rejected)
    -- Proof of transfer
    transfer_proof_url   VARCHAR(500),                             -- URL/path to transfer receipt/screenshot
    transfer_reference   VARCHAR(100),                             -- Bank transfer reference number
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);

CREATE TABLE withdrawals_2026_04 PARTITION OF withdrawals
    FOR VALUES FROM ('2026-04-01') TO ('2026-05-01');
CREATE TABLE withdrawals_2026_05 PARTITION OF withdrawals
    FOR VALUES FROM ('2026-05-01') TO ('2026-06-01');
CREATE TABLE withdrawals_2026_06 PARTITION OF withdrawals
    FOR VALUES FROM ('2026-06-01') TO ('2026-07-01');

-- Index: Merchant's withdrawal history
CREATE INDEX idx_wd_merchant ON withdrawals(merchant_id, status, created_at DESC);
-- Index: Admin view pending withdrawals for approval
CREATE INDEX idx_wd_status ON withdrawals(status, created_at DESC);

-- ==========================================================================
-- 12. COMMISSIONS - Per-transaction commission breakdown (PARTITIONED)
--     Every completed deposit/withdrawal generates a commission record.
--     Fee is split between: System (admin), Agent (if any), Partner (account owner).
--     Formula: total_fee = amount * merchant_fee_pct
--              agent_share  = amount * agent_pct
--              partner_share = amount * partner_pct
--              system_share = total_fee - agent_share - partner_share
-- ==========================================================================
CREATE TABLE commissions (
    id               UUID NOT NULL DEFAULT gen_random_uuid(),  -- Unique commission record identifier
    transaction_type VARCHAR(10) NOT NULL,                     -- 'deposit' or 'withdrawal'
    transaction_id   UUID NOT NULL,                            -- ID of the deposit_order or withdrawal
    merchant_id      UUID NOT NULL,                            -- Which merchant's transaction
    -- Commission breakdown (all in absolute amounts, not percentages)
    total_fee_amount NUMERIC(12,2) NOT NULL,                   -- Total fee collected from merchant
    system_amount    NUMERIC(12,2) NOT NULL,                   -- System's share (remainder after agent + partner)
    agent_id         UUID,                                     -- Agent who gets commission (NULL if direct merchant)
    agent_amount     NUMERIC(12,2) NOT NULL DEFAULT 0,         -- Agent's commission amount
    partner_id       UUID,                                     -- Partner (account owner) who gets commission
    partner_amount   NUMERIC(12,2) NOT NULL DEFAULT 0,         -- Partner's commission amount
    -- Rate snapshot (saved at time of transaction for audit trail)
    merchant_fee_pct NUMERIC(5,4) NOT NULL,                    -- Merchant's fee rate at time of transaction
    agent_pct        NUMERIC(5,4) NOT NULL DEFAULT 0,          -- Agent's commission rate at time of transaction
    partner_pct      NUMERIC(5,4) NOT NULL DEFAULT 0,          -- Partner's commission rate at time of transaction
    currency         VARCHAR(3) NOT NULL DEFAULT 'THB',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);

CREATE TABLE commissions_2026_04 PARTITION OF commissions
    FOR VALUES FROM ('2026-04-01') TO ('2026-05-01');
CREATE TABLE commissions_2026_05 PARTITION OF commissions
    FOR VALUES FROM ('2026-05-01') TO ('2026-06-01');
CREATE TABLE commissions_2026_06 PARTITION OF commissions
    FOR VALUES FROM ('2026-06-01') TO ('2026-07-01');

-- Index: Merchant's commission history
CREATE INDEX idx_comm_merchant ON commissions(merchant_id, created_at DESC);
-- Index: Agent's commission (only for records that have an agent)
CREATE INDEX idx_comm_agent ON commissions(agent_id, created_at DESC) WHERE agent_id IS NOT NULL;
-- Index: Partner's commission (only for records that have a partner)
CREATE INDEX idx_comm_partner ON commissions(partner_id, created_at DESC) WHERE partner_id IS NOT NULL;

-- ==========================================================================
-- 13. COMMISSION DAILY SUMMARY - Pre-aggregated daily totals (NOT partitioned)
--     This table is NEVER archived - it stays forever for fast dashboard queries.
--     Instead of scanning millions of commission rows, dashboards read from here.
--     Updated by scheduler-service at end of each day.
-- ==========================================================================
CREATE TABLE commission_daily_summary (
    id               BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    summary_date     DATE NOT NULL,                            -- The date this summary covers
    owner_type       VARCHAR(10) NOT NULL,                     -- 'system', 'agent', 'merchant', or 'partner'
    owner_id         UUID NOT NULL,                            -- The owner's ID
    transaction_type VARCHAR(10) NOT NULL,                     -- 'deposit' or 'withdrawal'
    currency         VARCHAR(3) NOT NULL DEFAULT 'THB',
    total_tx_count   INT NOT NULL DEFAULT 0,                   -- Total number of transactions that day
    total_volume     NUMERIC(16,2) NOT NULL DEFAULT 0,         -- Total transaction volume (sum of amounts)
    total_fee        NUMERIC(14,2) NOT NULL DEFAULT 0,         -- Total fees collected
    total_commission NUMERIC(14,2) NOT NULL DEFAULT 0,         -- Total commission earned by this owner
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- One summary per owner per day per transaction type per currency
    UNIQUE(summary_date, owner_type, owner_id, transaction_type, currency)
);
-- Index: Dashboard query - get owner's summaries sorted by date
CREATE INDEX idx_cds_owner ON commission_daily_summary(owner_type, owner_id, summary_date DESC);

-- ==========================================================================
-- 14. SMS MESSAGES - Raw SMS messages received from bank account monitoring (PARTITIONED)
--     Every SMS from bank notification is stored here for audit and debugging.
--     parser-service parses these and attempts to match with pending orders.
-- ==========================================================================
CREATE TABLE sms_messages (
    id               UUID NOT NULL DEFAULT gen_random_uuid(),  -- Unique SMS record identifier
    sender_number    VARCHAR(20) NOT NULL,                     -- Phone number that sent this SMS
    raw_message      TEXT NOT NULL,                            -- Full raw SMS text as received
    -- Parsed data (filled by parser-service)
    bank_code        VARCHAR(10),                              -- Detected bank code (e.g. 'KBANK', 'SCB')
    parsed_amount    NUMERIC(12,2),                            -- Extracted payment amount
    parsed_ref       VARCHAR(100),                             -- Extracted transaction reference
    parsed_timestamp TIMESTAMPTZ,                              -- Extracted transaction timestamp from SMS text
    -- Matching
    bank_account_id  UUID,                                     -- Which bank account this SMS belongs to
    matched_order_id UUID,                                     -- Which deposit order was matched (NULL if unmatched)
    -- Match status: 'pending' = not yet processed, 'matched' = successfully matched to an order,
    --              'unmatched' = no matching order found, 'duplicate' = same transaction already processed,
    --              'spoofing_suspect' = possible fake SMS detected
    match_status     VARCHAR(20) NOT NULL DEFAULT 'pending',
    -- Validation
    is_valid         BOOLEAN,                                  -- Whether this SMS passed anti-spoofing checks
    validation_notes VARCHAR(500),                             -- Details about validation (e.g. "sender not in whitelist")
    received_at      TIMESTAMPTZ NOT NULL DEFAULT now(),       -- When the SMS was received by our system
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);

CREATE TABLE sms_messages_2026_04 PARTITION OF sms_messages
    FOR VALUES FROM ('2026-04-01') TO ('2026-05-01');
CREATE TABLE sms_messages_2026_05 PARTITION OF sms_messages
    FOR VALUES FROM ('2026-05-01') TO ('2026-06-01');
CREATE TABLE sms_messages_2026_06 PARTITION OF sms_messages
    FOR VALUES FROM ('2026-06-01') TO ('2026-07-01');

-- Index: Find pending SMS for a bank account with specific amount (HOT PATH for matching)
CREATE INDEX idx_sms_pending ON sms_messages(bank_account_id, parsed_amount, match_status)
    WHERE match_status = 'pending';

-- ==========================================================================
-- 15. SLIP VERIFICATIONS - Payment slip images verified via Telegram (PARTITIONED)
--     Fallback flow when SMS/Email doesn't catch the payment.
--     Merchant sends slip photo to their Telegram group -> Bot processes it.
--     Uses EasySlip API to verify slip authenticity and extract transaction data.
-- ==========================================================================
CREATE TABLE slip_verifications (
    id                  UUID NOT NULL DEFAULT gen_random_uuid(),  -- Unique verification record identifier
    merchant_id         UUID NOT NULL,                            -- Which merchant submitted this slip
    telegram_group_id   BIGINT NOT NULL,                          -- Telegram group where slip was submitted
    telegram_message_id BIGINT NOT NULL,                          -- Telegram message ID (for reference/reply)
    -- Image data
    image_hash          VARCHAR(64) NOT NULL,                     -- SHA-256 hash of slip image (for duplicate detection)
    image_url           VARCHAR(500),                             -- Stored image URL/path
    -- EasySlip API verification results
    easyslip_ref        VARCHAR(100),                             -- Transaction reference from EasySlip
    easyslip_amount     NUMERIC(12,2),                            -- Payment amount extracted from slip
    easyslip_sender     VARCHAR(200),                             -- Sender name from slip
    easyslip_receiver   VARCHAR(200),                             -- Receiver name from slip
    easyslip_timestamp  TIMESTAMPTZ,                              -- Transaction timestamp from slip
    easyslip_raw        JSONB,                                    -- Full raw response from EasySlip API (for debugging)
    -- Matching
    matched_order_id    UUID,                                     -- Which deposit order was matched (NULL if no match)
    -- Status: 'pending' = processing, 'verified' = slip is real and matched,
    --         'duplicate_slip' = this slip was already used, 'no_match' = real slip but no matching order,
    --         'api_error' = EasySlip API call failed
    status              VARCHAR(20) NOT NULL DEFAULT 'pending',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);

CREATE TABLE slip_verifications_2026_04 PARTITION OF slip_verifications
    FOR VALUES FROM ('2026-04-01') TO ('2026-05-01');
CREATE TABLE slip_verifications_2026_05 PARTITION OF slip_verifications
    FOR VALUES FROM ('2026-05-01') TO ('2026-06-01');
CREATE TABLE slip_verifications_2026_06 PARTITION OF slip_verifications
    FOR VALUES FROM ('2026-06-01') TO ('2026-07-01');

-- Index: Prevent duplicate slips (same image submitted twice)
CREATE UNIQUE INDEX idx_slip_hash ON slip_verifications(image_hash, created_at);
-- Index: Prevent reuse of same transaction ref (same real slip used for different orders)
CREATE INDEX idx_slip_ref ON slip_verifications(easyslip_ref, created_at) WHERE easyslip_ref IS NOT NULL;

-- ==========================================================================
-- 16. FUND TRANSFERS - Records of money moved from receiving accounts to holding accounts
--     Admin transfers money from partner's bank accounts (receiving) to system's
--     holding accounts for safety. Can be manual or automatic (threshold-based).
-- ==========================================================================
CREATE TABLE fund_transfers (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),  -- Unique transfer record identifier
    from_bank_account_id  UUID NOT NULL REFERENCES bank_accounts(id),  -- Source: partner's receiving account
    to_holding_account_id UUID NOT NULL REFERENCES holding_accounts(id), -- Destination: system's holding account (MUST be pre-registered)
    amount                NUMERIC(14,2) NOT NULL,                       -- Transfer amount
    trigger_type          VARCHAR(10) NOT NULL,                         -- 'manual' = admin clicked transfer, 'auto' = threshold triggered
    -- Status: 'pending' = initiated, 'completed' = money arrived, 'failed' = transfer failed
    status                VARCHAR(20) NOT NULL DEFAULT 'pending',
    initiated_by_admin    UUID REFERENCES admins(id),                   -- Which admin initiated this (NULL for auto transfers)
    transfer_reference    VARCHAR(100),                                 -- Bank transfer reference number
    notes                 VARCHAR(500),                                 -- Admin notes about this transfer
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at          TIMESTAMPTZ                                  -- When transfer was confirmed complete
);

-- ==========================================================================
-- 17. AUDIT LOGS - Complete record of every action in the system (PARTITIONED)
--     Tracks WHO did WHAT to WHICH resource and WHEN.
--     Used for security review, compliance, and debugging.
-- ==========================================================================
CREATE TABLE audit_logs (
    id              BIGINT GENERATED ALWAYS AS IDENTITY,       -- Auto-incrementing unique ID
    actor_type      VARCHAR(10) NOT NULL,                      -- Who performed the action: 'admin', 'agent', 'merchant', 'partner', 'system'
    actor_id        UUID,                                      -- The actor's user ID (NULL for system actions)
    action          VARCHAR(100) NOT NULL,                     -- What was done (e.g. 'merchant.create', 'withdrawal.approve', 'emergency.freeze')
    resource_type   VARCHAR(50),                               -- What type of resource was affected (e.g. 'merchant', 'withdrawal', 'bank_account')
    resource_id     UUID,                                      -- ID of the affected resource
    ip_address      INET,                                      -- Actor's IP address
    user_agent      VARCHAR(500),                              -- Actor's browser user agent string
    request_data    JSONB,                                     -- Sanitized request data (passwords/secrets REMOVED)
    response_status SMALLINT,                                  -- HTTP response status code
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);

CREATE TABLE audit_logs_2026_04 PARTITION OF audit_logs
    FOR VALUES FROM ('2026-04-01') TO ('2026-05-01');
CREATE TABLE audit_logs_2026_05 PARTITION OF audit_logs
    FOR VALUES FROM ('2026-05-01') TO ('2026-06-01');
CREATE TABLE audit_logs_2026_06 PARTITION OF audit_logs
    FOR VALUES FROM ('2026-06-01') TO ('2026-07-01');

-- Index: Find actions by a specific user
CREATE INDEX idx_audit_actor ON audit_logs(actor_type, actor_id, created_at DESC);
-- Index: Find all actions on a specific resource (e.g. all changes to merchant X)
CREATE INDEX idx_audit_resource ON audit_logs(resource_type, resource_id, created_at DESC);
-- Index: Find all occurrences of a specific action type
CREATE INDEX idx_audit_action ON audit_logs(action, created_at DESC);

-- ==========================================================================
-- 18. SYSTEM CONFIG - Global system settings (key-value store)
--     Used for system-wide settings that can be changed at runtime.
-- ==========================================================================
CREATE TABLE system_config (
    key        VARCHAR(100) PRIMARY KEY,                       -- Setting name (e.g. 'emergency_freeze', 'default_deposit_timeout')
    value      JSONB NOT NULL,                                 -- Setting value as JSON (flexible structure)
    updated_by UUID,                                           -- Which admin last changed this setting
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Insert default system configuration
INSERT INTO system_config (key, value) VALUES
    ('emergency_freeze', '{"enabled": false}'::jsonb),                        -- Emergency freeze: stops ALL deposits, withdrawals, and fund transfers
    ('default_deposit_timeout', '{"seconds": 300}'::jsonb),                   -- Default order expiry: 5 minutes
    ('global_rate_limits', '{"per_minute": 60, "per_day": 10000}'::jsonb);    -- API rate limits per merchant

-- ==========================================================================
-- 19. WEBHOOK DELIVERIES - Tracks webhook delivery attempts to merchants (PARTITIONED)
--     When a deposit matches, system sends webhook to merchant's callback URL.
--     Retries up to 5 times with exponential backoff (10s, 30s, 90s, 270s, 810s).
-- ==========================================================================
CREATE TABLE webhook_deliveries (
    id              UUID NOT NULL DEFAULT gen_random_uuid(),    -- Unique delivery record identifier
    merchant_id     UUID NOT NULL,                              -- Which merchant this webhook is for
    order_id        UUID NOT NULL,                              -- Which order triggered this webhook
    url             VARCHAR(500) NOT NULL,                      -- Destination URL (merchant's webhook_url)
    payload         JSONB NOT NULL,                             -- Full webhook payload (JSON body sent to merchant)
    response_status SMALLINT,                                   -- HTTP status code received from merchant (NULL if no response yet)
    response_body   TEXT,                                       -- Response body from merchant (for debugging)
    attempt         SMALLINT NOT NULL DEFAULT 1,                -- Current attempt number (1-5)
    -- Status: 'pending' = not yet sent, 'success' = merchant returned 2xx,
    --         'failed' = merchant returned error (will retry), 'exhausted' = all 5 retries failed
    status          VARCHAR(20) NOT NULL DEFAULT 'pending',
    next_retry_at   TIMESTAMPTZ,                               -- When to retry next (NULL if not scheduled)
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);

CREATE TABLE webhook_deliveries_2026_04 PARTITION OF webhook_deliveries
    FOR VALUES FROM ('2026-04-01') TO ('2026-05-01');
CREATE TABLE webhook_deliveries_2026_05 PARTITION OF webhook_deliveries
    FOR VALUES FROM ('2026-05-01') TO ('2026-06-01');
CREATE TABLE webhook_deliveries_2026_06 PARTITION OF webhook_deliveries
    FOR VALUES FROM ('2026-06-01') TO ('2026-07-01');

-- ==========================================================================
-- 20. COMMISSION PAYOUTS - Tracks actual money transfers to agents/partners
--     Admin initiates payout for a date range -> transfers money -> marks complete.
--     This is separate from commission_daily_summary which tracks earned amounts.
-- ==========================================================================
CREATE TABLE commission_payouts (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),  -- Unique payout record identifier
    owner_type         VARCHAR(10) NOT NULL,                        -- Who is being paid: 'agent' or 'partner'
    owner_id           UUID NOT NULL,                               -- The recipient's ID
    amount             NUMERIC(14,2) NOT NULL,                      -- Total payout amount
    currency           VARCHAR(3) NOT NULL DEFAULT 'THB',
    period_from        DATE NOT NULL,                               -- Payout covers commissions from this date
    period_to          DATE NOT NULL,                               -- Payout covers commissions until this date
    transfer_reference VARCHAR(100),                                -- Bank transfer reference number
    initiated_by_admin UUID REFERENCES admins(id),                  -- Which admin initiated this payout
    -- Status: 'pending' = created, 'completed' = money transferred, 'cancelled'
    status             VARCHAR(20) NOT NULL DEFAULT 'pending',
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at       TIMESTAMPTZ                                 -- When payout was confirmed complete
);
-- Index: Find payouts for a specific agent/partner
CREATE INDEX idx_cp_owner ON commission_payouts(owner_type, owner_id, created_at DESC);
