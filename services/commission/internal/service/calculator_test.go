package service

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/farritpcz/richpayment/pkg/models"
)

// =============================================================================
// mockCommissionRepo is a minimal mock of repository.CommissionRepository.
//
// For calculator tests we only need CalculateCommission, which does NOT call
// the repository at all (it is a pure computation). However, NewCalculator
// requires a non-nil repo, so we provide this no-op mock.
// =============================================================================
type mockCommissionRepo struct{}

func (m *mockCommissionRepo) InsertCommission(_ context.Context, _ *models.Commission) error {
	return nil
}
func (m *mockCommissionRepo) CreditWallet(_ context.Context, _ uuid.UUID, _ decimal.Decimal, _ models.LedgerEntryType, _ uuid.UUID, _ string) error {
	return nil
}
func (m *mockCommissionRepo) GetCommissionsByDate(_ context.Context, _ time.Time) ([]models.Commission, error) {
	return nil, nil
}
func (m *mockCommissionRepo) UpsertDailySummary(_ context.Context, _ *models.CommissionDailySummary) error {
	return nil
}
func (m *mockCommissionRepo) GetDailySummaries(_ context.Context, _ models.OwnerType, _ uuid.UUID, _, _ time.Time) ([]models.CommissionDailySummary, error) {
	return nil, nil
}
func (m *mockCommissionRepo) GetMonthlySummary(_ context.Context, _ models.OwnerType, _ uuid.UUID, _ int, _ time.Month) (*models.CommissionDailySummary, error) {
	return nil, nil
}

// newTestCalculator creates a Calculator with a no-op repository and a
// discarding logger. This isolates tests to only exercise the pure
// calculation logic without any I/O side effects.
func newTestCalculator() *Calculator {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	return NewCalculator(&mockCommissionRepo{}, logger)
}

// =============================================================================
// TestCalculateCommission_WithAgentAndPartner verifies the standard three-way
// commission split: system + agent + partner.
//
// Scenario:
//   - Transaction amount: 10,000 THB
//   - Merchant fee: 3% (0.03)
//   - Agent commission: 1% (0.01)
//   - Partner commission: 0.5% (0.005)
//
// Expected results:
//   - Total fee:     10,000 * 0.03   = 300.00 THB
//   - Agent share:   10,000 * 0.01   = 100.00 THB
//   - Partner share: 10,000 * 0.005  = 50.00 THB
//   - System share:  300 - 100 - 50  = 150.00 THB
//
// This is the most common real-world scenario and tests the core formula.
// =============================================================================
func TestCalculateCommission_WithAgentAndPartner(t *testing.T) {
	calc := newTestCalculator()
	ctx := context.Background()

	agentID := uuid.New()
	partnerID := uuid.New()

	input := CommissionInput{
		TransactionType:      models.TransactionTypeDeposit,
		TransactionID:        uuid.New(),
		MerchantID:           uuid.New(),
		TransactionAmount:    decimal.NewFromInt(10000),
		MerchantFeePct:       decimal.NewFromFloat(0.03),
		AgentID:              &agentID,
		AgentCommissionPct:   decimal.NewFromFloat(0.01),
		PartnerID:            &partnerID,
		PartnerCommissionPct: decimal.NewFromFloat(0.005),
		Currency:             "THB",
	}

	result, err := calc.CalculateCommission(ctx, input)
	if err != nil {
		t.Fatalf("CalculateCommission returned unexpected error: %v", err)
	}

	comm := result.Commission

	// Verify total fee = 10000 * 0.03 = 300.00
	assertDecimalEqual(t, "TotalFeeAmount", comm.TotalFeeAmount, "300")

	// Verify agent share = 10000 * 0.01 = 100.00
	assertDecimalEqual(t, "AgentAmount", comm.AgentAmount, "100")

	// Verify partner share = 10000 * 0.005 = 50.00
	assertDecimalEqual(t, "PartnerAmount", comm.PartnerAmount, "50")

	// Verify system share = 300 - 100 - 50 = 150.00
	assertDecimalEqual(t, "SystemAmount", comm.SystemAmount, "150")

	// Verify the invariant: total_fee = system + agent + partner.
	// This is the fundamental accounting equation that must always hold.
	sum := comm.SystemAmount.Add(comm.AgentAmount).Add(comm.PartnerAmount)
	if !sum.Equal(comm.TotalFeeAmount) {
		t.Errorf("accounting invariant violated: system(%s) + agent(%s) + partner(%s) = %s, want total_fee(%s)",
			comm.SystemAmount, comm.AgentAmount, comm.PartnerAmount, sum, comm.TotalFeeAmount)
	}
}

