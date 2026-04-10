// Package integration ทดสอบ end-to-end flow ของระบบ RichPayment
// ไฟล์นี้ทดสอบ withdrawal flow ตั้งแต่สร้าง withdrawal จนถึง complete/reject
//
// Withdrawal Flow ที่ทดสอบ:
//   1. Merchant สร้าง withdrawal request -> ตรวจ daily limit + balance -> hold balance
//   2. Admin อนุมัติ (approve) -> status เปลี่ยนเป็น approved
//   3. Finance team ยืนยันการโอน -> complete -> คำนวณ fee -> debit hold
//   4. Commission service บันทึก fee split
//
// กรณี reject:
//   1. Admin ปฏิเสธ -> release hold -> balance กลับเข้า wallet
//
// Error cases:
//   - Daily limit exceeded: ยอดรวมวันนี้ + withdrawal ใหม่ > limit
//   - Insufficient balance: balance < withdrawal amount
//
// เนื่องจาก integration tests อยู่ใน module แยก ไม่สามารถ import
// internal packages ของ services ได้โดยตรง ดังนั้น test จะจำลอง
// flow ผ่าน mock repositories โดย orchestrate logic ของ withdrawal-service
// แบบ manual เพื่อ verify ว่า contract ของแต่ละ step ถูกต้อง
//
// ทุก test ใช้ mock repositories และ clients เพื่อจำลอง infrastructure
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
// Test: Withdrawal Flow — Table-Driven Tests
// ---------------------------------------------------------------------------

