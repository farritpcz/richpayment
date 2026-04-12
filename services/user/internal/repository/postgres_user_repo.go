// Package repository ให้บริการ PostgreSQL implementation ของ UserRepository interface
// สำหรับจัดการข้อมูล admins, merchants, agents, และ partners ในฐานข้อมูล
// ใช้ pgxpool connection pool จาก jackc/pgx driver เพื่อประสิทธิภาพสูงสุด
//
// Package repository provides the PostgreSQL-backed implementation of the
// UserRepository interface for managing admins, merchants, agents, and partners.
// It uses pgxpool from the jackc/pgx driver for high-performance database access.
package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/farritpcz/richpayment/pkg/errors"
	"github.com/farritpcz/richpayment/pkg/models"
)

// ---------------------------------------------------------------------------
// Compile-time assertion / ตรวจสอบว่า struct implement interface ถูกต้อง
// ---------------------------------------------------------------------------

// ตรวจสอบตอน compile ว่า PostgresUserRepo implement UserRepository ครบทุก method
// Compile-time check that PostgresUserRepo satisfies the UserRepository interface.
var _ UserRepository = (*PostgresUserRepo)(nil)

// ---------------------------------------------------------------------------
// Struct definition / โครงสร้าง PostgresUserRepo
// ---------------------------------------------------------------------------

// PostgresUserRepo คือ implementation ของ UserRepository ที่ใช้ PostgreSQL เป็น backend
// ใช้ pgxpool.Pool สำหรับ connection pooling เพื่อรองรับ concurrent requests
//
// PostgresUserRepo is the PostgreSQL-backed implementation of UserRepository.
// It uses a pgxpool.Pool for connection pooling and high concurrency support.
type PostgresUserRepo struct {
	// pool คือ connection pool ที่ใช้ร่วมกันทุก method ใน repository
	// pool is the pgx connection pool shared across all repository calls.
	pool *pgxpool.Pool
}

// NewPostgresUserRepo สร้าง instance ใหม่ของ PostgresUserRepo พร้อม connection pool
// ผู้เรียกต้องเปิดและปิด pool เอง
//
// NewPostgresUserRepo constructs a new PostgresUserRepo with the given pool.
// The caller is responsible for opening and closing the pool.
func NewPostgresUserRepo(pool *pgxpool.Pool) *PostgresUserRepo {
	return &PostgresUserRepo{pool: pool}
}

// ===========================================================================
// Helper functions / ฟังก์ชันช่วยเหลือ
// ===========================================================================

// statusToIsActive แปลงค่า status จาก model ให้เป็น boolean is_active สำหรับ DB
// เฉพาะ "active" เท่านั้นที่จะ return true, สถานะอื่นๆ ทั้งหมด return false
//
// statusToIsActive converts a model status string to the DB's is_active boolean.
// Only "active" maps to true; all other statuses map to false.
func statusToIsActive(status interface{}) bool {
	// ตรวจสอบทุก type ของ status ที่เป็นไปได้ (Admin, Merchant, Agent, Partner)
	// Check all possible status types from the domain models.
	switch v := status.(type) {
	case models.AdminStatus:
		return v == models.AdminStatusActive
	case models.MerchantStatus:
		return v == models.MerchantStatusActive
	case models.AgentStatus:
		return v == models.AgentStatusActive
	case models.PartnerStatus:
		return v == models.PartnerStatusActive
	case string:
		// fallback กรณีส่งเป็น string ธรรมดา / fallback for plain string values
		return v == "active"
	default:
		return false
	}
}

// isActiveToAdminStatus แปลงค่า boolean is_active จาก DB กลับเป็น AdminStatus
// isActiveToAdminStatus converts the DB's is_active boolean back to AdminStatus.
func isActiveToAdminStatus(isActive bool) models.AdminStatus {
	if isActive {
		return models.AdminStatusActive
	}
	return models.AdminStatusSuspended
}

// isActiveToMerchantStatus แปลงค่า boolean is_active จาก DB กลับเป็น MerchantStatus
// isActiveToMerchantStatus converts the DB's is_active boolean back to MerchantStatus.
func isActiveToMerchantStatus(isActive bool) models.MerchantStatus {
	if isActive {
		return models.MerchantStatusActive
	}
	return models.MerchantStatusSuspended
}

// isActiveToAgentStatus แปลงค่า boolean is_active จาก DB กลับเป็น AgentStatus
// isActiveToAgentStatus converts the DB's is_active boolean back to AgentStatus.
func isActiveToAgentStatus(isActive bool) models.AgentStatus {
	if isActive {
		return models.AgentStatusActive
	}
	return models.AgentStatusSuspended
}

// isActiveToPartnerStatus แปลงค่า boolean is_active จาก DB กลับเป็น PartnerStatus
// isActiveToPartnerStatus converts the DB's is_active boolean back to PartnerStatus.
func isActiveToPartnerStatus(isActive bool) models.PartnerStatus {
	if isActive {
		return models.PartnerStatusActive
	}
	return models.PartnerStatusSuspended
}

// ===========================================================================
// Admin operations / การจัดการข้อมูล Admin
// ===========================================================================

// CreateAdmin บันทึก admin record ใหม่ลงในตาราง admins
// admin.ID ต้องถูก generate (UUID v4) มาก่อนจากผู้เรียก
// return error หาก insert ล้มเหลว เช่น email ซ้ำ, connection หลุด
//
// CreateAdmin persists a new admin record into the admins table.
// The admin.ID must be pre-generated (UUID v4) by the caller.
// Returns an error if the insert fails (e.g. duplicate email, connection loss).
func (r *PostgresUserRepo) CreateAdmin(ctx context.Context, admin *models.Admin) error {
	// SQL สำหรับ insert admin record ใหม่
	// แปลง Status เป็น is_active boolean สำหรับเก็บใน DB
	// SQL to insert a new admin record.
	// We convert the model's Status to the DB's is_active boolean column.
	const query = `
		INSERT INTO admins (
			id, email, password_hash, display_name, role_mask,
			is_active, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8
		)
	`

	// แปลง status ของ model เป็น boolean is_active สำหรับ DB
	// Convert model status to DB boolean is_active.
	isActive := statusToIsActive(admin.Status)

	// Execute insert พร้อม map ค่าจาก struct ไปยัง placeholder ตามลำดับ
	// Execute the insert, mapping struct fields to positional placeholders.
	_, err := r.pool.Exec(ctx, query,
		admin.ID,           // $1: primary key UUID
		admin.Email,        // $2: unique email address
		admin.PasswordHash, // $3: bcrypt hashed password
		admin.DisplayName,  // $4: ชื่อแสดงผล / display name
		admin.RoleMask,     // $5: bitmask สิทธิ์ / permission bitmask
		isActive,           // $6: สถานะ active/inactive / active flag
		admin.CreatedAt,    // $7: เวลาสร้าง / creation timestamp
		admin.UpdatedAt,    // $8: เวลาอัพเดทล่าสุด / last update timestamp
	)
	if err != nil {
		return fmt.Errorf("insert admin: %w", err)
	}

	return nil
}

