// Package integration ทดสอบ end-to-end flow ของระบบ RichPayment
// ไฟล์นี้ทดสอบ commission calculation และ aggregation flow
//
// Commission Flow ที่ทดสอบ:
//   1. Transaction complete (deposit/withdrawal) → คำนวณ fee
//   2. Fee ถูก split ตาม configuration:
//      - System share = total_fee - agent_share - partner_share
//      - Agent share  = transaction_amount * agent_commission_pct
//      - Partner share = transaction_amount * partner_commission_pct
//   3. Commission record ถูกบันทึก + wallet credited
//   4. Daily aggregation: group commissions by owner + date → summary rows
//   5. Monthly summary: aggregate daily summaries → monthly totals
//
// เนื่องจาก integration tests อยู่ใน module แยก ไม่สามารถ import
// internal packages ของ services ได้โดยตรง ดังนั้น test จะจำลอง
// commission calculation logic แบบ manual ผ่าน mock repositories
// เพื่อ verify ว่า formula และ recording ทำงานถูกต้อง
//
// การคำนวณ commission:
//   total_fee     = transaction_amount * merchant_fee_pct
//   agent_share   = transaction_amount * agent_commission_pct  (0 ถ้าไม่มี agent)
//   partner_share = transaction_amount * partner_commission_pct (0 ถ้าไม่มี partner)
//   system_share  = total_fee - agent_share - partner_share
//
// ทุก test ใช้ mock repositories เพื่อจำลอง infrastructure
package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/farritpcz/richpayment/pkg/models"
	"github.com/farritpcz/richpayment/tests/integration/testhelper"
)

// ---------------------------------------------------------------------------
// Helper: calculateAndRecordCommission
// จำลอง commission calculation logic ของ commission-service Calculator
// ใช้ formula เดียวกัน: total_fee = txn_amount * fee_pct,
// agent_share = txn_amount * agent_pct, partner_share = txn_amount * partner_pct,
// system_share = total_fee - agent_share - partner_share
// ---------------------------------------------------------------------------

// commissionInput เก็บ parameters สำหรับ commission calculation
// duplicate จาก commission/internal/service.CommissionInput
type commissionInput struct {
	// TransactionType คือ deposit หรือ withdrawal
	TransactionType models.TransactionType

	// TransactionID คือ UUID ของ transaction
	TransactionID uuid.UUID

	// MerchantID คือ UUID ของ merchant
	MerchantID uuid.UUID

	// TransactionAmount คือยอด transaction ก่อนหัก fee
	TransactionAmount decimal.Decimal

	// MerchantFeePct คือ total fee percentage (เช่น 0.023 = 2.3%)
	MerchantFeePct decimal.Decimal

	// AgentID คือ agent UUID (nil ถ้าไม่มี agent)
	AgentID *uuid.UUID

	// AgentCommissionPct คือ agent commission percentage
	AgentCommissionPct decimal.Decimal

	// PartnerID คือ partner UUID (nil ถ้าไม่มี partner)
	PartnerID *uuid.UUID

	// PartnerCommissionPct คือ partner commission percentage
	PartnerCommissionPct decimal.Decimal

	// Currency คือ ISO 4217 currency code
	Currency string
}

