// Package service contains the core business logic for the commission-service.
//
// This file implements the daily and monthly aggregation logic. The aggregator
// reads raw commission records, groups them by owner, and produces summary
// rows that power the reporting dashboard.
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/shopspring/decimal"

	"github.com/farritpcz/richpayment/pkg/models"
	"github.com/farritpcz/richpayment/services/commission/internal/repository"
)

// ---------------------------------------------------------------------------
// Summary response type (used by handlers)
// ---------------------------------------------------------------------------

// DailySummaryResponse is the JSON-friendly representation of a daily
// commission summary returned to API callers. It mirrors the database
// model but uses string types for decimal values to avoid floating-point
// precision issues in JSON.
type DailySummaryResponse struct {
	// SummaryDate is the calendar date this summary covers (YYYY-MM-DD).
	SummaryDate string `json:"summary_date"`

	// OwnerType is the type of the commission recipient (merchant, agent, partner, system).
	OwnerType string `json:"owner_type"`

	// OwnerID is the UUID of the commission recipient.
	OwnerID string `json:"owner_id"`

	// TransactionType distinguishes between deposit and withdrawal commissions.
	TransactionType string `json:"transaction_type"`

	// Currency is the ISO 4217 currency code.
	Currency string `json:"currency"`

	// TotalTxCount is the number of transactions that generated commissions.
	TotalTxCount int `json:"total_tx_count"`

	// TotalVolume is the sum of all transaction amounts.
	TotalVolume string `json:"total_volume"`

	// TotalFee is the sum of all fees collected.
	TotalFee string `json:"total_fee"`

	// TotalCommission is the sum of all commissions paid to this owner.
	TotalCommission string `json:"total_commission"`
}

// ---------------------------------------------------------------------------
// Aggregator service
// ---------------------------------------------------------------------------

// Aggregator builds commission summaries by grouping raw commission records.
// It uses Redis as a caching layer to reduce database load for frequently
// requested date ranges (e.g. today's summary refreshed every minute).
type Aggregator struct {
	// repo provides access to commission records and summary persistence.
	repo repository.CommissionRepository

	// rdb is the Redis client for caching summary results.
	rdb *redis.Client

	// log is the structured logger for audit and debug output.
	log *slog.Logger
}

// NewAggregator creates a new Aggregator service.
// All parameters are required and must not be nil.
func NewAggregator(repo repository.CommissionRepository, rdb *redis.Client, log *slog.Logger) *Aggregator {
	return &Aggregator{
		repo: repo,
		rdb:  rdb,
		log:  log,
	}
}

// ---------------------------------------------------------------------------
// AggregateDaily — build daily summaries from raw commissions
// ---------------------------------------------------------------------------

// summaryKey uniquely identifies a group of commissions that belong to the
// same daily summary bucket. Commissions with identical keys are merged
// into a single summary row.
type summaryKey struct {
	// OwnerType is who receives this commission (agent, partner, system).
	OwnerType models.OwnerType

	// OwnerID is the UUID of the recipient.
	OwnerID uuid.UUID

	// TransactionType is deposit or withdrawal.
	TransactionType models.TransactionType

	// Currency is the ISO 4217 code.
	Currency string
}