// GetAdminByID ดึง admin record จากฐานข้อมูลโดยใช้ UUID เป็น primary key
// return (nil, ErrNotFound) เมื่อไม่พบ admin ที่ตรงกับ id
//
// GetAdminByID retrieves a single admin by its unique identifier.
// Returns (nil, ErrNotFound) when no row matches the given id.
func (r *PostgresUserRepo) GetAdminByID(ctx context.Context, id uuid.UUID) (*models.Admin, error) {
	// SQL สำหรับดึง admin ตาม ID
	// เลือกเฉพาะ column ที่ model ต้องการ, ไม่ดึง totp_secret_enc หรือ column อื่นที่ไม่มีใน model
	// SQL to fetch a single admin by ID.
	// Only select columns that map to the Admin model fields.
	const query = `
		SELECT id, email, password_hash, display_name, role_mask,
		       is_active, created_at, updated_at
		FROM admins
		WHERE id = $1
	`

	// ตัวแปรสำหรับรับค่า admin และ is_active จาก DB
	// Variables to hold the scanned admin and the DB's is_active boolean.
	var admin models.Admin
	// isActive ใช้รับค่า boolean จาก DB แล้วแปลงกลับเป็น AdminStatus
	// isActive holds the DB boolean, converted to AdminStatus after scan.
	var isActive bool

	// QueryRow ดึง 1 row แล้ว Scan ลง struct
	// QueryRow returns at most one row; Scan maps columns to variables.
	err := r.pool.QueryRow(ctx, query, id).Scan(
		&admin.ID,           // id
		&admin.Email,        // email
		&admin.PasswordHash, // password_hash
		&admin.DisplayName,  // display_name
		&admin.RoleMask,     // role_mask
		&isActive,           // is_active -> จะแปลงเป็น Status / will convert to Status
		&admin.CreatedAt,    // created_at
		&admin.UpdatedAt,    // updated_at
	)
	if err != nil {
		// pgx.ErrNoRows หมายถึงไม่พบ record ที่ตรง UUID
		// pgx.ErrNoRows means the UUID did not match any row.
		if err == pgx.ErrNoRows {
			return nil, errors.ErrNotFound
		}
		return nil, fmt.Errorf("get admin by id: %w", err)
	}

	// แปลง boolean is_active กลับเป็น AdminStatus ใน model
	// Convert the DB's is_active boolean back to model's AdminStatus.
	admin.Status = isActiveToAdminStatus(isActive)

	return &admin, nil
}

// ListAdmins ดึงรายการ admin แบบ paginated พร้อม total count
// ใช้ COUNT query แยกจาก SELECT เพื่อความถูกต้องของ pagination
// เรียงตาม created_at DESC (ใหม่สุดก่อน)
//
// ListAdmins returns a paginated list of admins and the total count.
// Uses a separate COUNT query for accurate pagination.
// Results are ordered by created_at descending (newest first).
func (r *PostgresUserRepo) ListAdmins(ctx context.Context, offset, limit int) ([]models.Admin, int, error) {
	// ---------------------------------------------------------------------------
	// Step 1: นับจำนวน admin ทั้งหมดสำหรับ pagination
	// Step 1: Count total admins for pagination metadata.
	// ---------------------------------------------------------------------------
	const countQuery = `SELECT COUNT(*) FROM admins`

	// totalCount เก็บจำนวน admin ทั้งหมดในตาราง
	// totalCount stores the total number of admins in the table.
	var totalCount int
	err := r.pool.QueryRow(ctx, countQuery).Scan(&totalCount)
	if err != nil {
		return nil, 0, fmt.Errorf("count admins: %w", err)
	}

	// ถ้าไม่มี record เลย return slice ว่างกับ count 0
	// If there are no records, return early with an empty slice.
	if totalCount == 0 {
		return []models.Admin{}, 0, nil
	}

	// ---------------------------------------------------------------------------
	// Step 2: ดึง admin records ตาม offset/limit
	// Step 2: Fetch paginated admin records.
	// ---------------------------------------------------------------------------
	const listQuery = `
		SELECT id, email, password_hash, display_name, role_mask,
		       is_active, created_at, updated_at
		FROM admins
		ORDER BY created_at DESC
		LIMIT $1 OFFSET $2
	`

	// Query ดึงหลาย rows / Query returns multiple rows.
	rows, err := r.pool.Query(ctx, listQuery, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list admins: %w", err)
	}
	// ปิด rows เมื่อ function จบ / Close rows when done.
	defer rows.Close()

	// admins เก็บผลลัพธ์ทั้งหมด / admins collects all result rows.
	var admins []models.Admin

	// วนอ่านทุก row แล้ว scan ลง struct / Iterate and scan each row.
	for rows.Next() {
		var admin models.Admin
		// isActive ใช้รับค่า boolean จาก DB / isActive holds the DB boolean.
		var isActive bool

		if err := rows.Scan(
			&admin.ID,
			&admin.Email,
			&admin.PasswordHash,
			&admin.DisplayName,
			&admin.RoleMask,
			&isActive,
			&admin.CreatedAt,
			&admin.UpdatedAt,
		); err != nil {
			return nil, 0, fmt.Errorf("scan admin row: %w", err)
		}

		// แปลง is_active เป็น AdminStatus / Convert is_active to AdminStatus.
		admin.Status = isActiveToAdminStatus(isActive)
		admins = append(admins, admin)
	}

	// ตรวจ error ที่เกิดระหว่าง iteration / Check for iteration errors.
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate admin rows: %w", err)
	}

	return admins, totalCount, nil
}

