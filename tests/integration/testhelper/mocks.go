// Package testhelper ให้ mock implementations สำหรับ integration tests
// ทุก mock ในไฟล์นี้ implement interface ที่ duplicate จาก repository/service layer
// ของแต่ละ service เพื่อให้ test สามารถจำลองพฤติกรรมของ database, Redis,
// และ inter-service calls โดยไม่ต้องเชื่อมต่อกับ infrastructure จริง
//
// หมายเหตุ: เนื่องจาก integration tests อยู่ใน module แยก ไม่สามารถ import
// internal packages ของ service ได้โดยตรง ดังนั้น types ที่จำเป็น
// (เช่น SMSMessage, PendingOrderInfo, SlipVerification) จะถูก duplicate ไว้ที่นี่
// เพื่อให้ test สามารถทำงานได้อย่างอิสระ
//
// Mock ทุกตัวเป็น thread-safe (ใช้ sync.Mutex) เพราะ service layer อาจเรียกใช้
// จาก goroutine หลายตัวพร้อมกัน
//
// การออกแบบ mock:
// - ใช้ in-memory map แทน database/Redis
// - มี field สำหรับ inject error เพื่อทดสอบ error handling
// - มี counter/log สำหรับตรวจสอบว่า method ถูกเรียกกี่ครั้ง
package testhelper

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	apperrors "github.com/farritpcz/richpayment/pkg/errors"
	"github.com/farritpcz/richpayment/pkg/models"
)

// ---------------------------------------------------------------------------
// Duplicated error sentinels (from wallet internal repository)
// เนื่องจากไม่สามารถ import wallet internal package ได้โดยตรง
// จึง duplicate error sentinels ไว้ที่นี่
// ---------------------------------------------------------------------------

// ErrWalletNotFound ใช้เมื่อไม่พบ wallet ที่ตรงกับ query criteria
var ErrWalletNotFound = errors.New("wallet not found")

// ErrVersionConflict ใช้เมื่อ optimistic lock version ไม่ตรง
// เกิดเมื่อมีคน update wallet พร้อมกัน (concurrent access)
var ErrVersionConflict = errors.New("wallet version conflict: row was modified by another transaction")

// ---------------------------------------------------------------------------
// Duplicated types from parser internal repository
// เนื่องจากไม่สามารถ import parser internal package ได้โดยตรง
// จึง duplicate types ที่จำเป็นไว้ที่นี่
// ---------------------------------------------------------------------------

// SMSMessage duplicate จาก parser/internal/repository.SMSMessage
// ใช้สำหรับ mock SMS persistence ใน parser-service flow
type SMSMessage struct {
	// ID คือ unique identifier ของ SMS record (UUID v4)
	ID uuid.UUID

	// BankAccountID คือ bank account ที่รับเงิน
	BankAccountID uuid.UUID

	// BankCode เช่น "KBANK", "SCB"
	BankCode string

	// SenderNumber คือ sender ของ SMS
	SenderNumber string

	// Amount คือจำนวนเงินที่ parse ได้จาก SMS
	Amount decimal.Decimal

	// SenderName คือชื่อผู้โอน
	SenderName string

	// Reference คือ bank transaction reference
	Reference string

	// RawMessage คือ SMS text ดั้งเดิม
	RawMessage string

	// MatchedOrderID คือ order ที่ match (nil ถ้าไม่ match)
	MatchedOrderID *uuid.UUID

	// Status เช่น "matched", "unmatched", "error"
	Status string

	// ReceivedAt คือเวลาที่ SMS gateway รับ SMS
	ReceivedAt time.Time

	// CreatedAt คือเวลาที่ record ถูกสร้าง
	CreatedAt time.Time
}

// PendingOrderInfo duplicate จาก parser/internal/repository.PendingOrderInfo
// ใช้สำหรับ mock order matching
type PendingOrderInfo struct {
	// OrderID คือ UUID ของ deposit order
	OrderID uuid.UUID

	// BankAccountID คือ bank account ที่ลูกค้าโอนเข้า
	BankAccountID uuid.UUID

	// Amount คือ adjusted amount ที่ลูกค้าต้องโอน
	Amount decimal.Decimal
}

// ---------------------------------------------------------------------------
// Duplicated types from telegram internal repository
// เนื่องจากไม่สามารถ import telegram internal package ได้โดยตรง
// ---------------------------------------------------------------------------

// SlipVerificationStatus represents the outcome of a slip verification attempt
// duplicate จาก telegram/internal/repository.SlipVerificationStatus
type SlipVerificationStatus string

const (
	// SlipStatusVerified — slip verified and matched to order
	SlipStatusVerified SlipVerificationStatus = "verified"

	// SlipStatusDuplicate — slip (by image hash or transaction ref) already submitted
	SlipStatusDuplicate SlipVerificationStatus = "duplicate"

	// SlipStatusNoMatch — slip valid but no pending order matched
	SlipStatusNoMatch SlipVerificationStatus = "no_match"

	// SlipStatusAlreadyCompleted — matching order already completed by another channel
	SlipStatusAlreadyCompleted SlipVerificationStatus = "already_completed"

	// SlipStatusFailed — slip verification failed
	SlipStatusFailed SlipVerificationStatus = "failed"
)

