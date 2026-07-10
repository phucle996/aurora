package iamRepoImpl

import (
	"context"
	"errors"
	"fmt"
	"time"

	"controlplane/internal/config"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamModel "controlplane/internal/iam/model"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	"controlplane/pkg/constant"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// [COMMENT]: DeviceSelfRepository thực thi việc thao tác thiết bị cho chính người dùng cá nhân
type DeviceSelfRepository struct {
	db     *pgxpool.Pool
	schema string
}

// [COMMENT]: NewDeviceSelfRepository khởi tạo DeviceSelfRepository
func NewDeviceSelfRepository(cfg *config.Config, db *pgxpool.Pool) iamRepoInterface.DeviceSelfRepository {
	return &DeviceSelfRepository{db: db, schema: cfg.SchemaSQL.IAM}
}

// [COMMENT]: UpsertLoginDevice lưu hoặc cập nhật thông tin thiết bị khi user đăng nhập thành công
func (r *DeviceSelfRepository) UpsertLoginDevice(ctx context.Context, device iamEntity.Device) (*iamEntity.Device, error) {
	deviceID := uuid.New()
	now := device.UpdatedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	query := fmt.Sprintf(`
		INSERT INTO %s.devices (
			id, user_id, device_name, device_type, os_name, browser_name,
			public_key, public_key_fingerprint, client_device_id,
			last_seen_ip, last_seen_user_agent, last_seen_at, created_at, updated_at
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$12,$12)
		ON CONFLICT (user_id, client_device_id)
		WHERE client_device_id IS NOT NULL
		DO UPDATE SET
			device_name = EXCLUDED.device_name,
			device_type = EXCLUDED.device_type,
			os_name = EXCLUDED.os_name,
			browser_name = EXCLUDED.browser_name,
			last_seen_ip = EXCLUDED.last_seen_ip,
			last_seen_user_agent = EXCLUDED.last_seen_user_agent,
			last_seen_at = EXCLUDED.last_seen_at,
			updated_at = EXCLUDED.updated_at
		RETURNING id, user_id, device_name, device_type, os_name, browser_name, public_key,
		       public_key_fingerprint, client_device_id, risk_flags, revoked_at,
		       last_seen_ip::text, last_seen_user_agent, last_seen_at, created_at, updated_at
	`, r.schema)

	item := iamModel.Device{}
	if err := r.db.QueryRow(ctx, query,
		deviceID,
		device.UserID,
		device.DeviceName,
		device.DeviceType,
		device.OSName,
		device.BrowserName,
		device.PublicKey,
		device.PublicKeyFingerprint,
		device.ClientDeviceID,
		device.LastSeenIP,
		device.LastSeenUserAgent,
		now,
	).Scan(
		&item.ID, &item.UserID, &item.DeviceName, &item.DeviceType, &item.OSName, &item.BrowserName, &item.PublicKey,
		&item.PublicKeyFingerprint, &item.ClientDeviceID, &item.RiskFlags, &item.RevokedAt,
		&item.LastSeenIP, &item.LastSeenUserAgent, &item.LastSeenAt, &item.CreatedAt, &item.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("iam repo: upsert login device: %w", err)
	}
	entity := iamModel.DeviceModelToEntity(item)
	return &entity, nil
}

// [COMMENT]: ListDevicesByUserID lấy danh sách thiết bị của một user cá nhân dưới dạng DevicePresence gọn nhẹ
func (r *DeviceSelfRepository) ListDevicesByUserID(ctx context.Context, userID uuid.UUID, limit int, offset int) ([]iamEntity.DevicePresence, error) {
	query := fmt.Sprintf(`
		SELECT COALESCE(client_device_id, id::text), device_name, last_seen_ip::text, last_seen_user_agent, last_seen_at, revoked_at
		FROM %s.devices
		WHERE user_id = $1
		ORDER BY last_seen_at DESC NULLS LAST, created_at DESC
		LIMIT $2 OFFSET $3
	`, r.schema)
	rows, err := r.db.Query(ctx, query, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("iam repo: list devices by user id: %w", err)
	}
	defer rows.Close()
	items := make([]iamEntity.DevicePresence, 0, limit)
	for rows.Next() {
		var item iamEntity.DevicePresence
		if scanErr := rows.Scan(
			&item.ID, &item.DeviceName, &item.LastIP, &item.LastUA, &item.LastSeenAt, &item.RevokedAt,
		); scanErr != nil {
			return nil, fmt.Errorf("iam repo: scan list device: %w", scanErr)
		}
		items = append(items, item)
	}
	return items, nil
}

// [COMMENT]: ResolveDeviceIDByFingerprint trả về client_device_id của thiết bị khớp với user và fingerprint
func (r *DeviceSelfRepository) ResolveDeviceIDByFingerprint(ctx context.Context, userID uuid.UUID, fingerprint string) (string, error) {
	query := fmt.Sprintf(`
		SELECT client_device_id
		FROM %s.devices
		WHERE user_id = $1 AND public_key_fingerprint = $2 AND status != 'revoked'
		LIMIT 1
	`, r.schema)
	var clientDeviceID *string
	if err := r.db.QueryRow(ctx, query, userID, fingerprint).Scan(&clientDeviceID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("iam repo: resolve device ID: %w", err)
	}
	if clientDeviceID == nil {
		return "", nil
	}
	return *clientDeviceID, nil
}

// [COMMENT]: RevokeMyDevice thu hồi thiết bị chỉ định bằng CTE theo client_device_id, ngăn chặn tự hu hồi thiết bị hiện tại
func (r *DeviceSelfRepository) RevokeMyDevice(ctx context.Context, clientDeviceID uuid.UUID, userID uuid.UUID, currentDeviceID uuid.UUID) error {
	// 1. Kiểm tra xem thiết bị cần thu hồi có phải là thiết bị hiện tại không
	var targetID uuid.UUID
	var isRevoked bool
	queryCheck := fmt.Sprintf("SELECT id, revoked_at IS NOT NULL FROM %s.devices WHERE client_device_id = $1 AND user_id = $2", r.schema)
	err := r.db.QueryRow(ctx, queryCheck, clientDeviceID.String(), userID).Scan(&targetID, &isRevoked)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return iamTaxonomy.ErrZeroRowsAffected
		}
		return err
	}

	if targetID == currentDeviceID {
		return iamTaxonomy.ErrActionNotAllowed
	}

	if isRevoked {
		return nil // Đã bị thu hồi rồi, trả về success (idempotent)
	}

	// 2. Thực hiện thu hồi qua CTE
	query := fmt.Sprintf(`
		WITH revoked_device AS (
			UPDATE %s.devices
			SET revoked_at=now(), updated_at=now()
			WHERE client_device_id = $1 AND user_id = $2
			RETURNING id
		),
		deleted_tokens AS (
			DELETE FROM %s.refresh_tokens
			WHERE user_id = $2 AND device_id = (SELECT id FROM revoked_device)
			RETURNING 1
		)
		SELECT 
			(SELECT COUNT(*) FROM revoked_device) AS updated_count
	`, r.schema, r.schema)
	var updatedCount int64
	if err := r.db.QueryRow(ctx, query, clientDeviceID.String(), userID).Scan(&updatedCount); err != nil {
		return err
	}
	if updatedCount == 0 {
		return iamTaxonomy.ErrZeroRowsAffected
	}
	return nil
}