// =============================================================================
// TestCalculateCommission_NoAgent verifies that when there is no agent
// (AgentID is nil), the agent's share goes entirely to the system.
//
// Scenario:
//   - Transaction amount: 10,000 THB
//   - Merchant fee: 3% (0.03)
//   - No agent (AgentID = nil, AgentCommissionPct = 0)
//   - Partner commission: 0.5% (0.005)
//
// Expected: agent_share = 0, system_share = 300 - 0 - 50 = 250
// =============================================================================
func TestCalculateCommission_NoAgent(t *testing.T) {
	calc := newTestCalculator()
	ctx := context.Background()

	partnerID := uuid.New()

	input := CommissionInput{
		TransactionType:      models.TransactionTypeDeposit,
		TransactionID:        uuid.New(),
		MerchantID:           uuid.New(),
		TransactionAmount:    decimal.NewFromInt(10000),
		MerchantFeePct:       decimal.NewFromFloat(0.03),
		AgentID:              nil, // No agent assigned
		AgentCommissionPct:   decimal.Zero,
		PartnerID:            &partnerID,
		PartnerCommissionPct: decimal.NewFromFloat(0.005),
		Currency:             "THB",
	}

	result, err := calc.CalculateCommission(ctx, input)
	if err != nil {
		t.Fatalf("CalculateCommission returned unexpected error: %v", err)
	}

	comm := result.Commission

	// Agent share should be zero since there is no agent.
	assertDecimalEqual(t, "AgentAmount", comm.AgentAmount, "0")

	// Partner share is still calculated normally.
	assertDecimalEqual(t, "PartnerAmount", comm.PartnerAmount, "50")

	// System gets the rest: 300 - 0 - 50 = 250.
	assertDecimalEqual(t, "SystemAmount", comm.SystemAmount, "250")

	// Total fee unchanged: 300.
	assertDecimalEqual(t, "TotalFeeAmount", comm.TotalFeeAmount, "300")
}

// =============================================================================
// TestCalculateCommission_NoPartner verifies that when there is no partner,
// the partner's share goes to the system.
//
// Scenario:
//   - Transaction amount: 10,000 THB
//   - Merchant fee: 3% (0.03)
//   - Agent commission: 1% (0.01)
//   - No partner (PartnerID = nil, PartnerCommissionPct = 0)
//
// Expected: partner_share = 0, system_share = 300 - 100 - 0 = 200
// =============================================================================
func TestCalculateCommission_NoPartner(t *testing.T) {
	calc := newTestCalculator()
	ctx := context.Background()

	agentID := uuid.New()

	input := CommissionInput{
		TransactionType:      models.TransactionTypeDeposit,
		TransactionID:        uuid.New(),
		MerchantID:           uuid.New(),
		TransactionAmount:    decimal.NewFromInt(10000),
		MerchantFeePct:       decimal.NewFromFloat(0.03),
		AgentID:              &agentID,
		AgentCommissionPct:   decimal.NewFromFloat(0.01),
		PartnerID:            nil, // No partner
		PartnerCommissionPct: decimal.Zero,
		Currency:             "THB",
	}

	result, err := calc.CalculateCommission(ctx, input)
	if err != nil {
		t.Fatalf("CalculateCommission returned unexpected error: %v", err)
	}

	comm := result.Commission

	// Partner share should be zero.
	assertDecimalEqual(t, "PartnerAmount", comm.PartnerAmount, "0")

	// Agent share is still calculated normally.
	assertDecimalEqual(t, "AgentAmount", comm.AgentAmount, "100")

	// System gets 300 - 100 - 0 = 200.
	assertDecimalEqual(t, "SystemAmount", comm.SystemAmount, "200")
}