// SlipVerification duplicate จาก telegram/internal/repository.SlipVerification
// ใช้สำหรับ mock slip verification persistence
type SlipVerification struct {
	// ID คือ unique identifier (UUID v4)
	ID uuid.UUID

	// MerchantID คือ merchant ที่เกี่ยวข้อง
	MerchantID uuid.UUID

	// TelegramGroupID คือ Telegram chat ID ของ group
	TelegramGroupID int64

	// TelegramMessageID คือ message ID ของ slip photo
	TelegramMessageID int

	// ImageHash คือ SHA-256 hex digest ของ image
	ImageHash string

	// TransactionRef คือ bank transaction reference จาก EasySlip
	TransactionRef string

	// Amount คือจำนวนเงินจาก slip
	Amount decimal.Decimal

	// SenderName คือชื่อผู้โอน
	SenderName string

	// ReceiverName คือชื่อผู้รับ
	ReceiverName string

	// OrderID คือ order ที่ match (nil ถ้าไม่ match)
	OrderID *uuid.UUID

	// Status คือผลลัพธ์ของ verification
	Status SlipVerificationStatus

	// StatusDetail คือคำอธิบายผลลัพธ์
	StatusDetail string

	// RawResponse คือ JSON response จาก EasySlip API
	RawResponse string

	// CreatedAt คือเวลาที่ record ถูกสร้าง
	CreatedAt time.Time
}

// ---------------------------------------------------------------------------
// MockOrderRepository — จำลอง order-service repository
// ---------------------------------------------------------------------------

// MockOrderRepository เป็น in-memory implementation ของ OrderRepository interface
// ใช้สำหรับ integration tests ที่ต้องการจำลอง deposit order CRUD operations
// โดยไม่ต้องเชื่อมต่อกับ PostgreSQL จริง
//
// ทุก method ตรวจสอบ ForceError ก่อน — ถ้า set ไว้จะ return error ทันที
// เพื่อให้ test สามารถจำลอง database failure ได้
type MockOrderRepository struct {
	// mu ป้องกัน concurrent access ไปยัง orders map
	mu sync.Mutex

	// orders เก็บ deposit orders ทั้งหมดที่ถูก Create
	// key คือ order UUID, value คือ pointer ไปยัง order
	orders map[uuid.UUID]*models.DepositOrder

	// ForceError ถ้า set เป็น non-nil ทุก method จะ return error นี้
	// ใช้สำหรับ test error handling path
	ForceError error

	// CreateCount นับจำนวนครั้งที่ Create ถูกเรียก
	// ใช้สำหรับ verify ว่า service เรียก repo ตามจำนวนที่คาดหวัง
	CreateCount int

	// UpdateStatusCalls เก็บ log ของทุกครั้งที่ UpdateStatus ถูกเรียก
	// แต่ละ entry เก็บ order ID, new status, และ update fields
	UpdateStatusCalls []UpdateStatusCall
}

// UpdateStatusCall เก็บข้อมูลของแต่ละ call ไปยัง UpdateStatus
// ใช้สำหรับ assert ว่า service ส่ง parameters ที่ถูกต้อง
type UpdateStatusCall struct {
	// ID คือ order UUID ที่ถูก update
	ID uuid.UUID

	// Status คือ status ใหม่ที่ถูก set
	Status models.OrderStatus

	// Fields คือ additional fields ที่ถูก update พร้อมกับ status
	Fields map[string]interface{}
}

// NewMockOrderRepository สร้าง MockOrderRepository ใหม่พร้อม map ว่าง
func NewMockOrderRepository() *MockOrderRepository {
	return &MockOrderRepository{
		orders: make(map[uuid.UUID]*models.DepositOrder),
	}
}

// Create เก็บ deposit order ลงใน in-memory map
// จำลอง INSERT INTO deposit_orders ใน PostgreSQL
// return error ถ้า ForceError ถูก set หรือ order ID ซ้ำ
func (m *MockOrderRepository) Create(_ context.Context, order *models.DepositOrder) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// ตรวจสอบ forced error ก่อน — ใช้สำหรับ test database failure
	if m.ForceError != nil {
		return m.ForceError
	}

	// เก็บ copy ของ order เพื่อป้องกัน caller จาก mutate หลัง Create
	clone := *order
	m.orders[order.ID] = &clone
	m.CreateCount++
	return nil
}

// GetByID ค้นหา deposit order จาก UUID
// จำลอง SELECT * FROM deposit_orders WHERE id = $1
// return ErrNotFound ถ้าไม่เจอ
func (m *MockOrderRepository) GetByID(_ context.Context, id uuid.UUID) (*models.DepositOrder, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// ตรวจสอบ forced error
	if m.ForceError != nil {
		return nil, m.ForceError
	}

	order, ok := m.orders[id]
	if !ok {
		return nil, apperrors.ErrNotFound
	}

	// Return copy เพื่อป้องกัน caller จาก mutate stored order
	clone := *order
	return &clone, nil
}

// UpdateStatus เปลี่ยน status ของ order และ apply additional fields
// จำลอง UPDATE deposit_orders SET status = $2, ... WHERE id = $1
// บันทึก call ลง UpdateStatusCalls สำหรับ assertion ใน test
func (m *MockOrderRepository) UpdateStatus(_ context.Context, id uuid.UUID, status models.OrderStatus, fields map[string]interface{}) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// ตรวจสอบ forced error
	if m.ForceError != nil {
		return m.ForceError
	}

	order, ok := m.orders[id]
	if !ok {
		return apperrors.ErrNotFound
	}

	// Apply status change
	order.Status = status

	// Apply additional fields ตาม key name
	for key, val := range fields {
		switch key {
		case "matched_by":
			if v, ok := val.(string); ok {
				order.MatchedBy = models.MatchedBy(v)
			}
		case "matched_at":
			if v, ok := val.(time.Time); ok {
				order.MatchedAt = &v
			}
		case "actual_amount":
			if v, ok := val.(decimal.Decimal); ok {
				order.ActualAmount = v
			}
		case "fee_amount":
			if v, ok := val.(decimal.Decimal); ok {
				order.FeeAmount = v
			}
		case "net_amount":
			if v, ok := val.(decimal.Decimal); ok {
				order.NetAmount = v
			}
		}
	}

	// บันทึก call สำหรับ test assertion
	m.UpdateStatusCalls = append(m.UpdateStatusCalls, UpdateStatusCall{
		ID:     id,
		Status: status,
		Fields: fields,
	})

	return nil
}