// [COMMENT]: RevokeMyOtherDevices thu hồi các thiết bị khác ngoại trừ thiết bị chỉ định theo client_device_id, trả về danh sách client_device_id đã thu hồi
func (r *DeviceSelfRepository) RevokeMyOtherDevices(ctx context.Context, userID uuid.UUID, keepDeviceID *uuid.UUID) ([]uuid.UUID, error) {
	query := fmt.Sprintf(`
		WITH revoked_devices AS (
			UPDATE %s.devices
			SET revoked_at=now(), updated_at=now()
			WHERE user_id = $1 AND ($2::uuid IS NULL OR id != $2) AND revoked_at IS NULL
			RETURNING id, COALESCE(client_device_id, id::text) AS client_device_id
		),
		deleted_tokens AS (
			DELETE FROM %s.refresh_tokens
			WHERE user_id = $1 AND device_id IN (SELECT id FROM revoked_devices)
			RETURNING 1
		)
		SELECT client_device_id FROM revoked_devices
	`, r.schema, r.schema)
	rows, err := r.db.Query(ctx, query, userID, keepDeviceID)
	if err != nil {
		return nil, fmt.Errorf("iam repo: revoke other devices CTE: %w", err)
	}
	defer rows.Close()

	var clientDeviceIDs []uuid.UUID
	for rows.Next() {
		var idStr string
		if err := rows.Scan(&idStr); err != nil {
			return nil, fmt.Errorf("iam repo: scan revoked client device id: %w", err)
		}
		id, err := uuid.Parse(idStr)
		if err == nil {
			clientDeviceIDs = append(clientDeviceIDs, id)
		}
	}
	return clientDeviceIDs, nil
}