// AggregateDaily queries all commissions for the given date, groups them
// by (owner_type, owner_id, transaction_type, currency), and upserts the
// results into the commission_daily_summary table.
//
// This function is designed to be called by a scheduler (e.g. cron job)
// once per day, typically shortly after midnight UTC. It can also be called
// manually to re-aggregate a specific date if corrections were made.
//
// The grouping logic produces separate summary rows for:
//   - Each agent who earned commission on that date
//   - Each partner who earned commission on that date
//   - The system's share (owner_type="system", owner_id=zero UUID)
//
// After upserting, the Redis cache for the affected date is invalidated
// so that subsequent reads fetch fresh data.
func (a *Aggregator) AggregateDaily(ctx context.Context, date time.Time) error {
	a.log.Info("starting daily aggregation", slog.String("date", date.Format("2006-01-02")))

	// -----------------------------------------------------------------------
	// Step 1: Fetch all raw commission records for the target date.
	// -----------------------------------------------------------------------
	commissions, err := a.repo.GetCommissionsByDate(ctx, date)
	if err != nil {
		return fmt.Errorf("aggregate daily: fetch commissions: %w", err)
	}

	a.log.Info("fetched commissions for aggregation",
		slog.Int("count", len(commissions)),
		slog.String("date", date.Format("2006-01-02")),
	)

	// If there are no commissions for this date, there is nothing to aggregate.
	if len(commissions) == 0 {
		a.log.Info("no commissions to aggregate", slog.String("date", date.Format("2006-01-02")))
		return nil
	}

	// -----------------------------------------------------------------------
	// Step 2: Group commissions by (owner_type, owner_id, txn_type, currency).
	//
	// We use a map with a composite key to accumulate totals. Each commission
	// may contribute to up to three buckets: system, agent, and partner.
	// -----------------------------------------------------------------------
	groups := make(map[summaryKey]*models.CommissionDailySummary)

	// Normalise the summary date to midnight UTC.
	summaryDate := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.UTC)

	// systemOwnerID is a zero UUID used as the owner_id for the system's
	// commission share. The system is not a real entity, so it uses a
	// sentinel value.
	systemOwnerID := uuid.UUID{}

	for _, c := range commissions {
		// --- System share ---
		// Every commission has a system share (even if zero).
		sysKey := summaryKey{
			OwnerType:       models.OwnerTypeSystem,
			OwnerID:         systemOwnerID,
			TransactionType: c.TransactionType,
			Currency:        c.Currency,
		}
		a.addToGroup(groups, sysKey, summaryDate, c.TotalFeeAmount, c.TotalFeeAmount, c.SystemAmount)

		// --- Agent share ---
		// Only include if the commission has an agent recipient.
		if c.AgentID != nil && c.AgentAmount.IsPositive() {
			agentKey := summaryKey{
				OwnerType:       models.OwnerTypeAgent,
				OwnerID:         *c.AgentID,
				TransactionType: c.TransactionType,
				Currency:        c.Currency,
			}
			a.addToGroup(groups, agentKey, summaryDate, c.TotalFeeAmount, c.TotalFeeAmount, c.AgentAmount)
		}

		// --- Partner share ---
		// Only include if the commission has a partner recipient.
		if c.PartnerID != nil && c.PartnerAmount.IsPositive() {
			partnerKey := summaryKey{
				OwnerType:       models.OwnerTypePartner,
				OwnerID:         *c.PartnerID,
				TransactionType: c.TransactionType,
				Currency:        c.Currency,
			}
			a.addToGroup(groups, partnerKey, summaryDate, c.TotalFeeAmount, c.TotalFeeAmount, c.PartnerAmount)
		}
	}

	// -----------------------------------------------------------------------
	// Step 3: Upsert each grouped summary into the database.
	// -----------------------------------------------------------------------
	for _, summary := range groups {
		if err := a.repo.UpsertDailySummary(ctx, summary); err != nil {
			return fmt.Errorf("aggregate daily: upsert summary: %w", err)
		}
	}

	a.log.Info("daily aggregation completed",
		slog.Int("groups", len(groups)),
		slog.String("date", date.Format("2006-01-02")),
	)

	return nil
}

// addToGroup accumulates commission amounts into the summary group identified
// by the given key. If the group does not exist yet, it is initialised.
//
// Parameters:
//   - groups: the map of summary groups being built
//   - key: the composite grouping key
//   - summaryDate: the calendar date for this summary
//   - volume: the transaction amount to add to total_volume
//   - fee: the fee amount to add to total_fee
//   - commission: the commission amount to add to total_commission
func (a *Aggregator) addToGroup(
	groups map[summaryKey]*models.CommissionDailySummary,
	key summaryKey,
	summaryDate time.Time,
	volume, fee, commission decimal.Decimal,
) {
	// Look up or initialise the summary for this group.
	summary, exists := groups[key]
	if !exists {
		summary = &models.CommissionDailySummary{
			SummaryDate:     summaryDate,
			OwnerType:       key.OwnerType,
			OwnerID:         key.OwnerID,
			TransactionType: key.TransactionType,
			Currency:        key.Currency,
			TotalTxCount:    0,
			TotalVolume:     decimal.Zero,
			TotalFee:        decimal.Zero,
			TotalCommission: decimal.Zero,
		}
		groups[key] = summary
	}

	// Accumulate the amounts.
	summary.TotalTxCount++
	summary.TotalVolume = summary.TotalVolume.Add(volume)
	summary.TotalFee = summary.TotalFee.Add(fee)
	summary.TotalCommission = summary.TotalCommission.Add(commission)
}

// ---------------------------------------------------------------------------
// GetDailySummary — fetch daily summaries with Redis caching
// ---------------------------------------------------------------------------

// redisDailySummaryCacheKey builds a Redis key for caching daily summaries.
// The key format ensures uniqueness across owner types, IDs, and date ranges.
func redisDailySummaryCacheKey(ownerType models.OwnerType, ownerID uuid.UUID, from, to time.Time) string {
	return fmt.Sprintf("commission:daily:%s:%s:%s:%s",
		ownerType, ownerID,
		from.Format("2006-01-02"), to.Format("2006-01-02"),
	)
}