// calculateAndRecordCommission จำลอง Calculator.CalculateCommission + RecordCommission
// ใช้ formula:
//   - total_fee = transaction_amount * merchant_fee_pct (round 2)
//   - agent_share = transaction_amount * agent_commission_pct (round 2) — 0 ถ้าไม่มี agent
//   - partner_share = transaction_amount * partner_commission_pct (round 2) — 0 ถ้าไม่มี partner
//   - system_share = total_fee - agent_share - partner_share
//
// Returns commission model หลัง calculate + record
func calculateAndRecordCommission(
	ctx context.Context,
	repo *testhelper.MockCommissionRepository,
	input commissionInput,
) (*models.Commission, error) {
	// --- Validate: agent_pct + partner_pct <= merchant_fee_pct ---
	combinedPct := input.AgentCommissionPct.Add(input.PartnerCommissionPct)
	if combinedPct.GreaterThan(input.MerchantFeePct) {
		return nil, fmt.Errorf("commission split exceeds merchant fee: %s > %s", combinedPct, input.MerchantFeePct)
	}

	// --- Calculate each party's share ---
	totalFee := input.TransactionAmount.Mul(input.MerchantFeePct).Round(2)

	agentShare := decimal.Zero
	if input.AgentID != nil {
		agentShare = input.TransactionAmount.Mul(input.AgentCommissionPct).Round(2)
	}

	partnerShare := decimal.Zero
	if input.PartnerID != nil {
		partnerShare = input.TransactionAmount.Mul(input.PartnerCommissionPct).Round(2)
	}

	// System gets the remainder — guaranteed no rounding gap
	systemShare := totalFee.Sub(agentShare).Sub(partnerShare)

	if systemShare.IsNegative() {
		return nil, fmt.Errorf("negative system share: %s", systemShare)
	}

	// --- Build commission record ---
	commission := models.Commission{
		ID:              uuid.New(),
		TransactionType: input.TransactionType,
		TransactionID:   input.TransactionID,
		MerchantID:      input.MerchantID,
		TotalFeeAmount:  totalFee,
		SystemAmount:    systemShare,
		AgentID:         input.AgentID,
		AgentAmount:     agentShare,
		PartnerID:       input.PartnerID,
		PartnerAmount:   partnerShare,
		MerchantFeePct:  input.MerchantFeePct,
		AgentPct:        input.AgentCommissionPct,
		PartnerPct:      input.PartnerCommissionPct,
		Currency:        input.Currency,
		CreatedAt:       time.Now().UTC(),
	}

	// --- Record: insert commission ---
	if err := repo.InsertCommission(ctx, &commission); err != nil {
		return nil, fmt.Errorf("insert commission: %w", err)
	}

	// --- Record: credit agent wallet ---
	if commission.AgentID != nil && commission.AgentAmount.IsPositive() {
		desc := fmt.Sprintf("Commission from txn %s", commission.TransactionID)
		if err := repo.CreditWallet(ctx, *commission.AgentID, commission.AgentAmount, models.LedgerCommissionCredit, commission.ID, desc); err != nil {
			return nil, fmt.Errorf("credit agent wallet: %w", err)
		}
	}

	// --- Record: credit partner wallet ---
	if commission.PartnerID != nil && commission.PartnerAmount.IsPositive() {
		desc := fmt.Sprintf("Commission from txn %s", commission.TransactionID)
		if err := repo.CreditWallet(ctx, *commission.PartnerID, commission.PartnerAmount, models.LedgerCommissionCredit, commission.ID, desc); err != nil {
			return nil, fmt.Errorf("credit partner wallet: %w", err)
		}
	}

	return &commission, nil
}

// ---------------------------------------------------------------------------
// Test: Commission Flow — Table-Driven Tests
// ---------------------------------------------------------------------------