// UpdateAdmin อัพเดท admin record แบบ partial update (เฉพาะ field ที่ระบุ)
// ใช้ dynamic SET clause สร้าง SQL ตาม fields map ที่ส่งเข้ามา
// field name จาก caller จะถูก map ไปยัง column name ที่ถูกต้องใน DB
//
// UpdateAdmin applies partial updates to an admin record. The fields map
// uses logical field names as keys, which are mapped to actual DB column names.
// Builds a dynamic SET clause similar to UpdateStatus in the order repo.
func (r *PostgresUserRepo) UpdateAdmin(ctx context.Context, id uuid.UUID, fields map[string]interface{}) error {
	// setClauses เก็บ SQL SET fragments เช่น "display_name = $1"
	// setClauses collects SQL SET fragments like "display_name = $1".
	var setClauses []string
	// args เก็บค่า parameter ตาม positional placeholder
	// args holds the parameter values for positional placeholders.
	var args []interface{}
	// argIndex ติดตาม placeholder ถัดไป ($1, $2, ...)
	// argIndex tracks the next placeholder number ($1, $2, ...).
	argIndex := 1

	// วนลูป fields map เพื่อสร้าง SET clause แบบ dynamic
	// Iterate over the fields map to build dynamic SET clauses.
	for field, val := range fields {
		// แปลง field name จาก caller ไปเป็น column name ที่ถูกต้องของ DB
		// Map logical field names from caller to actual DB column names.
		switch field {
		case "display_name":
			// display_name -> display_name (ตรงกัน / direct mapping)
			setClauses = append(setClauses, fmt.Sprintf("display_name = $%d", argIndex))
			args = append(args, val)
		case "role_mask":
			// role_mask -> role_mask (ตรงกัน / direct mapping)
			setClauses = append(setClauses, fmt.Sprintf("role_mask = $%d", argIndex))
			args = append(args, val)
		case "status":
			// status -> is_active (แปลง model status เป็น boolean)
			// status -> is_active (convert model status to boolean)
			setClauses = append(setClauses, fmt.Sprintf("is_active = $%d", argIndex))
			args = append(args, statusToIsActive(val))
		case "email":
			// email -> email (ตรงกัน / direct mapping)
			setClauses = append(setClauses, fmt.Sprintf("email = $%d", argIndex))
			args = append(args, val)
		case "password_hash":
			// password_hash -> password_hash (ตรงกัน / direct mapping)
			setClauses = append(setClauses, fmt.Sprintf("password_hash = $%d", argIndex))
			args = append(args, val)
		default:
			// field ที่ไม่รู้จักจะถูกข้ามไป / unknown fields are skipped
			continue
		}
		argIndex++
	}

	// ถ้าไม่มี field ที่ valid เลย return error
	// If no valid fields were provided, return an error.
	if len(setClauses) == 0 {
		return fmt.Errorf("update admin: no valid fields provided")
	}

	// เพิ่ม updated_at อัตโนมัติทุกครั้งที่ update
	// Always update the updated_at timestamp.
	setClauses = append(setClauses, fmt.Sprintf("updated_at = $%d", argIndex))
	args = append(args, time.Now().UTC())
	argIndex++

	// เพิ่ม ID เป็น parameter สุดท้ายสำหรับ WHERE clause
	// Append the ID as the final parameter for the WHERE clause.
	args = append(args, id)

	// สร้าง UPDATE SQL สมบูรณ์พร้อม dynamic SET clauses
	// Build the complete UPDATE SQL with all dynamic SET clauses.
	query := fmt.Sprintf(
		"UPDATE admins SET %s WHERE id = $%d",
		strings.Join(setClauses, ", "),
		argIndex,
	)

	// Execute update แล้วตรวจ rows affected
	// Execute the update and check affected rows.
	tag, err := r.pool.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("update admin: %w", err)
	}

	// ถ้าไม่มี row ถูก update แสดงว่า ID ไม่มีอยู่
	// If no rows were affected, the admin ID does not exist.
	if tag.RowsAffected() == 0 {
		return errors.ErrNotFound
	}

	return nil
}

// ===========================================================================
// Merchant operations / การจัดการข้อมูล Merchant
// ===========================================================================

// CreateMerchant บันทึก merchant record ใหม่ลงในตาราง merchants
// merchant.ID ต้องถูก generate มาก่อนจากผู้เรียก
// merchant.Name จะถูก map ไปยัง company_name ใน DB
// merchant.HMACSecret จะถูก map ไปยัง hmac_secret_enc ใน DB
//
// CreateMerchant persists a new merchant record into the merchants table.
// The merchant.ID must be pre-generated (UUID v4) by the caller.
// Model's Name maps to DB's company_name; HMACSecret maps to hmac_secret_enc.
func (r *PostgresUserRepo) CreateMerchant(ctx context.Context, merchant *models.Merchant) error {
	// SQL สำหรับ insert merchant record ใหม่
	// เฉพาะ column ที่มี field ใน model เท่านั้น (ข้าม column ที่ไม่มีใน model)
	// SQL to insert a new merchant record.
	// Only columns that have corresponding model fields are included.
	const query = `
		INSERT INTO merchants (
			id, email, company_name, api_key_hash, hmac_secret_enc,
			webhook_url, agent_id, deposit_fee_pct, withdraw_fee_pct,
			daily_withdraw_limit_thb, is_active, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8, $9,
			$10, $11, $12, $13
		)
	`

	// แปลง status เป็น is_active boolean / Convert status to is_active boolean.
	isActive := statusToIsActive(merchant.Status)

	// Execute insert พร้อม map ค่าทุกตัว
	// Execute insert with all mapped values.
	_, err := r.pool.Exec(ctx, query,
		merchant.ID,                  // $1: primary key UUID
		merchant.Email,               // $2: email address
		merchant.Name,                // $3: company_name ใน DB / maps to company_name in DB
		merchant.APIKeyHash,          // $4: hashed API key
		merchant.HMACSecret,          // $5: hmac_secret_enc ใน DB / maps to hmac_secret_enc in DB
		merchant.WebhookURL,          // $6: webhook callback URL
		merchant.AgentID,             // $7: agent UUID (nullable)
		merchant.DepositFeePct,       // $8: ค่าธรรมเนียมฝาก % / deposit fee percentage
		merchant.WithdrawalFeePct,    // $9: ค่าธรรมเนียมถอน % / withdrawal fee percentage
		merchant.DailyWithdrawalLimit, // $10: วงเงินถอนรายวัน / daily withdrawal limit THB
		isActive,                     // $11: สถานะ active/inactive / active flag
		merchant.CreatedAt,           // $12: เวลาสร้าง / creation timestamp
		merchant.UpdatedAt,           // $13: เวลาอัพเดทล่าสุด / last update timestamp
	)
	if err != nil {
		return fmt.Errorf("insert merchant: %w", err)
	}

	return nil
}

