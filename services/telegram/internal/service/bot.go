// Package service implements the core business logic for the telegram-service.
// This file contains the Telegram bot lifecycle management: starting
// long-polling for updates, routing incoming updates to the correct handler,
// processing slip photos, and sending reply messages to Telegram groups.
//
// The bot communicates with the Telegram Bot API using standard HTTP calls.
// It does NOT use a third-party Telegram library; instead, it makes direct
// API calls for maximum control and minimal dependencies.
package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/farritpcz/richpayment/pkg/logger"
)

// ---------------------------------------------------------------------------
// Telegram Bot API response types.
// ---------------------------------------------------------------------------

// TelegramUpdate represents a single update from the Telegram Bot API.
// Each update contains either a message, an edited message, a callback
// query, or other event types. For the RichPayment bot, we primarily care
// about messages that contain photos (slip images).
type TelegramUpdate struct {
	// UpdateID is the unique sequential identifier for this update. The bot
	// uses this to track which updates have already been processed and to
	// request only newer updates via the offset parameter.
	UpdateID int `json:"update_id"`

	// Message contains the message data when the update is a new message.
	// Nil for non-message updates (e.g. edited messages, callback queries).
	Message *TelegramMessage `json:"message,omitempty"`
}

// TelegramMessage represents a Telegram message. For slip verification, we
// are interested in messages that contain photos (the Photo field).
type TelegramMessage struct {
	// MessageID is the unique message identifier within the chat. Used for
	// reply_to_message_id when sending responses.
	MessageID int `json:"message_id"`

	// Chat contains information about the chat (group, channel, or private)
	// where the message was sent.
	Chat TelegramChat `json:"chat"`

	// Photo is a list of PhotoSize objects representing the same photo at
	// different resolutions. Nil if the message does not contain a photo.
	// We pick the largest resolution (last element) for best OCR quality.
	Photo []TelegramPhotoSize `json:"photo,omitempty"`
}

// TelegramChat represents a Telegram chat (group, supergroup, channel, or
// private chat). We use the ID to identify which merchant group the slip
// was posted in.
type TelegramChat struct {
	// ID is the unique identifier for the chat. Negative values indicate
	// group chats; positive values indicate private chats.
	ID int64 `json:"id"`

	// Title is the display title of the group/channel. Empty for private chats.
	Title string `json:"title,omitempty"`
}

// TelegramPhotoSize represents a photo at a specific resolution. Telegram
// provides multiple sizes for each uploaded photo; we use the largest one
// (highest resolution) for the best OCR and verification results.
type TelegramPhotoSize struct {
	// FileID is the Telegram-internal identifier for this file. Used to
	// download the actual image data via the getFile API endpoint.
	FileID string `json:"file_id"`

	// FileUniqueID is a unique identifier that remains constant across
	// different bots and file downloads. Useful for deduplication.
	FileUniqueID string `json:"file_unique_id"`

	// Width is the photo width in pixels.
	Width int `json:"width"`

	// Height is the photo height in pixels.
	Height int `json:"height"`

	// FileSize is the approximate file size in bytes. May be 0 if unknown.
	FileSize int `json:"file_size,omitempty"`
}

// telegramGetUpdatesResponse is the raw JSON response from the Telegram
// getUpdates API endpoint. It wraps the list of updates in an "ok" + "result"
// envelope.
type telegramGetUpdatesResponse struct {
	// Ok indicates whether the API call was successful.
	Ok bool `json:"ok"`

	// Result contains the list of pending updates.
	Result []TelegramUpdate `json:"result"`
}

// telegramGetFileResponse is the raw JSON response from the Telegram
// getFile API endpoint. It contains the file path needed to download
// the actual file content.
type telegramGetFileResponse struct {
	// Ok indicates whether the API call was successful.
	Ok bool `json:"ok"`

	// Result contains the file metadata including the download path.
	Result struct {
		// FileID is the file identifier (same as in PhotoSize).
		FileID string `json:"file_id"`

		// FilePath is the relative path for downloading the file.
		// Append this to https://api.telegram.org/file/bot{token}/ to
		// get the full download URL.
		FilePath string `json:"file_path"`
	} `json:"result"`
}

// ---------------------------------------------------------------------------
// BotService — Telegram bot lifecycle manager.
// ---------------------------------------------------------------------------

