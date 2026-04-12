// Package repository provides PostgreSQL-backed implementations of the
// data-access interfaces used by the telegram-service. This file implements
// the SlipRepository interface using the jackc/pgx driver and pgxpool
// connection pool for production-grade, concurrent-safe database access.
//
// The slip_verifications table is partitioned by created_at, so the primary
// key is a composite (id, created_at). All queries that touch this table
// should include created_at in ORDER BY to help the query planner prune
// irrelevant partitions.
package repository

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

// ---------------------------------------------------------------------------
// Compile-time interface satisfaction check.
// ---------------------------------------------------------------------------

// Verify at compile time that PostgresSlipRepo fully implements the
// SlipRepository interface. If any method is missing or has the wrong
// signature, the build will fail with a clear error message.
var _ SlipRepository = (*PostgresSlipRepo)(nil)

// ---------------------------------------------------------------------------
// PostgresSlipRepo — PostgreSQL implementation of SlipRepository.
// ---------------------------------------------------------------------------

// PostgresSlipRepo is the PostgreSQL-backed implementation of the
// SlipRepository interface. It persists slip verification records into the
// slip_verifications table and provides lookup methods for duplicate
// detection by image hash and transaction reference.
//
// This implementation is safe for concurrent use by multiple goroutines
// because the underlying pgxpool.Pool manages connection concurrency.
type PostgresSlipRepo struct {
	// pool is the pgx connection pool shared across all repository calls.
	// The pool handles connection acquisition, release, health checks, and
	// max-connection limits transparently.
	pool *pgxpool.Pool
}

// NewPostgresSlipRepo constructs a new PostgresSlipRepo backed by the given
// connection pool. The caller is responsible for creating, configuring, and
// eventually closing the pool. Typical usage:
//
//	pool, _ := pgxpool.New(ctx, databaseURL)
//	repo := repository.NewPostgresSlipRepo(pool)
func NewPostgresSlipRepo(pool *pgxpool.Pool) *PostgresSlipRepo {
	return &PostgresSlipRepo{pool: pool}
}

// ---------------------------------------------------------------------------
// Create — insert a new slip verification record.
// ---------------------------------------------------------------------------

// Create inserts a new SlipVerification record into the slip_verifications
// table. The caller must populate ID, CreatedAt, and all other required
// fields before calling this method.
//
// Column mapping from struct to table:
//   - ID            -> id
//   - MerchantID    -> merchant_id
//   - TelegramGroupID   -> telegram_group_id
//   - TelegramMessageID -> telegram_message_id
//   - ImageHash     -> image_hash
//   - TransactionRef -> easyslip_ref
//   - Amount        -> easyslip_amount
//   - SenderName    -> easyslip_sender
//   - ReceiverName  -> easyslip_receiver
//   - OrderID       -> matched_order_id  (nullable UUID)
//   - Status        -> status
//   - RawResponse   -> easyslip_raw      (cast to JSONB)
//   - CreatedAt     -> created_at
//
// Note: StatusDetail is NOT persisted to its own column. It is considered
// a transient field; the raw EasySlip API response in easyslip_raw contains
// all the detail needed for debugging and audit.
//
// Returns an error if the INSERT fails (e.g. constraint violation, connection
// loss, or context cancellation).
func (r *PostgresSlipRepo) Create(ctx context.Context, sv *SlipVerification) error {
	// SQL INSERT statement mapping all SlipVerification fields to their
	// corresponding columns in the slip_verifications table.
	// The easyslip_raw column is JSONB, so we cast the raw JSON string
	// using $12::jsonb to ensure PostgreSQL stores it as binary JSON
	// rather than a plain text string.
	const query = `
		INSERT INTO slip_verifications (
			id,
			merchant_id,
			telegram_group_id,
			telegram_message_id,
			image_hash,
			easyslip_ref,
			easyslip_amount,
			easyslip_sender,
			easyslip_receiver,
			matched_order_id,
			status,
			easyslip_raw,
			created_at
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8, $9, $10,
			$11, $12::jsonb, $13
		)
	`

	// Execute the insert with all struct fields mapped to positional
	// placeholders. pgx handles type conversion for uuid.UUID,
	// decimal.Decimal, *uuid.UUID (nullable), and time.Time automatically.
	_, err := r.pool.Exec(ctx, query,
		sv.ID,                 // $1  — id (UUID)
		sv.MerchantID,         // $2  — merchant_id (UUID)
		sv.TelegramGroupID,    // $3  — telegram_group_id (BIGINT)
		sv.TelegramMessageID,  // $4  — telegram_message_id (INT)
		sv.ImageHash,          // $5  — image_hash (TEXT)
		sv.TransactionRef,     // $6  — easyslip_ref (TEXT)
		sv.Amount,             // $7  — easyslip_amount (NUMERIC)
		sv.SenderName,         // $8  — easyslip_sender (TEXT)
		sv.ReceiverName,       // $9  — easyslip_receiver (TEXT)
		sv.OrderID,            // $10 — matched_order_id (UUID, nullable)
		sv.Status,             // $11 — status (TEXT/ENUM)
		sv.RawResponse,        // $12 — easyslip_raw (JSONB, cast from text)
		sv.CreatedAt,          // $13 — created_at (TIMESTAMPTZ)
	)
	if err != nil {
		// Wrap the error with context so callers can identify which
		// repository operation failed without inspecting stack traces.
		return fmt.Errorf("insert slip verification: %w", err)
	}

	return nil
}