// GetMerchantByID ดึง merchant record จากฐานข้อมูลโดยใช้ UUID
// company_name จาก DB จะถูก map กลับไปเป็น Name ใน model
// hmac_secret_enc จาก DB จะถูก map กลับไปเป็น HMACSecret ใน model
//
// GetMerchantByID retrieves a single merchant by its unique identifier.
// DB's company_name maps to model's Name; hmac_secret_enc maps to HMACSecret.
// Returns (nil, ErrNotFound) when no row matches the given id.
func (r *PostgresUserRepo) GetMerchantByID(ctx context.Context, id uuid.UUID) (*models.Merchant, error) {
	// SQL เลือกเฉพาะ column ที่ model ต้องการ
	// SQL selects only columns needed by the Merchant model.
	const query = `
		SELECT id, email, company_name, api_key_hash, hmac_secret_enc,
		       webhook_url, agent_id, deposit_fee_pct, withdraw_fee_pct,
		       daily_withdraw_limit_thb, is_active, created_at, updated_at
		FROM merchants
		WHERE id = $1
	`

	// ตัวแปรสำหรับ scan / Variables for scanning.
	var merchant models.Merchant
	// isActive รับค่า boolean จาก DB / isActive holds the DB boolean.
	var isActive bool

	// QueryRow ดึง 1 row / QueryRow fetches a single row.
	err := r.pool.QueryRow(ctx, query, id).Scan(
		&merchant.ID,                  // id
		&merchant.Email,               // email
		&merchant.Name,                // company_name -> Name
		&merchant.APIKeyHash,          // api_key_hash
		&merchant.HMACSecret,          // hmac_secret_enc -> HMACSecret
		&merchant.WebhookURL,          // webhook_url
		&merchant.AgentID,             // agent_id (nullable)
		&merchant.DepositFeePct,       // deposit_fee_pct
		&merchant.WithdrawalFeePct,    // withdraw_fee_pct
		&merchant.DailyWithdrawalLimit, // daily_withdraw_limit_thb
		&isActive,                     // is_active -> จะแปลงเป็น Status
		&merchant.CreatedAt,           // created_at
		&merchant.UpdatedAt,           // updated_at
	)
	if err != nil {
		// ไม่พบ record / No matching row found.
		if err == pgx.ErrNoRows {
			return nil, errors.ErrNotFound
		}
		return nil, fmt.Errorf("get merchant by id: %w", err)
	}

	// แปลง is_active เป็น MerchantStatus / Convert is_active to MerchantStatus.
	merchant.Status = isActiveToMerchantStatus(isActive)

	return &merchant, nil
}

// ListMerchants ดึงรายการ merchant แบบ paginated
// ถ้า agentID ไม่เป็น nil จะกรองเฉพาะ merchant ของ agent นั้น
// ถ้า agentID เป็น nil จะดึงทุก merchant
// เรียงตาม created_at DESC (ใหม่สุดก่อน)
//
// ListMerchants returns a paginated list of merchants, optionally filtered
// by the managing agent's UUID. If agentID is nil, all merchants are returned.
// Results are ordered by created_at descending (newest first).
func (r *PostgresUserRepo) ListMerchants(ctx context.Context, agentID *uuid.UUID, offset, limit int) ([]models.Merchant, int, error) {
	// ---------------------------------------------------------------------------
	// Step 1: นับจำนวน merchant ทั้งหมด (พร้อม filter ถ้ามี agentID)
	// Step 1: Count total merchants (with optional agent filter).
	// ---------------------------------------------------------------------------
	var totalCount int

	// สร้าง query แบบ dynamic ตาม filter
	// Build query dynamically based on whether agentID filter is provided.
	if agentID != nil {
		// กรณีมี agentID -> กรองเฉพาะ merchant ของ agent นั้น
		// With agentID -> filter merchants belonging to that agent.
		const countQuery = `SELECT COUNT(*) FROM merchants WHERE agent_id = $1`
		err := r.pool.QueryRow(ctx, countQuery, *agentID).Scan(&totalCount)
		if err != nil {
			return nil, 0, fmt.Errorf("count merchants by agent: %w", err)
		}
	} else {
		// กรณีไม่มี agentID -> นับทุก merchant
		// Without agentID -> count all merchants.
		const countQuery = `SELECT COUNT(*) FROM merchants`
		err := r.pool.QueryRow(ctx, countQuery).Scan(&totalCount)
		if err != nil {
			return nil, 0, fmt.Errorf("count merchants: %w", err)
		}
	}

	// ถ้าไม่มี record เลย return slice ว่าง
	// If no records exist, return early with an empty slice.
	if totalCount == 0 {
		return []models.Merchant{}, 0, nil
	}

	// ---------------------------------------------------------------------------
	// Step 2: ดึง merchant records ตาม offset/limit (พร้อม filter ถ้ามี)
	// Step 2: Fetch paginated merchant records (with optional filter).
	// ---------------------------------------------------------------------------

	// columns ที่จะ SELECT (ใช้ร่วมกันทั้ง 2 กรณี)
	// Shared SELECT columns for both filtered and unfiltered queries.
	const selectCols = `id, email, company_name, api_key_hash, hmac_secret_enc,
		       webhook_url, agent_id, deposit_fee_pct, withdraw_fee_pct,
		       daily_withdraw_limit_thb, is_active, created_at, updated_at`

	var rows pgx.Rows
	var err error

	if agentID != nil {
		// กรณีมี agentID filter / With agentID filter.
		query := fmt.Sprintf(
			`SELECT %s FROM merchants WHERE agent_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`,
			selectCols,
		)
		rows, err = r.pool.Query(ctx, query, *agentID, limit, offset)
	} else {
		// กรณีไม่มี filter / Without filter, return all merchants.
		query := fmt.Sprintf(
			`SELECT %s FROM merchants ORDER BY created_at DESC LIMIT $1 OFFSET $2`,
			selectCols,
		)
		rows, err = r.pool.Query(ctx, query, limit, offset)
	}
	if err != nil {
		return nil, 0, fmt.Errorf("list merchants: %w", err)
	}
	defer rows.Close()

	// merchants เก็บผลลัพธ์ทั้งหมด / merchants collects all result rows.
	var merchants []models.Merchant

	// วนอ่านทุก row / Iterate and scan each row.
	for rows.Next() {
		var m models.Merchant
		var isActive bool

		if err := rows.Scan(
			&m.ID,
			&m.Email,
			&m.Name,                // company_name -> Name
			&m.APIKeyHash,          // api_key_hash
			&m.HMACSecret,          // hmac_secret_enc -> HMACSecret
			&m.WebhookURL,          // webhook_url
			&m.AgentID,             // agent_id (nullable)
			&m.DepositFeePct,       // deposit_fee_pct
			&m.WithdrawalFeePct,    // withdraw_fee_pct
			&m.DailyWithdrawalLimit, // daily_withdraw_limit_thb
			&isActive,              // is_active
			&m.CreatedAt,           // created_at
			&m.UpdatedAt,           // updated_at
		); err != nil {
			return nil, 0, fmt.Errorf("scan merchant row: %w", err)
		}

		// แปลง is_active เป็น MerchantStatus / Convert is_active to MerchantStatus.
		m.Status = isActiveToMerchantStatus(isActive)
		merchants = append(merchants, m)
	}

	// ตรวจ iteration error / Check for iteration errors.
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate merchant rows: %w", err)
	}

	return merchants, totalCount, nil
}

