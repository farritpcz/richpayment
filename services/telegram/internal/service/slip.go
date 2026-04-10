// Package service (slip.go) implements the slip verification pipeline.
// When a user posts a bank transfer slip photo in a Telegram group, this
// service orchestrates the full verification flow: image hashing, duplicate
// detection, EasySlip API verification, order matching, and result storage.
//
// The verification flow is designed to catch duplicates at two levels:
//  1. Image-level: SHA-256 hash of the raw image bytes prevents the same
//     photo from being submitted twice.
//  2. Transaction-level: the bank transaction reference from EasySlip
//     prevents different photos of the same slip from being accepted.
package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/farritpcz/richpayment/pkg/logger"
	"github.com/farritpcz/richpayment/services/telegram/internal/easyslip"
	"github.com/farritpcz/richpayment/services/telegram/internal/repository"
)

// ---------------------------------------------------------------------------
// SlipService — orchestrates the slip verification pipeline.
// ---------------------------------------------------------------------------

// SlipService coordinates the end-to-end slip verification process. It
// connects the EasySlip API client, the slip verification repository, and
// the order-matching logic into a single coherent pipeline.
type SlipService struct {
	// easySlipClient is the HTTP client for the EasySlip verification API.
	// It sends base64-encoded slip images and returns parsed transaction data.
	easySlipClient *easyslip.Client

	// slipRepo is the persistence layer for slip verification records.
	// Used to store results and check for duplicates.
	slipRepo repository.SlipRepository
}

// NewSlipService constructs a new SlipService with the given dependencies.
//
// Parameters:
//   - easySlipClient: the configured EasySlip API client.
//   - slipRepo: the repository for storing and querying slip verifications.
//
// Returns a ready-to-use SlipService instance.
func NewSlipService(easySlipClient *easyslip.Client, slipRepo repository.SlipRepository) *SlipService {
	return &SlipService{
		easySlipClient: easySlipClient,
		slipRepo:       slipRepo,
	}
}

// ---------------------------------------------------------------------------
// VerifySlip — the main verification pipeline.
// ---------------------------------------------------------------------------

