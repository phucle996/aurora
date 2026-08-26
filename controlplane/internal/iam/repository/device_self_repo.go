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
		WHERE user_id = $1 AND public_key_fingerprint = $2 AND revoked_at IS NULL
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