// FindPendingByAmount ค้นหา pending order ที่มี adjusted amount ตรงกับ amount ที่ให้
// จำลอง SELECT * FROM deposit_orders WHERE bank_account_id = $1 AND adjusted_amount = $2 AND status = 'pending'
// ใช้โดย time-based matcher strategy
func (m *MockOrderRepository) FindPendingByAmount(_ context.Context, bankAccountID uuid.UUID, amount decimal.Decimal) (*models.DepositOrder, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// ตรวจสอบ forced error
	if m.ForceError != nil {
		return nil, m.ForceError
	}

	// หา order ที่ match ทั้ง bank account, amount, และ status
	for _, order := range m.orders {
		if order.BankAccountID == bankAccountID &&
			order.AdjustedAmount.Equal(amount) &&
			order.Status == models.OrderStatusPending {
			clone := *order
			return &clone, nil
		}
	}

	return nil, apperrors.ErrNotFound
}

// FindExpired หา orders ที่ยัง pending แต่เลยเวลา expiry แล้ว
// จำลอง SELECT * FROM deposit_orders WHERE status = 'pending' AND expires_at < $1
func (m *MockOrderRepository) FindExpired(_ context.Context, before time.Time) ([]models.DepositOrder, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// ตรวจสอบ forced error
	if m.ForceError != nil {
		return nil, m.ForceError
	}

	var result []models.DepositOrder
	for _, order := range m.orders {
		if order.Status == models.OrderStatusPending && order.ExpiresAt.Before(before) {
			result = append(result, *order)
		}
	}
	return result, nil
}

// GetOrder เป็น helper method สำหรับ test — ดึง order จาก internal map โดยตรง
// ไม่ผ่าน ForceError check เพื่อให้ test สามารถ inspect state ได้เสมอ
func (m *MockOrderRepository) GetOrder(id uuid.UUID) *models.DepositOrder {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.orders[id]
}

// SeedOrder ใส่ order ลง internal map โดยตรง ไม่ผ่าน Create
// ใช้สำหรับ setup test data ก่อนรัน test case
func (m *MockOrderRepository) SeedOrder(order *models.DepositOrder) {
	m.mu.Lock()
	defer m.mu.Unlock()
	clone := *order
	m.orders[order.ID] = &clone
}

// ---------------------------------------------------------------------------
// MockWalletRepository — จำลอง wallet-service repository
// ---------------------------------------------------------------------------

// MockWalletRepository เป็น in-memory implementation ของ WalletRepository interface
// ใช้สำหรับ test wallet operations: credit, debit, hold, release
// รองรับ optimistic locking ผ่าน Version field เหมือน production
type MockWalletRepository struct {
	// mu ป้องกัน concurrent access
	mu sync.Mutex

	// wallets เก็บ wallet records keyed by wallet UUID
	wallets map[uuid.UUID]*models.Wallet

	// walletsByOwner เก็บ wallet UUID keyed by "ownerType:ownerID:currency"
	// ใช้สำหรับ GetByOwner lookup
	walletsByOwner map[string]uuid.UUID

	// ledger เก็บ ledger entries ทั้งหมด
	// ใช้สำหรับ verify ว่า credit/debit สร้าง audit trail ถูกต้อง
	ledger []models.WalletLedger

	// ForceError ถ้า set จะทำให้ทุก method return error
	ForceError error

	// ForceVersionConflict ถ้า set เป็น true จะทำให้ UpdateBalance
	// return ErrVersionConflict เสมอ — ใช้ test retry logic
	ForceVersionConflict bool
}

// NewMockWalletRepository สร้าง mock wallet repository ใหม่
func NewMockWalletRepository() *MockWalletRepository {
	return &MockWalletRepository{
		wallets:        make(map[uuid.UUID]*models.Wallet),
		walletsByOwner: make(map[string]uuid.UUID),
	}
}

// ownerKey สร้าง composite key สำหรับ walletsByOwner lookup
func ownerKey(ownerType models.OwnerType, ownerID uuid.UUID, currency string) string {
	return fmt.Sprintf("%s:%s:%s", ownerType, ownerID.String(), currency)
}

// GetByOwner ค้นหา wallet จาก owner type + owner ID + currency
// จำลอง SELECT * FROM wallets WHERE owner_type = $1 AND owner_id = $2 AND currency = $3
func (m *MockWalletRepository) GetByOwner(_ context.Context, ownerType models.OwnerType, ownerID uuid.UUID, currency string) (*models.Wallet, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ForceError != nil {
		return nil, m.ForceError
	}

	key := ownerKey(ownerType, ownerID, currency)
	walletID, ok := m.walletsByOwner[key]
	if !ok {
		return nil, ErrWalletNotFound
	}

	w := m.wallets[walletID]
	clone := *w
	return &clone, nil
}

// GetByID ค้นหา wallet จาก UUID
// จำลอง SELECT * FROM wallets WHERE id = $1
func (m *MockWalletRepository) GetByID(_ context.Context, id uuid.UUID) (*models.Wallet, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ForceError != nil {
		return nil, m.ForceError
	}

	w, ok := m.wallets[id]
	if !ok {
		return nil, ErrWalletNotFound
	}

	clone := *w
	return &clone, nil
}

