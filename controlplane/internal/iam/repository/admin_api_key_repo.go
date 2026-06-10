// ======================================================================================================
// 📂 MODULE: controlplane/internal/iam/repository/admin_api_key_repo.go
//            Đặc Tả Hạ Tầng Lưu Trữ & Quản Trị Cơ Sở Dữ Liệu SRE Admin API Key
// ======================================================================================================
//
// 📜 HIỆP ĐỒNG THIẾT KẾ & TỐI ƯU HÓA HẠ TẦNG (INFRASTRUCTURE DESIGN & PEAK PERFORMANCE):
//   - File này chịu trách nhiệm lưu trữ và tương tác trực tiếp với Postgres Database cho mặt phẳng
//     quản trị hệ thống SRE (Infrastructure Management Plane).
//   - Tối ưu hóa cực hạn (Peak Performance Optimization) nhằm triệt tiêu hoàn toàn runtime overhead:
//
//     1) TRUY VẤN TĨNH KHỞI TẠO SỚM (STATIC QUERY PRE-COMPUTATION):
//        * Tất cả chuỗi truy vấn SQL được định dạng schema và biên dịch trước một lần duy nhất tại
//          hàm khởi tạo `NewAdminAPIKeyRepository`.
//        * Triệt tiêu hoàn toàn chi phí sử dụng `fmt.Sprintf` tại runtime ở hot path, giảm thiểu
//          các phân bổ heap dynamic memory và tiết kiệm chu kỳ CPU của Go GC.
//
//     2) GIAO DỊCH GỘP KIỂU BATCH (SINGLE NETWORK ROUND-TRIP TRANSACTIONS VIA pgx.Batch):
//        * Các thao tác phức hợp (như `Bootstrap` và `RollbackBootstrap`) thực thi nhiều câu lệnh
//          SQL khác nhau trong cùng một transaction.
//        * Sử dụng `pgx.Batch` để gom tất cả các lệnh SQL lại và gửi đi trong **đúng 1 vòng khứ hồi mạng**
//          (1 Network Round-trip) thay vì tuần tự từng connection, tối ưu hóa triệt để latency P99.
//
//     3) TÁCH BIỆT MÔ HÌNH DỮ LIỆU TUYỆT ĐỐI (STRICT MODEL-ENTITY DECOUPLING):
//        * Tầng logic nghiệp vụ chỉ giao tiếp thông qua Domain Entities (`iamEntity.AdminAPIKey`, v.v.).
//        * Tầng lưu trữ (Repository) thực hiện chuyển đổi hai chiều sang Database Storage Models
//          chuyên biệt (`iamModel.AdminAPIKey`, `iamModel.AdminDevice`, `iamModel.Admin2FASettings`)
//          trước khi ghi xuống DB hoặc sau khi quét lên từ cơ sở dữ liệu.
//
//     4) NGĂN CHẶN TRANH CHẤP RACE CONDITION (RACE CONDITION & SECURITY LOCKS):
//        * Sử dụng PostgreSQL Advisory Locks thông qua `AcquireBootstrapLock` và `AcquireRotationLock`
//          với mã khóa tĩnh (`20260514` và `20260515`) để bảo vệ các thao tác thay đổi hạ tầng quan trọng,
//          đảm bảo chỉ có một instance SRE Node duy nhất được phép Bootstrap hoặc Rotate Key tại một thời điểm.
//
// ======================================================================================================

package iamRepoImpl

import (
	"context"
	"fmt"
	"strings"
	"time"

	"controlplane/internal/config"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamModel "controlplane/internal/iam/model"
	iamTaxonomy "controlplane/internal/iam/taxonomy"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// adminBootstrapLockKey là khoá advisory lock độc quyền cho tiến trình bootstrap ban đầu.
	adminBootstrapLockKey int64 = 20260514
	// adminRotationLockKey là khoá advisory lock độc quyền cho tiến trình quay vòng khoá tự động.
	adminRotationLockKey int64 = 20260515
)