// UpdateMerchant อัพเดท merchant record แบบ partial update
// field name จาก caller จะถูก map ไปยัง column name ที่ถูกต้อง:
//   - "name" -> company_name
//   - "status" -> is_active (แปลงเป็น boolean)
//   - "hmac_secret" -> hmac_secret_enc
//   - "commission_pct" -> deposit_commission_pct (ไม่มีใน merchant model แต่รองรับไว้)
//
// UpdateMerchant applies partial updates to a merchant record.
// Logical field names are mapped to actual DB column names.
func (r *PostgresUserRepo) UpdateMerchant(ctx context.Context, id uuid.UUID, fields map[string]interface{}) error {
	// setClauses และ args สำหรับ dynamic SQL
	// setClauses and args for building dynamic SQL.
	var setClauses []string
	var args []interface{}
	argIndex := 1

	// วนลูป fields map เพื่อ map field name -> column name
	// Iterate over fields map to map logical names to DB column names.
	for field, val := range fields {
		switch field {
		case "name":
			// name -> company_name ใน DB / name maps to company_name in DB
			setClauses = append(setClauses, fmt.Sprintf("company_name = $%d", argIndex))
			args = append(args, val)
		case "email":
			setClauses = append(setClauses, fmt.Sprintf("email = $%d", argIndex))
			args = append(args, val)
		case "status":
			// status -> is_active (แปลง model status เป็น boolean)
			// status -> is_active (convert model status to boolean)
			setClauses = append(setClauses, fmt.Sprintf("is_active = $%d", argIndex))
			args = append(args, statusToIsActive(val))
		case "webhook_url":
			setClauses = append(setClauses, fmt.Sprintf("webhook_url = $%d", argIndex))
			args = append(args, val)
		case "api_key_hash":
			setClauses = append(setClauses, fmt.Sprintf("api_key_hash = $%d", argIndex))
			args = append(args, val)
		case "hmac_secret":
			// hmac_secret -> hmac_secret_enc ใน DB / maps to hmac_secret_enc in DB
			setClauses = append(setClauses, fmt.Sprintf("hmac_secret_enc = $%d", argIndex))
			args = append(args, val)
		case "deposit_fee_pct":
			setClauses = append(setClauses, fmt.Sprintf("deposit_fee_pct = $%d", argIndex))
			args = append(args, val)
		case "withdrawal_fee_pct":
			setClauses = append(setClauses, fmt.Sprintf("withdraw_fee_pct = $%d", argIndex))
			args = append(args, val)
		case "daily_withdrawal_limit":
			setClauses = append(setClauses, fmt.Sprintf("daily_withdraw_limit_thb = $%d", argIndex))
			args = append(args, val)
		case "agent_id":
			setClauses = append(setClauses, fmt.Sprintf("agent_id = $%d", argIndex))
			args = append(args, val)
		default:
			// field ที่ไม่รู้จักจะถูกข้ามไป / unknown fields are skipped
			continue
		}
		argIndex++
	}

	// ถ้าไม่มี field ที่ valid เลย return error
	// If no valid fields were provided, return an error.
	if len(setClauses) == 0 {
		return fmt.Errorf("update merchant: no valid fields provided")
	}

	// เพิ่ม updated_at อัตโนมัติ / Always update the updated_at timestamp.
	setClauses = append(setClauses, fmt.Sprintf("updated_at = $%d", argIndex))
	args = append(args, time.Now().UTC())
	argIndex++

	// เพิ่ม ID สำหรับ WHERE clause / Append ID for the WHERE clause.
	args = append(args, id)

	// สร้าง UPDATE SQL สมบูรณ์ / Build the complete UPDATE SQL.
	query := fmt.Sprintf(
		"UPDATE merchants SET %s WHERE id = $%d",
		strings.Join(setClauses, ", "),
		argIndex,
	)

	// Execute update / Execute the update.
	tag, err := r.pool.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("update merchant: %w", err)
	}

	// ตรวจว่ามี row ถูก update หรือไม่ / Check if any row was updated.
	if tag.RowsAffected() == 0 {
		return errors.ErrNotFound
	}

	return nil
}

// ===========================================================================
// Agent operations / การจัดการข้อมูล Agent
// ===========================================================================