// =============================================================================
// TestCalculateCommission_ZeroFee verifies the edge case where the merchant
// fee percentage is zero (free tier merchant).
//
// When the fee is 0%, all shares (total, agent, partner, system) must be zero.
// This prevents any money from being incorrectly allocated.
// =============================================================================
func TestCalculateCommission_ZeroFee(t *testing.T) {
	calc := newTestCalculator()
	ctx := context.Background()

	input := CommissionInput{
		TransactionType:      models.TransactionTypeDeposit,
		TransactionID:        uuid.New(),
		MerchantID:           uuid.New(),
		TransactionAmount:    decimal.NewFromInt(10000),
		MerchantFeePct:       decimal.Zero, // Free tier: no fee
		AgentID:              nil,
		AgentCommissionPct:   decimal.Zero,
		PartnerID:            nil,
		PartnerCommissionPct: decimal.Zero,
		Currency:             "THB",
	}

	result, err := calc.CalculateCommission(ctx, input)
	if err != nil {
		t.Fatalf("CalculateCommission returned unexpected error: %v", err)
	}

	comm := result.Commission

	// All amounts should be zero when the fee percentage is zero.
	assertDecimalEqual(t, "TotalFeeAmount", comm.TotalFeeAmount, "0")
	assertDecimalEqual(t, "AgentAmount", comm.AgentAmount, "0")
	assertDecimalEqual(t, "PartnerAmount", comm.PartnerAmount, "0")
	assertDecimalEqual(t, "SystemAmount", comm.SystemAmount, "0")
}

// =============================================================================
// TestCalculateCommission_LargeAmount tests with a very large transaction
// amount (100,000,000 THB) which represents a realistic daily volume for
// a high-volume payment processor.
//
// This verifies that:
//   - Decimal arithmetic handles large numbers without overflow.
//   - Rounding to 2 decimal places works correctly at scale.
//   - The accounting invariant (total = system + agent + partner) holds.
// =============================================================================
func TestCalculateCommission_LargeAmount(t *testing.T) {
	calc := newTestCalculator()
	ctx := context.Background()

	agentID := uuid.New()
	partnerID := uuid.New()

	input := CommissionInput{
		TransactionType:      models.TransactionTypeDeposit,
		TransactionID:        uuid.New(),
		MerchantID:           uuid.New(),
		TransactionAmount:    decimal.NewFromInt(100_000_000), // 100 million THB
		MerchantFeePct:       decimal.NewFromFloat(0.03),
		AgentID:              &agentID,
		AgentCommissionPct:   decimal.NewFromFloat(0.01),
		PartnerID:            &partnerID,
		PartnerCommissionPct: decimal.NewFromFloat(0.005),
		Currency:             "THB",
	}

	result, err := calc.CalculateCommission(ctx, input)
	if err != nil {
		t.Fatalf("CalculateCommission returned unexpected error: %v", err)
	}

	comm := result.Commission

	// Total fee: 100,000,000 * 0.03 = 3,000,000
	assertDecimalEqual(t, "TotalFeeAmount", comm.TotalFeeAmount, "3000000")

	// Agent: 100,000,000 * 0.01 = 1,000,000
	assertDecimalEqual(t, "AgentAmount", comm.AgentAmount, "1000000")

	// Partner: 100,000,000 * 0.005 = 500,000
	assertDecimalEqual(t, "PartnerAmount", comm.PartnerAmount, "500000")

	// System: 3,000,000 - 1,000,000 - 500,000 = 1,500,000
	assertDecimalEqual(t, "SystemAmount", comm.SystemAmount, "1500000")

	// Verify accounting invariant at large scale.
	sum := comm.SystemAmount.Add(comm.AgentAmount).Add(comm.PartnerAmount)
	if !sum.Equal(comm.TotalFeeAmount) {
		t.Errorf("accounting invariant violated at large scale: sum=%s, total_fee=%s", sum, comm.TotalFeeAmount)
	}
}