// Create สร้าง wallet ใหม่ใน in-memory map
// จำลอง INSERT INTO wallets ... ON CONFLICT DO NOTHING
func (m *MockWalletRepository) Create(_ context.Context, wallet *models.Wallet) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ForceError != nil {
		return m.ForceError
	}

	// ON CONFLICT DO NOTHING behavior — ถ้ามี wallet อยู่แล้วจะ skip
	key := ownerKey(wallet.OwnerType, wallet.OwnerID, wallet.Currency)
	if _, exists := m.walletsByOwner[key]; exists {
		return nil // silent no-op like ON CONFLICT DO NOTHING
	}

	clone := *wallet
	m.wallets[wallet.ID] = &clone
	m.walletsByOwner[key] = wallet.ID
	return nil
}

// UpdateBalance อัพเดท balance และ hold_balance ของ wallet
// รองรับ optimistic locking — ถ้า version ไม่ตรงจะ return ErrVersionConflict
// จำลอง UPDATE wallets SET balance = $2, hold_balance = $3, version = version + 1 WHERE id = $1 AND version = $4
func (m *MockWalletRepository) UpdateBalance(_ context.Context, id uuid.UUID, newBalance, newHold string, expectedVersion int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ForceError != nil {
		return m.ForceError
	}

	// จำลอง version conflict สำหรับ test retry logic
	if m.ForceVersionConflict {
		return ErrVersionConflict
	}

	w, ok := m.wallets[id]
	if !ok {
		return ErrWalletNotFound
	}

	// ตรวจสอบ optimistic lock version
	if w.Version != expectedVersion {
		return ErrVersionConflict
	}

	// Parse new balance values
	bal, _ := decimal.NewFromString(newBalance)
	hold, _ := decimal.NewFromString(newHold)

	w.Balance = bal
	w.HoldBalance = hold
	w.Version++
	w.UpdatedAt = time.Now().UTC()

	return nil
}

// CreateLedgerEntry เพิ่ม ledger entry สำหรับ audit trail
// จำลอง INSERT INTO wallet_ledger ...
func (m *MockWalletRepository) CreateLedgerEntry(_ context.Context, entry *models.WalletLedger) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ForceError != nil {
		return m.ForceError
	}

	m.ledger = append(m.ledger, *entry)
	return nil
}

// SeedWallet ใส่ wallet ลง internal map โดยตรง
// ใช้สำหรับ setup test data ก่อนรัน test case
func (m *MockWalletRepository) SeedWallet(wallet *models.Wallet) {
	m.mu.Lock()
	defer m.mu.Unlock()

	clone := *wallet
	m.wallets[wallet.ID] = &clone
	key := ownerKey(wallet.OwnerType, wallet.OwnerID, wallet.Currency)
	m.walletsByOwner[key] = wallet.ID
}

// GetWallet ดึง wallet จาก internal map โดยตรง สำหรับ assertion ใน test
func (m *MockWalletRepository) GetWallet(id uuid.UUID) *models.Wallet {
	m.mu.Lock()
	defer m.mu.Unlock()
	if w, ok := m.wallets[id]; ok {
		clone := *w
		return &clone
	}
	return nil
}

// GetLedgerEntries return ทุก ledger entry ที่ถูกบันทึก
func (m *MockWalletRepository) GetLedgerEntries() []models.WalletLedger {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]models.WalletLedger, len(m.ledger))
	copy(result, m.ledger)
	return result
}

// ---------------------------------------------------------------------------
// MockCommissionRepository — จำลอง commission-service repository
// ---------------------------------------------------------------------------

// MockCommissionRepository เป็น in-memory implementation ของ CommissionRepository
// ใช้สำหรับ test commission calculation และ recording
type MockCommissionRepository struct {
	// mu ป้องกัน concurrent access
	mu sync.Mutex

	// commissions เก็บ commission records ทั้งหมด
	commissions []models.Commission

	// walletCredits เก็บ log ของทุก wallet credit operation
	walletCredits []WalletCreditCall

	// dailySummaries เก็บ daily summary records
	dailySummaries []models.CommissionDailySummary

	// ForceError ถ้า set จะทำให้ทุก method return error
	ForceError error
}

// WalletCreditCall เก็บข้อมูลของแต่ละ call ไปยัง CreditWallet
// ใช้สำหรับ verify ว่า commission service credit wallet ถูกต้อง
type WalletCreditCall struct {
	// WalletID คือ UUID ของ wallet ที่ถูก credit
	WalletID uuid.UUID

	// Amount คือจำนวนเงินที่ credit
	Amount decimal.Decimal

	// EntryType คือประเภทของ ledger entry
	EntryType models.LedgerEntryType

	// ReferenceID คือ UUID ของ entity ที่เป็นต้นเหตุ
	ReferenceID uuid.UUID

	// Description คือคำอธิบาย
	Description string
}

// NewMockCommissionRepository สร้าง mock commission repository ใหม่
func NewMockCommissionRepository() *MockCommissionRepository {
	return &MockCommissionRepository{}
}

// InsertCommission เก็บ commission record ลง in-memory slice
// จำลอง INSERT INTO commissions ...
func (m *MockCommissionRepository) InsertCommission(_ context.Context, c *models.Commission) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ForceError != nil {
		return m.ForceError
	}

	m.commissions = append(m.commissions, *c)
	return nil
}

