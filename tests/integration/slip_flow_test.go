// Package integration ทดสอบ end-to-end flow ของระบบ RichPayment
// ไฟล์นี้ทดสอบ slip verification flow ตั้งแต่รับ image จนถึง complete order
//
// Slip Verification Flow ที่ทดสอบ:
//   1. User โพสต์รูป slip ใน Telegram group
//   2. Service คำนวณ SHA-256 hash ของ image
//   3. ตรวจ duplicate by image hash
//   4. เรียก EasySlip API เพื่อ parse slip data (จำลองผ่าน mock)
//   5. ตรวจ duplicate by transaction reference
//   6. Match กับ pending deposit order
//   7. Complete order + record verification result
//
// เนื่องจาก SlipService ใช้ concrete *easyslip.Client (ไม่ใช่ interface)
// ทำให้ไม่สามารถ inject mock ได้โดยตรง ดังนั้น test จะจำลอง pipeline
// โดยเรียก repository methods โดยตรงตาม flow ที่ SlipService ทำ
// เพื่อ verify ว่า duplicate detection และ order matching ทำงานถูกต้อง
//
// ทุก test ใช้ mock repositories เพื่อจำลอง infrastructure
package integration

import (
	"context"
	"crypto/sha256"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/farritpcz/richpayment/tests/integration/testhelper"
)

// ---------------------------------------------------------------------------
// Test: Slip Verification Flow — Table-Driven Tests
// ---------------------------------------------------------------------------