// ---------------------------------------------------------------------------
// GetByImageHash — duplicate detection by SHA-256 image hash.
// ---------------------------------------------------------------------------

// GetByImageHash looks up the most recent slip verification record that
// matches the given SHA-256 image hash. This is the primary mechanism for
// detecting duplicate slip submissions — if two different photos produce
// the same hash, the second submission is flagged as a duplicate.
//
// The query orders by created_at DESC and limits to 1 row so that the
// newest matching record is returned. This is useful when the same slip
// image was submitted multiple times and we want the latest status.
//
// Returns:
//   - (*SlipVerification, nil) if a matching record was found.
//   - (nil, nil)               if no record exists with that hash.
//   - (nil, error)             if a database error occurred.
func (r *PostgresSlipRepo) GetByImageHash(ctx context.Context, imageHash string) (*SlipVerification, error) {
	// SQL query to find the most recent slip verification with the given
	// image hash. ORDER BY created_at DESC ensures we get the latest
	// record, and LIMIT 1 caps the result to a single row.
	const query = `
		SELECT
			id,
			merchant_id,
			telegram_group_id,
			telegram_message_id,
			image_hash,
			easyslip_ref,
			easyslip_amount,
			easyslip_sender,
			easyslip_receiver,
			matched_order_id,
			status,
			easyslip_raw,
			created_at
		FROM slip_verifications
		WHERE image_hash = $1
		ORDER BY created_at DESC
		LIMIT 1
	`

	// scanSlipRow handles the Scan call and returns the populated struct.
	return r.scanSlipRow(ctx, query, imageHash)
}

// ---------------------------------------------------------------------------
// GetByTransactionRef — duplicate detection by bank transaction reference.
// ---------------------------------------------------------------------------