// [COMMENT]: BulkTouchDevices cập nhật hàng loạt last_seen_at/ip/ua cho danh sách thiết bị bằng unnest Bulk Upsert —
// thay thế cơ chế ghi đơn lẻ per-request trước đây; được gọi từ NATS Consumer sau mỗi chu kỳ 30s.
func (r *DeviceSelfRepository) BulkTouchDevices(ctx context.Context, updates []iamEntity.DevicePresenceUpdate) error {
	if len(updates) == 0 {
		return nil
	}

	// [COMMENT]: Tách danh sách updates thành các mảng riêng cho unnest
	ids := make([]string, len(updates))
	timestamps := make([]int64, len(updates))
	ips := make([]string, len(updates))
	uas := make([]string, len(updates))
	for i, u := range updates {
		ids[i] = u.DeviceID
		timestamps[i] = u.LastSeenAt
		ips[i] = u.LastSeenIP
		uas[i] = u.LastSeenUserAgent
	}

	// [COMMENT]: Bulk Upsert dùng unnest để tối thiểu số round-trip — N devices = 1 query duy nhất
	query := fmt.Sprintf(`
		UPDATE %s.devices AS d
		SET
			last_seen_at = to_timestamp(v.ts),
			last_seen_ip = NULLIF(v.ip, ''),
			last_seen_user_agent = NULLIF(v.ua, ''),
			updated_at = now()
		FROM (
			SELECT
				unnest($1::text[]) AS device_id,
				unnest($2::bigint[]) AS ts,
				unnest($3::text[]) AS ip,
				unnest($4::text[]) AS ua
		) AS v
		WHERE d.id::text IN (
			SELECT id::text FROM %s.devices WHERE client_device_id = v.device_id
		) OR d.client_device_id = v.device_id
	`, r.schema, r.schema)

	if _, err := r.db.Exec(ctx, query, ids, timestamps, ips, uas); err != nil {
		return fmt.Errorf("iam repo: bulk touch devices: %w", err)
	}
	return nil
}

// [COMMENT]: InsertAuditEvent ghi nhận sự kiện nhật ký
func (r *DeviceSelfRepository) InsertAuditEvent(ctx context.Context, actorUserID *uuid.UUID, event string, severity string) error {
	var ipStr, uaStr string
	if v, ok := ctx.Value(constant.RemoteIPKey).(string); ok {
		ipStr = v
	}
	if v, ok := ctx.Value(constant.UserAgentKey).(string); ok {
		uaStr = v
	}
	var ip *string
	if ipStr != "" {
		ip = &ipStr
	}
	var userAgent *string
	if uaStr != "" {
		userAgent = &uaStr
	}

	query := fmt.Sprintf(`
		INSERT INTO %s.audit_events (actor_user_id, tenant_id, workspace_id, event, severity, ip_address, user_agent, created_at)
		VALUES ($1, NULL, NULL, $2, $3::audit_severity, $4, $5, now())
	`, r.schema)
	if _, err := r.db.Exec(ctx, query, actorUserID, event, severity, ip, userAgent); err != nil {
		return fmt.Errorf("iam repo: insert audit event: %w", err)
	}
	return nil
}

