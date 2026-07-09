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
		SELECT id, device_name, last_seen_ip::text, last_seen_user_agent, last_seen_at, revoked_at
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

// [COMMENT]: GetActiveDeviceID trả về client_device_id của thiết bị đang hoạt động khớp với user và fingerprint
func (r *DeviceSelfRepository) GetActiveDeviceID(ctx context.Context, userID uuid.UUID, fingerprint string) (string, error) {
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
		return "", fmt.Errorf("iam repo: get active device ID: %w", err)
	}
	if clientDeviceID == nil {
		return "", nil
	}
	return *clientDeviceID, nil
}

// [COMMENT]: RevokeDeviceByIDAndUserID thu hồi thiết bị chỉ định bằng CTE
func (r *DeviceSelfRepository) RevokeDeviceByIDAndUserID(ctx context.Context, deviceID uuid.UUID, userID uuid.UUID, currentDeviceID uuid.UUID) error {
	query := fmt.Sprintf(`
		WITH revoked_device AS (
			UPDATE %s.devices
			SET status='revoked', revoked_at=now(), updated_at=now()
			WHERE id = $1 AND user_id = $2 AND id != $3
			RETURNING id
		),
		deleted_tokens AS (
			DELETE FROM %s.refresh_tokens
			WHERE user_id = $2 AND device_id = $1 AND device_id != $3
			RETURNING 1
		)
		SELECT 
			(SELECT COUNT(*) FROM revoked_device) AS updated_count
	`, r.schema, r.schema)
	var updatedCount int64
	if err := r.db.QueryRow(ctx, query, deviceID, userID, currentDeviceID).Scan(&updatedCount); err != nil {
		return fmt.Errorf("iam repo: revoke device by id and user id CTE: %w", err)
	}
	if updatedCount == 0 {
		return iamTaxonomy.ErrZeroRowsAffected
	}
	return nil
}

// [COMMENT]: RevokeOtherDevicesByUserID thu hồi các thiết bị khác
func (r *DeviceSelfRepository) RevokeOtherDevicesByUserID(ctx context.Context, userID uuid.UUID, keepDeviceID *uuid.UUID) (int64, error) {
	query := fmt.Sprintf(`
		UPDATE %s.devices
		SET status='revoked', revoked_at=now(), updated_at=now()
		WHERE user_id = $1 AND ($2::uuid IS NULL OR id <> $2)
	`, r.schema)
	res, err := r.db.Exec(ctx, query, userID, keepDeviceID)
	if err != nil {
		return 0, fmt.Errorf("iam repo: revoke other devices by user id: %w", err)
	}
	return res.RowsAffected(), nil
}

// [COMMENT]: TouchDeviceLastSeen cập nhật thời gian hoạt động cuối cùng
func (r *DeviceSelfRepository) TouchDeviceLastSeen(ctx context.Context, deviceID uuid.UUID) error {
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
		UPDATE %s.devices
		SET last_seen_ip = $2, last_seen_user_agent = $3, last_seen_at = now(), updated_at = now()
		WHERE id = $1
	`, r.schema)
	if _, err := r.db.Exec(ctx, query, deviceID, ip, userAgent); err != nil {
		return fmt.Errorf("iam repo: touch device last seen: %w", err)
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
