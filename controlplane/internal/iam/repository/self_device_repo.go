package iamRepoImpl

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"controlplane/internal/config"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// [COMMENT]: SelfDeviceRepository thực thi các thao tác truy xuất và cập nhật thiết bị bền vững cho chủ sở hữu danh tính (/me scope)
type SelfDeviceRepository struct {
	db     *pgxpool.Pool
	schema string
}

// [COMMENT]: NewSelfDeviceRepository khởi tạo một thể hiện mới của SelfDeviceRepository với database pool
func NewSelfDeviceRepository(cfg *config.Config, db *pgxpool.Pool) iamRepoInterface.SelfDeviceRepository {
	return &SelfDeviceRepository{db: db, schema: cfg.SchemaSQL.IAM}
}

// [COMMENT]: UpsertLoginDevice lưu mới hoặc cập nhật thông tin thiết bị khi user đăng nhập thành công
func (r *SelfDeviceRepository) UpsertLoginDevice(ctx context.Context, device iamEntity.Device) (*iamEntity.Device, error) {
	// [COMMENT]: Thực hiện UPSERT trên bảng devices dựa trên unique constraint (user_id, client_device_id)
	query := fmt.Sprintf(`
		INSERT INTO %s.devices (
			id,
			user_id,
			device_name,
			device_type,
			os_name,
			browser_name,
			public_key,
			public_key_fingerprint,
			client_device_id,
			last_seen_ip,
			last_seen_user_agent,
			last_seen_at,
			created_at,
			updated_at
		)
		VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $12, $12
		)
		ON CONFLICT (user_id, client_device_id)
		WHERE client_device_id IS NOT NULL
		DO UPDATE SET
			device_name          = EXCLUDED.device_name,
			device_type          = EXCLUDED.device_type,
			os_name              = EXCLUDED.os_name,
			browser_name         = EXCLUDED.browser_name,
			last_seen_ip         = EXCLUDED.last_seen_ip,
			last_seen_user_agent = EXCLUDED.last_seen_user_agent,
			last_seen_at         = EXCLUDED.last_seen_at,
			updated_at           = EXCLUDED.updated_at
		RETURNING 
			id,
			user_id,
			device_name,
			device_type,
			os_name,
			browser_name,
			public_key,
			public_key_fingerprint,
			client_device_id,
			risk_flags,
			revoked_at,
			last_seen_ip::text,
			last_seen_user_agent,
			last_seen_at,
			created_at,
			updated_at
	`, r.schema)

	var entity iamEntity.Device
	if err := r.db.QueryRow(ctx, query,
		device.ID,
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
		device.UpdatedAt,
	).Scan(
		&entity.ID,
		&entity.UserID,
		&entity.DeviceName,
		&entity.DeviceType,
		&entity.OSName,
		&entity.BrowserName,
		&entity.PublicKey,
		&entity.PublicKeyFingerprint,
		&entity.ClientDeviceID,
		&entity.RiskFlags,
		&entity.RevokedAt,
		&entity.LastSeenIP,
		&entity.LastSeenUserAgent,
		&entity.LastSeenAt,
		&entity.CreatedAt,
		&entity.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("iam repo: upsert login device: %w", err)
	}

	return &entity, nil
}