// GetByTransactionRef looks up the most recent slip verification record
// that matches the given bank transaction reference (easyslip_ref). This
// provides a second layer of duplicate detection — even if the image bytes
// differ (e.g. screenshot vs. photo), the same underlying bank transfer
// will have the same reference number.
//
// Returns:
//   - (*SlipVerification, nil) if a matching record was found.
//   - (nil, nil)               if no record exists with that reference.
//   - (nil, error)             if a database error occurred.
func (r *PostgresSlipRepo) GetByTransactionRef(ctx context.Context, ref string) (*SlipVerification, error) {
	// SQL query to find the most recent slip verification with the given
	// transaction reference. Same ordering strategy as GetByImageHash.
	const query = `
		SELECT
			id,
			merchant_id,
			telegram_group_id,
			telegram_message_id,
			image_hash,
			easyslip_ref,
			easyslip_amount,
			easyslip_sender,
			easyslip_receiver,
			matched_order_id,
			status,
			easyslip_raw,
			created_at
		FROM slip_verifications
		WHERE easyslip_ref = $1
		ORDER BY created_at DESC
		LIMIT 1
	`

	// Reuse the same scanning helper for consistency.
	return r.scanSlipRow(ctx, query, ref)
}

// ---------------------------------------------------------------------------
// Internal helpers.
// ---------------------------------------------------------------------------

// scanSlipRow executes a query that is expected to return at most one row
// from the slip_verifications table, scans the columns into a
// SlipVerification struct, and returns it.
//
// If the query returns no rows (pgx.ErrNoRows), this method returns
// (nil, nil) — matching the contract defined by the SlipRepository
// interface where "not found" is not considered an error.
//
// This helper eliminates duplicated Scan code across GetByImageHash and
// GetByTransactionRef, which share the same SELECT column list.
func (r *PostgresSlipRepo) scanSlipRow(ctx context.Context, query string, arg interface{}) (*SlipVerification, error) {
	// Prepare the target struct to receive scanned column values.
	var sv SlipVerification

	// rawJSON will hold the easyslip_raw JSONB column value as a string.
	// pgx can scan JSONB into a *string or []byte; we use string to match
	// the RawResponse field type.
	var rawJSON *string

	// easyslipAmount is scanned as a decimal.Decimal via the shopspring
	// pgx type. We declare it here so the Scan call reads it correctly.
	var easyslipAmount decimal.Decimal

	// Execute the query with the single parameter (image_hash or easyslip_ref).
	// QueryRow returns exactly one row or pgx.ErrNoRows.
	err := r.pool.QueryRow(ctx, query, arg).Scan(
		&sv.ID,                // id (UUID)
		&sv.MerchantID,        // merchant_id (UUID)
		&sv.TelegramGroupID,   // telegram_group_id (BIGINT)
		&sv.TelegramMessageID, // telegram_message_id (INT)
		&sv.ImageHash,         // image_hash (TEXT)
		&sv.TransactionRef,    // easyslip_ref (TEXT)
		&easyslipAmount,       // easyslip_amount (NUMERIC)
		&sv.SenderName,        // easyslip_sender (TEXT)
		&sv.ReceiverName,      // easyslip_receiver (TEXT)
		&sv.OrderID,           // matched_order_id (UUID, nullable)
		&sv.Status,            // status (TEXT/ENUM)
		&rawJSON,              // easyslip_raw (JSONB -> *string)
		&sv.CreatedAt,         // created_at (TIMESTAMPTZ)
	)
	if err != nil {
		// pgx.ErrNoRows means the WHERE clause matched zero rows.
		// Per the interface contract, this is NOT an error — it simply
		// means no duplicate was found. Return nil for both values.
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		// Any other error (connection loss, type mismatch, etc.) is
		// wrapped with context and propagated to the caller.
		return nil, fmt.Errorf("scan slip verification row: %w", err)
	}

	// Map the scanned values back to the struct fields.
	// Amount is assigned from the locally scanned decimal variable.
	sv.Amount = easyslipAmount

	// RawResponse is the JSONB column value as a plain string.
	// If the column is NULL (shouldn't happen, but defensively handled),
	// we leave RawResponse as an empty string.
	if rawJSON != nil {
		sv.RawResponse = *rawJSON
	}

	// StatusDetail is not stored in its own column — it's a transient
	// field that is only meaningful during the verification flow. When
	// reading from the database, it remains the zero value (empty string).

	return &sv, nil
}