// CreditWallet บันทึก wallet credit operation
// จำลอง UPDATE wallets SET balance = balance + $1 WHERE id = $2
func (m *MockCommissionRepository) CreditWallet(_ context.Context, walletID uuid.UUID, amount decimal.Decimal, entryType models.LedgerEntryType, referenceID uuid.UUID, description string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ForceError != nil {
		return m.ForceError
	}

	m.walletCredits = append(m.walletCredits, WalletCreditCall{
		WalletID:    walletID,
		Amount:      amount,
		EntryType:   entryType,
		ReferenceID: referenceID,
		Description: description,
	})
	return nil
}

// GetCommissionsByDate return commissions ที่อยู่ในวันที่ระบุ
// จำลอง SELECT * FROM commissions WHERE created_at >= start_of_day AND created_at < end_of_day
func (m *MockCommissionRepository) GetCommissionsByDate(_ context.Context, date time.Time) ([]models.Commission, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ForceError != nil {
		return nil, m.ForceError
	}

	startOfDay := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.UTC)
	endOfDay := startOfDay.Add(24 * time.Hour)

	var result []models.Commission
	for _, c := range m.commissions {
		if !c.CreatedAt.Before(startOfDay) && c.CreatedAt.Before(endOfDay) {
			result = append(result, c)
		}
	}
	return result, nil
}

// UpsertDailySummary เก็บ daily summary record
// จำลอง INSERT INTO commission_daily_summary ... ON CONFLICT DO UPDATE
func (m *MockCommissionRepository) UpsertDailySummary(_ context.Context, summary *models.CommissionDailySummary) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ForceError != nil {
		return m.ForceError
	}

	m.dailySummaries = append(m.dailySummaries, *summary)
	return nil
}

// GetDailySummaries return daily summaries ตาม owner และ date range
func (m *MockCommissionRepository) GetDailySummaries(_ context.Context, ownerType models.OwnerType, ownerID uuid.UUID, from, to time.Time) ([]models.CommissionDailySummary, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ForceError != nil {
		return nil, m.ForceError
	}

	var result []models.CommissionDailySummary
	for _, s := range m.dailySummaries {
		if s.OwnerType == ownerType && s.OwnerID == ownerID &&
			!s.SummaryDate.Before(from) && !s.SummaryDate.After(to) {
			result = append(result, s)
		}
	}
	return result, nil
}

// GetMonthlySummary return aggregated monthly summary
func (m *MockCommissionRepository) GetMonthlySummary(_ context.Context, ownerType models.OwnerType, ownerID uuid.UUID, year int, month time.Month) (*models.CommissionDailySummary, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ForceError != nil {
		return nil, m.ForceError
	}

	// Aggregate ทุก daily summary ในเดือนนั้น
	firstDay := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)
	lastDay := firstDay.AddDate(0, 1, -1)

	summary := &models.CommissionDailySummary{
		SummaryDate: firstDay,
		OwnerType:   ownerType,
		OwnerID:     ownerID,
	}

	for _, s := range m.dailySummaries {
		if s.OwnerType == ownerType && s.OwnerID == ownerID &&
			!s.SummaryDate.Before(firstDay) && !s.SummaryDate.After(lastDay) {
			summary.TotalTxCount += s.TotalTxCount
			summary.TotalVolume = summary.TotalVolume.Add(s.TotalVolume)
			summary.TotalFee = summary.TotalFee.Add(s.TotalFee)
			summary.TotalCommission = summary.TotalCommission.Add(s.TotalCommission)
		}
	}

	return summary, nil
}

// GetCommissions return ทุก commission ที่ถูกบันทึก — สำหรับ test assertion
func (m *MockCommissionRepository) GetCommissions() []models.Commission {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]models.Commission, len(m.commissions))
	copy(result, m.commissions)
	return result
}

// GetWalletCredits return ทุก wallet credit call — สำหรับ test assertion
func (m *MockCommissionRepository) GetWalletCredits() []WalletCreditCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]WalletCreditCall, len(m.walletCredits))
	copy(result, m.walletCredits)
	return result
}

// ---------------------------------------------------------------------------
// MockSMSRepository — จำลอง parser-service SMS repository
// ---------------------------------------------------------------------------

// MockSMSRepository เป็น in-memory implementation ของ SMSRepository
// ใช้สำหรับ test SMS persistence ใน parser-service flow
type MockSMSRepository struct {
	// mu ป้องกัน concurrent access
	mu sync.Mutex

	// messages เก็บ SMS messages ทั้งหมดที่ถูก Store
	messages []SMSMessage

	// ForceError ถ้า set จะทำให้ Store return error
	ForceError error
}

// NewMockSMSRepository สร้าง mock SMS repository ใหม่
func NewMockSMSRepository() *MockSMSRepository {
	return &MockSMSRepository{}
}

// Store เก็บ SMS message ลง in-memory slice
// จำลอง INSERT INTO sms_messages ...
func (m *MockSMSRepository) Store(_ context.Context, msg *SMSMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ForceError != nil {
		return m.ForceError
	}

	m.messages = append(m.messages, *msg)
	return nil
}

// GetMessages return ทุก SMS message ที่ถูก store — สำหรับ test assertion
func (m *MockSMSRepository) GetMessages() []SMSMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]SMSMessage, len(m.messages))
	copy(result, m.messages)
	return result
}

// ---------------------------------------------------------------------------
// MockOrderMatcher — จำลอง Redis-backed order matcher
// ---------------------------------------------------------------------------