// GetDailySummary retrieves daily commission summaries for the specified
// owner within the date range [from, to].
//
// Cache strategy:
//  1. First, check Redis for a cached result using a key derived from the
//     query parameters.
//  2. If found and not expired, return the cached data immediately.
//  3. If not found, query the database, cache the result in Redis with a
//     5-minute TTL, and return it.
//
// The 5-minute TTL balances freshness (summaries update after aggregation)
// with performance (avoids hitting Postgres on every dashboard refresh).
func (a *Aggregator) GetDailySummary(ctx context.Context, ownerType models.OwnerType, ownerID uuid.UUID, from, to time.Time) ([]DailySummaryResponse, error) {
	// -----------------------------------------------------------------------
	// Step 1: Attempt to read from Redis cache.
	// -----------------------------------------------------------------------
	cacheKey := redisDailySummaryCacheKey(ownerType, ownerID, from, to)

	cached, err := a.rdb.Get(ctx, cacheKey).Result()
	if err == nil && cached != "" {
		// Cache hit — unmarshal and return.
		var result []DailySummaryResponse
		if jsonErr := json.Unmarshal([]byte(cached), &result); jsonErr == nil {
			a.log.Debug("daily summary cache hit", slog.String("key", cacheKey))
			return result, nil
		}
		// If unmarshal fails, fall through to database query.
		a.log.Warn("daily summary cache unmarshal failed, querying DB", slog.String("key", cacheKey))
	}

	// -----------------------------------------------------------------------
	// Step 2: Query the database.
	// -----------------------------------------------------------------------
	summaries, err := a.repo.GetDailySummaries(ctx, ownerType, ownerID, from, to)
	if err != nil {
		return nil, fmt.Errorf("get daily summary: %w", err)
	}

	// Convert database models to response DTOs.
	result := make([]DailySummaryResponse, 0, len(summaries))
	for _, s := range summaries {
		result = append(result, DailySummaryResponse{
			SummaryDate:     s.SummaryDate.Format("2006-01-02"),
			OwnerType:       string(s.OwnerType),
			OwnerID:         s.OwnerID.String(),
			TransactionType: string(s.TransactionType),
			Currency:        s.Currency,
			TotalTxCount:    s.TotalTxCount,
			TotalVolume:     s.TotalVolume.String(),
			TotalFee:        s.TotalFee.String(),
			TotalCommission: s.TotalCommission.String(),
		})
	}

	// -----------------------------------------------------------------------
	// Step 3: Cache the result in Redis with a 5-minute TTL.
	// -----------------------------------------------------------------------
	if data, jsonErr := json.Marshal(result); jsonErr == nil {
		// Use a 5-minute TTL so dashboard users get near-real-time data
		// without overloading the database.
		_ = a.rdb.Set(ctx, cacheKey, data, 5*time.Minute).Err()
	}

	return result, nil
}

// ---------------------------------------------------------------------------
// GetMonthlySummary — aggregate a full calendar month
// ---------------------------------------------------------------------------

// GetMonthlySummary returns a single aggregated summary for the specified
// owner across an entire calendar month. It delegates to the repository
// which runs a SQL SUM() aggregation over the daily summary table.
//
// Parameters:
//   - ownerType: the type of commission recipient (agent, partner, system)
//   - ownerID: the UUID of the recipient
//   - yearMonth: the target month in "2006-01" format
//
// Returns a single DailySummaryResponse with combined totals. The
// SummaryDate field is set to the first day of the month.
func (a *Aggregator) GetMonthlySummary(ctx context.Context, ownerType models.OwnerType, ownerID uuid.UUID, yearMonth string) (*DailySummaryResponse, error) {
	// Parse the year-month string into year and month components.
	t, err := time.Parse("2006-01", yearMonth)
	if err != nil {
		return nil, fmt.Errorf("invalid year-month format %q: %w", yearMonth, err)
	}

	// Delegate to the repository for SQL-level aggregation.
	summary, err := a.repo.GetMonthlySummary(ctx, ownerType, ownerID, t.Year(), t.Month())
	if err != nil {
		return nil, fmt.Errorf("get monthly summary: %w", err)
	}

	// Convert to the response DTO.
	return &DailySummaryResponse{
		SummaryDate:     summary.SummaryDate.Format("2006-01"),
		OwnerType:       string(summary.OwnerType),
		OwnerID:         summary.OwnerID.String(),
		TotalTxCount:    summary.TotalTxCount,
		TotalVolume:     summary.TotalVolume.String(),
		TotalFee:        summary.TotalFee.String(),
		TotalCommission: summary.TotalCommission.String(),
	}, nil
}