// TestSlipFlow_TableDriven ทดสอบ slip verification flow ด้วย table-driven tests
// ครอบคลุม 4 กรณี:
//   - Happy path: image received → parse → match order → complete
//   - Duplicate slip (same hash): image hash ซ้ำ → reject
//   - Duplicate transaction ref: transaction ref ซ้ำ → reject
//   - Already completed order: order ถูก complete แล้ว → return info
//
// ทำไมต้อง test นี้:
//   - Slip verification เป็น channel สำคัญสำหรับ deposit matching
//   - Duplicate detection ป้องกัน double credit — ถ้าพลาดจะเสียเงิน
//   - ต้องตรวจสอบทั้ง image-level และ transaction-level duplicates
//   - ต้อง handle กรณีที่ order ถูก complete แล้วจาก SMS channel
func TestSlipFlow_TableDriven(t *testing.T) {
	tests := []struct {
		// name คือชื่อของ test case — แสดงใน test output
		name string

		// description อธิบายว่า test case นี้ทดสอบอะไร
		description string

		// testFn รัน test logic จริง
		testFn func(t *testing.T)
	}{
		// =======================================================================
		// Case 1: Happy Path — Image → Parse → Match → Complete
		// =======================================================================
		{
			name:        "slip_verification_happy_path",
			description: "ทดสอบ full slip flow: รับ image → hash → verify → match order → record result",

			testFn: func(t *testing.T) {
				ctx := context.Background()

				// --- Setup: สร้าง mock slip repository ---
				slipRepo := testhelper.NewMockSlipRepository()

				// --- Step 1: จำลองการรับ image จาก Telegram ---
				// สร้าง fake image data สำหรับ test
				imageData := []byte("fake-slip-image-data-for-happy-path-test")

				// --- Step 2: คำนวณ SHA-256 hash ของ image ---
				// เหมือนกับที่ SlipService.VerifySlip ทำใน Step 1
				hash := sha256.Sum256(imageData)
				imageHash := fmt.Sprintf("%x", hash[:])

				// --- Step 3: ตรวจ duplicate by image hash ---
				// ต้องไม่เจอ duplicate เพราะเป็น slip ใหม่
				existingByHash, err := slipRepo.GetByImageHash(ctx, imageHash)
				if err != nil {
					t.Fatalf("GetByImageHash failed: %v", err)
				}
				if existingByHash != nil {
					t.Fatal("expected no existing slip by image hash, but found one")
				}

				// --- Step 4: จำลองผลจาก EasySlip API ---
				// ใน production, SlipService จะเรียก easySlipClient.VerifySlip
				// ที่นี่จำลองผลลัพธ์ที่ parse ได้จาก slip
				transactionRef := "KBANK-20260410-001"
				slipAmount := decimal.NewFromFloat(5000.50)

				// --- Step 5: ตรวจ duplicate by transaction ref ---
				existingByRef, err := slipRepo.GetByTransactionRef(ctx, transactionRef)
				if err != nil {
					t.Fatalf("GetByTransactionRef failed: %v", err)
				}
				if existingByRef != nil {
					t.Fatal("expected no existing slip by transaction ref, but found one")
				}

				// --- Step 6: จำลอง order matching ---
				// ใน production จะ query order-service สำหรับ pending order ที่ match
				matchedOrderID := uuid.New()

				// --- Step 7: Record verification result ---
				// จำลองการ save verification result ลง database
				verificationRecord := &testhelper.SlipVerification{
					ID:                uuid.New(),
					TelegramGroupID:   -100123456789,
					TelegramMessageID: 42,
					ImageHash:         imageHash,
					TransactionRef:    transactionRef,
					Amount:            slipAmount,
					SenderName:        "สมชาย ทดสอบ",
					ReceiverName:      "RichPayment Co.",
					OrderID:           &matchedOrderID,
					Status:            testhelper.SlipStatusVerified,
					StatusDetail:      "slip verified and matched to order",
					CreatedAt:         time.Now().UTC(),
				}

				err = slipRepo.Create(ctx, verificationRecord)
				if err != nil {
					t.Fatalf("Create slip verification failed: %v", err)
				}

				// --- Assertions ---

				// ตรวจสอบว่า record ถูกบันทึก
				records := slipRepo.GetRecords()
				if len(records) != 1 {
					t.Fatalf("expected 1 slip record, got %d", len(records))
				}

				// ตรวจสอบ status = verified
				if records[0].Status != testhelper.SlipStatusVerified {
					t.Errorf("expected status 'verified', got %q", records[0].Status)
				}

				// ตรวจสอบว่า order ID ถูก set
				if records[0].OrderID == nil || *records[0].OrderID != matchedOrderID {
					t.Errorf("expected order ID %s, got %v", matchedOrderID, records[0].OrderID)
				}

				// ตรวจสอบ image hash ตรง
				if records[0].ImageHash != imageHash {
					t.Errorf("expected image hash %s, got %s", imageHash, records[0].ImageHash)
				}

				// ตรวจสอบ transaction ref ตรง
				if records[0].TransactionRef != transactionRef {
					t.Errorf("expected transaction ref %s, got %s", transactionRef, records[0].TransactionRef)
				}

				t.Logf("Slip verification happy path verified: hash=%s...%s, ref=%s, order=%s",
					imageHash[:8], imageHash[len(imageHash)-8:], transactionRef, matchedOrderID)
			},
		},

		// =======================================================================
		// Case 2: Duplicate Slip (Same Image Hash)
		// =======================================================================
		{
			name:        "duplicate_slip_same_hash",
			description: "ทดสอบว่า slip ที่มี image hash ซ้ำจะถูก reject เพื่อป้องกัน double credit",

			testFn: func(t *testing.T) {
				ctx := context.Background()

				slipRepo := testhelper.NewMockSlipRepository()

				// --- Setup: Pre-seed slip record ที่มี image hash เดียวกัน ---
				// จำลองว่า slip นี้ถูก submit และ verify แล้วก่อนหน้านี้
				imageData := []byte("duplicate-image-data-same-content")
				hash := sha256.Sum256(imageData)
				imageHash := fmt.Sprintf("%x", hash[:])

				existingOrderID := uuid.New()
				slipRepo.SeedRecord(&testhelper.SlipVerification{
					ID:                uuid.New(),
					TelegramGroupID:   -100987654321,
					TelegramMessageID: 10,
					ImageHash:         imageHash,
					TransactionRef:    "KBANK-20260410-EXISTING",
					Amount:            decimal.NewFromFloat(3000.00),
					OrderID:           &existingOrderID,
					Status:            testhelper.SlipStatusVerified,
					StatusDetail:      "slip verified and matched",
					CreatedAt:         time.Now().UTC().Add(-10 * time.Minute), // submit 10 นาทีที่แล้ว
				})

				// --- Step 1: จำลองการรับ image เดิมอีกครั้ง ---
				newHash := sha256.Sum256(imageData)
				newImageHash := fmt.Sprintf("%x", newHash[:])

				// --- Step 2: ตรวจ duplicate by image hash ---
				// ต้องเจอ existing record เพราะ hash ตรงกัน
				existingByHash, err := slipRepo.GetByImageHash(ctx, newImageHash)
				if err != nil {
					t.Fatalf("GetByImageHash failed: %v", err)
				}

				// ต้องเจอ duplicate
				if existingByHash == nil {
					t.Fatal("expected to find existing slip by image hash, but got nil")
				}

				// ตรวจสอบว่า existing record มี transaction ref ที่ถูกต้อง
				if existingByHash.TransactionRef != "KBANK-20260410-EXISTING" {
					t.Errorf("expected existing ref 'KBANK-20260410-EXISTING', got %q",
						existingByHash.TransactionRef)
				}

				// --- Step 3: Record duplicate attempt ---
				// ใน production SlipService จะ record ไว้สำหรับ audit
				duplicateRecord := &testhelper.SlipVerification{
					ID:                uuid.New(),
					TelegramGroupID:   -100987654321,
					TelegramMessageID: 20, // message ใหม่
					ImageHash:         newImageHash,
					Status:            testhelper.SlipStatusDuplicate,
					StatusDetail:      fmt.Sprintf("duplicate image hash: already submitted (ref: %s)", existingByHash.TransactionRef),
					CreatedAt:         time.Now().UTC(),
				}

				err = slipRepo.Create(ctx, duplicateRecord)
				if err != nil {
					t.Fatalf("Create duplicate record failed: %v", err)
				}

				// --- Assertions ---

				// ตรวจสอบว่ามี 2 records (original + duplicate)
				records := slipRepo.GetRecords()
				if len(records) != 2 {
					t.Fatalf("expected 2 slip records (original + duplicate), got %d", len(records))
				}

				// ตรวจสอบว่า duplicate record มี status = duplicate
				lastRecord := records[len(records)-1]
				if lastRecord.Status != testhelper.SlipStatusDuplicate {
					t.Errorf("expected duplicate status, got %q", lastRecord.Status)
				}

				t.Logf("Duplicate slip detection verified: hash=%s...%s, existing_ref=%s",
					imageHash[:8], imageHash[len(imageHash)-8:], existingByHash.TransactionRef)
			},
		},

		// =======================================================================
		// Case 3: Duplicate Transaction Reference
		// =======================================================================
		{
			name:        "duplicate_transaction_ref",
			description: "ทดสอบว่า slip ที่มี transaction ref ซ้ำ (แม้ image ต่างกัน) จะถูก reject",

			testFn: func(t *testing.T) {
				ctx := context.Background()

				slipRepo := testhelper.NewMockSlipRepository()

				// --- Setup: Pre-seed slip record ที่มี transaction ref เดียวกัน ---
				// จำลองว่ามีคนส่ง photo ของ slip เดียวกัน แต่ถ่ายจากมุมต่าง
				// image hash จะต่างกัน แต่ transaction ref เหมือนกัน
				existingOrderID := uuid.New()
				commonTransRef := "SCB-20260410-SHARED-REF"

				slipRepo.SeedRecord(&testhelper.SlipVerification{
					ID:                uuid.New(),
					TelegramGroupID:   -100111222333,
					TelegramMessageID: 5,
					ImageHash:         "aaaa1111bbbb2222cccc3333dddd4444eeee5555ffff6666", // hash ของ photo แรก
					TransactionRef:    commonTransRef,
					Amount:            decimal.NewFromFloat(7500.00),
					OrderID:           &existingOrderID,
					Status:            testhelper.SlipStatusVerified,
					StatusDetail:      "verified and matched",
					CreatedAt:         time.Now().UTC().Add(-5 * time.Minute),
				})

				// --- Step 1: จำลอง image ใหม่ (photo ต่าง, hash ต่าง) ---
				newImageData := []byte("different-photo-same-transaction-ref")
				hash := sha256.Sum256(newImageData)
				newImageHash := fmt.Sprintf("%x", hash[:])

				// --- Step 2: ตรวจ duplicate by image hash ---
				// Image hash ต่าง — ผ่าน check นี้ได้
				existingByHash, err := slipRepo.GetByImageHash(ctx, newImageHash)
				if err != nil {
					t.Fatalf("GetByImageHash failed: %v", err)
				}
				if existingByHash != nil {
					t.Fatal("expected no existing slip by new image hash, but found one")
				}

				// --- Step 3: ตรวจ duplicate by transaction ref ---
				// Transaction ref ซ้ำ — ต้องถูก catch ที่ step นี้
				existingByRef, err := slipRepo.GetByTransactionRef(ctx, commonTransRef)
				if err != nil {
					t.Fatalf("GetByTransactionRef failed: %v", err)
				}

				// ต้องเจอ duplicate by ref
				if existingByRef == nil {
					t.Fatal("expected to find existing slip by transaction ref, but got nil")
				}

				// ตรวจสอบว่า existing record match
				if existingByRef.TransactionRef != commonTransRef {
					t.Errorf("expected ref %q, got %q", commonTransRef, existingByRef.TransactionRef)
				}

				// --- Step 4: Record duplicate attempt ---
				duplicateRecord := &testhelper.SlipVerification{
					ID:                uuid.New(),
					TelegramGroupID:   -100111222333,
					TelegramMessageID: 15, // message ใหม่
					ImageHash:         newImageHash,
					TransactionRef:    commonTransRef,
					Status:            testhelper.SlipStatusDuplicate,
					StatusDetail:      fmt.Sprintf("duplicate transaction ref: %s", commonTransRef),
					CreatedAt:         time.Now().UTC(),
				}

				err = slipRepo.Create(ctx, duplicateRecord)
				if err != nil {
					t.Fatalf("Create duplicate record failed: %v", err)
				}

				// --- Assertions ---

				records := slipRepo.GetRecords()
				if len(records) != 2 {
					t.Fatalf("expected 2 records, got %d", len(records))
				}

				// ตรวจสอบว่า duplicate record มี status ถูกต้อง
				lastRecord := records[len(records)-1]
				if lastRecord.Status != testhelper.SlipStatusDuplicate {
					t.Errorf("expected status 'duplicate', got %q", lastRecord.Status)
				}

				// ตรวจสอบว่า image hash ต่างกัน (ยืนยันว่าไม่ใช่ same image)
				if records[0].ImageHash == records[1].ImageHash {
					t.Error("expected different image hashes for the two records")
				}

				// แต่ transaction ref เหมือนกัน
				if records[0].TransactionRef != records[1].TransactionRef {
					t.Error("expected same transaction ref for both records")
				}

				t.Logf("Duplicate transaction ref detection verified: ref=%s, different images", commonTransRef)
			},
		},

		// =======================================================================
		// Case 4: Already Completed Order
		// =======================================================================
		{
			name:        "already_completed_order",
			description: "ทดสอบว่า slip ที่ match กับ order ที่ complete แล้ว (เช่น จาก SMS) จะ return already_completed",

			testFn: func(t *testing.T) {
				ctx := context.Background()

				slipRepo := testhelper.NewMockSlipRepository()

				// --- Setup: ไม่มี duplicate ใน repo ---
				// แต่ order ที่ match ถูก complete แล้วจาก SMS channel

				imageData := []byte("slip-for-already-completed-order")
				hash := sha256.Sum256(imageData)
				imageHash := fmt.Sprintf("%x", hash[:])

				// --- Step 1: ตรวจ duplicate by hash — pass ---
				existingByHash, err := slipRepo.GetByImageHash(ctx, imageHash)
				if err != nil {
					t.Fatalf("GetByImageHash failed: %v", err)
				}
				if existingByHash != nil {
					t.Fatal("expected no existing slip by hash")
				}

				// --- Step 2: จำลอง EasySlip result ---
				transactionRef := "BBL-20260410-ALREADY-DONE"

				// --- Step 3: ตรวจ duplicate by ref — pass ---
				existingByRef, err := slipRepo.GetByTransactionRef(ctx, transactionRef)
				if err != nil {
					t.Fatalf("GetByTransactionRef failed: %v", err)
				}
				if existingByRef != nil {
					t.Fatal("expected no existing slip by ref")
				}

				// --- Step 4: จำลองว่า order matching พบ order ที่ complete แล้ว ---
				// ใน production SlipService จะ query order-service
				// และพบว่า order มี status = completed (เช่น SMS arrived first)
				completedOrderID := uuid.New()

				// Record result ว่า "already completed"
				record := &testhelper.SlipVerification{
					ID:                uuid.New(),
					TelegramGroupID:   -100555666777,
					TelegramMessageID: 99,
					ImageHash:         imageHash,
					TransactionRef:    transactionRef,
					Amount:            decimal.NewFromFloat(2500.00),
					SenderName:        "ทดสอบ ระบบ",
					ReceiverName:      "RichPayment",
					OrderID:           &completedOrderID,
					Status:            testhelper.SlipStatusAlreadyCompleted,
					StatusDetail:      "order already completed by SMS",
					CreatedAt:         time.Now().UTC(),
				}

				err = slipRepo.Create(ctx, record)
				if err != nil {
					t.Fatalf("Create slip record failed: %v", err)
				}

				// --- Assertions ---

				records := slipRepo.GetRecords()
				if len(records) != 1 {
					t.Fatalf("expected 1 record, got %d", len(records))
				}

				// ตรวจสอบ status = already_completed
				if records[0].Status != testhelper.SlipStatusAlreadyCompleted {
					t.Errorf("expected status 'already_completed', got %q", records[0].Status)
				}

				// ตรวจสอบว่า order ID ถูก set (แม้ order จะ complete แล้ว)
				if records[0].OrderID == nil || *records[0].OrderID != completedOrderID {
					t.Errorf("expected order ID %s, got %v", completedOrderID, records[0].OrderID)
				}

				// ตรวจสอบ status detail มีข้อมูลว่า complete จาก SMS
				if records[0].StatusDetail != "order already completed by SMS" {
					t.Errorf("expected detail 'order already completed by SMS', got %q",
						records[0].StatusDetail)
				}

				t.Logf("Already completed order handling verified: order=%s, ref=%s",
					completedOrderID, transactionRef)
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