// MockOrderMatcher เป็น in-memory implementation ของ OrderMatcher interface
// ใช้สำหรับ test SMS-to-order matching ใน parser-service
// สามารถ configure ให้ return specific order หรือ nil (no match) หรือ error
type MockOrderMatcher struct {
	// mu ป้องกัน concurrent access
	mu sync.Mutex

	// pendingOrders เก็บ pending orders ที่สามารถ match ได้
	// key คือ "bankAccountID:amount" string
	pendingOrders map[string]*PendingOrderInfo

	// ForceError ถ้า set จะทำให้ FindPendingOrder return error
	ForceError error

	// FindCallCount นับจำนวนครั้งที่ FindPendingOrder ถูกเรียก
	FindCallCount int
}

// NewMockOrderMatcher สร้าง mock order matcher ใหม่
func NewMockOrderMatcher() *MockOrderMatcher {
	return &MockOrderMatcher{
		pendingOrders: make(map[string]*PendingOrderInfo),
	}
}

// pendingKey สร้าง lookup key จาก bank account ID และ amount
func pendingKey(bankAccountID uuid.UUID, amount decimal.Decimal) string {
	return fmt.Sprintf("%s:%s", bankAccountID.String(), amount.String())
}

// AddPendingOrder เพิ่ม pending order ลง mock สำหรับ matching
// ใช้สำหรับ setup test data — จำลองว่ามี order รอ match อยู่ใน Redis
func (m *MockOrderMatcher) AddPendingOrder(info *PendingOrderInfo) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := pendingKey(info.BankAccountID, info.Amount)
	m.pendingOrders[key] = info
}

// FindPendingOrder ค้นหา pending order ที่ match กับ bank account + amount
// จำลอง Redis ZRANGEBYSCORE lookup ที่ order-service ใช้
func (m *MockOrderMatcher) FindPendingOrder(_ context.Context, bankAccountID uuid.UUID, amount decimal.Decimal) (*PendingOrderInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.FindCallCount++

	if m.ForceError != nil {
		return nil, m.ForceError
	}

	key := pendingKey(bankAccountID, amount)
	info, ok := m.pendingOrders[key]
	if !ok {
		// ไม่เจอ order — return nil, nil (ไม่ใช่ error แต่แค่ไม่ match)
		return nil, nil
	}

	// ลบ order ออกเพื่อจำลอง Redis ZREM หลัง match
	delete(m.pendingOrders, key)
	return info, nil
}

// ---------------------------------------------------------------------------
// MockWithdrawalWalletClient — จำลอง wallet client สำหรับ withdrawal-service
// ---------------------------------------------------------------------------

// MockWithdrawalWalletClient implement WalletClient interface ของ withdrawal-service
// ใช้สำหรับ test withdrawal flow: balance check, hold, release, debit
type MockWithdrawalWalletClient struct {
	// mu ป้องกัน concurrent access
	mu sync.Mutex

	// Balance คือ balance ที่จะ return จาก GetBalance
	Balance decimal.Decimal

	// HoldCalls นับจำนวนครั้งที่ HoldBalance ถูกเรียก
	HoldCalls int

	// ReleaseCalls นับจำนวนครั้งที่ ReleaseHold ถูกเรียก
	ReleaseCalls int

	// DebitCalls นับจำนวนครั้งที่ DebitHold ถูกเรียก
	DebitCalls int

	// ForceGetBalanceError ถ้า set จะทำให้ GetBalance return error
	ForceGetBalanceError error

	// ForceHoldError ถ้า set จะทำให้ HoldBalance return error
	ForceHoldError error

	// ForceReleaseError ถ้า set จะทำให้ ReleaseHold return error
	ForceReleaseError error

	// ForceDebitError ถ้า set จะทำให้ DebitHold return error
	ForceDebitError error
}

// GetBalance return configured balance
func (m *MockWithdrawalWalletClient) GetBalance(_ context.Context, _ uuid.UUID, _ string) (decimal.Decimal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ForceGetBalanceError != nil {
		return decimal.Zero, m.ForceGetBalanceError
	}
	return m.Balance, nil
}

// HoldBalance จำลอง hold operation — นับ call count
func (m *MockWithdrawalWalletClient) HoldBalance(_ context.Context, _ uuid.UUID, _ decimal.Decimal, _ string, _ uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ForceHoldError != nil {
		return m.ForceHoldError
	}
	m.HoldCalls++
	return nil
}

// ReleaseHold จำลอง release operation — นับ call count
func (m *MockWithdrawalWalletClient) ReleaseHold(_ context.Context, _ uuid.UUID, _ decimal.Decimal, _ string, _ uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ForceReleaseError != nil {
		return m.ForceReleaseError
	}
	m.ReleaseCalls++
	return nil
}

// DebitHold จำลอง debit operation — นับ call count
func (m *MockWithdrawalWalletClient) DebitHold(_ context.Context, _ uuid.UUID, _ decimal.Decimal, _ string, _ uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ForceDebitError != nil {
		return m.ForceDebitError
	}
	m.DebitCalls++
	return nil
}

// ---------------------------------------------------------------------------
// MockWithdrawalCommissionClient — จำลอง commission client สำหรับ withdrawal
// ---------------------------------------------------------------------------

// MockWithdrawalCommissionClient implement CommissionClient interface
// ของ withdrawal-service สำหรับ test commission recording
type MockWithdrawalCommissionClient struct {
	// mu ป้องกัน concurrent access
	mu sync.Mutex

	// RecordCalls นับจำนวนครั้งที่ RecordWithdrawalCommission ถูกเรียก
	RecordCalls int

	// LastFeeAmount เก็บ fee amount จาก call ล่าสุด — สำหรับ assertion
	LastFeeAmount decimal.Decimal

	// ForceError ถ้า set จะทำให้ RecordWithdrawalCommission return error
	ForceError error
}