// [COMMENT]: EvictExcessDevices loại bỏ các thiết bị vượt quá số lượng tối đa
func (r *DeviceSelfRepository) EvictExcessDevices(ctx context.Context, userID uuid.UUID, cap int) ([]iamRepoInterface.EvictedDevice, error) {
	if cap <= 0 {
		return nil, fmt.Errorf("iam repo: cap must be positive")
	}
	query := fmt.Sprintf(`
		WITH excess AS (
			SELECT id, client_device_id
			FROM %s.devices
			WHERE user_id = $1 AND status != 'revoked'
			ORDER BY last_seen_at DESC NULLS LAST, created_at DESC
			OFFSET $2
		), revoked AS (
			UPDATE %s.devices d
			SET status='revoked', revoked_at=now(), updated_at=now()
			FROM excess e
			WHERE d.id = e.id
			RETURNING d.id, d.client_device_id
		)
		SELECT id, client_device_id FROM revoked
	`, r.schema, r.schema)
	rows, err := r.db.Query(ctx, query, userID, cap)
	if err != nil {
		return nil, fmt.Errorf("iam repo: evict excess devices: %w", err)
	}
	defer rows.Close()
	out := make([]iamRepoInterface.EvictedDevice, 0)
	for rows.Next() {
		var item iamRepoInterface.EvictedDevice
		if err := rows.Scan(&item.DeviceID, &item.ClientDeviceID); err != nil {
			return nil, fmt.Errorf("iam repo: scan evicted device: %w", err)
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// [COMMENT]: ListUsersExceedingDeviceCap lấy danh sách ID người dùng có số lượng thiết bị vượt giới hạn
func (r *DeviceSelfRepository) ListUsersExceedingDeviceCap(ctx context.Context, cap int, limit int) ([]uuid.UUID, error) {
	if cap <= 0 {
		return nil, fmt.Errorf("iam repo: cap must be positive")
	}
	if limit <= 0 {
		limit = 100
	}
	query := fmt.Sprintf(`
		SELECT user_id
		FROM %s.devices
		WHERE status != 'revoked'
		GROUP BY user_id
		HAVING count(*) > $1
		LIMIT $2
	`, r.schema)
	rows, err := r.db.Query(ctx, query, cap, limit)
	if err != nil {
		return nil, fmt.Errorf("iam repo: list users exceeding cap: %w", err)
	}
	defer rows.Close()
	out := make([]uuid.UUID, 0)
	for rows.Next() {
		var id uuid.UUID
		if scanErr := rows.Scan(&id); scanErr != nil {
			return nil, fmt.Errorf("iam repo: scan user id: %w", scanErr)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// [COMMENT]: EvictDevices thu hồi hàng loạt thiết bị của một user dựa trên danh sách client_device_id,
// đồng thời xóa bỏ Refresh Token tương ứng trong database bằng 1 câu lệnh CTE duy nhất để tối ưu hiệu năng.
func (r *DeviceSelfRepository) EvictDevices(ctx context.Context, userID uuid.UUID, clientDeviceIDs []string) error {
	if len(clientDeviceIDs) == 0 {
		return nil
	}
	query := fmt.Sprintf(`
		WITH updated_devices AS (
			UPDATE %s.devices
			SET status = 'revoked', revoked_at = now(), updated_at = now()
			WHERE user_id = $1 AND client_device_id = ANY($2)
			RETURNING id
		)
		DELETE FROM %s.refresh_tokens
		WHERE user_id = $1 AND device_id IN (SELECT id FROM updated_devices)
	`, r.schema, r.schema)
	if _, err := r.db.Exec(ctx, query, userID, clientDeviceIDs); err != nil {
		return fmt.Errorf("iam repo: evict devices and tokens by client device ids: %w", err)
	}
	return nil
}