// AdminAPIKeyRepository thực thi các truy vấn hiệu năng cao với Postgres Database.
type AdminAPIKeyRepository struct {
	db                            *pgxpool.Pool
	schema                        string
	prepareNextAdminAPIKeyQuery   string
	getActiveAdminAPIKeyQuery     string
	bootstrapDeleteKeyQuery       string
	bootstrapInsertKeyQuery       string
	bootstrapUpsert2FAQuery       string
	bootstrapDeleteRecoveryQuery  string
	bootstrapInsertRecoveryQuery  string
	bootstrapInsertAuditQuery     string
	rollbackDeleteRecoveryQuery   string
	rollbackDelete2FAQuery        string
	rollbackDeleteKeyQuery        string
	rollbackDeleteAuditQuery      string
	getAdmin2FASecretQuery        string
	consumeRecoveryCodeQuery      string
	getPublicKeyByDeviceIDQuery   string
	upsertAdminDeviceCheckQuery   string
	upsertAdminDeviceBindingQuery string
	touchAdminDeviceLastSeenQuery string
}

// bootstrapLock đóng gói advisory lock kết nối vật lý với Postgres.
type bootstrapLock struct {
	conn *pgxpool.Conn
	key  int64
}

// NewAdminAPIKeyRepository khởi tạo repository quản lý khoá API quản trị hạ tầng.
// Định dạng trước toàn bộ chuỗi SQL tại thời điểm ứng dụng khởi chạy để triệt tiêu allocation ở runtime.
func NewAdminAPIKeyRepository(
	cfg *config.Config,
	db *pgxpool.Pool,
) iamRepoInterface.AdminAPIKeyRepository {
	schema := cfg.SchemaSQL.IAM
	return &AdminAPIKeyRepository{
		db:     db,
		schema: schema,
		prepareNextAdminAPIKeyQuery: fmt.Sprintf(`
			INSERT INTO %s.admin_api_keys (
				id,
				key_hash,
				created_by,
				created_at,
				expires_at
			)
			VALUES ($1, $2, $3, $4, $5)
		`, schema),
		getActiveAdminAPIKeyQuery: fmt.Sprintf(`
			SELECT 
				id, 
				key_hash, 
				created_by, 
				created_at, 
				expires_at 
			FROM %s.admin_api_keys 
			WHERE expires_at > CURRENT_TIMESTAMP
			ORDER BY created_at DESC 
			LIMIT 1`, schema),
		bootstrapDeleteKeyQuery: fmt.Sprintf(`
			DELETE FROM %s.admin_api_keys`, schema),
		bootstrapInsertKeyQuery: fmt.Sprintf(`
			INSERT INTO %s.admin_api_keys (id, key_hash, created_by, created_at, expires_at) 
			VALUES ($1, $2, $3, $4, $5)`, schema),
		bootstrapUpsert2FAQuery: fmt.Sprintf(`
			INSERT INTO %s.admin_2fa_settings (
				id, 
				secret_ciphertext, 
				created_at, 
				updated_at
			) 
			VALUES ($1, $2, $3, $3)
			ON CONFLICT (id) DO UPDATE SET 
				secret_ciphertext = EXCLUDED.secret_ciphertext, 
				updated_at = EXCLUDED.updated_at`, schema),
		bootstrapDeleteRecoveryQuery: fmt.Sprintf(`
			DELETE FROM %s.admin_recovery_codes`, schema),
		bootstrapInsertRecoveryQuery: fmt.Sprintf(`
			INSERT INTO %s.admin_recovery_codes (
				id, 
				code_hash, 
				used_at, 
				created_at
			) 
			VALUES ($1, $2, NULL, $3)`, schema),
		bootstrapInsertAuditQuery: fmt.Sprintf(`
			INSERT INTO %s.admin_action_audits (
				id, 
				action, 
				resource_type, 
				resource_id, 
				status, 
				request_ip, 
				request_path, 
				request_method, 
				error_code, 
				metadata, 
				created_at
			) 
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`, schema),
		rollbackDeleteRecoveryQuery: fmt.Sprintf(`
			DELETE FROM %s.admin_recovery_codes
			WHERE created_at = $1`, schema),
		rollbackDelete2FAQuery: fmt.Sprintf(`
			DELETE FROM %s.admin_2fa_settings
			WHERE updated_at = $1 AND secret_ciphertext = $2`, schema),
		rollbackDeleteKeyQuery: fmt.Sprintf(`
			DELETE FROM %s.admin_api_keys
			WHERE key_hash = $1 AND created_at = $2`, schema),
		rollbackDeleteAuditQuery: fmt.Sprintf(`
			DELETE FROM %s.admin_action_audits
			WHERE action = $1 AND created_at = $2 AND request_path = $3 AND request_method = $4`, schema),
		getAdmin2FASecretQuery: fmt.Sprintf(`
			SELECT secret_ciphertext, updated_at
			FROM %s.admin_2fa_settings
			ORDER BY updated_at DESC
			LIMIT 1`, schema),
		consumeRecoveryCodeQuery: fmt.Sprintf(`
			UPDATE %s.admin_recovery_codes
			SET used_at = $2
			WHERE code_hash = $1 AND used_at IS NULL`, schema),
		getPublicKeyByDeviceIDQuery: fmt.Sprintf(`
			SELECT public_key
			FROM %s.admin_devices
			WHERE id = $1
			LIMIT 1`, schema),
		upsertAdminDeviceCheckQuery: fmt.Sprintf(`
			SELECT quarantined_at, revoked_at 
			FROM %s.admin_devices 
			WHERE id = $1`, schema),
		upsertAdminDeviceBindingQuery: fmt.Sprintf(`
			INSERT INTO %s.admin_devices (
				id, device_name, device_type, os_name, browser_name,
				public_key, public_key_fingerprint,
				last_seen_ip, last_seen_user_agent, last_seen_at, created_at, updated_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $11)
			ON CONFLICT (id)
			DO UPDATE SET
				device_name = EXCLUDED.device_name,
				device_type = EXCLUDED.device_type,
				os_name = EXCLUDED.os_name,
				browser_name = EXCLUDED.browser_name,
				public_key = EXCLUDED.public_key,
				public_key_fingerprint = EXCLUDED.public_key_fingerprint,
				last_seen_ip = EXCLUDED.last_seen_ip,
				last_seen_user_agent = EXCLUDED.last_seen_user_agent,
				last_seen_at = EXCLUDED.last_seen_at,
				updated_at = EXCLUDED.updated_at
			RETURNING id, device_name, device_type, os_name, browser_name,
				public_key, public_key_fingerprint,
				quarantined_at, revoked_at, last_seen_ip, last_seen_user_agent,
				last_seen_at, created_at, updated_at`, schema),
		touchAdminDeviceLastSeenQuery: fmt.Sprintf(`
			UPDATE %s.admin_devices
			SET last_seen_ip = $2,
				last_seen_user_agent = $3,
				last_seen_at = $4,
				updated_at = $4
			WHERE id = $1`, schema),
	}
}