// TestWithdrawalFlow_TableDriven ทดสอบ withdrawal flow ด้วย table-driven tests
// ครอบคลุม 4 กรณี:
//   - Happy path: create → approve → complete → fee deduction + commission
//   - Rejection flow: create → reject → balance released
//   - Daily limit exceeded: create fail เพราะ daily limit
//   - Insufficient balance: create fail เพราะ balance ไม่พอ
//
// ทำไมต้อง test นี้:
//   - Withdrawal เป็น flow ที่ sensitive ที่สุด — เงินออกจากระบบ
//   - ต้องตรวจสอบว่า hold/release/debit ทำงานถูกต้อง
//   - ต้องตรวจสอบว่า fee calculation ถูกต้อง
//   - ต้องตรวจสอบว่า daily limit enforcement ทำงาน
//   - ต้องตรวจสอบว่า balance check ป้องกัน overdraft
func TestWithdrawalFlow_TableDriven(t *testing.T) {
	tests := []struct {
		// name คือชื่อของ test case — แสดงใน test output
		name string

		// description อธิบายว่า test case นี้ทดสอบอะไร
		description string

		// testFn รัน test logic จริง
		testFn func(t *testing.T)
	}{
		// =======================================================================
		// Case 1: Happy Path — Create → Approve → Complete
		// =======================================================================
		{
			name:        "complete_withdrawal_happy_path",
			description: "ทดสอบ full withdrawal flow: create → approve → complete พร้อม fee deduction และ commission recording",

			testFn: func(t *testing.T) {
				ctx := context.Background()
				merchantID := uuid.New()

				// --- Setup mock dependencies ---
				repo := testhelper.NewMockWithdrawalRepository()
				walletClient := &testhelper.MockWithdrawalWalletClient{
					Balance: decimal.NewFromFloat(50000.00), // 50,000 THB available
				}
				commissionClient := &testhelper.MockWithdrawalCommissionClient{}
				merchantClient := &testhelper.MockMerchantClient{
					FeePct:     decimal.NewFromFloat(0.01), // 1% withdrawal fee
					DailyLimit: decimal.NewFromFloat(100000.00),
				}

				// --- Step 1: Create Withdrawal (จำลอง WithdrawalService.CreateWithdrawal) ---
				withdrawalAmount := decimal.NewFromFloat(10000.00)

				// Step 1a: ตรวจ daily limit
				dailyLimit, _ := merchantClient.GetDailyWithdrawalLimit(ctx, merchantID)
				dailySum, _ := repo.SumDailyWithdrawals(ctx, merchantID, time.Now().UTC())
				if dailyLimit.IsPositive() && dailySum.Add(withdrawalAmount).GreaterThan(dailyLimit) {
					t.Fatal("unexpected daily limit exceeded")
				}

				// Step 1b: ตรวจ balance
				balance, _ := walletClient.GetBalance(ctx, merchantID, "THB")
				if balance.LessThan(withdrawalAmount) {
					t.Fatal("unexpected insufficient balance")
				}

				// Step 1c: Hold balance
				withdrawalID := uuid.New()
				err := walletClient.HoldBalance(ctx, merchantID, withdrawalAmount, "THB", withdrawalID)
				if err != nil {
					t.Fatalf("HoldBalance failed: %v", err)
				}

				// Step 1d: Create withdrawal record
				now := time.Now().UTC()
				withdrawal := &models.Withdrawal{
					ID:          withdrawalID,
					MerchantID:  merchantID,
					Amount:      withdrawalAmount,
					FeeAmount:   decimal.Zero,
					NetAmount:   decimal.Zero,
					Currency:    "THB",
					DestType:    models.WithdrawalDestBank,
					DestDetails: `{"bank":"KBANK","account":"123-4-56789-0","name":"สมชาย ทดสอบ"}`,
					Status:      models.WithdrawalStatusPending,
					CreatedAt:   now,
					UpdatedAt:   now,
				}
				err = repo.Create(ctx, withdrawal)
				if err != nil {
					t.Fatalf("Create withdrawal failed: %v", err)
				}

				// ตรวจสอบ: status = pending, hold called
				if walletClient.HoldCalls != 1 {
					t.Errorf("expected HoldBalance called 1 time, got %d", walletClient.HoldCalls)
				}

				// --- Step 2: Approve Withdrawal (จำลอง WithdrawalService.ApproveWithdrawal) ---
				adminID := uuid.New()
				adminIDCopy := adminID
				approvedAt := time.Now().UTC()
				err = repo.UpdateStatus(ctx, withdrawalID, models.WithdrawalStatusApproved, map[string]interface{}{
					"approved_by": &adminIDCopy,
					"approved_at": &approvedAt,
				})
				if err != nil {
					t.Fatalf("Approve failed: %v", err)
				}

				// ตรวจสอบ: status = approved
				approvedW := repo.GetWithdrawal(withdrawalID)
				if approvedW.Status != models.WithdrawalStatusApproved {
					t.Errorf("expected status 'approved', got %q", approvedW.Status)
				}

				// --- Step 3: Complete Withdrawal (จำลอง WithdrawalService.CompleteWithdrawal) ---
				// Step 3a: คำนวณ fee
				feePct, _ := merchantClient.GetWithdrawalFeePct(ctx, merchantID)
				feeAmount := withdrawalAmount.Mul(feePct).Round(2) // 10000 * 0.01 = 100
				netAmount := withdrawalAmount.Sub(feeAmount)       // 10000 - 100 = 9900

				// Step 3b: Debit hold
				err = walletClient.DebitHold(ctx, merchantID, withdrawalAmount, "THB", withdrawalID)
				if err != nil {
					t.Fatalf("DebitHold failed: %v", err)
				}

				// Step 3c: Record commission
				err = commissionClient.RecordWithdrawalCommission(ctx, withdrawalID, merchantID, feeAmount, "THB")
				if err != nil {
					t.Fatalf("RecordWithdrawalCommission failed: %v", err)
				}

				// Step 3d: Update status to completed
				completedAt := time.Now().UTC()
				err = repo.UpdateStatus(ctx, withdrawalID, models.WithdrawalStatusCompleted, map[string]interface{}{
					"transfer_ref": "KBANK-TXN-12345",
					"proof_url":    "https://proof.example.com/12345.pdf",
					"completed_at": &completedAt,
					"fee_amount":   feeAmount,
					"net_amount":   netAmount,
				})
				if err != nil {
					t.Fatalf("Complete failed: %v", err)
				}

				// --- Final Assertions ---

				// ตรวจสอบ DebitHold ถูกเรียก
				if walletClient.DebitCalls != 1 {
					t.Errorf("expected DebitHold called 1 time, got %d", walletClient.DebitCalls)
				}

				// ตรวจสอบ commission recorded
				if commissionClient.RecordCalls != 1 {
					t.Errorf("expected RecordWithdrawalCommission called 1 time, got %d", commissionClient.RecordCalls)
				}

				// ตรวจสอบ fee amount: 10000 * 1% = 100
				expectedFee := decimal.NewFromFloat(100.00)
				if !commissionClient.LastFeeAmount.Equal(expectedFee) {
					t.Errorf("expected fee %s, got %s", expectedFee, commissionClient.LastFeeAmount)
				}

				// ตรวจสอบ final state
				completedW := repo.GetWithdrawal(withdrawalID)
				if completedW.Status != models.WithdrawalStatusCompleted {
					t.Errorf("expected status 'completed', got %q", completedW.Status)
				}
				if !completedW.FeeAmount.Equal(expectedFee) {
					t.Errorf("expected fee_amount %s, got %s", expectedFee, completedW.FeeAmount)
				}
				expectedNet := decimal.NewFromFloat(9900.00)
				if !completedW.NetAmount.Equal(expectedNet) {
					t.Errorf("expected net_amount %s, got %s", expectedNet, completedW.NetAmount)
				}

				t.Logf("Complete flow: id=%s, fee=%s, net=%s", withdrawalID, feeAmount, netAmount)
			},
		},

		// =======================================================================
		// Case 2: Rejection Flow — Create → Reject (balance released)
		// =======================================================================
		{
			name:        "rejection_flow_balance_released",
			description: "ทดสอบว่า withdrawal ที่ถูก reject จะ release hold balance กลับ wallet",

			testFn: func(t *testing.T) {
				ctx := context.Background()
				merchantID := uuid.New()

				repo := testhelper.NewMockWithdrawalRepository()
				walletClient := &testhelper.MockWithdrawalWalletClient{
					Balance: decimal.NewFromFloat(30000.00),
				}
				commissionClient := &testhelper.MockWithdrawalCommissionClient{}

				// Create withdrawal 5,000 THB
				withdrawalID := uuid.New()
				withdrawalAmount := decimal.NewFromFloat(5000.00)

				_ = walletClient.HoldBalance(ctx, merchantID, withdrawalAmount, "THB", withdrawalID)

				now := time.Now().UTC()
				_ = repo.Create(ctx, &models.Withdrawal{
					ID:          withdrawalID,
					MerchantID:  merchantID,
					Amount:      withdrawalAmount,
					Currency:    "THB",
					DestType:    models.WithdrawalDestPromptPay,
					DestDetails: `{"phone":"0812345678"}`,
					Status:      models.WithdrawalStatusPending,
					CreatedAt:   now,
					UpdatedAt:   now,
				})

				if walletClient.HoldCalls != 1 {
					t.Errorf("expected HoldBalance called 1 time, got %d", walletClient.HoldCalls)
				}

				// --- Reject withdrawal ---
				// Step 1: Release hold
				err := walletClient.ReleaseHold(ctx, merchantID, withdrawalAmount, "THB", withdrawalID)
				if err != nil {
					t.Fatalf("ReleaseHold failed: %v", err)
				}

				// Step 2: Update status to rejected
				adminID := uuid.New()
				adminIDCopy := adminID
				rejectedAt := time.Now().UTC()
				err = repo.UpdateStatus(ctx, withdrawalID, models.WithdrawalStatusRejected, map[string]interface{}{
					"rejected_by":      &adminIDCopy,
					"rejected_at":      &rejectedAt,
					"rejection_reason": "ข้อมูลบัญชีปลายทางไม่ถูกต้อง",
				})
				if err != nil {
					t.Fatalf("Reject failed: %v", err)
				}

				// --- Assertions ---
				if walletClient.ReleaseCalls != 1 {
					t.Errorf("expected ReleaseHold called 1 time, got %d", walletClient.ReleaseCalls)
				}

				rejectedW := repo.GetWithdrawal(withdrawalID)
				if rejectedW.Status != models.WithdrawalStatusRejected {
					t.Errorf("expected status 'rejected', got %q", rejectedW.Status)
				}
				if rejectedW.RejectionReason != "ข้อมูลบัญชีปลายทางไม่ถูกต้อง" {
					t.Errorf("expected rejection reason set, got %q", rejectedW.RejectionReason)
				}

				// DebitHold ไม่ควรถูกเรียก
				if walletClient.DebitCalls != 0 {
					t.Errorf("expected DebitHold not called, got %d calls", walletClient.DebitCalls)
				}

				// Commission ไม่ควรถูก record
				if commissionClient.RecordCalls != 0 {
					t.Errorf("expected commission not recorded, got %d calls", commissionClient.RecordCalls)
				}

				t.Logf("Rejection flow: id=%s, status=%s, hold_released=%d",
					withdrawalID, rejectedW.Status, walletClient.ReleaseCalls)
			},
		},

		// =======================================================================
		// Case 3: Daily Limit Exceeded
		// =======================================================================
		{
			name:        "daily_limit_exceeded",
			description: "ทดสอบว่า withdrawal ที่ทำให้ยอดรวมวันนี้เกิน daily limit จะถูก reject",

			testFn: func(t *testing.T) {
				ctx := context.Background()
				merchantID := uuid.New()

				repo := testhelper.NewMockWithdrawalRepository()
				walletClient := &testhelper.MockWithdrawalWalletClient{
					Balance: decimal.NewFromFloat(500000.00),
				}
				merchantClient := &testhelper.MockMerchantClient{
					FeePct:     decimal.NewFromFloat(0.01),
					DailyLimit: decimal.NewFromFloat(100000.00), // Daily limit 100,000
				}

				// Seed existing withdrawal 80,000 THB วันนี้
				repo.SeedWithdrawal(&models.Withdrawal{
					ID:          uuid.New(),
					MerchantID:  merchantID,
					Amount:      decimal.NewFromFloat(80000.00),
					Currency:    "THB",
					Status:      models.WithdrawalStatusPending,
					DestType:    models.WithdrawalDestBank,
					DestDetails: `{"bank":"SCB"}`,
					CreatedAt:   time.Now().UTC(),
					UpdatedAt:   time.Now().UTC(),
				})

				// พยายามสร้าง withdrawal 30,000 THB
				// existing 80,000 + 30,000 = 110,000 > daily limit 100,000
				newAmount := decimal.NewFromFloat(30000.00)

				// Step 1: Check daily limit (จำลอง service logic)
				dailyLimit, _ := merchantClient.GetDailyWithdrawalLimit(ctx, merchantID)
				dailySum, _ := repo.SumDailyWithdrawals(ctx, merchantID, time.Now().UTC())

				limitExceeded := dailyLimit.IsPositive() && dailySum.Add(newAmount).GreaterThan(dailyLimit)

				// ต้อง exceeded
				if !limitExceeded {
					t.Fatal("expected daily limit to be exceeded, but it was not")
				}

				// ตรวจสอบว่า HoldBalance ไม่ถูกเรียก
				if walletClient.HoldCalls != 0 {
					t.Errorf("expected HoldBalance not called, got %d calls", walletClient.HoldCalls)
				}

				// ตรวจสอบว่าไม่มี withdrawal ถูกสร้าง
				if repo.CreateCount != 0 {
					t.Errorf("expected no withdrawal created, got CreateCount=%d", repo.CreateCount)
				}

				t.Logf("Daily limit exceeded: attempted %s with existing %s against limit %s",
					newAmount, dailySum, dailyLimit)
			},
		},

		// =======================================================================
		// Case 4: Insufficient Balance
		// =======================================================================
		{
			name:        "insufficient_balance",
			description: "ทดสอบว่า withdrawal ที่ amount > wallet balance จะถูก reject",

			testFn: func(t *testing.T) {
				ctx := context.Background()
				merchantID := uuid.New()

				repo := testhelper.NewMockWithdrawalRepository()
				walletClient := &testhelper.MockWithdrawalWalletClient{
					Balance: decimal.NewFromFloat(1000.00), // แค่ 1,000 THB
				}
				merchantClient := &testhelper.MockMerchantClient{
					FeePct:     decimal.NewFromFloat(0.01),
					DailyLimit: decimal.NewFromFloat(500000.00), // Limit สูงพอ
				}

				// พยายามสร้าง withdrawal 5,000 THB (balance มีแค่ 1,000)
				newAmount := decimal.NewFromFloat(5000.00)

				// Step 1: Check daily limit — pass
				dailyLimit, _ := merchantClient.GetDailyWithdrawalLimit(ctx, merchantID)
				dailySum, _ := repo.SumDailyWithdrawals(ctx, merchantID, time.Now().UTC())
				limitExceeded := dailyLimit.IsPositive() && dailySum.Add(newAmount).GreaterThan(dailyLimit)
				if limitExceeded {
					t.Fatal("daily limit should not be exceeded")
				}

				// Step 2: Check balance — fail
				balance, _ := walletClient.GetBalance(ctx, merchantID, "THB")
				insufficientBalance := balance.LessThan(newAmount)

				// ต้อง insufficient
				if !insufficientBalance {
					t.Fatal("expected insufficient balance, but check passed")
				}

				// ตรวจสอบว่า HoldBalance ไม่ถูกเรียก
				if walletClient.HoldCalls != 0 {
					t.Errorf("expected HoldBalance not called, got %d calls", walletClient.HoldCalls)
				}

				// ตรวจสอบว่าไม่มี withdrawal ถูกสร้าง
				if repo.CreateCount != 0 {
					t.Errorf("expected no withdrawal created, got CreateCount=%d", repo.CreateCount)
				}

				t.Logf("Insufficient balance: attempted %s with balance %s", newAmount, balance)
			},
		},
	}

	// ---------------------------------------------------------------------------
	// Run ทุก test case
	// ---------------------------------------------------------------------------
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Logf("Description: %s", tc.description)
			tc.testFn(t)
		})
	}
}