// CreateAgent บันทึก agent record ใหม่ลงในตาราง agents
// agent.Name จะถูก map ไปยัง company_name ใน DB
// agent.PartnerID ไม่มี column ตรงใน agents table แต่สามารถเก็บความสัมพันธ์ผ่าน created_by_admin_id ได้
// agent.CommissionPct จะถูก map ไปยัง deposit_commission_pct ใน DB
//
// CreateAgent persists a new agent record into the agents table.
// Model's Name maps to DB's company_name.
// Model's CommissionPct maps to DB's deposit_commission_pct.
func (r *PostgresUserRepo) CreateAgent(ctx context.Context, agent *models.Agent) error {
	// SQL สำหรับ insert agent record ใหม่
	// SQL to insert a new agent record.
	const query = `
		INSERT INTO agents (
			id, email, password_hash, company_name,
			deposit_commission_pct, is_active, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4,
			$5, $6, $7, $8
		)
	`

	// แปลง status เป็น is_active boolean / Convert status to is_active boolean.
	isActive := statusToIsActive(agent.Status)

	// Execute insert / Execute the insert.
	_, err := r.pool.Exec(ctx, query,
		agent.ID,            // $1: primary key UUID
		agent.Email,         // $2: email address
		agent.PasswordHash,  // $3: bcrypt hashed password
		agent.Name,          // $4: company_name ใน DB / maps to company_name
		agent.CommissionPct, // $5: deposit_commission_pct / commission percentage
		isActive,            // $6: สถานะ active/inactive / active flag
		agent.CreatedAt,     // $7: เวลาสร้าง / creation timestamp
		agent.UpdatedAt,     // $8: เวลาอัพเดทล่าสุด / last update timestamp
	)
	if err != nil {
		return fmt.Errorf("insert agent: %w", err)
	}

	return nil
}

// GetAgentByID ดึง agent record จากฐานข้อมูลโดยใช้ UUID
// company_name จาก DB จะถูก map กลับไปเป็น Name ใน model
// deposit_commission_pct จาก DB จะถูก map กลับไปเป็น CommissionPct ใน model
//
// GetAgentByID retrieves a single agent by its unique identifier.
// DB's company_name maps to model's Name; deposit_commission_pct maps to CommissionPct.
// Returns (nil, ErrNotFound) when no row matches the given id.
func (r *PostgresUserRepo) GetAgentByID(ctx context.Context, id uuid.UUID) (*models.Agent, error) {
	// SQL เลือกเฉพาะ column ที่ model ต้องการ
	// SQL selects only columns needed by the Agent model.
	const query = `
		SELECT id, email, password_hash, company_name,
		       deposit_commission_pct, is_active, created_at, updated_at
		FROM agents
		WHERE id = $1
	`

	var agent models.Agent
	var isActive bool

	// QueryRow ดึง 1 row / QueryRow fetches a single row.
	err := r.pool.QueryRow(ctx, query, id).Scan(
		&agent.ID,            // id
		&agent.Email,         // email
		&agent.PasswordHash,  // password_hash
		&agent.Name,          // company_name -> Name
		&agent.CommissionPct, // deposit_commission_pct -> CommissionPct
		&isActive,            // is_active -> จะแปลงเป็น Status
		&agent.CreatedAt,     // created_at
		&agent.UpdatedAt,     // updated_at
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, errors.ErrNotFound
		}
		return nil, fmt.Errorf("get agent by id: %w", err)
	}

	// แปลง is_active เป็น AgentStatus / Convert is_active to AgentStatus.
	agent.Status = isActiveToAgentStatus(isActive)

	return &agent, nil
}

// ListAgents ดึงรายการ agent แบบ paginated
// ถ้า partnerID ไม่เป็น nil จะกรองเฉพาะ agent ของ partner นั้น
// (ใน DB table agents ไม่มี partner_id column โดยตรง จึงใช้ created_by_admin_id เป็น filter)
// ถ้า partnerID เป็น nil จะดึงทุก agent
// เรียงตาม created_at DESC (ใหม่สุดก่อน)
//
// ListAgents returns a paginated list of agents, optionally filtered
// by the partner's UUID. If partnerID is nil, all agents are returned.
// Results are ordered by created_at descending (newest first).
func (r *PostgresUserRepo) ListAgents(ctx context.Context, partnerID *uuid.UUID, offset, limit int) ([]models.Agent, int, error) {
	// ---------------------------------------------------------------------------
	// Step 1: นับจำนวน agent ทั้งหมด (พร้อม filter ถ้ามี partnerID)
	// Step 1: Count total agents (with optional partner filter).
	// ---------------------------------------------------------------------------
	var totalCount int

	if partnerID != nil {
		// กรณีมี partnerID -> กรองโดยใช้ created_by_admin_id
		// With partnerID -> filter using created_by_admin_id.
		const countQuery = `SELECT COUNT(*) FROM agents WHERE created_by_admin_id = $1`
		err := r.pool.QueryRow(ctx, countQuery, *partnerID).Scan(&totalCount)
		if err != nil {
			return nil, 0, fmt.Errorf("count agents by partner: %w", err)
		}
	} else {
		// กรณีไม่มี filter -> นับทุก agent
		// Without filter -> count all agents.
		const countQuery = `SELECT COUNT(*) FROM agents`
		err := r.pool.QueryRow(ctx, countQuery).Scan(&totalCount)
		if err != nil {
			return nil, 0, fmt.Errorf("count agents: %w", err)
		}
	}

	// ถ้าไม่มี record เลย return slice ว่าง
	// If no records exist, return early with an empty slice.
	if totalCount == 0 {
		return []models.Agent{}, 0, nil
	}

	// ---------------------------------------------------------------------------
	// Step 2: ดึง agent records ตาม offset/limit
	// Step 2: Fetch paginated agent records.
	// ---------------------------------------------------------------------------
	const selectCols = `id, email, password_hash, company_name,
		       deposit_commission_pct, is_active, created_at, updated_at`

	var rows pgx.Rows
	var err error

	if partnerID != nil {
		// กรณีมี partnerID filter / With partnerID filter.
		query := fmt.Sprintf(
			`SELECT %s FROM agents WHERE created_by_admin_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`,
			selectCols,
		)
		rows, err = r.pool.Query(ctx, query, *partnerID, limit, offset)
	} else {
		// กรณีไม่มี filter / Without filter.
		query := fmt.Sprintf(
			`SELECT %s FROM agents ORDER BY created_at DESC LIMIT $1 OFFSET $2`,
			selectCols,
		)
		rows, err = r.pool.Query(ctx, query, limit, offset)
	}
	if err != nil {
		return nil, 0, fmt.Errorf("list agents: %w", err)
	}
	defer rows.Close()

	// agents เก็บผลลัพธ์ทั้งหมด / agents collects all result rows.
	var agents []models.Agent

	// วนอ่านทุก row / Iterate and scan each row.
	for rows.Next() {
		var a models.Agent
		var isActive bool

		if err := rows.Scan(
			&a.ID,            // id
			&a.Email,         // email
			&a.PasswordHash,  // password_hash
			&a.Name,          // company_name -> Name
			&a.CommissionPct, // deposit_commission_pct -> CommissionPct
			&isActive,        // is_active
			&a.CreatedAt,     // created_at
			&a.UpdatedAt,     // updated_at
		); err != nil {
			return nil, 0, fmt.Errorf("scan agent row: %w", err)
		}

		// แปลง is_active เป็น AgentStatus / Convert is_active to AgentStatus.
		a.Status = isActiveToAgentStatus(isActive)
		agents = append(agents, a)
	}

	// ตรวจ iteration error / Check for iteration errors.
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate agent rows: %w", err)
	}

	return agents, totalCount, nil
}

