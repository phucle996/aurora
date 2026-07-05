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
func (r *UserRepository) ListUsers(ctx context.Context, callerLevel int32, limit int, offset int) ([]*iamEntity.User, error) {
	// [COMMENT]: Sử dụng LEFT JOIN với user_role để xác định level của từng user ở platform scope (nil UUID).
	// Nếu user không có platform role, mặc định coi họ có level là 100 (End User).
	// Thực hiện gom nhóm GROUP BY u.id để tính MIN(ur.role_level) đại diện cho level cao nhất của user.
	// Lọc HAVING level > callerLevel.
	query := fmt.Sprintf(`
		SELECT 
			u.id, 
			u.username, 
			u.email, 
			u.status, 
			COALESCE(MIN(ur.role_level), 100) AS role_level, 
			u.created_at, 
			u.updated_at
		FROM %s.users u
		LEFT JOIN %s.user_role ur ON u.id = ur.user_id 
		                         AND ur.workspace_id = '00000000-0000-0000-0000-000000000000'
		GROUP BY u.id
		HAVING COALESCE(MIN(ur.role_level), 100) > $1
		ORDER BY u.created_at DESC
		LIMIT $2 OFFSET $3
	`, r.schema, r.schema)

	rows, err := r.db.Query(ctx, query, callerLevel, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("iam repo: query list users: %w", err)
	}
	defer rows.Close()

	var users []*iamEntity.User
	for rows.Next() {
		var u iamEntity.User
		var level int32
		if err := rows.Scan(&u.ID, &u.Username, &u.Email, &u.Status, &level, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, fmt.Errorf("iam repo: scan user row: %w", err)
		}
		u.Level = level
		users = append(users, &u)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return users, nil
}

// [COMMENT]: UpdateUserStatus thực hiện cập nhật trạng thái hoạt động (status) của user dưới DB
func (r *UserRepository) UpdateUserStatus(ctx context.Context, userID uuid.UUID, status string) error {
	query := fmt.Sprintf("UPDATE %s.users SET status = $1, updated_at = NOW() WHERE id = $2", r.schema)
	res, err := r.db.Exec(ctx, query, status, userID)
	if err != nil {
		return err
	}
	if res.RowsAffected() == 0 {
		return iamTaxonomy.ErrUserNotFound
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