// RecordWithdrawalCommission จำลอง commission recording — เก็บ fee amount
func (m *MockWithdrawalCommissionClient) RecordWithdrawalCommission(_ context.Context, _ uuid.UUID, _ uuid.UUID, feeAmount decimal.Decimal, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ForceError != nil {
		return m.ForceError
	}

	m.RecordCalls++
	m.LastFeeAmount = feeAmount
	return nil
}

// ---------------------------------------------------------------------------
// MockMerchantClient — จำลอง merchant config client สำหรับ withdrawal
// ---------------------------------------------------------------------------

// MockMerchantClient implement MerchantClient interface ของ withdrawal-service
// ใช้สำหรับ test การดึง merchant configuration (fee %, daily limit)
type MockMerchantClient struct {
	// FeePct คือ withdrawal fee percentage ที่จะ return
	FeePct decimal.Decimal

	// DailyLimit คือ daily withdrawal limit ที่จะ return
	DailyLimit decimal.Decimal

	// ForceError ถ้า set จะทำให้ทั้งสอง method return error
	ForceError error
}

// GetWithdrawalFeePct return configured fee percentage
func (m *MockMerchantClient) GetWithdrawalFeePct(_ context.Context, _ uuid.UUID) (decimal.Decimal, error) {
	if m.ForceError != nil {
		return decimal.Zero, m.ForceError
	}
	return m.FeePct, nil
}

// GetDailyWithdrawalLimit return configured daily limit
func (m *MockMerchantClient) GetDailyWithdrawalLimit(_ context.Context, _ uuid.UUID) (decimal.Decimal, error) {
	if m.ForceError != nil {
		return decimal.Zero, m.ForceError
	}
	return m.DailyLimit, nil
}

// ---------------------------------------------------------------------------
// MockWithdrawalRepository — จำลอง withdrawal repository สำหรับ integration tests
// ---------------------------------------------------------------------------

// MockWithdrawalRepository เป็น in-memory implementation ของ WithdrawalRepository interface
// ใช้สำหรับ integration tests ที่ต้องการจำลอง withdrawal CRUD operations
// โดยไม่ต้องเชื่อมต่อกับ PostgreSQL จริง
type MockWithdrawalRepository struct {
	// mu ป้องกัน concurrent access ไปยัง withdrawals map
	mu sync.Mutex

	// withdrawals เก็บ withdrawal records ทั้งหมดที่ถูก Create
	withdrawals map[uuid.UUID]*models.Withdrawal

	// ForceError ถ้า set เป็น non-nil ทุก method จะ return error นี้
	ForceError error

	// CreateCount นับจำนวนครั้งที่ Create ถูกเรียก
	CreateCount int

	// UpdateStatusCalls เก็บ log ของทุกครั้งที่ UpdateStatus ถูกเรียก
	UpdateStatusCalls []WithdrawalUpdateStatusCall
}

// WithdrawalUpdateStatusCall เก็บข้อมูลของแต่ละ call ไปยัง withdrawal UpdateStatus
type WithdrawalUpdateStatusCall struct {
	// ID คือ withdrawal UUID ที่ถูก update
	ID uuid.UUID

	// Status คือ status ใหม่ที่ถูก set
	Status models.WithdrawalStatus

	// Fields คือ additional fields ที่ถูก update พร้อมกับ status
	Fields map[string]interface{}
}

// NewMockWithdrawalRepository สร้าง MockWithdrawalRepository ใหม่พร้อม map ว่าง
func NewMockWithdrawalRepository() *MockWithdrawalRepository {
	return &MockWithdrawalRepository{
		withdrawals: make(map[uuid.UUID]*models.Withdrawal),
	}
}

// Create เก็บ withdrawal ลงใน in-memory map
func (m *MockWithdrawalRepository) Create(_ context.Context, w *models.Withdrawal) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ForceError != nil {
		return m.ForceError
	}

	clone := *w
	m.withdrawals[w.ID] = &clone
	m.CreateCount++
	return nil
}

// GetByID ค้นหา withdrawal จาก UUID
func (m *MockWithdrawalRepository) GetByID(_ context.Context, id uuid.UUID) (*models.Withdrawal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ForceError != nil {
		return nil, m.ForceError
	}

	w, ok := m.withdrawals[id]
	if !ok {
		return nil, apperrors.ErrNotFound
	}

	clone := *w
	return &clone, nil
}

// UpdateStatus เปลี่ยน status ของ withdrawal และ apply additional fields
func (m *MockWithdrawalRepository) UpdateStatus(_ context.Context, id uuid.UUID, status models.WithdrawalStatus, fields map[string]interface{}) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ForceError != nil {
		return m.ForceError
	}

	w, ok := m.withdrawals[id]
	if !ok {
		return apperrors.ErrNotFound
	}

	w.Status = status

	for key, val := range fields {
		switch key {
		case "approved_by":
			if v, ok := val.(*uuid.UUID); ok {
				w.ApprovedBy = v
			}
		case "approved_at":
			if v, ok := val.(*time.Time); ok {
				w.ApprovedAt = v
			}
		case "rejected_by":
			if v, ok := val.(*uuid.UUID); ok {
				w.RejectedBy = v
			}
		case "rejected_at":
			if v, ok := val.(*time.Time); ok {
				w.RejectedAt = v
			}
		case "rejection_reason":
			if v, ok := val.(string); ok {
				w.RejectionReason = v
			}
		case "transfer_ref":
			if v, ok := val.(string); ok {
				w.TransferRef = v
			}
		case "proof_url":
			if v, ok := val.(string); ok {
				w.ProofURL = v
			}
		case "completed_at":
			if v, ok := val.(*time.Time); ok {
				w.CompletedAt = v
			}
		case "fee_amount":
			if v, ok := val.(decimal.Decimal); ok {
				w.FeeAmount = v
			}
		case "net_amount":
			if v, ok := val.(decimal.Decimal); ok {
				w.NetAmount = v
			}
		}
	}

	m.UpdateStatusCalls = append(m.UpdateStatusCalls, WithdrawalUpdateStatusCall{
		ID:     id,
		Status: status,
		Fields: fields,
	})

	return nil
}

