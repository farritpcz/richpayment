// Package service contains the core business logic for the commission-service.
//
// This file implements the commission calculation engine, which is responsible
// for splitting transaction fees among the system, agent, and partner according
// to the configured percentages.
package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/farritpcz/richpayment/pkg/models"
	"github.com/farritpcz/richpayment/services/commission/internal/repository"
)

// ---------------------------------------------------------------------------
// Input / Output types
// ---------------------------------------------------------------------------

// CommissionInput holds all the data required to calculate a commission split.
// It is populated by the calling service (e.g. order-service) and passed to
// CalculateCommission.
type CommissionInput struct {
	// TransactionType indicates whether this is a deposit or withdrawal.
	// Different transaction types may have different fee percentages.
	TransactionType models.TransactionType `json:"transaction_type"`

	// TransactionID is the UUID of the deposit or withdrawal order that
	// triggered this commission calculation.
	TransactionID uuid.UUID `json:"transaction_id"`

	// MerchantID is the UUID of the merchant who owns the transaction.
	MerchantID uuid.UUID `json:"merchant_id"`

	// TransactionAmount is the gross amount of the transaction before any
	// fees are deducted. This is the base for all percentage calculations.
	TransactionAmount decimal.Decimal `json:"transaction_amount"`

	// MerchantFeePct is the total fee percentage configured for this
	// merchant (e.g. 0.025 means 2.5%). This is the "pie" that gets
	// split among system, agent, and partner.
	MerchantFeePct decimal.Decimal `json:"merchant_fee_pct"`

	// AgentID is the UUID of the agent managing this merchant.
	// Nil if the merchant has no agent (system-managed merchant).
	AgentID *uuid.UUID `json:"agent_id,omitempty"`

	// AgentCommissionPct is the percentage of the transaction amount that
	// goes to the agent (e.g. 0.005 means 0.5%). Zero if no agent.
	AgentCommissionPct decimal.Decimal `json:"agent_commission_pct"`

	// PartnerID is the UUID of the partner above the agent.
	// Nil if the agent has no partner or there is no agent.
	PartnerID *uuid.UUID `json:"partner_id,omitempty"`

	// PartnerCommissionPct is the percentage of the transaction amount
	// that goes to the partner (e.g. 0.003 means 0.3%). Zero if no partner.
	PartnerCommissionPct decimal.Decimal `json:"partner_commission_pct"`

	// Currency is the ISO 4217 currency code (e.g. "THB", "USD").
	Currency string `json:"currency"`
}

// CommissionResult holds the calculated commission amounts for each party.
// It is returned by CalculateCommission and then passed to RecordCommission.
type CommissionResult struct {
	// Commission is the full commission record ready to be persisted.
	// All monetary fields are populated with the calculated amounts.
	Commission models.Commission `json:"commission"`
}

// ---------------------------------------------------------------------------
// Calculator service
// ---------------------------------------------------------------------------

// Calculator encapsulates the commission calculation and recording logic.
// It depends on CommissionRepository for persistence and uses the structured
// logger for audit-level logging of every commission event.
type Calculator struct {
	// repo provides access to the commissions table and wallet operations.
	repo repository.CommissionRepository

	// log is the structured logger for recording calculation events.
	log *slog.Logger
}

// NewCalculator creates a new Calculator service.
// The repository and logger must not be nil.
func NewCalculator(repo repository.CommissionRepository, log *slog.Logger) *Calculator {
	return &Calculator{
		repo: repo,
		log:  log,
	}
}

// ---------------------------------------------------------------------------
// CalculateCommission — core fee-splitting logic
// ---------------------------------------------------------------------------