// TestCommissionFlow_TableDriven ทดสอบ commission calculation ด้วย table-driven tests
// ครอบคลุม 5 กรณี:
//   - 3-way split: system + agent + partner
//   - No agent: system + partner
//   - No partner: system + agent
//   - Daily aggregation: multiple commissions → verify totals
//   - Monthly summary: multiple days → monthly totals
//
// ทำไมต้อง test นี้:
//   - Commission calculation ต้อง accurate — เกี่ยวข้องกับเงินจริง
//   - ต้องตรวจสอบว่า system share = total_fee - agent - partner (ไม่มี rounding gap)
//   - ต้อง handle กรณีไม่มี agent หรือ partner ได้ถูกต้อง
//   - Daily/monthly aggregation ต้อง sum ถูกต้อง
func TestCommissionFlow_TableDriven(t *testing.T) {
	tests := []struct {
		// name คือชื่อของ test case — แสดงใน test output
		name string

		// description อธิบายว่า test case นี้ทดสอบอะไร
		description string

		// testFn รัน test logic จริง
		testFn func(t *testing.T)
	}{
		// =======================================================================
		// Case 1: 3-Way Commission Split (System + Agent + Partner)
		// =======================================================================
		{
			name:        "three_way_commission_split",
			description: "ทดสอบ commission split 3 ทาง: system 1.5% + agent 0.5% + partner 0.3% จาก merchant fee 2.3%",

			testFn: func(t *testing.T) {
				ctx := context.Background()
				commissionRepo := testhelper.NewMockCommissionRepository()

				agentID := uuid.New()
				partnerID := uuid.New()

				commission, err := calculateAndRecordCommission(ctx, commissionRepo, commissionInput{
					TransactionType:      models.TransactionTypeDeposit,
					TransactionID:        uuid.New(),
					MerchantID:           uuid.New(),
					TransactionAmount:    decimal.NewFromFloat(10000.00), // 10,000 THB
					MerchantFeePct:       decimal.NewFromFloat(0.023),    // 2.3%
					AgentID:              &agentID,
					AgentCommissionPct:   decimal.NewFromFloat(0.005), // 0.5%
					PartnerID:            &partnerID,
					PartnerCommissionPct: decimal.NewFromFloat(0.003), // 0.3%
					Currency:             "THB",
				})
				if err != nil {
					t.Fatalf("calculateAndRecordCommission failed: %v", err)
				}

				// Total fee = 10000 * 0.023 = 230.00
				if !commission.TotalFeeAmount.Equal(decimal.NewFromFloat(230.00)) {
					t.Errorf("expected total fee 230.00, got %s", commission.TotalFeeAmount)
				}

				// Agent share = 10000 * 0.005 = 50.00
				if !commission.AgentAmount.Equal(decimal.NewFromFloat(50.00)) {
					t.Errorf("expected agent share 50.00, got %s", commission.AgentAmount)
				}

				// Partner share = 10000 * 0.003 = 30.00
				if !commission.PartnerAmount.Equal(decimal.NewFromFloat(30.00)) {
					t.Errorf("expected partner share 30.00, got %s", commission.PartnerAmount)
				}

				// System share = 230 - 50 - 30 = 150.00
				if !commission.SystemAmount.Equal(decimal.NewFromFloat(150.00)) {
					t.Errorf("expected system share 150.00, got %s", commission.SystemAmount)
				}

				// ตรวจสอบ: total = system + agent + partner (ไม่มี rounding gap)
				sum := commission.SystemAmount.Add(commission.AgentAmount).Add(commission.PartnerAmount)
				if !sum.Equal(commission.TotalFeeAmount) {
					t.Errorf("split does not sum to total: %s != %s", sum, commission.TotalFeeAmount)
				}

				// ตรวจสอบ wallet credits: agent + partner = 2
				credits := commissionRepo.GetWalletCredits()
				if len(credits) != 2 {
					t.Fatalf("expected 2 wallet credits, got %d", len(credits))
				}

				t.Logf("3-way split: total=%s, system=%s, agent=%s, partner=%s",
					commission.TotalFeeAmount, commission.SystemAmount,
					commission.AgentAmount, commission.PartnerAmount)
			},
		},

		// =======================================================================
		// Case 2: Commission with No Agent
		// =======================================================================
		{
			name:        "commission_no_agent",
			description: "ทดสอบ commission เมื่อ merchant ไม่มี agent — system ได้ส่วนที่เหลือหลังหัก partner",

			testFn: func(t *testing.T) {
				ctx := context.Background()
				commissionRepo := testhelper.NewMockCommissionRepository()

				partnerID := uuid.New()

				commission, err := calculateAndRecordCommission(ctx, commissionRepo, commissionInput{
					TransactionType:      models.TransactionTypeWithdrawal,
					TransactionID:        uuid.New(),
					MerchantID:           uuid.New(),
					TransactionAmount:    decimal.NewFromFloat(20000.00), // 20,000
					MerchantFeePct:       decimal.NewFromFloat(0.015),    // 1.5%
					AgentID:              nil,                            // ไม่มี agent
					AgentCommissionPct:   decimal.Zero,
					PartnerID:            &partnerID,
					PartnerCommissionPct: decimal.NewFromFloat(0.002), // 0.2%
					Currency:             "THB",
				})
				if err != nil {
					t.Fatalf("calculateAndRecordCommission failed: %v", err)
				}

				// Total fee = 20000 * 0.015 = 300.00
				if !commission.TotalFeeAmount.Equal(decimal.NewFromFloat(300.00)) {
					t.Errorf("expected total fee 300.00, got %s", commission.TotalFeeAmount)
				}

				// Agent = 0
				if !commission.AgentAmount.IsZero() {
					t.Errorf("expected agent share 0, got %s", commission.AgentAmount)
				}

				// Partner = 20000 * 0.002 = 40.00
				if !commission.PartnerAmount.Equal(decimal.NewFromFloat(40.00)) {
					t.Errorf("expected partner share 40.00, got %s", commission.PartnerAmount)
				}

				// System = 300 - 0 - 40 = 260.00
				if !commission.SystemAmount.Equal(decimal.NewFromFloat(260.00)) {
					t.Errorf("expected system share 260.00, got %s", commission.SystemAmount)
				}

				// Only 1 wallet credit (partner)
				credits := commissionRepo.GetWalletCredits()
				if len(credits) != 1 {
					t.Fatalf("expected 1 wallet credit, got %d", len(credits))
				}
				if credits[0].WalletID != partnerID {
					t.Errorf("expected partner credit, got wallet %s", credits[0].WalletID)
				}

				t.Logf("No-agent: total=%s, system=%s, partner=%s",
					commission.TotalFeeAmount, commission.SystemAmount, commission.PartnerAmount)
			},
		},

		// =======================================================================
		// Case 3: Commission with No Partner
		// =======================================================================
		{
			name:        "commission_no_partner",
			description: "ทดสอบ commission เมื่อ agent ไม่มี partner — system + agent split เท่านั้น",

			testFn: func(t *testing.T) {
				ctx := context.Background()
				commissionRepo := testhelper.NewMockCommissionRepository()

				agentID := uuid.New()

				commission, err := calculateAndRecordCommission(ctx, commissionRepo, commissionInput{
					TransactionType:      models.TransactionTypeDeposit,
					TransactionID:        uuid.New(),
					MerchantID:           uuid.New(),
					TransactionAmount:    decimal.NewFromFloat(15000.00), // 15,000
					MerchantFeePct:       decimal.NewFromFloat(0.02),     // 2%
					AgentID:              &agentID,
					AgentCommissionPct:   decimal.NewFromFloat(0.007), // 0.7%
					PartnerID:            nil,
					PartnerCommissionPct: decimal.Zero,
					Currency:             "THB",
				})
				if err != nil {
					t.Fatalf("calculateAndRecordCommission failed: %v", err)
				}

				// Total = 15000 * 0.02 = 300
				if !commission.TotalFeeAmount.Equal(decimal.NewFromFloat(300.00)) {
					t.Errorf("expected total fee 300.00, got %s", commission.TotalFeeAmount)
				}

				// Agent = 15000 * 0.007 = 105
				if !commission.AgentAmount.Equal(decimal.NewFromFloat(105.00)) {
					t.Errorf("expected agent share 105.00, got %s", commission.AgentAmount)
				}

				// Partner = 0
				if !commission.PartnerAmount.IsZero() {
					t.Errorf("expected partner share 0, got %s", commission.PartnerAmount)
				}

				// System = 300 - 105 = 195
				if !commission.SystemAmount.Equal(decimal.NewFromFloat(195.00)) {
					t.Errorf("expected system share 195.00, got %s", commission.SystemAmount)
				}

				// Only 1 wallet credit (agent)
				credits := commissionRepo.GetWalletCredits()
				if len(credits) != 1 {
					t.Fatalf("expected 1 wallet credit, got %d", len(credits))
				}
				if credits[0].WalletID != agentID {
					t.Errorf("expected agent credit, got wallet %s", credits[0].WalletID)
				}

				t.Logf("No-partner: total=%s, system=%s, agent=%s",
					commission.TotalFeeAmount, commission.SystemAmount, commission.AgentAmount)
			},
		},

		// =======================================================================
		// Case 4: Daily Aggregation
		// =======================================================================
		{
			name:        "daily_aggregation",
			description: "ทดสอบ daily aggregation: หลาย commissions ในวันเดียว → verify totals",

			testFn: func(t *testing.T) {
				ctx := context.Background()
				commissionRepo := testhelper.NewMockCommissionRepository()

				agentID := uuid.New()
				partnerID := uuid.New()
				merchantID := uuid.New()

				// --- สร้าง 3 commissions ---
				// Commission 1: deposit 10,000, fee 2.3%
				_, err := calculateAndRecordCommission(ctx, commissionRepo, commissionInput{
					TransactionType:      models.TransactionTypeDeposit,
					TransactionID:        uuid.New(),
					MerchantID:           merchantID,
					TransactionAmount:    decimal.NewFromFloat(10000.00),
					MerchantFeePct:       decimal.NewFromFloat(0.023),
					AgentID:              &agentID,
					AgentCommissionPct:   decimal.NewFromFloat(0.005),
					PartnerID:            &partnerID,
					PartnerCommissionPct: decimal.NewFromFloat(0.003),
					Currency:             "THB",
				})
				if err != nil {
					t.Fatalf("Commission #1 failed: %v", err)
				}

				// Commission 2: deposit 5,000, fee 2.3%
				_, err = calculateAndRecordCommission(ctx, commissionRepo, commissionInput{
					TransactionType:      models.TransactionTypeDeposit,
					TransactionID:        uuid.New(),
					MerchantID:           merchantID,
					TransactionAmount:    decimal.NewFromFloat(5000.00),
					MerchantFeePct:       decimal.NewFromFloat(0.023),
					AgentID:              &agentID,
					AgentCommissionPct:   decimal.NewFromFloat(0.005),
					PartnerID:            &partnerID,
					PartnerCommissionPct: decimal.NewFromFloat(0.003),
					Currency:             "THB",
				})
				if err != nil {
					t.Fatalf("Commission #2 failed: %v", err)
				}

				// Commission 3: withdrawal 8,000, fee 1.5%
				_, err = calculateAndRecordCommission(ctx, commissionRepo, commissionInput{
					TransactionType:      models.TransactionTypeWithdrawal,
					TransactionID:        uuid.New(),
					MerchantID:           merchantID,
					TransactionAmount:    decimal.NewFromFloat(8000.00),
					MerchantFeePct:       decimal.NewFromFloat(0.015),
					AgentID:              &agentID,
					AgentCommissionPct:   decimal.NewFromFloat(0.005),
					PartnerID:            &partnerID,
					PartnerCommissionPct: decimal.NewFromFloat(0.003),
					Currency:             "THB",
				})
				if err != nil {
					t.Fatalf("Commission #3 failed: %v", err)
				}

				// --- Assertions ---
				commissions := commissionRepo.GetCommissions()
				if len(commissions) != 3 {
					t.Fatalf("expected 3 commissions, got %d", len(commissions))
				}

				// คำนวณ totals
				totalFee := decimal.Zero
				totalAgent := decimal.Zero
				totalPartner := decimal.Zero
				totalSystem := decimal.Zero

				for _, c := range commissions {
					totalFee = totalFee.Add(c.TotalFeeAmount)
					totalAgent = totalAgent.Add(c.AgentAmount)
					totalPartner = totalPartner.Add(c.PartnerAmount)
					totalSystem = totalSystem.Add(c.SystemAmount)
				}

				// Total fee = 230 + 115 + 120 = 465
				if !totalFee.Equal(decimal.NewFromFloat(465.00)) {
					t.Errorf("expected total fee 465.00, got %s", totalFee)
				}

				// Agent = 50 + 25 + 40 = 115
				if !totalAgent.Equal(decimal.NewFromFloat(115.00)) {
					t.Errorf("expected total agent 115.00, got %s", totalAgent)
				}

				// Partner = 30 + 15 + 24 = 69
				if !totalPartner.Equal(decimal.NewFromFloat(69.00)) {
					t.Errorf("expected total partner 69.00, got %s", totalPartner)
				}

				// System = 465 - 115 - 69 = 281
				if !totalSystem.Equal(decimal.NewFromFloat(281.00)) {
					t.Errorf("expected total system 281.00, got %s", totalSystem)
				}

				// Wallet credits: 3 * 2 = 6
				credits := commissionRepo.GetWalletCredits()
				if len(credits) != 6 {
					t.Fatalf("expected 6 wallet credits, got %d", len(credits))
				}

				t.Logf("Daily aggregation: 3 commissions, total=%s, system=%s, agent=%s, partner=%s",
					totalFee, totalSystem, totalAgent, totalPartner)
			},
		},

		// =======================================================================
		// Case 5: Monthly Summary
		// =======================================================================
		{
			name:        "monthly_summary",
			description: "ทดสอบ monthly summary: daily summaries หลายวัน → aggregate เป็น monthly totals",

			testFn: func(t *testing.T) {
				ctx := context.Background()
				commissionRepo := testhelper.NewMockCommissionRepository()

				agentID := uuid.New()

				// --- Seed daily summaries สำหรับ 3 วัน ใน April 2026 ---

				// Day 1: April 5
				_ = commissionRepo.UpsertDailySummary(ctx, &models.CommissionDailySummary{
					SummaryDate:     time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC),
					OwnerType:       models.OwnerTypeAgent,
					OwnerID:         agentID,
					TransactionType: models.TransactionTypeDeposit,
					Currency:        "THB",
					TotalTxCount:    10,
					TotalVolume:     decimal.NewFromFloat(100000.00),
					TotalFee:        decimal.NewFromFloat(2300.00),
					TotalCommission: decimal.NewFromFloat(500.00),
				})

				// Day 2: April 10
				_ = commissionRepo.UpsertDailySummary(ctx, &models.CommissionDailySummary{
					SummaryDate:     time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC),
					OwnerType:       models.OwnerTypeAgent,
					OwnerID:         agentID,
					TransactionType: models.TransactionTypeDeposit,
					Currency:        "THB",
					TotalTxCount:    15,
					TotalVolume:     decimal.NewFromFloat(150000.00),
					TotalFee:        decimal.NewFromFloat(3450.00),
					TotalCommission: decimal.NewFromFloat(750.00),
				})

				// Day 3: April 20
				_ = commissionRepo.UpsertDailySummary(ctx, &models.CommissionDailySummary{
					SummaryDate:     time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC),
					OwnerType:       models.OwnerTypeAgent,
					OwnerID:         agentID,
					TransactionType: models.TransactionTypeDeposit,
					Currency:        "THB",
					TotalTxCount:    8,
					TotalVolume:     decimal.NewFromFloat(80000.00),
					TotalFee:        decimal.NewFromFloat(1840.00),
					TotalCommission: decimal.NewFromFloat(400.00),
				})

				// --- Monthly summary ---
				monthly, err := commissionRepo.GetMonthlySummary(ctx, models.OwnerTypeAgent, agentID, 2026, time.April)
				if err != nil {
					t.Fatalf("GetMonthlySummary failed: %v", err)
				}

				// tx_count = 10 + 15 + 8 = 33
				if monthly.TotalTxCount != 33 {
					t.Errorf("expected tx count 33, got %d", monthly.TotalTxCount)
				}

				// volume = 100K + 150K + 80K = 330K
				if !monthly.TotalVolume.Equal(decimal.NewFromFloat(330000.00)) {
					t.Errorf("expected volume 330000.00, got %s", monthly.TotalVolume)
				}

				// fee = 2300 + 3450 + 1840 = 7590
				if !monthly.TotalFee.Equal(decimal.NewFromFloat(7590.00)) {
					t.Errorf("expected fee 7590.00, got %s", monthly.TotalFee)
				}

				// commission = 500 + 750 + 400 = 1650
				if !monthly.TotalCommission.Equal(decimal.NewFromFloat(1650.00)) {
					t.Errorf("expected commission 1650.00, got %s", monthly.TotalCommission)
				}

				// ตรวจสอบ daily summaries
				from := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
				to := time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC)
				dailies, err := commissionRepo.GetDailySummaries(ctx, models.OwnerTypeAgent, agentID, from, to)
				if err != nil {
					t.Fatalf("GetDailySummaries failed: %v", err)
				}
				if len(dailies) != 3 {
					t.Fatalf("expected 3 daily summaries, got %d", len(dailies))
				}

				t.Logf("Monthly: April 2026, agent=%s, tx=%d, volume=%s, fee=%s, commission=%s",
					agentID, monthly.TotalTxCount, monthly.TotalVolume, monthly.TotalFee, monthly.TotalCommission)
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