// BotService manages the Telegram bot's lifecycle: polling for updates,
// routing them to handlers, downloading photos, and sending replies.
// It holds references to the Telegram Bot API token, the slip verification
// service, and a mapping of Telegram group IDs to merchant UUIDs.
type BotService struct {
	// token is the Telegram Bot API token used to authenticate all API calls.
	// Format: "123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11".
	token string

	// slipSvc is the slip verification service that processes photo uploads.
	// It is called when a user sends a photo in a monitored group.
	slipSvc *SlipService

	// alertSvc is the alert service used to send security notifications.
	// Referenced here so the bot can forward certain events as alerts.
	alertSvc *AlertService

	// httpClient is the HTTP client used for all Telegram Bot API calls.
	// Configured with a generous timeout because file downloads can be slow.
	httpClient *http.Client
}

// NewBotService constructs a new BotService with all required dependencies.
//
// Parameters:
//   - token: the Telegram Bot API token for authentication.
//   - slipSvc: the slip verification service for processing photo uploads.
//   - alertSvc: the alert service for sending security notifications.
//
// Returns a ready-to-use BotService instance.
func NewBotService(token string, slipSvc *SlipService, alertSvc *AlertService) *BotService {
	return &BotService{
		token:   token,
		slipSvc: slipSvc,
		alertSvc: alertSvc,
		httpClient: &http.Client{
			// 60-second timeout for long-polling. The getUpdates call uses
			// a 30-second long-poll timeout, so the HTTP client must wait
			// at least that long plus some buffer for network latency.
			Timeout: 60 * time.Second,
		},
	}
}

// ---------------------------------------------------------------------------
// StartBot — begin long-polling for Telegram updates.
// ---------------------------------------------------------------------------

// StartBot starts the long-polling loop that continuously fetches new
// updates from the Telegram Bot API. It runs in a blocking loop until the
// context is cancelled (e.g. on graceful shutdown). Each batch of updates
// is processed sequentially by calling HandleUpdate for each one.
//
// The long-polling approach is simpler to deploy than webhooks because it
// does not require a public HTTPS endpoint, a TLS certificate, or webhook
// registration. However, webhooks are more efficient at scale.
//
// Parameters:
//   - ctx: context for cancellation. When cancelled, the polling loop exits.
func (b *BotService) StartBot(ctx context.Context) {
	logger.Info("starting telegram bot long-polling")

	// offset tracks the ID of the last processed update. By sending
	// offset = lastUpdateID + 1, we tell Telegram to only return newer
	// updates, effectively acknowledging all previous ones.
	offset := 0

	// Main polling loop. Runs until the context is cancelled.
	for {
		// Check if the context has been cancelled before making a new request.
		select {
		case <-ctx.Done():
			logger.Info("telegram bot polling stopped", "reason", ctx.Err())
			return
		default:
			// Continue polling.
		}

		// Fetch the next batch of updates from Telegram.
		// The timeout=30 parameter tells Telegram to hold the connection
		// open for up to 30 seconds if no updates are available (long-poll).
		url := fmt.Sprintf(
			"https://api.telegram.org/bot%s/getUpdates?offset=%d&timeout=30",
			b.token, offset,
		)

		// Create the HTTP request with the polling context so it can be
		// cancelled during shutdown.
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			logger.Error("failed to create getUpdates request", "error", err)
			// Brief pause before retrying to avoid tight error loops.
			time.Sleep(2 * time.Second)
			continue
		}

		// Execute the long-poll request.
		resp, err := b.httpClient.Do(req)
		if err != nil {
			// Network errors are expected during shutdown (context cancelled).
			if ctx.Err() != nil {
				return
			}
			logger.Error("getUpdates request failed", "error", err)
			time.Sleep(2 * time.Second)
			continue
		}

		// Read the full response body.
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			logger.Error("failed to read getUpdates response", "error", err)
			time.Sleep(2 * time.Second)
			continue
		}

		// Parse the JSON response.
		var updatesResp telegramGetUpdatesResponse
		if err := json.Unmarshal(body, &updatesResp); err != nil {
			logger.Error("failed to unmarshal getUpdates response", "error", err)
			time.Sleep(2 * time.Second)
			continue
		}

		// Check that the Telegram API returned a success status.
		if !updatesResp.Ok {
			logger.Error("getUpdates returned not ok", "body", string(body))
			time.Sleep(5 * time.Second)
			continue
		}

		// Process each update sequentially.
		for _, update := range updatesResp.Result {
			b.HandleUpdate(ctx, update)

			// Advance the offset so we don't re-process this update.
			if update.UpdateID >= offset {
				offset = update.UpdateID + 1
			}
		}
	}
}