// VerifySlip runs the complete slip verification pipeline for a photo
// received in a Telegram group. The pipeline performs the following steps
// in order, short-circuiting at the first failure or duplicate:
//
//  1. Compute SHA-256 hash of the raw image bytes for duplicate detection.
//  2. Check if a slip with the same image hash already exists in the DB.
//  3. Call the EasySlip API to extract transaction data from the slip.
//  4. Check if a slip with the same transaction reference already exists.
//  5. Find a matching pending deposit order (by amount + merchant).
//  6. If the matching order is already completed (e.g. SMS caught it first),
//     return "already completed" with details.
//  7. If a pending match is found, trigger order completion.
//  8. If no match is found, return "no matching order".
//  9. Store the verification result in the slip_verifications table.
//
// Parameters:
//   - ctx: context for cancellation and deadline propagation.
//   - groupID: the Telegram chat ID of the group (for record-keeping).
//   - messageID: the Telegram message ID of the slip photo.
//   - imageData: the raw image bytes downloaded from Telegram.
//
// Returns a human-readable result string (for replying to the Telegram
// group) and an error if the pipeline encounters an unrecoverable failure.
func (s *SlipService) VerifySlip(
	ctx context.Context,
	groupID int64,
	messageID int,
	imageData []byte,
) (string, error) {

	// ---------------------------------------------------------------
	// Step 1: Compute SHA-256 hash of the image for duplicate detection.
	// SHA-256 is used because it is fast, widely supported, and has
	// negligible collision probability for our use case.
	// ---------------------------------------------------------------
	hash := sha256.Sum256(imageData)
	imageHash := fmt.Sprintf("%x", hash[:])

	logger.Info("computed slip image hash",
		"image_hash", imageHash,
		"group_id", groupID,
		"message_id", messageID,
	)

	// ---------------------------------------------------------------
	// Step 2: Check for duplicate by image hash.
	// If the same image was already submitted, reject it immediately.
	// This prevents users from submitting the same slip photo twice.
	// ---------------------------------------------------------------
	existingByHash, err := s.slipRepo.GetByImageHash(ctx, imageHash)
	if err != nil {
		return "", fmt.Errorf("check duplicate by image hash: %w", err)
	}

	if existingByHash != nil {
		logger.Warn("duplicate slip detected by image hash",
			"image_hash", imageHash,
			"existing_id", existingByHash.ID.String(),
			"group_id", groupID,
		)

		// Store the duplicate attempt for audit purposes.
		s.storeVerificationResult(ctx, groupID, messageID, imageHash, "", nil,
			repository.SlipStatusDuplicate, "duplicate image hash: already submitted",
		)

		return fmt.Sprintf(
			"Duplicate slip detected. This image was already submitted (ref: %s).",
			existingByHash.TransactionRef,
		), nil
	}

	// ---------------------------------------------------------------
	// Step 3: Call EasySlip API to verify and extract slip data.
	// Encode the raw image bytes as base64 for the API request.
	// ---------------------------------------------------------------
	base64Image := base64.StdEncoding.EncodeToString(imageData)

	slipResult, err := s.easySlipClient.VerifySlip(ctx, base64Image)
	if err != nil {
		logger.Error("easyslip verification failed",
			"error", err,
			"group_id", groupID,
			"message_id", messageID,
		)

		// Store the failed attempt for audit purposes.
		s.storeVerificationResult(ctx, groupID, messageID, imageHash, "", nil,
			repository.SlipStatusFailed, fmt.Sprintf("easyslip error: %s", err.Error()),
		)

		return "", fmt.Errorf("easyslip verification: %w", err)
	}

	logger.Info("easyslip returned result",
		"ref", slipResult.Ref,
		"amount", slipResult.Amount.String(),
		"sender", slipResult.Sender,
		"receiver", slipResult.Receiver,
	)

	// ---------------------------------------------------------------
	// Step 4: Check for duplicate by transaction reference.
	// Even if the image is different (e.g. screenshot vs. photo of
	// screen), the same transaction ref means it is the same transfer.
	// ---------------------------------------------------------------
	existingByRef, err := s.slipRepo.GetByTransactionRef(ctx, slipResult.Ref)
	if err != nil {
		return "", fmt.Errorf("check duplicate by transaction ref: %w", err)
	}

	if existingByRef != nil {
		logger.Warn("duplicate slip detected by transaction ref",
			"ref", slipResult.Ref,
			"existing_id", existingByRef.ID.String(),
			"group_id", groupID,
		)

		// Store the duplicate for audit.
		s.storeVerificationResult(ctx, groupID, messageID, imageHash, slipResult.Ref, nil,
			repository.SlipStatusDuplicate,
			fmt.Sprintf("duplicate transaction ref: %s", slipResult.Ref),
		)

		return fmt.Sprintf(
			"Duplicate slip detected. Transaction ref %s was already processed.",
			slipResult.Ref,
		), nil
	}

	// ---------------------------------------------------------------
	// Step 5-8: Order matching.
	// TODO: In production, this section would:
	//   - Query the deposit_orders table for a pending order matching
	//     the slip amount and the merchant associated with this group.
	//   - If a match is found and the order is already completed (Step 6),
	//     return "already completed by SMS/email".
	//   - If a match is found and pending (Step 7), trigger completion.
	//   - If no match (Step 8), return "no matching order".
	//
	// For now, we log the slip data and return a verification-only
	// result. The order matching integration will be completed when
	// the inter-service communication layer is finalised.
	// ---------------------------------------------------------------

	logger.Info("slip verified, attempting order match",
		"ref", slipResult.Ref,
		"amount", slipResult.Amount.String(),
		"group_id", groupID,
	)

	// Store the successful verification (pending order match).
	s.storeVerificationResult(ctx, groupID, messageID, imageHash, slipResult.Ref, nil,
		repository.SlipStatusNoMatch,
		"slip verified, no matching order found (order matching pending implementation)",
	)

	// ---------------------------------------------------------------
	// Step 9: Return the verification result as a human-readable message.
	// ---------------------------------------------------------------
	return fmt.Sprintf(
		"Slip verified.\nRef: %s\nAmount: %s THB\nSender: %s\nReceiver: %s\nStatus: Awaiting order match.",
		slipResult.Ref,
		slipResult.Amount.StringFixed(2),
		slipResult.Sender,
		slipResult.Receiver,
	), nil
}

// ---------------------------------------------------------------------------
// storeVerificationResult — persist the verification outcome.
// ---------------------------------------------------------------------------

// storeVerificationResult saves a SlipVerification record to the database
// for auditing and duplicate detection. This method is called at every
// exit point of the VerifySlip pipeline to ensure a complete audit trail.
//
// Parameters:
//   - ctx: context for the database operation.
//   - groupID: the Telegram group where the slip was posted.
//   - messageID: the Telegram message ID of the slip photo.
//   - imageHash: the SHA-256 hex digest of the image.
//   - transactionRef: the bank transaction reference (empty if not available).
//   - orderID: the matched order ID (nil if no match).
//   - status: the verification outcome status.
//   - detail: a human-readable explanation of the outcome.
func (s *SlipService) storeVerificationResult(
	ctx context.Context,
	groupID int64,
	messageID int,
	imageHash string,
	transactionRef string,
	orderID *uuid.UUID,
	status repository.SlipVerificationStatus,
	detail string,
) {
	// Build the verification record with all available data.
	record := &repository.SlipVerification{
		ID:                uuid.New(),
		TelegramGroupID:   groupID,
		TelegramMessageID: messageID,
		ImageHash:         imageHash,
		TransactionRef:    transactionRef,
		OrderID:           orderID,
		Status:            status,
		StatusDetail:      detail,
		CreatedAt:         time.Now().UTC(),
	}

	// Attempt to persist the record. Log errors but do not propagate
	// them — the verification result itself should still be returned
	// to the user even if the audit record fails to save.
	if err := s.slipRepo.Create(ctx, record); err != nil {
		logger.Error("failed to store slip verification result",
			"error", err,
			"image_hash", imageHash,
			"status", string(status),
		)
	}
}