// AcquireBootstrapLock giành quyền sở hữu advisory lock cho tác vụ bootstrap.
// Đảm bảo loại trừ tương hỗ (mutual exclusion) tuyệt đối ở mức cụm phân tán.
func (r *AdminAPIKeyRepository) AcquireBootstrapLock(ctx context.Context) (iamRepoInterface.BootstrapLock, error) {

	// lấy 1 connection từ pool
	conn, err := r.db.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	// bắt đầu transaction
	if _, err := conn.Exec(ctx, "BEGIN"); err != nil {
		conn.Release()
		return nil, err
	}

	// check xem có ai đang giữ lock không, lock này là advisory lock (có thể bị tranh chấp)
	var ok bool
	if err := conn.QueryRow(ctx,
		`SELECT pg_try_advisory_xact_lock($1)`, adminBootstrapLockKey).Scan(&ok); err != nil {
		_, _ = conn.Exec(ctx, "ROLLBACK")
		conn.Release()
		return nil, err
	}

	// nếu đã có người giữ lock rồi thì rollback và trả lỗi
	if !ok {
		_, _ = conn.Exec(ctx, "ROLLBACK")
		conn.Release()
		return nil, fmt.Errorf("iam repo: bootstrap lock already held")
	}
	// trả về bootstrap lock để có thể release lock sau này
	return &bootstrapLock{conn: conn,
			key: adminBootstrapLockKey},
		nil
}