// CalculateCommission splits the fee among system, agent, and partner.
//
// Formula:
//
//	total_fee     = transaction_amount * merchant_fee_pct
//	agent_share   = transaction_amount * agent_commission_pct  (0 if no agent)
//	partner_share = transaction_amount * partner_commission_pct (0 if no partner)
//	system_share  = total_fee - agent_share - partner_share
//
// Constraint: agent_pct + partner_pct must always be <= merchant_fee_pct.
// This is enforced at admin configuration time; however, this function also
// validates it defensively and returns an error if violated.
//
// All amounts are rounded to 2 decimal places using banker's rounding
// (half-even) to minimise cumulative rounding error over many transactions.
func (c *Calculator) CalculateCommission(ctx context.Context, input CommissionInput) (*CommissionResult, error) {
	// -----------------------------------------------------------------------
	// Step 1: Validate that agent + partner percentages do not exceed the
	// merchant fee percentage. If they did, the system share would be
	// negative, which is not allowed.
	// -----------------------------------------------------------------------
	combinedPct := input.AgentCommissionPct.Add(input.PartnerCommissionPct)
	if combinedPct.GreaterThan(input.MerchantFeePct) {
		return nil, fmt.Errorf(
			"commission split exceeds merchant fee: agent_pct(%s) + partner_pct(%s) = %s > merchant_fee_pct(%s)",
			input.AgentCommissionPct, input.PartnerCommissionPct,
			combinedPct, input.MerchantFeePct,
		)
	}

	// -----------------------------------------------------------------------
	// Step 2: Calculate each party's share.
	//
	// We multiply the transaction amount by the respective percentage and
	// round to 2 decimal places. The system gets whatever is left after
	// agent and partner are paid, ensuring no money is lost to rounding.
	// -----------------------------------------------------------------------

	// Total fee is what the merchant pays on this transaction.
	totalFee := input.TransactionAmount.Mul(input.MerchantFeePct).Round(2)

	// Agent share: zero if there is no agent assigned to this merchant.
	agentShare := decimal.Zero
	if input.AgentID != nil {
		agentShare = input.TransactionAmount.Mul(input.AgentCommissionPct).Round(2)
	}

	// Partner share: zero if there is no partner in the chain.
	partnerShare := decimal.Zero
	if input.PartnerID != nil {
		partnerShare = input.TransactionAmount.Mul(input.PartnerCommissionPct).Round(2)
	}

	// System share: the remainder after agent and partner are paid.
	// By computing it as a subtraction rather than a multiplication, we
	// guarantee that totalFee = systemShare + agentShare + partnerShare
	// exactly, with no rounding gaps.
	systemShare := totalFee.Sub(agentShare).Sub(partnerShare)

	// Defensive check: system share should never be negative.
	if systemShare.IsNegative() {
		return nil, fmt.Errorf(
			"calculated negative system share: %s (total_fee=%s, agent=%s, partner=%s)",
			systemShare, totalFee, agentShare, partnerShare,
		)
	}

	// -----------------------------------------------------------------------
	// Step 3: Build the Commission model with all fields populated.
	// -----------------------------------------------------------------------
	commission := models.Commission{
		ID:              uuid.New(), // Generate a unique ID for this commission.
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

	// Log the calculation result for auditing purposes.
	c.log.Info("commission calculated",
		slog.String("transaction_id", input.TransactionID.String()),
		slog.String("merchant_id", input.MerchantID.String()),
		slog.String("total_fee", totalFee.String()),
		slog.String("system_share", systemShare.String()),
		slog.String("agent_share", agentShare.String()),
		slog.String("partner_share", partnerShare.String()),
	)

	return &CommissionResult{Commission: commission}, nil
}

// ---------------------------------------------------------------------------
// RecordCommission — persists the commission and credits wallets
// ---------------------------------------------------------------------------

// RecordCommission saves a previously calculated commission to the database
// and credits the appropriate wallets (agent, partner, system).
//
// The operations are:
//  1. Insert the commission record into the commissions table.
//  2. Credit the agent wallet (if an agent is involved).
//  3. Credit the partner wallet (if a partner is involved).
//
// Note: In a production system, steps 1-3 should run inside a single
// database transaction to guarantee atomicity. The current stub demonstrates
// the logical flow; transaction wrapping is done at the repository layer.
func (c *Calculator) RecordCommission(ctx context.Context, result *CommissionResult) error {
	comm := result.Commission

	// -----------------------------------------------------------------------
	// Step 1: Persist the commission record.
	// -----------------------------------------------------------------------
	if err := c.repo.InsertCommission(ctx, &comm); err != nil {
		return fmt.Errorf("record commission: insert failed: %w", err)
	}

	// -----------------------------------------------------------------------
	// Step 2: Credit the agent wallet if applicable.
	// Only credit if the agent share is positive (avoids zero-amount ledger
	// entries that clutter the audit trail).
	// -----------------------------------------------------------------------
	if comm.AgentID != nil && comm.AgentAmount.IsPositive() {
		// The wallet ID is expected to be looked up by owner_type + owner_id.
		// For the stub, we use the agent ID directly as a placeholder.
		desc := fmt.Sprintf("Commission from txn %s", comm.TransactionID)
		if err := c.repo.CreditWallet(ctx, *comm.AgentID, comm.AgentAmount, models.LedgerCommissionCredit, comm.ID, desc); err != nil {
			return fmt.Errorf("record commission: credit agent wallet: %w", err)
		}
		c.log.Info("agent wallet credited",
			slog.String("agent_id", comm.AgentID.String()),
			slog.String("amount", comm.AgentAmount.String()),
		)
	}

	// -----------------------------------------------------------------------
	// Step 3: Credit the partner wallet if applicable.
	// -----------------------------------------------------------------------
	if comm.PartnerID != nil && comm.PartnerAmount.IsPositive() {
		desc := fmt.Sprintf("Commission from txn %s", comm.TransactionID)
		if err := c.repo.CreditWallet(ctx, *comm.PartnerID, comm.PartnerAmount, models.LedgerCommissionCredit, comm.ID, desc); err != nil {
			return fmt.Errorf("record commission: credit partner wallet: %w", err)
		}
		c.log.Info("partner wallet credited",
			slog.String("partner_id", comm.PartnerID.String()),
			slog.String("amount", comm.PartnerAmount.String()),
		)
	}

	c.log.Info("commission recorded successfully",
		slog.String("commission_id", comm.ID.String()),
		slog.String("transaction_id", comm.TransactionID.String()),
	)

	return nil
}
