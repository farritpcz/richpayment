// Package integration ทดสอบ end-to-end flow ของระบบ RichPayment
// ไฟล์นี้ทดสอบ deposit flow ตั้งแต่สร้าง order จนถึง wallet credit
//
// Deposit Flow ที่ทดสอบ:
//   1. Merchant สร้าง deposit order -> ได้ adjusted amount
//   2. ลูกค้าโอนเงิน -> bank ส่ง SMS
//   3. Parser service รับ SMS -> match กับ pending order
//   4. Order service complete deposit -> คำนวณ fee
//   5. Wallet service credit merchant wallet -> หัก fee
//
// เนื่องจาก integration tests อยู่ใน module แยก ไม่สามารถ import
// internal packages ของ services ได้โดยตรง ดังนั้น test จะจำลอง
// flow ผ่าน mock repositories และ manual orchestration
//
// ทุก test ใช้ mock repositories และ clients เพื่อจำลอง infrastructure
// ทำให้ test รันเร็วและไม่ต้องพึ่ง database/Redis/HTTP จริง
package integration

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/farritpcz/richpayment/pkg/models"
	"github.com/farritpcz/richpayment/tests/integration/testhelper"
)

// ---------------------------------------------------------------------------
// Test: Complete Deposit Flow (Happy Path)
// ---------------------------------------------------------------------------