// ListPending return pending withdrawals พร้อม pagination
func (m *MockWithdrawalRepository) ListPending(_ context.Context, offset, limit int) ([]models.Withdrawal, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ForceError != nil {
		return nil, 0, m.ForceError
	}

	var pending []models.Withdrawal
	for _, w := range m.withdrawals {
		if w.Status == models.WithdrawalStatusPending {
			pending = append(pending, *w)
		}
	}

	total := len(pending)

	if offset >= len(pending) {
		return nil, total, nil
	}
	pending = pending[offset:]
	if limit > 0 && limit < len(pending) {
		pending = pending[:limit]
	}

	return pending, total, nil
}

// SumDailyWithdrawals คำนวณยอดรวม withdrawal ของ merchant ในวันที่ระบุ
func (m *MockWithdrawalRepository) SumDailyWithdrawals(_ context.Context, merchantID uuid.UUID, date time.Time) (decimal.Decimal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ForceError != nil {
		return decimal.Zero, m.ForceError
	}

	sum := decimal.Zero
	dayStart := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.UTC)
	dayEnd := dayStart.Add(24 * time.Hour)

	for _, w := range m.withdrawals {
		if w.MerchantID != merchantID {
			continue
		}
		if w.CreatedAt.Before(dayStart) || !w.CreatedAt.Before(dayEnd) {
			continue
		}
		if w.Status == models.WithdrawalStatusRejected || w.Status == models.WithdrawalStatusFailed {
			continue
		}
		sum = sum.Add(w.Amount)
	}

	return sum, nil
}

// GetWithdrawal ดึง withdrawal จาก internal map โดยตรง สำหรับ assertion ใน test
func (m *MockWithdrawalRepository) GetWithdrawal(id uuid.UUID) *models.Withdrawal {
	m.mu.Lock()
	defer m.mu.Unlock()
	if w, ok := m.withdrawals[id]; ok {
		clone := *w
		return &clone
	}
	return nil
}

// SeedWithdrawal ใส่ withdrawal ลง internal map โดยตรง
func (m *MockWithdrawalRepository) SeedWithdrawal(w *models.Withdrawal) {
	m.mu.Lock()
	defer m.mu.Unlock()
	clone := *w
	m.withdrawals[w.ID] = &clone
}

// ---------------------------------------------------------------------------
// MockSlipRepository — จำลอง slip verification repository สำหรับ integration tests
// ---------------------------------------------------------------------------

// MockSlipRepository เป็น in-memory implementation ของ SlipRepository interface
// ใช้สำหรับ integration tests ที่ต้องการจำลอง slip verification CRUD
type MockSlipRepository struct {
	// mu ป้องกัน concurrent access
	mu sync.Mutex

	// records เก็บ slip verification records ทั้งหมด
	records []*SlipVerification

	// ForceError ถ้า set จะทำให้ทุก method return error
	ForceError error

	// CreateCount นับจำนวนครั้งที่ Create ถูกเรียก
	CreateCount int
}

// NewMockSlipRepository สร้าง MockSlipRepository ใหม่
func NewMockSlipRepository() *MockSlipRepository {
	return &MockSlipRepository{
		records: make([]*SlipVerification, 0),
	}
}

// Create เก็บ slip verification record ลง in-memory slice
func (m *MockSlipRepository) Create(_ context.Context, sv *SlipVerification) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ForceError != nil {
		return m.ForceError
	}

	clone := *sv
	m.records = append(m.records, &clone)
	m.CreateCount++
	return nil
}

// GetByImageHash ค้นหา slip verification จาก SHA-256 image hash
// return nil ถ้าไม่เจอ (ไม่ใช่ error แค่ไม่มี duplicate)
func (m *MockSlipRepository) GetByImageHash(_ context.Context, imageHash string) (*SlipVerification, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ForceError != nil {
		return nil, m.ForceError
	}

	for _, rec := range m.records {
		if rec.ImageHash == imageHash {
			return rec, nil
		}
	}
	return nil, nil
}

// GetByTransactionRef ค้นหา slip verification จาก bank transaction reference
// return nil ถ้าไม่เจอ
func (m *MockSlipRepository) GetByTransactionRef(_ context.Context, ref string) (*SlipVerification, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ForceError != nil {
		return nil, m.ForceError
	}

	for _, rec := range m.records {
		if rec.TransactionRef == ref {
			return rec, nil
		}
	}
	return nil, nil
}

// GetRecords return ทุก slip verification record — สำหรับ test assertion
func (m *MockSlipRepository) GetRecords() []*SlipVerification {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]*SlipVerification, len(m.records))
	copy(result, m.records)
	return result
}

// SeedRecord ใส่ slip verification record ลง internal slice โดยตรง
func (m *MockSlipRepository) SeedRecord(sv *SlipVerification) {
	m.mu.Lock()
	defer m.mu.Unlock()
	clone := *sv
	m.records = append(m.records, &clone)
}