// [COMMENT]: ListDevicesByUserID lấy danh sách thiết bị của một user cá nhân dưới dạng DevicePresence gọn nhẹ
func (r *SelfDeviceRepository) ListDevicesByUserID(ctx context.Context, userID uuid.UUID, limit int, offset int) ([]iamEntity.DevicePresence, error) {
	// [COMMENT]: Truy vấn danh sách thiết bị sắp xếp theo thời gian hoạt động gần nhất (last_seen_at) và ngày tạo
	query := fmt.Sprintf(`
		SELECT 
			COALESCE(client_device_id, id),
			device_name,
			last_seen_ip::text,
			last_seen_user_agent,
			last_seen_at,
			revoked_at
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
			&item.ID,
			&item.DeviceName,
			&item.LastIP,
			&item.LastUA,
			&item.LastSeenAt,
			&item.RevokedAt,
		); scanErr != nil {
			return nil, fmt.Errorf("iam repo: scan list device: %w", scanErr)
		}
		items = append(items, item)
	}

	// [COMMENT]: Kiểm tra lỗi stream rows sau khi duyệt
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iam repo: rows error in list self devices: %w", err)
	}

	return items, nil
}

// [COMMENT]: ResolveDeviceIDByFingerprint trả về client_device_id của thiết bị khớp với user và fingerprint
func (r *SelfDeviceRepository) ResolveDeviceIDByFingerprint(ctx context.Context, userID uuid.UUID, fingerprint string) (*uuid.UUID, error) {
	// [COMMENT]: Truy vấn client_device_id chưa bị thu hồi dựa trên khóa công khai fingerprint
	query := fmt.Sprintf(`
		SELECT client_device_id
		FROM %s.devices
		WHERE user_id = $1 
		  AND public_key_fingerprint = $2 
		  AND revoked_at IS NULL
		LIMIT 1
	`, r.schema)

	var clientDeviceID *uuid.UUID
	if err := r.db.QueryRow(ctx, query, userID, fingerprint).Scan(&clientDeviceID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("iam repo: resolve device ID: %w", err)
	}
	return clientDeviceID, nil
}

// [COMMENT]: RevokeSelfDevice thực hiện thu hồi một thiết bị cụ thể của user trong 1 CTE nguyên tử:
// 1. target_device: Khóa và kiểm tra thiết bị mục tiêu có tồn tại và thuộc sở hữu của user.
// 2. revoked_device: Cập nhật revoked_at nếu thiết bị chưa thu hồi và không trùng thiết bị hiện tại đang gọi.
// 3. deleted_tokens: Xóa refresh token gắn liền với thiết bị vừa bị thu hồi.
func (r *SelfDeviceRepository) RevokeSelfDevice(
	ctx context.Context,
	command iamEntity.DeviceRuntimeRevokeDevice,
) (iamEntity.DeviceRuntimeRevokeResult, error) {
	query := fmt.Sprintf(`
		WITH target_device AS MATERIALIZED (
			SELECT 
				id,
				COALESCE(client_device_id, id) AS client_device_id,
				revoked_at
			FROM %s.devices
			WHERE user_id = $1 
			  AND COALESCE(client_device_id, id) = $2
			FOR UPDATE
		),
		revoked_device AS (
			UPDATE %s.devices AS device
			SET 
				revoked_at = NOW(),
				updated_at = NOW()
			FROM target_device AS target
			WHERE device.id = target.id
			  AND target.client_device_id <> $3
			  AND target.revoked_at IS NULL
			RETURNING device.id
		),
		deleted_tokens AS (
			DELETE FROM %s.refresh_tokens
			WHERE user_id = $1 
			  AND device_id IN (SELECT id FROM revoked_device)
			RETURNING id
		)
		SELECT
			EXISTS (SELECT 1 FROM target_device),
			EXISTS (SELECT 1 FROM target_device WHERE client_device_id = $3),
			(SELECT COUNT(*) FROM revoked_device)
	`, r.schema, r.schema, r.schema)

	var result iamEntity.DeviceRuntimeRevokeResult
	if err := r.db.QueryRow(
		ctx,
		query,
		command.UserID,
		command.ClientDeviceID,
		command.CurrentDeviceID,
	).Scan(&result.TargetExists, &result.CurrentDevice, &result.Affected); err != nil {
		return iamEntity.DeviceRuntimeRevokeResult{}, fmt.Errorf("iam device runtime revoke: revoke device: %w", err)
	}
	return result, nil
}

// [COMMENT]: RevokeOtherSelfDevices thu hồi tất cả các thiết bị khác ngoại trừ thiết bị hiện tại của user trong 1 CTE nguyên tử:
// 1. target_devices: Khóa tất cả các thiết bị khác thiết bị hiện tại (client_device_id <> $2).
// 2. revoked_devices: Cập nhật revoked_at cho các thiết bị chưa bị thu hồi và trả về client_device_id.
// 3. deleted_tokens: Xóa toàn bộ refresh tokens thuộc các thiết bị bị thu hồi.
func (r *SelfDeviceRepository) RevokeOtherSelfDevices(
	ctx context.Context,
	command iamEntity.DeviceRuntimeRevokeOthers,
) (iamEntity.DeviceRuntimeRevokeOthersResult, error) {
	query := fmt.Sprintf(`
		WITH target_devices AS MATERIALIZED (
			SELECT 
				id,
				COALESCE(client_device_id, id) AS client_device_id,
				revoked_at
			FROM %s.devices
			WHERE user_id = $1 
			  AND COALESCE(client_device_id, id) <> $2
			FOR UPDATE
		),
		revoked_devices AS (
			UPDATE %s.devices AS device
			SET 
				revoked_at = NOW(),
				updated_at = NOW()
			FROM target_devices AS target
			WHERE device.id = target.id 
			  AND target.revoked_at IS NULL
			RETURNING device.id, COALESCE(device.client_device_id, device.id) AS client_device_id
		),
		deleted_tokens AS (
			DELETE FROM %s.refresh_tokens
			WHERE user_id = $1 
			  AND device_id IN (SELECT id FROM revoked_devices)
			RETURNING id
		)
		SELECT 
			COALESCE(ARRAY_AGG(client_device_id ORDER BY client_device_id), '{}')::uuid[],
			(SELECT COUNT(*) FROM target_devices)
		FROM revoked_devices
	`, r.schema, r.schema, r.schema)

	var result iamEntity.DeviceRuntimeRevokeOthersResult
	if err := r.db.QueryRow(
		ctx,
		query,
		command.UserID,
		command.CurrentDeviceID,
	).Scan(&result.RevokedDeviceIDs, &result.Affected); err != nil {
		return iamEntity.DeviceRuntimeRevokeOthersResult{}, fmt.Errorf("iam device runtime revoke: revoke other devices: %w", err)
	}
	return result, nil
}

// [COMMENT]: ApplyDevicePresenceProjection cập nhật hàng loạt trạng thái hiện diện (last_seen_at, last_seen_ip, user_agent) từ advisory presence batch stream
func (r *SelfDeviceRepository) ApplyDevicePresenceProjection(
	ctx context.Context,
	updates []iamEntity.DevicePresenceUpdate,
) error {
	ids := make([]uuid.UUID, 0, len(updates))
	timestamps := make([]int64, 0, len(updates))
	ips := make([]string, 0, len(updates))
	userAgents := make([]string, 0, len(updates))
	for _, update := range updates {
		if uid, err := uuid.Parse(strings.TrimSpace(update.DeviceID)); err == nil && uid != uuid.Nil {
			ids = append(ids, uid)
			timestamps = append(timestamps, update.LastSeenAt)
			ips = append(ips, update.LastSeenIP)
			userAgents = append(userAgents, update.LastSeenUserAgent)
		}
	}
	if len(ids) == 0 {
		return nil
	}

	// [COMMENT]: CTE raw_updates giải nén mảng đầu vào, updates chọn bản ghi mới nhất theo timestamp,
	// và câu UPDATE chỉ cập nhật nếu thời gian mới hơn dữ liệu hiện tại trong DB (monotonic update)
	query := fmt.Sprintf(`
		WITH raw_updates AS (
			SELECT
				unnest($1::uuid[])   AS client_device_id,
				unnest($2::bigint[]) AS last_seen_at_unix,
				unnest($3::text[])   AS last_seen_ip,
				unnest($4::text[])   AS last_seen_user_agent
		),
		updates AS (
			SELECT DISTINCT ON (client_device_id)
				client_device_id,
				last_seen_at_unix,
				last_seen_ip,
				last_seen_user_agent
			FROM raw_updates
			ORDER BY client_device_id, last_seen_at_unix DESC
		)
		UPDATE %s.devices AS device
		SET
			last_seen_at         = to_timestamp(updates.last_seen_at_unix),
			last_seen_ip         = NULLIF(updates.last_seen_ip, '')::inet,
			last_seen_user_agent = NULLIF(updates.last_seen_user_agent, ''),
			updated_at           = NOW()
		FROM updates
		WHERE device.client_device_id = updates.client_device_id
		  AND (device.last_seen_at IS NULL OR device.last_seen_at <= to_timestamp(updates.last_seen_at_unix))
	`, r.schema)

	if _, err := r.db.Exec(ctx, query, ids, timestamps, ips, userAgents); err != nil {
		return fmt.Errorf("iam device presence projection: apply: %w", err)
	}
	return nil
}

// [COMMENT]: ApplyDeviceSessionCapacityEviction thực hiện thu hồi hàng loạt thiết bị và xóa refresh tokens khi vượt quá giới hạn session capacity (50 thiết bị)
func (r *SelfDeviceRepository) ApplyDeviceSessionCapacityEviction(
	ctx context.Context,
	userID uuid.UUID,
	clientDeviceIDs []uuid.UUID,
) error {
	if len(clientDeviceIDs) == 0 {
		return nil
	}

	// [COMMENT]: CTE requested_devices lọc các ID bị evict, revoked_devices đánh dấu revoked_at = NOW(),
	// và DELETE xóa tất cả refresh tokens tương ứng trong một transaction ngầm định
	query := fmt.Sprintf(`
		WITH requested_devices AS (
			SELECT DISTINCT unnest($2::uuid[]) AS client_device_id
		),
		revoked_devices AS (
			UPDATE %s.devices AS device
			SET 
				revoked_at = NOW(),
				updated_at = NOW()
			FROM requested_devices
			WHERE device.user_id = $1
			  AND device.client_device_id = requested_devices.client_device_id
			  AND device.revoked_at IS NULL
			RETURNING device.id
		)
		DELETE FROM %s.refresh_tokens
		WHERE user_id = $1
		  AND device_id IN (SELECT id FROM revoked_devices)
	`, r.schema, r.schema)

	if _, err := r.db.Exec(ctx, query, userID, clientDeviceIDs); err != nil {
		return fmt.Errorf("iam device session capacity eviction: apply: %w", err)
	}
	return nil
}