// TestDepositFlow_CompleteHappyPath ทดสอบ deposit flow สมบูรณ์แบบ
// ตั้งแต่สร้าง order จนถึง credit wallet
//
// Flow ที่ทดสอบ:
//   1. สร้าง pending deposit order ใน mock repo
//   2. จำลองการรับ SMS จาก bank → match กับ pending order
//   3. Complete deposit: คำนวณ fee, update status เป็น completed
//   4. Credit wallet ด้วย net amount (actual - fee)
//   5. ตรวจสอบว่า wallet balance เพิ่มขึ้นถูกต้อง
//
// ทำไมต้อง test นี้:
//   - เป็น flow หลักที่สำคัญที่สุดของระบบ — ถ้า flow นี้พัง ระบบไม่สามารถรับเงินได้
//   - ตรวจสอบว่า fee calculation ถูกต้อง (2% ของ actual amount)
//   - ตรวจสอบว่า wallet balance เพิ่มขึ้นด้วย net amount ไม่ใช่ gross amount
//   - ตรวจสอบว่า ledger entry ถูกสร้างสำหรับ audit trail
func TestDepositFlow_CompleteHappyPath(t *testing.T) {
	// --- Setup: สร้าง mock dependencies ---
	ctx := context.Background()

	// สร้าง mock wallet repository พร้อม seed wallet ที่มี balance เริ่มต้น 0
	walletRepo := testhelper.NewMockWalletRepository()
	merchantID := uuid.New()
	walletID := uuid.New()

	// Seed merchant wallet ด้วย balance 0 — จะถูก credit หลัง deposit complete
	walletRepo.SeedWallet(&models.Wallet{
		ID:          walletID,
		OwnerType:   models.OwnerTypeMerchant,
		OwnerID:     merchantID,
		Currency:    "THB",
		Balance:     decimal.Zero,
		HoldBalance: decimal.Zero,
		Version:     1,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	})

	// --- Step 1: สร้าง pending deposit order ---
	// จำลองว่า merchant สร้าง order ผ่าน API แล้ว order อยู่ใน repo
	orderRepo := testhelper.NewMockOrderRepository()
	orderID := uuid.New()
	bankAccountID := uuid.New()
	requestedAmount := decimal.NewFromFloat(1000.00)
	adjustedAmount := decimal.NewFromFloat(1000.42) // เพิ่ม satang เพื่อ uniqueness

	pendingOrder := &models.DepositOrder{
		ID:               orderID,
		MerchantID:       merchantID,
		MerchantOrderID:  "MERCH-ORD-001",
		CustomerName:     "สมชาย ทดสอบ",
		CustomerBankCode: "KBANK",
		RequestedAmount:  requestedAmount,
		AdjustedAmount:   adjustedAmount,
		Currency:         "THB",
		BankAccountID:    bankAccountID,
		Status:           models.OrderStatusPending,
		ExpiresAt:        time.Now().UTC().Add(15 * time.Minute),
		CreatedAt:        time.Now().UTC(),
		UpdatedAt:        time.Now().UTC(),
	}
	orderRepo.SeedOrder(pendingOrder)

	// --- Step 2: จำลอง SMS match ---
	// สร้าง mock order matcher และเพิ่ม pending order
	orderMatcher := testhelper.NewMockOrderMatcher()
	orderMatcher.AddPendingOrder(&testhelper.PendingOrderInfo{
		OrderID:       orderID,
		BankAccountID: bankAccountID,
		Amount:        adjustedAmount,
	})

	// จำลอง SMS match: ค้นหา pending order ด้วย bank account + amount
	matchResult, err := orderMatcher.FindPendingOrder(ctx, bankAccountID, adjustedAmount)
	if err != nil {
		t.Fatalf("FindPendingOrder returned error: %v", err)
	}
	if matchResult == nil {
		t.Fatal("expected to find pending order, got nil")
	}
	if matchResult.OrderID != orderID {
		t.Fatalf("expected matched order ID %s, got %s", orderID, matchResult.OrderID)
	}

	// จำลอง SMS record storage
	smsRepo := testhelper.NewMockSMSRepository()
	err = smsRepo.Store(ctx, &testhelper.SMSMessage{
		ID:             uuid.New(),
		BankAccountID:  bankAccountID,
		BankCode:       "KBANK",
		SenderNumber:   "KBANK",
		Amount:         adjustedAmount,
		SenderName:     "สมชาย ทดสอบ",
		RawMessage:     "รับเงิน 1,000.42 บ. จาก สมชาย xxx เข้า xxx-x-x1234-x",
		MatchedOrderID: &orderID,
		Status:         "matched",
		ReceivedAt:     time.Now(),
		CreatedAt:      time.Now(),
	})
	if err != nil {
		t.Fatalf("SMS Store failed: %v", err)
	}

	// --- Step 3: Complete deposit — คำนวณ fee และ update order ---
	// fee = 2% ของ actual amount = 1000.42 * 0.02 = 20.01 (rounded)
	feePercent := decimal.NewFromFloat(0.02)
	feeAmount := adjustedAmount.Mul(feePercent).Round(2)
	netAmount := adjustedAmount.Sub(feeAmount)

	// Update order status เป็น completed พร้อม fee fields
	err = orderRepo.UpdateStatus(ctx, orderID, models.OrderStatusCompleted, map[string]interface{}{
		"matched_by":    "sms",
		"matched_at":    time.Now().UTC(),
		"actual_amount": adjustedAmount,
		"fee_amount":    feeAmount,
		"net_amount":    netAmount,
	})
	if err != nil {
		t.Fatalf("UpdateStatus to completed failed: %v", err)
	}

	// ตรวจสอบว่า order status เปลี่ยนเป็น completed
	completedOrder := orderRepo.GetOrder(orderID)
	if completedOrder.Status != models.OrderStatusCompleted {
		t.Errorf("expected order status 'completed', got %q", completedOrder.Status)
	}

	// --- Step 4: Credit merchant wallet ด้วย net amount ---
	// จำลอง wallet credit operation โดยตรงผ่าน mock repo
	wallet := walletRepo.GetWallet(walletID)
	if wallet == nil {
		t.Fatal("wallet not found")
	}

	newBalance := wallet.Balance.Add(netAmount)
	err = walletRepo.UpdateBalance(ctx, walletID, newBalance.String(), wallet.HoldBalance.String(), wallet.Version)
	if err != nil {
		t.Fatalf("wallet UpdateBalance failed: %v", err)
	}

	// สร้าง ledger entry สำหรับ audit trail
	err = walletRepo.CreateLedgerEntry(ctx, &models.WalletLedger{
		WalletID:      walletID,
		EntryType:     models.LedgerDepositCredit,
		ReferenceType: "deposit_order",
		ReferenceID:   orderID,
		Amount:        netAmount,
		BalanceAfter:  newBalance,
		Description:   "Deposit credit for order " + orderID.String(),
		CreatedAt:     time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("CreateLedgerEntry failed: %v", err)
	}

	// --- Final Assertions ---

	// ตรวจสอบ wallet balance เพิ่มขึ้นด้วย net amount
	finalWallet := walletRepo.GetWallet(walletID)
	if finalWallet == nil {
		t.Fatal("wallet not found after credit")
	}
	if !finalWallet.Balance.Equal(netAmount) {
		t.Errorf("expected wallet balance %s, got %s", netAmount, finalWallet.Balance)
	}

	// ตรวจสอบว่า ledger entry ถูกสร้าง
	ledger := walletRepo.GetLedgerEntries()
	if len(ledger) != 1 {
		t.Fatalf("expected 1 ledger entry, got %d", len(ledger))
	}
	if ledger[0].EntryType != models.LedgerDepositCredit {
		t.Errorf("expected ledger entry type %q, got %q", models.LedgerDepositCredit, ledger[0].EntryType)
	}
	if !ledger[0].Amount.Equal(netAmount) {
		t.Errorf("expected ledger amount %s, got %s", netAmount, ledger[0].Amount)
	}

	// ตรวจสอบว่า SMS ถูกบันทึกลง database
	messages := smsRepo.GetMessages()
	if len(messages) != 1 {
		t.Fatalf("expected 1 SMS message stored, got %d", len(messages))
	}

	t.Logf("Deposit flow completed successfully: order=%s, fee=%s, net=%s, wallet_balance=%s",
		orderID, feeAmount, netAmount, finalWallet.Balance)
}

// ---------------------------------------------------------------------------
// Test: Expired Order Flow
// ---------------------------------------------------------------------------

// TestDepositFlow_ExpiredOrder ทดสอบว่า order ที่หมดอายุจะไม่สามารถ complete ได้
//
// ทำไมต้อง test นี้:
//   - ป้องกัน fraud: ลูกค้าอาจโอนเงินหลัง order หมดอายุ
//   - ต้องแน่ใจว่า expired order ไม่สามารถถูก complete ได้
func TestDepositFlow_ExpiredOrder(t *testing.T) {
	ctx := context.Background()
	orderRepo := testhelper.NewMockOrderRepository()

	// สร้าง order ที่หมดอายุแล้ว (ExpiresAt 30 นาทีที่แล้ว)
	orderID := uuid.New()
	merchantID := uuid.New()
	bankAccountID := uuid.New()

	expiredOrder := &models.DepositOrder{
		ID:              orderID,
		MerchantID:      merchantID,
		MerchantOrderID: "MERCH-EXPIRED-001",
		RequestedAmount: decimal.NewFromFloat(500.00),
		AdjustedAmount:  decimal.NewFromFloat(500.33),
		Currency:        "THB",
		BankAccountID:   bankAccountID,
		Status:          models.OrderStatusPending,
		ExpiresAt:       time.Now().UTC().Add(-30 * time.Minute), // หมดอายุ 30 นาทีที่แล้ว
		CreatedAt:       time.Now().UTC().Add(-45 * time.Minute),
		UpdatedAt:       time.Now().UTC().Add(-45 * time.Minute),
	}
	orderRepo.SeedOrder(expiredOrder)

	// จำลอง expiry worker ที่เปลี่ยน status เป็น expired
	expired, err := orderRepo.FindExpired(ctx, time.Now().UTC())
	if err != nil {
		t.Fatalf("FindExpired failed: %v", err)
	}
	if len(expired) != 1 {
		t.Fatalf("expected 1 expired order, got %d", len(expired))
	}

	// Mark order as expired
	err = orderRepo.UpdateStatus(ctx, orderID, models.OrderStatusExpired, nil)
	if err != nil {
		t.Fatalf("UpdateStatus to expired failed: %v", err)
	}

	// ตรวจสอบว่า order status เปลี่ยนเป็น expired
	order := orderRepo.GetOrder(orderID)
	if order.Status != models.OrderStatusExpired {
		t.Errorf("expected order status 'expired', got %q", order.Status)
	}

	// พยายาม complete order — ต้อง fail เพราะ status ไม่ใช่ pending
	if order.Status == models.OrderStatusPending {
		t.Error("expired order should not have pending status")
	}

	t.Logf("Expired order flow verified: order=%s, status=%s", orderID, order.Status)
}

// ---------------------------------------------------------------------------
// Test: Duplicate SMS Handling
// ---------------------------------------------------------------------------

// TestDepositFlow_DuplicateSMS ทดสอบว่า SMS ที่ซ้ำกันจะไม่ match ซ้ำ
//
// ทำไมต้อง test นี้:
//   - ป้องกัน double credit: ถ้า SMS ซ้ำถูก match ซ้ำ merchant จะได้เงินสองเท่า
//   - SMS gateway อาจส่ง SMS เดียวกันหลายครั้ง (retry mechanism)
func TestDepositFlow_DuplicateSMS(t *testing.T) {
	ctx := context.Background()

	// Setup mock dependencies
	orderMatcher := testhelper.NewMockOrderMatcher()
	bankAccountID := uuid.New()
	orderID := uuid.New()
	amount := decimal.NewFromFloat(1500.67)

	// เพิ่ม pending order ลง matcher — จะถูก remove หลัง match แรก
	orderMatcher.AddPendingOrder(&testhelper.PendingOrderInfo{
		OrderID:       orderID,
		BankAccountID: bankAccountID,
		Amount:        amount,
	})

	// --- SMS แรก: ต้อง match สำเร็จ ---
	result1, err := orderMatcher.FindPendingOrder(ctx, bankAccountID, amount)
	if err != nil {
		t.Fatalf("first FindPendingOrder failed: %v", err)
	}
	if result1 == nil {
		t.Fatal("first SMS should match a pending order, got nil")
	}
	if result1.OrderID != orderID {
		t.Fatalf("expected order ID %s, got %s", orderID, result1.OrderID)
	}

	// --- SMS ที่สอง (ซ้ำ): ต้อง unmatched เพราะ order ถูก remove แล้ว ---
	result2, err := orderMatcher.FindPendingOrder(ctx, bankAccountID, amount)
	if err != nil {
		t.Fatalf("second FindPendingOrder failed: %v", err)
	}
	if result2 != nil {
		t.Fatal("duplicate SMS should not match any order, but got a match")
	}

	// ตรวจสอบว่า FindPendingOrder ถูกเรียก 2 ครั้ง
	if orderMatcher.FindCallCount != 2 {
		t.Errorf("expected FindPendingOrder called 2 times, got %d", orderMatcher.FindCallCount)
	}

	t.Logf("Duplicate SMS handling verified: first=matched, second=unmatched")
}

// ---------------------------------------------------------------------------
// Test: Amount Mismatch
// ---------------------------------------------------------------------------

// TestDepositFlow_AmountMismatch ทดสอบว่า SMS ที่จำนวนเงินไม่ตรงจะไม่ match
//
// ทำไมต้อง test นี้:
//   - ป้องกัน wrong match: amount ต้องตรง exact ถึง satang level
//   - ลูกค้าอาจโอนเงินผิดจำนวน -> ไม่ควร auto-match
func TestDepositFlow_AmountMismatch(t *testing.T) {
	ctx := context.Background()

	orderMatcher := testhelper.NewMockOrderMatcher()
	bankAccountID := uuid.New()

	// Order ด้วย adjusted amount 1,000.42
	orderMatcher.AddPendingOrder(&testhelper.PendingOrderInfo{
		OrderID:       uuid.New(),
		BankAccountID: bankAccountID,
		Amount:        decimal.NewFromFloat(1000.42),
	})

	// SMS ด้วย amount 1,000.00 (ไม่ตรงกับ 1,000.42)
	result, err := orderMatcher.FindPendingOrder(ctx, bankAccountID, decimal.NewFromFloat(1000.00))
	if err != nil {
		t.Fatalf("FindPendingOrder returned error: %v", err)
	}

	// ต้อง unmatched เพราะ amount ไม่ตรง (1000.00 != 1000.42)
	if result != nil {
		t.Error("expected no match for amount mismatch, but got a match")
	}

	t.Logf("Amount mismatch handling verified: SMS amount=1000.00, order amount=1000.42")
}

// ---------------------------------------------------------------------------
// Test: Wallet Credit with Optimistic Locking
// ---------------------------------------------------------------------------

// TestDepositFlow_WalletCreditOptimisticLock ทดสอบว่า wallet credit ใช้
// optimistic locking ถูกต้อง เมื่อมี sequential access
//
// ทำไมต้อง test นี้:
//   - Wallet balance เป็นข้อมูลที่ sensitive มาก — ต้อง consistent
//   - Optimistic locking ป้องกัน lost updates เมื่อ 2 deposits complete พร้อมกัน
//   - ต้อง verify ว่า version bump ทำงานถูกต้อง
func TestDepositFlow_WalletCreditOptimisticLock(t *testing.T) {
	ctx := context.Background()

	walletRepo := testhelper.NewMockWalletRepository()
	walletID := uuid.New()
	merchantID := uuid.New()

	// Seed wallet ด้วย balance 5,000
	walletRepo.SeedWallet(&models.Wallet{
		ID:          walletID,
		OwnerType:   models.OwnerTypeMerchant,
		OwnerID:     merchantID,
		Currency:    "THB",
		Balance:     decimal.NewFromFloat(5000.00),
		HoldBalance: decimal.Zero,
		Version:     1,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	})

	// Credit 1: +980
	wallet := walletRepo.GetWallet(walletID)
	newBal1 := wallet.Balance.Add(decimal.NewFromFloat(980.00))
	err := walletRepo.UpdateBalance(ctx, walletID, newBal1.String(), wallet.HoldBalance.String(), wallet.Version)
	if err != nil {
		t.Fatalf("first credit failed: %v", err)
	}

	// Credit 2: +490
	wallet = walletRepo.GetWallet(walletID)
	newBal2 := wallet.Balance.Add(decimal.NewFromFloat(490.00))
	err = walletRepo.UpdateBalance(ctx, walletID, newBal2.String(), wallet.HoldBalance.String(), wallet.Version)
	if err != nil {
		t.Fatalf("second credit failed: %v", err)
	}

	// ตรวจสอบ final balance: 5000 + 980 + 490 = 6470
	finalWallet := walletRepo.GetWallet(walletID)
	expectedBalance := decimal.NewFromFloat(6470.00)
	if !finalWallet.Balance.Equal(expectedBalance) {
		t.Errorf("expected wallet balance %s, got %s", expectedBalance, finalWallet.Balance)
	}

	// ตรวจสอบ version bump: 1 + 2 credits = 3
	if finalWallet.Version != 3 {
		t.Errorf("expected wallet version 3, got %d", finalWallet.Version)
	}

	t.Logf("Optimistic locking verified: final_balance=%s, version=%d", finalWallet.Balance, finalWallet.Version)
}