// UpdateAgent อัพเดท agent record แบบ partial update
// field name mapping:
//   - "name" -> company_name
//   - "status" -> is_active (แปลงเป็น boolean)
//   - "commission_pct" -> deposit_commission_pct
//
// UpdateAgent applies partial updates to an agent record.
// Logical field names are mapped to actual DB column names.
func (r *PostgresUserRepo) UpdateAgent(ctx context.Context, id uuid.UUID, fields map[string]interface{}) error {
	var setClauses []string
	var args []interface{}
	argIndex := 1

	// วนลูป fields map เพื่อ map field name -> column name
	// Iterate over fields map to map logical names to DB column names.
	for field, val := range fields {
		switch field {
		case "name":
			// name -> company_name ใน DB / name maps to company_name in DB
			setClauses = append(setClauses, fmt.Sprintf("company_name = $%d", argIndex))
			args = append(args, val)
		case "email":
			setClauses = append(setClauses, fmt.Sprintf("email = $%d", argIndex))
			args = append(args, val)
		case "password_hash":
			setClauses = append(setClauses, fmt.Sprintf("password_hash = $%d", argIndex))
			args = append(args, val)
		case "status":
			// status -> is_active (แปลง model status เป็น boolean)
			setClauses = append(setClauses, fmt.Sprintf("is_active = $%d", argIndex))
			args = append(args, statusToIsActive(val))
		case "commission_pct":
			// commission_pct -> deposit_commission_pct ใน DB
			setClauses = append(setClauses, fmt.Sprintf("deposit_commission_pct = $%d", argIndex))
			args = append(args, val)
		default:
			continue
		}
		argIndex++
	}

	// ถ้าไม่มี field ที่ valid / If no valid fields provided.
	if len(setClauses) == 0 {
		return fmt.Errorf("update agent: no valid fields provided")
	}

	// เพิ่ม updated_at อัตโนมัติ / Always update updated_at.
	setClauses = append(setClauses, fmt.Sprintf("updated_at = $%d", argIndex))
	args = append(args, time.Now().UTC())
	argIndex++

	// เพิ่ม ID สำหรับ WHERE clause / Append ID for WHERE clause.
	args = append(args, id)

	// สร้าง UPDATE SQL / Build the UPDATE SQL.
	query := fmt.Sprintf(
		"UPDATE agents SET %s WHERE id = $%d",
		strings.Join(setClauses, ", "),
		argIndex,
	)

	// Execute update / Execute the update.
	tag, err := r.pool.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("update agent: %w", err)
	}

	// ตรวจ rows affected / Check affected rows.
	if tag.RowsAffected() == 0 {
		return errors.ErrNotFound
	}

	return nil
}

// ===========================================================================
// Partner operations / การจัดการข้อมูล Partner
// ===========================================================================

// CreatePartner บันทึก partner record ใหม่ลงในตาราง partners
// partner.Name จะถูก map ไปยัง display_name ใน DB (partners ใช้ display_name ไม่ใช่ company_name)
// partner.CommissionPct จะถูก map ไปยัง deposit_commission_pct ใน DB
//
// CreatePartner persists a new partner record into the partners table.
// Model's Name maps to DB's display_name (partners use display_name, not company_name).
// Model's CommissionPct maps to DB's deposit_commission_pct.
func (r *PostgresUserRepo) CreatePartner(ctx context.Context, partner *models.Partner) error {
	// SQL สำหรับ insert partner record ใหม่
	// SQL to insert a new partner record.
	const query = `
		INSERT INTO partners (
			id, email, password_hash, display_name,
			deposit_commission_pct, is_active, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4,
			$5, $6, $7, $8
		)
	`

	// แปลง status เป็น is_active boolean / Convert status to is_active boolean.
	isActive := statusToIsActive(partner.Status)

	// Execute insert / Execute the insert.
	_, err := r.pool.Exec(ctx, query,
		partner.ID,            // $1: primary key UUID
		partner.Email,         // $2: email address
		partner.PasswordHash,  // $3: bcrypt hashed password
		partner.Name,          // $4: display_name ใน DB / maps to display_name
		partner.CommissionPct, // $5: deposit_commission_pct / commission percentage
		isActive,              // $6: สถานะ active/inactive / active flag
		partner.CreatedAt,     // $7: เวลาสร้าง / creation timestamp
		partner.UpdatedAt,     // $8: เวลาอัพเดทล่าสุด / last update timestamp
	)
	if err != nil {
		return fmt.Errorf("insert partner: %w", err)
	}

	return nil
}

// GetPartnerByID ดึง partner record จากฐานข้อมูลโดยใช้ UUID
// display_name จาก DB จะถูก map กลับไปเป็น Name ใน model
// deposit_commission_pct จาก DB จะถูก map กลับไปเป็น CommissionPct ใน model
//
// GetPartnerByID retrieves a single partner by its unique identifier.
// DB's display_name maps to model's Name; deposit_commission_pct maps to CommissionPct.
// Returns (nil, ErrNotFound) when no row matches the given id.
func (r *PostgresUserRepo) GetPartnerByID(ctx context.Context, id uuid.UUID) (*models.Partner, error) {
	// SQL เลือกเฉพาะ column ที่ model ต้องการ
	// SQL selects only columns needed by the Partner model.
	const query = `
		SELECT id, email, password_hash, display_name,
		       deposit_commission_pct, is_active, created_at, updated_at
		FROM partners
		WHERE id = $1
	`

	var partner models.Partner
	var isActive bool

	// QueryRow ดึง 1 row / QueryRow fetches a single row.
	err := r.pool.QueryRow(ctx, query, id).Scan(
		&partner.ID,            // id
		&partner.Email,         // email
		&partner.PasswordHash,  // password_hash
		&partner.Name,          // display_name -> Name
		&partner.CommissionPct, // deposit_commission_pct -> CommissionPct
		&isActive,              // is_active -> จะแปลงเป็น Status
		&partner.CreatedAt,     // created_at
		&partner.UpdatedAt,     // updated_at
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, errors.ErrNotFound
		}
		return nil, fmt.Errorf("get partner by id: %w", err)
	}

	// แปลง is_active เป็น PartnerStatus / Convert is_active to PartnerStatus.
	partner.Status = isActiveToPartnerStatus(isActive)

	return &partner, nil
}