// ---------------------------------------------------------------------------
// HandleUpdate — route an update to the correct handler.
// ---------------------------------------------------------------------------

// HandleUpdate examines a Telegram update and routes it to the appropriate
// handler based on its content. Currently, the bot only processes messages
// that contain photos (slip images). All other update types are silently
// ignored.
//
// Parameters:
//   - ctx: context for cancellation and deadline propagation.
//   - update: the Telegram update to process.
func (b *BotService) HandleUpdate(ctx context.Context, update TelegramUpdate) {
	// Only process updates that contain a message with a photo.
	// Ignore text-only messages, edited messages, and callback queries.
	if update.Message == nil || len(update.Message.Photo) == 0 {
		return
	}

	// Extract the largest photo resolution for best verification quality.
	// Telegram always orders photo sizes from smallest to largest, so the
	// last element has the highest resolution.
	photos := update.Message.Photo
	largestPhoto := photos[len(photos)-1]

	// Extract the group (chat) ID and message ID for reply functionality.
	groupID := update.Message.Chat.ID
	messageID := update.Message.MessageID
	fileID := largestPhoto.FileID

	logger.Info("received photo in group",
		"group_id", groupID,
		"message_id", messageID,
		"file_id", fileID,
		"file_size", largestPhoto.FileSize,
	)

	// Delegate to the photo handler, which downloads the image and
	// initiates the slip verification flow.
	b.HandlePhoto(ctx, groupID, messageID, fileID)
}

// ---------------------------------------------------------------------------
// HandlePhoto — download and process a slip photo.
// ---------------------------------------------------------------------------

