package iamRepoImpl

import (
	"context"
	"fmt"

	"controlplane/internal/config"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamTaxonomy "controlplane/internal/iam/taxonomy" // [COMMENT]: Import taxonomy để trả về lỗi phân cấp ErrActionNotAllowed

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// [COMMENT]: DevicePlatformRepository thực thi việc truy vấn thiết bị để giám sát và quản lý thiết bị toàn hệ thống
type DevicePlatformRepository struct {
	db     *pgxpool.Pool
	schema string
}

// [COMMENT]: NewDevicePlatformRepository khởi tạo DevicePlatformRepository
func NewDevicePlatformRepository(cfg *config.Config, db *pgxpool.Pool) iamRepoInterface.DevicePlatformRepository {
	return &DevicePlatformRepository{db: db, schema: cfg.SchemaSQL.IAM}
}

// [COMMENT]: ListDevicesByUserIDWithHierarchy lấy danh sách thiết bị của một user phục vụ platform audit kèm hierarchy check trong 1 RTT CTE dưới dạng DevicePresence gọn nhẹ
func (r *DevicePlatformRepository) ListDevicesByUserIDWithHierarchy(ctx context.Context, userID uuid.UUID, callerLevel int32, limit int, offset int) ([]iamEntity.DevicePresence, error) {
	query := fmt.Sprintf(`
		WITH target_info AS (
			SELECT r.role_level
			FROM %s.user_roles ur
			JOIN %s.roles r ON r.id = ur.role_id
			WHERE ur.user_id = $1
			LIMIT 1
		),
		devs AS (
			SELECT d.id, d.device_name, d.last_seen_ip::text AS last_seen_ip, d.last_seen_user_agent, d.last_seen_at, d.revoked_at
			FROM %s.devices d
			WHERE d.user_id = $1 AND (SELECT role_level FROM target_info) > $2
			ORDER BY d.last_seen_at DESC NULLS LAST, d.created_at DESC
			LIMIT $3 OFFSET $4
		)
		SELECT 
			t.role_level AS target_level,
			d.id, d.device_name, d.last_seen_ip, d.last_seen_user_agent, d.last_seen_at, d.revoked_at
		FROM target_info t
		LEFT JOIN devs d ON true
	`, r.schema, r.schema, r.schema)

	rows, err := r.db.Query(ctx, query, userID, callerLevel, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("iam repo: list devices with hierarchy CTE: %w", err)
	}
	defer rows.Close()

	var targetLevel int32
	var hasTargetInfo bool
	items := make([]iamEntity.DevicePresence, 0)

	for rows.Next() {
		hasTargetInfo = true
		var item iamEntity.DevicePresence
		var deviceID *string // scan device ID dạng con trỏ để nhận diện giá trị NULL từ LEFT JOIN

		if scanErr := rows.Scan(
			&targetLevel,
			&deviceID, &item.DeviceName,
			&item.LastIP, &item.LastUA, &item.LastSeenAt, &item.RevokedAt,
		); scanErr != nil {
			return nil, fmt.Errorf("iam repo: scan list device with hierarchy CTE: %w", scanErr)
		}

		// [COMMENT]: 2. Kiểm tra phân cấp level ngay khi lấy dữ liệu dòng đầu tiên
		if targetLevel <= callerLevel {
			return nil, iamTaxonomy.ErrActionNotAllowed
		}

		if deviceID != nil {
			item.ID = *deviceID
			items = append(items, item)
		}
	}

	if !hasTargetInfo {
		// [COMMENT]: User không tồn tại hoặc chưa được gán vai trò nào
		return nil, fmt.Errorf("target user role level not found")
	}

	return items, nil
}