// ListPartners ดึงรายการ partner แบบ paginated พร้อม total count
// เรียงตาม created_at DESC (ใหม่สุดก่อน)
//
// ListPartners returns a paginated list of partners and the total count.
// Results are ordered by created_at descending (newest first).
func (r *PostgresUserRepo) ListPartners(ctx context.Context, offset, limit int) ([]models.Partner, int, error) {
	// ---------------------------------------------------------------------------
	// Step 1: นับจำนวน partner ทั้งหมดสำหรับ pagination
	// Step 1: Count total partners for pagination metadata.
	// ---------------------------------------------------------------------------
	const countQuery = `SELECT COUNT(*) FROM partners`

	// totalCount เก็บจำนวน partner ทั้งหมด / totalCount stores the total.
	var totalCount int
	err := r.pool.QueryRow(ctx, countQuery).Scan(&totalCount)
	if err != nil {
		return nil, 0, fmt.Errorf("count partners: %w", err)
	}

	// ถ้าไม่มี record เลย return slice ว่าง / If no records, return empty slice.
	if totalCount == 0 {
		return []models.Partner{}, 0, nil
	}

	// ---------------------------------------------------------------------------
	// Step 2: ดึง partner records ตาม offset/limit
	// Step 2: Fetch paginated partner records.
	// ---------------------------------------------------------------------------
	const listQuery = `
		SELECT id, email, password_hash, display_name,
		       deposit_commission_pct, is_active, created_at, updated_at
		FROM partners
		ORDER BY created_at DESC
		LIMIT $1 OFFSET $2
	`

	// Query ดึงหลาย rows / Query returns multiple rows.
	rows, err := r.pool.Query(ctx, listQuery, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list partners: %w", err)
	}
	defer rows.Close()

	// partners เก็บผลลัพธ์ทั้งหมด / partners collects all result rows.
	var partners []models.Partner

	// วนอ่านทุก row / Iterate and scan each row.
	for rows.Next() {
		var p models.Partner
		var isActive bool

		if err := rows.Scan(
			&p.ID,            // id
			&p.Email,         // email
			&p.PasswordHash,  // password_hash
			&p.Name,          // display_name -> Name
			&p.CommissionPct, // deposit_commission_pct -> CommissionPct
			&isActive,        // is_active
			&p.CreatedAt,     // created_at
			&p.UpdatedAt,     // updated_at
		); err != nil {
			return nil, 0, fmt.Errorf("scan partner row: %w", err)
		}

		// แปลง is_active เป็น PartnerStatus / Convert is_active to PartnerStatus.
		p.Status = isActiveToPartnerStatus(isActive)
		partners = append(partners, p)
	}

	// ตรวจ iteration error / Check for iteration errors.
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate partner rows: %w", err)
	}

	return partners, totalCount, nil
}

// UpdatePartner อัพเดท partner record แบบ partial update
// field name mapping:
//   - "name" -> display_name (partners ใช้ display_name)
//   - "status" -> is_active (แปลงเป็น boolean)
//   - "commission_pct" -> deposit_commission_pct
//
// UpdatePartner applies partial updates to a partner record.
// Logical field names are mapped to actual DB column names.
func (r *PostgresUserRepo) UpdatePartner(ctx context.Context, id uuid.UUID, fields map[string]interface{}) error {
	var setClauses []string
	var args []interface{}
	argIndex := 1

	// วนลูป fields map เพื่อ map field name -> column name
	// Iterate over fields map to map logical names to DB column names.
	for field, val := range fields {
		switch field {
		case "name":
			// name -> display_name ใน DB (partners ใช้ display_name ไม่ใช่ company_name)
			// name -> display_name in DB (partners use display_name, not company_name)
			setClauses = append(setClauses, fmt.Sprintf("display_name = $%d", argIndex))
			args = append(args, val)
		case "email":
			setClauses = append(setClauses, fmt.Sprintf("email = $%d", argIndex))
			args = append(args, val)
		case "password_hash":
			setClauses = append(setClauses, fmt.Sprintf("password_hash = $%d", argIndex))
			args = append(args, val)
		case "status":
			// status -> is_active (แปลง model status เป็น boolean)
			setClauses = append(setClauses, fmt.Sprintf("is_active = $%d", argIndex))
			args = append(args, statusToIsActive(val))
		case "commission_pct":
			// commission_pct -> deposit_commission_pct ใน DB
			setClauses = append(setClauses, fmt.Sprintf("deposit_commission_pct = $%d", argIndex))
			args = append(args, val)
		default:
			continue
		}
		argIndex++
	}

	// ถ้าไม่มี field ที่ valid / If no valid fields provided.
	if len(setClauses) == 0 {
		return fmt.Errorf("update partner: no valid fields provided")
	}

	// เพิ่ม updated_at อัตโนมัติ / Always update updated_at.
	setClauses = append(setClauses, fmt.Sprintf("updated_at = $%d", argIndex))
	args = append(args, time.Now().UTC())
	argIndex++

	// เพิ่ม ID สำหรับ WHERE clause / Append ID for WHERE clause.
	args = append(args, id)

	// สร้าง UPDATE SQL / Build the UPDATE SQL.
	query := fmt.Sprintf(
		"UPDATE partners SET %s WHERE id = $%d",
		strings.Join(setClauses, ", "),
		argIndex,
	)

	// Execute update / Execute the update.
	tag, err := r.pool.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("update partner: %w", err)
	}

	// ตรวจ rows affected / Check affected rows.
	if tag.RowsAffected() == 0 {
		return errors.ErrNotFound
	}

	return nil
}