// HandlePhoto downloads a photo from Telegram's servers and initiates the
// slip verification flow. It performs these steps:
//
//  1. Call the Telegram getFile API to get the file's download path.
//  2. Download the actual image bytes from Telegram's file server.
//  3. Convert the image data to base64 for the EasySlip API.
//  4. Call the SlipService to verify the slip.
//  5. Send a reply message to the group with the verification result.
//
// Parameters:
//   - ctx: context for cancellation.
//   - groupID: the Telegram chat ID of the group.
//   - messageID: the message ID of the photo message (for reply-to).
//   - fileID: the Telegram file ID of the photo to download.
func (b *BotService) HandlePhoto(ctx context.Context, groupID int64, messageID int, fileID string) {
	// ---------------------------------------------------------------
	// Step 1: Get the file path from Telegram.
	// The getFile API returns metadata including a temporary file path
	// that can be used to download the actual image bytes.
	// ---------------------------------------------------------------
	getFileURL := fmt.Sprintf(
		"https://api.telegram.org/bot%s/getFile?file_id=%s",
		b.token, fileID,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, getFileURL, nil)
	if err != nil {
		logger.Error("failed to create getFile request", "error", err)
		b.ReplyToGroup(ctx, groupID, messageID, "Internal error: could not retrieve file info.")
		return
	}

	resp, err := b.httpClient.Do(req)
	if err != nil {
		logger.Error("getFile request failed", "error", err)
		b.ReplyToGroup(ctx, groupID, messageID, "Internal error: could not contact Telegram API.")
		return
	}
	defer resp.Body.Close()

	// Parse the getFile response to extract the file path.
	var fileResp telegramGetFileResponse
	if err := json.NewDecoder(resp.Body).Decode(&fileResp); err != nil {
		logger.Error("failed to decode getFile response", "error", err)
		b.ReplyToGroup(ctx, groupID, messageID, "Internal error: invalid file response.")
		return
	}

	if !fileResp.Ok || fileResp.Result.FilePath == "" {
		logger.Error("getFile returned invalid result", "file_id", fileID)
		b.ReplyToGroup(ctx, groupID, messageID, "Internal error: file not found on Telegram.")
		return
	}

	// ---------------------------------------------------------------
	// Step 2: Download the actual image bytes.
	// Telegram hosts files at a temporary URL that expires after some time.
	// ---------------------------------------------------------------
	downloadURL := fmt.Sprintf(
		"https://api.telegram.org/file/bot%s/%s",
		b.token, fileResp.Result.FilePath,
	)

	dlReq, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		logger.Error("failed to create download request", "error", err)
		b.ReplyToGroup(ctx, groupID, messageID, "Internal error: could not create download request.")
		return
	}

	dlResp, err := b.httpClient.Do(dlReq)
	if err != nil {
		logger.Error("file download failed", "error", err)
		b.ReplyToGroup(ctx, groupID, messageID, "Internal error: could not download the slip image.")
		return
	}
	defer dlResp.Body.Close()

	// Read the entire image into memory for hashing and base64 encoding.
	imageBytes, err := io.ReadAll(dlResp.Body)
	if err != nil {
		logger.Error("failed to read downloaded image", "error", err)
		b.ReplyToGroup(ctx, groupID, messageID, "Internal error: could not read image data.")
		return
	}

	logger.Info("downloaded slip image",
		"group_id", groupID,
		"file_id", fileID,
		"image_size_bytes", len(imageBytes),
	)

	// ---------------------------------------------------------------
	// Step 3 & 4: Verify the slip via the SlipService.
	// The SlipService handles hashing, duplicate checks, EasySlip API
	// calls, order matching, and result persistence.
	// ---------------------------------------------------------------
	// TODO: In production, resolve the merchant ID from the group ID
	// using a group-to-merchant mapping stored in the database or cache.
	// For now we pass a zero UUID as a placeholder.
	result, err := b.slipSvc.VerifySlip(ctx, groupID, messageID, imageBytes)
	if err != nil {
		logger.Error("slip verification failed", "error", err, "group_id", groupID)
		b.ReplyToGroup(ctx, groupID, messageID, fmt.Sprintf("Slip verification error: %s", err.Error()))
		return
	}

	// ---------------------------------------------------------------
	// Step 5: Reply to the group with the verification result.
	// ---------------------------------------------------------------
	b.ReplyToGroup(ctx, groupID, messageID, result)
}

// ---------------------------------------------------------------------------
// ReplyToGroup — send a text reply to a Telegram group.
// ---------------------------------------------------------------------------

// ReplyToGroup sends a text message to a Telegram group as a reply to a
// specific message. This is used to report slip verification results back
// to the group where the slip was posted. The reply_to_message_id parameter
// creates a visible threading link in the Telegram UI.
//
// Parameters:
//   - ctx: context for cancellation.
//   - groupID: the Telegram chat ID to send the message to.
//   - replyToMsgID: the message ID to reply to (creates a thread link).
//   - text: the message text to send (supports Telegram markdown).
func (b *BotService) ReplyToGroup(ctx context.Context, groupID int64, replyToMsgID int, text string) {
	// Build the sendMessage API request payload.
	payload := map[string]interface{}{
		"chat_id":             groupID,
		"text":                text,
		"reply_to_message_id": replyToMsgID,
		"parse_mode":          "Markdown",
	}

	// Marshal the payload to JSON.
	body, err := json.Marshal(payload)
	if err != nil {
		logger.Error("failed to marshal sendMessage payload",
			"error", err,
			"group_id", groupID,
		)
		return
	}

	// Build the Telegram sendMessage API URL.
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", b.token)

	// Create the HTTP request with context support.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		logger.Error("failed to create sendMessage request",
			"error", err,
			"group_id", groupID,
		)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	// Execute the sendMessage request.
	resp, err := b.httpClient.Do(req)
	if err != nil {
		logger.Error("sendMessage request failed",
			"error", err,
			"group_id", groupID,
		)
		return
	}
	defer resp.Body.Close()

	// Log non-200 responses for debugging but do not treat them as fatal
	// errors — the slip verification result is still valid even if the
	// reply fails to send.
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		logger.Error("sendMessage returned non-200",
			"status", resp.StatusCode,
			"body", string(respBody),
			"group_id", groupID,
		)
		return
	}

	logger.Info("reply sent to group",
		"group_id", groupID,
		"reply_to", replyToMsgID,
	)
}
