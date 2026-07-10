package iamRepoImpl

import (
	"context"
	"errors"
	"fmt"

	"controlplane/internal/config"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamTaxonomy "controlplane/internal/iam/taxonomy"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type UserRepository struct {
	db     *pgxpool.Pool
	schema string
}

func NewUserRepository(
	cfg *config.Config,
	db *pgxpool.Pool,
) iamRepoInterface.UserRepository {
	return &UserRepository{
		db:     db,
		schema: cfg.SchemaSQL.IAM,
	}
}

// [COMMENT]: ListUsers lấy danh sách các user có level thấp hơn level hiện tại của caller (role_level số lớn hơn)
func (r *UserRepository) ListUsers(ctx context.Context, callerLevel uint8, limit int, offset int) ([]*iamEntity.User, error) {
	// [COMMENT]: Thực hiện JOIN 1-1 trực tiếp với bảng user_role tại platform scope (nil UUID)
	// để lấy thông tin role_name và role_level của từng user. Lọc theo phân cấp callerLevel.
	query := fmt.Sprintf(`
		SELECT 
			u.id, 
			u.username, 
			u.email, 
			u.status, 
			ur.role_level, 
			ur.role_name, 
			EXISTS (
				SELECT 1 FROM %s.mfa_settings ms 
				WHERE ms.user_id = u.id AND ms.disabled_at IS NULL
			) AS mfa_enabled,
			(
				SELECT COUNT(*) FROM %s.devices d 
				WHERE d.user_id = u.id
			) AS devices_count,
			u.created_at, 
			u.updated_at
		FROM %s.users u
		JOIN %s.user_role ur ON u.id = ur.user_id 
		                    AND ur.workspace_id = '00000000-0000-0000-0000-000000000000'
		WHERE ur.role_level > $1
		ORDER BY u.created_at DESC
		LIMIT $2 OFFSET $3
	`, r.schema, r.schema, r.schema, r.schema)

	rows, err := r.db.Query(ctx, query, callerLevel, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []*iamEntity.User
	for rows.Next() {
		var u iamEntity.User
		var level int32
		if err := rows.Scan(&u.ID, &u.Username, &u.Email, &u.Status, &level, &u.RoleName, &u.MfaEnabled, &u.DevicesCount, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, err
		}
		u.Level = level
		users = append(users, &u)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return users, nil
}

// [COMMENT]: UpdateUserStatus thực hiện cập nhật trạng thái hoạt động (status) của user dưới DB nếu đủ phân cấp dùng 1 query CTE để tối ưu và tránh race condition
func (r *UserRepository) UpdateUserStatus(ctx context.Context, callerLevel uint8, userID uuid.UUID, status string) error {
	query := fmt.Sprintf(`
		WITH target_user AS (
			SELECT role_level 
			FROM %s.user_role 
			WHERE user_id = $2 AND workspace_id = '00000000-0000-0000-0000-000000000000'
		),
		updater AS (
			UPDATE %s.users u
			SET status = $1, updated_at = NOW()
			FROM target_user tu
			WHERE u.id = $2 AND tu.role_level > $3
			RETURNING u.id
		)
		SELECT 
			(SELECT COUNT(*) FROM target_user) AS user_exists,
			(SELECT COUNT(*) FROM updater) AS update_success
	`, r.schema, r.schema)

	var userExists, updateSuccess int
	err := r.db.QueryRow(ctx, query, status, userID, callerLevel).Scan(&userExists, &updateSuccess)
	if err != nil {
		return err
	}

	// [COMMENT]: Xử lý kết quả trả về từ CTE:
	// 1. Nếu user_exists == 0 -> Đối tượng đích không tồn tại
	if userExists == 0 {
		return iamTaxonomy.ErrUserNotFound
	}
	// 2. Nếu user_exists == 1 nhưng update_success == 0 -> Phân cấp callerLevel >= targetLevel (không đủ quyền lực)
	if updateSuccess == 0 {
		return iamTaxonomy.ErrActionNotAllowed
	}

	return nil
}

// [COMMENT]: GetUserProfile lấy thông tin profile hiển thị của user từ bảng user_profiles
func (r *UserRepository) GetUserProfile(ctx context.Context, userID uuid.UUID) (*iamEntity.UserProfile, error) {
	query := fmt.Sprintf(`
		SELECT user_id, fullname, avatar_url, bio, locale, timezone, created_at, updated_at
		FROM %s.user_profiles
		WHERE user_id = $1
	`, r.schema)

	var p iamEntity.UserProfile
	err := r.db.QueryRow(ctx, query, userID).Scan(
		&p.UserID,
		&p.Fullname,
		&p.AvatarURL,
		&p.Bio,
		&p.Locale,
		&p.Timezone,
		&p.CreatedAt,
		&p.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, iamTaxonomy.ErrUserNotFound
		}
		return nil, err
	}

	return &p, nil
}