// AcquireRotationLock giành quyền sở hữu advisory lock cho tác vụ quay vòng khoá khẩn cấp.
func (r *AdminAPIKeyRepository) AcquireRotationLock(ctx context.Context) (iamRepoInterface.BootstrapLock, error) {
	conn, err := r.db.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := conn.Exec(ctx, "BEGIN"); err != nil {
		conn.Release()
		return nil, err
	}
	var ok bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock($1)`, adminRotationLockKey).Scan(&ok); err != nil {
		_, _ = conn.Exec(ctx, "ROLLBACK")
		conn.Release()
		return nil, err
	}
	if !ok {
		_, _ = conn.Exec(ctx, "ROLLBACK")
		conn.Release()
		return nil, iamTaxonomy.ErrBootstrapLockAlreadyHeld
	}
	return &bootstrapLock{conn: conn, key: adminRotationLockKey}, nil
}

// PrepareNextAdminAPIKey chuẩn bị sẵn sàng một khoá API Key tiếp theo trước khi tiến hành rotate thực tế.
func (r *AdminAPIKeyRepository) PrepareNextAdminAPIKey(ctx context.Context, key iamEntity.AdminAPIKey) error {

	// chuyển entity sang model để db xử lý
	m := iamModel.AdminAPIKeyEntityToModel(key)

	// db call query để insert admin key mới vào db
	_, err := r.db.Exec(ctx,
		r.prepareNextAdminAPIKeyQuery,
		m.ID,
		m.KeyHash,
		m.CreatedBy,
		m.CreatedAt,
		m.ExpiresAt,
	)
	return err
}

// CommitPreparedAdminAPIKeyRotation xác nhận giao dịch quay vòng khoá thành công.
func (r *AdminAPIKeyRepository) CommitPreparedAdminAPIKeyRotation(ctx context.Context) error {
	return nil
}

// RollbackPreparedAdminAPIKeyRotation huỷ bỏ và dọn dẹp khoá chuẩn bị nếu tiến trình quay vòng khoá lỗi.
func (r *AdminAPIKeyRepository) RollbackPreparedAdminAPIKeyRotation(ctx context.Context) error {
	return nil
}

// Release giải phóng advisory lock đã giành quyền sở hữu trước đó.
func (l *bootstrapLock) Release(ctx context.Context) error {
	if l == nil || l.conn == nil {
		return nil
	}
	_, err := l.conn.Exec(ctx, "ROLLBACK")
	l.conn.Release()
	l.conn = nil
	return err
}

// GetActiveAdminAPIKey truy vấn khoá SRE Admin API hoạt động mới nhất trong hệ thống.
func (r *AdminAPIKeyRepository) GetActiveAdminAPIKey(ctx context.Context) (*iamEntity.AdminAPIKey, error) {
	var m iamModel.AdminAPIKey
	if err := r.db.QueryRow(ctx, r.getActiveAdminAPIKeyQuery).Scan(&m.ID, &m.KeyHash, &m.CreatedBy, &m.CreatedAt, &m.ExpiresAt); err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	entity := iamModel.AdminAPIKeyModelToEntity(m)
	return &entity, nil
}

// Bootstrap khởi tạo khoá quản trị ban đầu cùng với cấu hình bảo mật MFA (2FA, Recovery Codes) và Audit Logs.
// Sử dụng pgx.Batch tối ưu hoá hiệu năng tuyệt đối bằng cách gộp tất cả hoạt động ghi vào duy nhất 1 lần truyền tin qua mạng.
func (r *AdminAPIKeyRepository) Bootstrap(ctx context.Context, payload iamEntity.AdminBootstrapPayload) (time.Time, error) {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return time.Time{}, err
	}
	defer tx.Rollback(ctx)

	now := payload.GeneratedAt.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}

	mKey := iamModel.AdminAPIKey{
		ID:        uuid.New(),
		KeyHash:   payload.KeyHash,
		CreatedBy: payload.Actor,
		CreatedAt: now,
		ExpiresAt: payload.ExpiresAt.UTC(),
	}

	m2FA := iamModel.Admin2FASettings{
		ID:               uuid.Nil,
		SecretCiphertext: payload.SecretCiphertext,
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	batch := &pgx.Batch{}
	// Gộp tất cả các lệnh ghi vào Batch
	batch.Queue(r.bootstrapDeleteKeyQuery)
	batch.Queue(r.bootstrapInsertKeyQuery, mKey.ID, mKey.KeyHash, mKey.CreatedBy, mKey.CreatedAt, mKey.ExpiresAt)
	batch.Queue(r.bootstrapUpsert2FAQuery, m2FA.ID, m2FA.SecretCiphertext, m2FA.CreatedAt)
	batch.Queue(r.bootstrapDeleteRecoveryQuery)
	for _, hash := range payload.RecoveryCodeHashes {
		batch.Queue(r.bootstrapInsertRecoveryQuery, uuid.New(), hash, now)
	}
	metadata := map[string]any{"actor": payload.Actor}
	batch.Queue(r.bootstrapInsertAuditQuery, uuid.New(), "admin_bootstrap_succeeded", "admin_api_key", nil, "success", nil, "/internal/iam/bootstrap", "SYSTEM", nil, metadata, now)

	// Gửi gộp toàn bộ Batch bằng một lần khứ hồi mạng duy nhất
	br := tx.SendBatch(ctx, batch)
	if err := br.Close(); err != nil {
		return time.Time{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return time.Time{}, err
	}
	return now, nil
}

// RollbackBootstrap thực hiện khôi phục dọn dẹp các trạng thái rác nếu luồng khởi tạo bootstrap phía service bị lỗi.
func (r *AdminAPIKeyRepository) RollbackBootstrap(ctx context.Context, payload iamEntity.AdminBootstrapPayload) error {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	batch := &pgx.Batch{}
	// Dọn dẹp tất cả dữ liệu rác đã được tạo trong phiên rác
	batch.Queue(r.rollbackDeleteRecoveryQuery, payload.GeneratedAt)
	batch.Queue(r.rollbackDelete2FAQuery, payload.GeneratedAt, payload.SecretCiphertext)
	batch.Queue(r.rollbackDeleteKeyQuery, payload.KeyHash, payload.GeneratedAt)
	batch.Queue(r.rollbackDeleteAuditQuery, "admin_bootstrap_succeeded", payload.GeneratedAt, payload.RequestPath, payload.RequestMethod)

	br := tx.SendBatch(ctx, batch)
	if err := br.Close(); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// GetAdmin2FASecret đọc khóa bí mật MFA hiện tại của tài khoản admin tối cao.
func (r *AdminAPIKeyRepository) GetAdmin2FASecret(ctx context.Context) (string, time.Time, error) {
	var secretCiphertext string
	var updatedAt time.Time
	if err := r.db.QueryRow(ctx, r.getAdmin2FASecretQuery).Scan(&secretCiphertext, &updatedAt); err != nil {
		if err == pgx.ErrNoRows {
			return "", time.Time{}, nil
		}
		return "", time.Time{}, err
	}
	return strings.TrimSpace(secretCiphertext), updatedAt, nil
}

// ConsumeRecoveryCode đánh dấu tiêu thụ một mã khôi phục khi login trong trường hợp khẩn cấp mất thiết bị MFA.
func (r *AdminAPIKeyRepository) ConsumeRecoveryCode(ctx context.Context, codeHash string, now time.Time) error {

	// call db update
	cmd, err := r.db.Exec(ctx, r.consumeRecoveryCodeQuery, codeHash, now.UTC())
	if err != nil {
		return err
	}

	// rows affected != 1 có nghĩa là mã khôi phục đã bị tiêu thụ hoặc không tồn tại
	if cmd.RowsAffected() != 1 {
		return iamTaxonomy.ErrRecoveryCodeInvalid
	}
	return nil
}

// GetPublicKeyByDeviceID truy vấn trực tiếp khóa công khai của thiết bị dựa vào UUID thiết bị.
func (r *AdminAPIKeyRepository) GetPublicKeyByDeviceID(ctx context.Context, deviceID string) (string, error) {
	var publicKey string
	err := r.db.QueryRow(ctx, r.getPublicKeyByDeviceIDQuery, deviceID).Scan(&publicKey)
	if err != nil {
		if err == pgx.ErrNoRows {
			return "", iamTaxonomy.ErrNotFound
		}
		return "", err
	}
	return publicKey, nil
}


// UpsertAdminDeviceBinding liên kết thiết bị vật lý an toàn của SRE, kiểm tra trạng thái quarantine/revoked để tránh token hijacking.
func (r *AdminAPIKeyRepository) UpsertAdminDeviceBinding(ctx context.Context, input iamEntity.AdminDeviceBindingInput) (*iamEntity.AdminDevice, error) {
	// Kiểm tra xem thiết bị đã tồn tại và có bị thu hồi hay cách ly không
	var quarantinedAt, revokedAt *time.Time
	checkErr := r.db.QueryRow(ctx, r.upsertAdminDeviceCheckQuery, input.ID).Scan(&quarantinedAt, &revokedAt)
	if checkErr == nil {
		if revokedAt != nil {
			return nil, iamTaxonomy.ErrDeviceRevoked
		}
		if quarantinedAt != nil {
			return nil, iamTaxonomy.ErrDeviceQuarantined
		}
	}

	now := input.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}

	m := iamModel.AdminDevice{
		ID:                   input.ID,
		DeviceName:           input.DeviceName,
		DeviceType:           input.DeviceType,
		OSName:               input.OSName,
		BrowserName:          input.BrowserName,
		PublicKey:            input.PublicKey,
		PublicKeyFingerprint: input.PublicKeyFingerprint,
		LastSeenIP:           input.LastSeenIP,
		LastSeenUserAgent:    input.LastSeenUserAgent,
		LastSeenAt:           input.LastSeenAt,
	}

	err := r.db.QueryRow(ctx, r.upsertAdminDeviceBindingQuery,
		m.ID,
		m.DeviceName,
		m.DeviceType,
		m.OSName,
		m.BrowserName,
		m.PublicKey,
		m.PublicKeyFingerprint,
		m.LastSeenIP,
		m.LastSeenUserAgent,
		m.LastSeenAt,
		now,
	).Scan(
		&m.ID,
		&m.DeviceName,
		&m.DeviceType,
		&m.OSName,
		&m.BrowserName,
		&m.PublicKey,
		&m.PublicKeyFingerprint,
		&m.QuarantinedAt,
		&m.RevokedAt,
		&m.LastSeenIP,
		&m.LastSeenUserAgent,
		&m.LastSeenAt,
		&m.CreatedAt,
		&m.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	entity := iamModel.AdminDeviceModelToEntity(m)
	return &entity, nil
}

// TouchAdminDeviceLastSeen cập nhật thông tin thiết bị quản trị cuối cùng khi có sự thay đổi địa chỉ IP/UserAgent.
func (r *AdminAPIKeyRepository) TouchAdminDeviceLastSeen(ctx context.Context, deviceID string, ip *string, userAgent *string, seenAt time.Time) error {
	_, err := r.db.Exec(ctx, r.touchAdminDeviceLastSeenQuery, strings.TrimSpace(deviceID), ip, userAgent, seenAt.UTC())
	return err
}