// =============================================================================
// TestCalculateCommission_SmallAmount tests with a very small transaction
// amount (100 THB) which is the minimum realistic payment amount.
//
// This verifies correct rounding behaviour for small decimal values and
// ensures the system doesn't produce negative or nonsensical results
// for amounts near the minimum.
// =============================================================================
func TestCalculateCommission_SmallAmount(t *testing.T) {
	calc := newTestCalculator()
	ctx := context.Background()

	agentID := uuid.New()
	partnerID := uuid.New()

	input := CommissionInput{
		TransactionType:      models.TransactionTypeDeposit,
		TransactionID:        uuid.New(),
		MerchantID:           uuid.New(),
		TransactionAmount:    decimal.NewFromInt(100), // 100 THB
		MerchantFeePct:       decimal.NewFromFloat(0.03),
		AgentID:              &agentID,
		AgentCommissionPct:   decimal.NewFromFloat(0.01),
		PartnerID:            &partnerID,
		PartnerCommissionPct: decimal.NewFromFloat(0.005),
		Currency:             "THB",
	}

	result, err := calc.CalculateCommission(ctx, input)
	if err != nil {
		t.Fatalf("CalculateCommission returned unexpected error: %v", err)
	}

	comm := result.Commission

	// Total fee: 100 * 0.03 = 3.00
	assertDecimalEqual(t, "TotalFeeAmount", comm.TotalFeeAmount, "3")

	// Agent: 100 * 0.01 = 1.00
	assertDecimalEqual(t, "AgentAmount", comm.AgentAmount, "1")

	// Partner: 100 * 0.005 = 0.50
	assertDecimalEqual(t, "PartnerAmount", comm.PartnerAmount, "0.5")

	// System: 3.00 - 1.00 - 0.50 = 1.50
	assertDecimalEqual(t, "SystemAmount", comm.SystemAmount, "1.5")

	// All amounts must be non-negative.
	if comm.SystemAmount.IsNegative() {
		t.Error("SystemAmount is negative for a small transaction; calculation error")
	}
}

// =============================================================================
// TestCalculateCommission_Constraint verifies that the calculator rejects
// input where agent_pct + partner_pct > merchant_fee_pct.
//
// This is a critical business rule: if the agent and partner percentages
// sum to more than the merchant fee, the system share would be negative,
// meaning the platform is paying money rather than earning it. This must
// never happen.
//
// The constraint is enforced at two levels:
//   1. Admin configuration time (when setting up merchant fee splits).
//   2. Defensively in CalculateCommission (this test verifies #2).
// =============================================================================
func TestCalculateCommission_Constraint(t *testing.T) {
	calc := newTestCalculator()
	ctx := context.Background()

	agentID := uuid.New()
	partnerID := uuid.New()

	input := CommissionInput{
		TransactionType:      models.TransactionTypeDeposit,
		TransactionID:        uuid.New(),
		MerchantID:           uuid.New(),
		TransactionAmount:    decimal.NewFromInt(10000),
		MerchantFeePct:       decimal.NewFromFloat(0.03),  // 3%
		AgentID:              &agentID,
		AgentCommissionPct:   decimal.NewFromFloat(0.02),  // 2%
		PartnerID:            &partnerID,
		PartnerCommissionPct: decimal.NewFromFloat(0.015), // 1.5%
		// Total: 2% + 1.5% = 3.5% > 3% merchant fee
		Currency: "THB",
	}

	result, err := calc.CalculateCommission(ctx, input)

	// The function must return an error because agent + partner exceeds the fee.
	if err == nil {
		t.Fatal("expected error when agent_pct + partner_pct > merchant_fee_pct, got nil")
	}
	if result != nil {
		t.Error("expected nil result when constraint is violated, got non-nil")
	}
}

// =============================================================================
// Helper: assertDecimalEqual compares a decimal.Decimal value against an
// expected string representation. Using string comparison avoids float
// precision issues and makes test output clear.
// =============================================================================
func assertDecimalEqual(t *testing.T, field string, got decimal.Decimal, wantStr string) {
	t.Helper()
	want, _ := decimal.NewFromString(wantStr)
	if !got.Equal(want) {
		t.Errorf("%s = %s, want %s", field, got.String(), wantStr)
	}
}
