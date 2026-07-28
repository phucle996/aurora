package iamRepoImpl

import (
	"context"
	"errors"
	"fmt"
	"time"

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
	// [COMMENT]: JOIN user_role để lấy phân cấp, LEFT JOIN device hoạt động gần nhất theo last_seen_at
	// để hiển thị IP thực tế và thời điểm hoạt động cuối cùng của user
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
			COALESCE(up.bio, '') AS bio,
			COALESCE(up.fullname, '') AS fullname,
			COALESCE(ld.last_seen_ip::text, '') AS last_seen_ip,
			ld.last_seen_at,
			u.created_at, 
			u.updated_at
		FROM %s.users u
		JOIN %s.user_role ur ON u.id = ur.user_id 
		                    AND ur.workspace_id = '00000000-0000-0000-0000-000000000000'
		LEFT JOIN %s.user_profiles up ON u.id = up.user_id
		LEFT JOIN LATERAL (
			SELECT last_seen_ip, last_seen_at
			FROM %s.devices
			WHERE user_id = u.id AND revoked_at IS NULL
			ORDER BY last_seen_at DESC NULLS LAST
			LIMIT 1
		) ld ON true
		WHERE ur.role_level > $1
		ORDER BY u.created_at DESC
		LIMIT $2 OFFSET $3
	`, r.schema, r.schema, r.schema, r.schema, r.schema, r.schema)

	rows, err := r.db.Query(ctx, query, callerLevel, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []*iamEntity.User
	for rows.Next() {
		var u iamEntity.User
		var level int32
		if err := rows.Scan(&u.ID, &u.Username, &u.Email, &u.Status, &level, &u.RoleName, &u.MfaEnabled, &u.DevicesCount, &u.Bio, &u.Fullname, &u.LastSeenIP, &u.LastSeenAt, &u.CreatedAt, &u.UpdatedAt); err != nil {
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

func (r *UserRepository) GetUserAuthMethods(
	ctx context.Context,
	callerLevel uint8,
	userID uuid.UUID,
) (*iamEntity.UserAuthMethods, error) {
	var (
		accountEmail      string
		passwordSet       bool
		googleProvider    *string
		googleEmail       *string
		googleVerifiedAt  *time.Time
		googleLastLoginAt *time.Time
		googleLinkedAt    *time.Time
		googleRevokedAt   *time.Time
		githubProvider    *string
		githubEmail       *string
		githubVerifiedAt  *time.Time
		githubLastLoginAt *time.Time
		githubLinkedAt    *time.Time
		githubRevokedAt   *time.Time
	)
	query := fmt.Sprintf(`
		WITH effective_role AS (
			SELECT user_id, MIN(role_level) AS role_level
			FROM %s.user_role
			WHERE workspace_id = '00000000-0000-0000-0000-000000000000'
			GROUP BY user_id
		)
		SELECT
			u.email,
			(u.password_hash IS NOT NULL),
			g.provider, g.provider_email, g.email_verified_at, g.last_login_at, g.created_at, g.revoked_at,
			h.provider, h.provider_email, h.email_verified_at, h.last_login_at, h.created_at, h.revoked_at
		FROM %s.users u
		JOIN effective_role er ON er.user_id = u.id AND er.role_level > $1
		LEFT JOIN %s.external_identities g
		       ON g.user_id = u.id AND g.provider = 'google'
		LEFT JOIN %s.external_identities h
		       ON h.user_id = u.id AND h.provider = 'github'
		WHERE u.id = $2
	`, r.schema, r.schema, r.schema, r.schema)
	err := r.db.QueryRow(ctx, query, callerLevel, userID).Scan(
		&accountEmail,
		&passwordSet,
		&googleProvider,
		&googleEmail,
		&googleVerifiedAt,
		&googleLastLoginAt,
		&googleLinkedAt,
		&googleRevokedAt,
		&githubProvider,
		&githubEmail,
		&githubVerifiedAt,
		&githubLastLoginAt,
		&githubLinkedAt,
		&githubRevokedAt,
	)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("iam repo: get user auth methods: %w", err)
		}
		var exists bool
		if existsErr := r.db.QueryRow(ctx, fmt.Sprintf(`SELECT EXISTS (SELECT 1 FROM %s.users WHERE id = $1)`, r.schema), userID).Scan(&exists); existsErr != nil {
			return nil, fmt.Errorf("iam repo: check user auth-method target: %w", existsErr)
		}
		if !exists {
			return nil, iamTaxonomy.ErrUserNotFound
		}
		return nil, iamTaxonomy.ErrActionNotAllowed
	}

	summary := func(provider *string, email *string, verifiedAt, lastLoginAt, linkedAt, revokedAt *time.Time, expected iamEntity.ExternalProvider) iamEntity.ExternalIdentitySummary {
		result := iamEntity.ExternalIdentitySummary{
			Provider: expected,
			State:    iamEntity.ExternalIdentityNotLinked,
		}
		if provider == nil {
			return result
		}
		result.ProviderEmail = valueOrEmpty(email)
		result.EmailVerifiedAt = verifiedAt
		result.LastLoginAt = lastLoginAt
		result.LinkedAt = linkedAt
		if revokedAt != nil {
			result.State = iamEntity.ExternalIdentityRevoked
		} else {
			result.State = iamEntity.ExternalIdentityLinked
		}
		return result
	}
	return &iamEntity.UserAuthMethods{
		AccountEmail: accountEmail,
		PasswordSet:  passwordSet,
		Google:       summary(googleProvider, googleEmail, googleVerifiedAt, googleLastLoginAt, googleLinkedAt, googleRevokedAt, iamEntity.ExternalProviderGoogle),
		GitHub:       summary(githubProvider, githubEmail, githubVerifiedAt, githubLastLoginAt, githubLinkedAt, githubRevokedAt, iamEntity.ExternalProviderGitHub),
	}, nil
}

// [COMMENT]: ResetUserPassword cập nhật mật khẩu mới của user dưới DB và ghi nhận mật khẩu cũ vào password_history qua CTE duy nhất bảo vệ phân cấp quyền lực
func (r *UserRepository) ResetUserPassword(ctx context.Context, callerLevel uint8, userID uuid.UUID, passwordHash string) error {
	query := fmt.Sprintf(`
		WITH target_user AS (
			SELECT u.id, u.password_hash, ur.role_level
			FROM %s.users u
			JOIN %s.user_role ur ON u.id = ur.user_id AND ur.workspace_id = '00000000-0000-0000-0000-000000000000'
			WHERE u.id = $2
		),
		updater AS (
			UPDATE %s.users u
			SET password_hash = $1, updated_at = NOW()
			FROM target_user tu
			WHERE u.id = $2 AND tu.role_level > $3
			RETURNING u.id, tu.password_hash AS old_password_hash
		),
		history_ins AS (
			INSERT INTO %s.password_history (id, user_id, password_hash, created_at)
			SELECT gen_random_uuid(), id, old_password_hash, NOW()
			FROM updater
			RETURNING id
		)
		SELECT 
			(SELECT COUNT(*) FROM target_user) AS user_exists,
			(SELECT COUNT(*) FROM updater) AS update_success
	`, r.schema, r.schema, r.schema, r.schema)

	var userExists, updateSuccess int
	err := r.db.QueryRow(ctx, query, passwordHash, userID, callerLevel).Scan(&userExists, &updateSuccess)
	if err != nil {
		return err
	}

	// [COMMENT]: Xử lý kết quả trả về tương tự logic cập nhật status để đảm bảo tính phân cấp
	if userExists == 0 {
		return iamTaxonomy.ErrUserNotFound
	}
	if updateSuccess == 0 {
		return iamTaxonomy.ErrActionNotAllowed
	}

	return nil
}
