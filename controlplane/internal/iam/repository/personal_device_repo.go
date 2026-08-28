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

// [COMMENT]: PersonalDeviceRepository thực thi interface quản lý thiết bị người dùng ở phạm vi platform-authorized (/personal branch)
type PersonalDeviceRepository struct {
	db     *pgxpool.Pool
	schema string
}

// [COMMENT]: NewPersonalDeviceRepository khởi tạo repository quản lý thiết bị cho personal branch với kết nối PostgreSQL pool
func NewPersonalDeviceRepository(cfg *config.Config, db *pgxpool.Pool) iamRepoInterface.PersonalDeviceRepository {
	return &PersonalDeviceRepository{db: db, schema: cfg.SchemaSQL.IAM}
}

// [COMMENT]: ListDevicesByUserID lấy danh sách thiết bị của một user phục vụ platform audit kèm hierarchy check trong 1 RTT CTE dưới dạng DevicePresence gọn nhẹ
func (r *PersonalDeviceRepository) ListDevicesByUserID(ctx context.Context, userID uuid.UUID, callerLevel int32, limit int, offset int) ([]iamEntity.PersonalDeviceListItem, error) {
	// [COMMENT]: Xây dựng câu truy vấn CTE nguyên tử:
	// - target_info: Truy vấn role_level của user mục tiêu từ bảng user_roles & platform_roles.
	// - devs: Lấy danh sách thiết bị khi và chỉ khi callerLevel có thẩm quyền cao hơn target user (role_level > callerLevel).
	// - SELECT ngoài kết hợp LEFT JOIN: Luôn trả về target_level ngay cả khi user chưa đăng ký thiết bị nào, bảo đảm rào chắn phân cấp.
	query := fmt.Sprintf(`
		WITH target_info AS (
			SELECT r.role_level
			FROM %s.user_roles ur
			JOIN %s.platform_roles r ON r.id = ur.role_id
			WHERE ur.user_id = $1
			LIMIT 1
		),
		devs AS (
			SELECT 
				d.id,
				d.device_name,
				d.last_seen_ip::text AS last_seen_ip,
				d.last_seen_user_agent,
				d.last_seen_at,
				d.revoked_at
			FROM %s.devices d
			WHERE d.user_id = $1 
			  AND (SELECT role_level FROM target_info) > $2
			ORDER BY d.last_seen_at DESC NULLS LAST, d.created_at DESC
			LIMIT $3 OFFSET $4
		)
		SELECT 
			t.role_level AS target_level,
			d.id,
			d.device_name,
			d.last_seen_ip,
			d.last_seen_user_agent,
			d.last_seen_at,
			d.revoked_at
		FROM target_info t
		LEFT JOIN devs d ON true
	`, r.schema, r.schema, r.schema)

	// [COMMENT]: Thực thi truy vấn với database pool trong 1 RTT duy nhất
	rows, err := r.db.Query(ctx, query, userID, callerLevel, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("iam repo: list devices with hierarchy CTE: %w", err)
	}
	defer rows.Close()

	var targetLevel int32
	var hasTargetInfo bool
	items := make([]iamEntity.PersonalDeviceListItem, 0)

	// [COMMENT]: Duyệt qua từng dòng kết quả trả về từ database
	for rows.Next() {
		hasTargetInfo = true
		var item iamEntity.PersonalDeviceListItem
		var deviceID *uuid.UUID // [COMMENT]: Scan device ID dạng con trỏ để nhận diện giá trị NULL khi user không có thiết bị

		if scanErr := rows.Scan(
			&targetLevel,
			&deviceID,
			&item.DeviceName,
			&item.LastIP,
			&item.LastUA,
			&item.LastSeenAt,
			&item.RevokedAt,
		); scanErr != nil {
			return nil, fmt.Errorf("iam repo: scan list device with hierarchy CTE: %w", scanErr)
		}

		// [COMMENT]: Kiểm tra phân cấp quyền: Nếu targetLevel <= callerLevel (target có quyền ngang hoặc cao hơn caller) thì từ chối truy cập
		if targetLevel <= callerLevel {
			return nil, iamTaxonomy.ErrActionNotAllowed
		}

		// [COMMENT]: Nếu có thiết bị hợp lệ (không phải NULL do LEFT JOIN), thêm vào danh sách kết quả
		if deviceID != nil {
			item.ID = *deviceID
			items = append(items, item)
		}
	}

	// [COMMENT]: Kiểm tra lỗi sau khi kết thúc duyệt rows
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iam repo: rows error in list devices: %w", err)
	}

	// [COMMENT]: Nếu không tìm thấy target_info, chứng tỏ user không tồn tại hoặc chưa được gán vai trò
	if !hasTargetInfo {
		return nil, fmt.Errorf("target user role level not found")
	}

	return items, nil
}
