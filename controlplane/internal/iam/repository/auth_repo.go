package iamRepoImpl

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"controlplane/internal/config"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamModel "controlplane/internal/iam/model"
	iamTaxonomy "controlplane/internal/iam/taxonomy"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type AuthRepository struct {
	db     *pgxpool.Pool
	schema string
}

func NewAuthRepository(
	cfg *config.Config,
	db *pgxpool.Pool,
) iamRepoInterface.AuthRepository {
	return &AuthRepository{
		db:     db,
		schema: cfg.SchemaSQL.IAM,
	}
}

func (r *AuthRepository) LoginUserByUsername(ctx context.Context, username string) (*iamEntity.LoginUser, error) {
	// [COMMENT]: Thực hiện truy vấn JOIN bảng users với user_role_assignments & roles để lấy thông tin user kèm max role
	query := fmt.Sprintf(`
		SELECT 
			u.id,
			u.username,
			u.email,
			u.password_hash, 
			u.status,
			r.code       AS role_code,
			r.role_level AS role_level
		FROM %s.users u
		JOIN %s.user_role_assignments ura ON ura.user_id = u.id 
		                                 AND ura.scope_type = 'platform' 
		                                 AND (ura.expires_at IS NULL OR ura.expires_at > NOW()) 
		                                 AND ura.revoked_at IS NULL
		JOIN %s.roles r                 ON r.id = ura.role_id
		WHERE u.username = $1
		ORDER BY r.role_level ASC
		LIMIT 1
	`, r.schema, r.schema, r.schema)

	var (
		userModel iamModel.User
		roleCode  string
		roleLevel int32
	)
	if err := r.db.QueryRow(ctx, query, username).Scan(
		&userModel.ID,
		&userModel.Username,
		&userModel.Email,
		&userModel.PasswordHash,
		&userModel.Status,
		&roleCode,
		&roleLevel,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, iamTaxonomy.ErrUserNotFound
		}
		return nil, fmt.Errorf("iam repo: get login user by username: %w", err)
	}

	loginUser := &iamEntity.LoginUser{
		ID:           userModel.ID,
		Username:     userModel.Username,
		Email:        userModel.Email,
		PasswordHash: userModel.PasswordHash,
		Status:       iamEntity.UserStatus(userModel.Status),
		Role:         roleCode,
		Level:        roleLevel,
	}

	return loginUser, nil
}

func (r *AuthRepository) LoginUserByUsernameAndTenantDomain(
	ctx context.Context,
	username string,
	tenantDomain string,
) (*iamEntity.LoginUser, error) {
	// [COMMENT]: Query JOIN hierarchy.tenant_memberships, tenants, tenant_domains
	// và INNER JOIN sang iam.user_role_assignments, iam.roles để lấy max role thuộc tenant đó.
	query := fmt.Sprintf(`
		SELECT
			u.id,
			u.username,
			u.email,
			u.password_hash,
			u.status,
			t.id::text   AS tenant_id,
			t.code       AS tenant_code,
			r.code       AS role_code,
			r.role_level AS role_level
		FROM %s.users u
		JOIN hierarchy.tenant_memberships tm ON tm.user_id = u.id AND tm.status = 'active'
		JOIN hierarchy.tenants t             ON t.id = tm.tenant_id
		JOIN hierarchy.tenant_domains td     ON td.tenant_id = t.id AND td.domain = $2
		JOIN %s.user_role_assignments ura ON ura.user_id = u.id 
		                                 AND ura.tenant_id = t.id 
		                                 AND (ura.expires_at IS NULL OR ura.expires_at > NOW()) 
		                                 AND ura.revoked_at IS NULL
		JOIN %s.roles r                 ON r.id = ura.role_id
		WHERE u.username = $1
		ORDER BY r.role_level ASC
		LIMIT 1
	`, r.schema, r.schema, r.schema)

	var (
		userModel  iamModel.User
		tenantID   string
		tenantCode string
		roleCode   string
		roleLevel  int32
	)
	if err := r.db.QueryRow(ctx, query, username, tenantDomain).Scan(
		&userModel.ID,
		&userModel.Username,
		&userModel.Email,
		&userModel.PasswordHash,
		&userModel.Status,
		&tenantID,
		&tenantCode,
		&roleCode,
		&roleLevel,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, iamTaxonomy.ErrUserNotFound
		}
		return nil, fmt.Errorf("iam repo: login user by username and tenant domain: %w", err)
	}

	loginUser := &iamEntity.LoginUser{
		ID:           userModel.ID,
		Username:     userModel.Username,
		Email:        userModel.Email,
		PasswordHash: userModel.PasswordHash,
		Status:       iamEntity.UserStatus(userModel.Status),
		TenantID:     &tenantID,
		TenantCode:   &tenantCode,
		Role:         roleCode,
		Level:        roleLevel,
	}

	return loginUser, nil
}

func (r *AuthRepository) CreateRegisteredUser(ctx context.Context, user iamEntity.User, profile iamEntity.UserProfile) error {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("iam repo: begin register tx: %w", err)
	}
	defer tx.Rollback(ctx)

	userModel := iamModel.UserEntityToModel(user)

	userQuery := fmt.Sprintf(`
		INSERT INTO %s.users (
			id,
			username,
			email,
			phone,
			password_hash,
			status,
			created_at,
			updated_at
		)
		VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8
		)
	`, r.schema)

	if _, err := tx.Exec(
		ctx,
		userQuery,
		userModel.ID,
		userModel.Username,
		userModel.Email,
		userModel.Phone,
		userModel.PasswordHash,
		userModel.Status,
		userModel.CreatedAt,
		userModel.UpdatedAt,
	); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			constraint := strings.ToLower(pgErr.ConstraintName)
			if strings.Contains(constraint, "users_email_lower_uidx") || strings.Contains(constraint, "users_username_lower_uidx") {
				return fmt.Errorf("%w: %v", iamTaxonomy.ErrUserAlreadyExist, err)
			}
		}
		return fmt.Errorf("iam repo: insert user: %w", err)
	}

	profileModel := iamModel.UserProfileEntityToModel(profile)

	profileQuery := fmt.Sprintf(`
		INSERT INTO %s.user_profiles (
			user_id,
			fullname,
			avatar_url,
			bio,
			locale,
			timezone,
			created_at,
			updated_at
		)
		VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8
		)
	`, r.schema)

	if _, err := tx.Exec(
		ctx,
		profileQuery,
		profileModel.UserID,
		profileModel.Fullname,
		profileModel.AvatarURL,
		profileModel.Bio,
		profileModel.Locale,
		profileModel.Timezone,
		profileModel.CreatedAt,
		profileModel.UpdatedAt,
	); err != nil {
		return fmt.Errorf("iam repo: insert user profile: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("iam repo: commit register tx: %w", err)
	}

	return nil
}

// [COMMENT]: ActivateUserWithRole thực hiện kích hoạt tài khoản và gán vai trò platform_user mặc định
// bằng 1 câu query duy nhất sử dụng CTE (Common Table Expressions) để tối ưu hóa I/O và giảm thiểu round-trip mạng.
func (r *AuthRepository) ActivateUserWithRole(ctx context.Context, userID uuid.UUID, roleCode string) error {
	// [COMMENT]: Sử dụng CTE để:
	// 1. UPDATE status của user thành active nếu nó đang ở trạng thái pending-active.
	// 2. INSERT một bản ghi user_role_assignments cho platform_user dựa trên kết quả cập nhật ở bước 1.
	// 3. Trả về thông tin trạng thái để code Go kiểm tra tính tồn tại và xử lý tính idempotent.
	query := fmt.Sprintf(`
		WITH updated_user AS (
			UPDATE %s.users 
			SET status = 'active', updated_at = NOW() 
			WHERE id = $1 AND status = 'pending-active'
			RETURNING id
		),
		inserted_role AS (
			INSERT INTO %s.user_role_assignments (
				id, user_id, role_id, scope_type, tenant_id, workspace_id, assigned_by, assigned_at
			)
			SELECT $2, $1, r.id, 'platform', NULL, NULL, $1, NOW()
			FROM %s.roles r
			WHERE r.code = $3 AND r.scope_type = 'platform' 
			  AND EXISTS (SELECT 1 FROM updated_user)
			ON CONFLICT DO NOTHING
			RETURNING user_id
		)
		SELECT 
			EXISTS (SELECT 1 FROM updated_user) AS activated,
			EXISTS (SELECT 1 FROM %s.users WHERE id = $1) AS user_exists,
			COALESCE((SELECT status FROM %s.users WHERE id = $1), '') AS current_status;
	`, r.schema, r.schema, r.schema, r.schema, r.schema)

	assignmentID := uuid.Must(uuid.NewV7())

	var (
		activated     bool
		userExists    bool
		currentStatus string
	)

	// [COMMENT]: Thực thi câu query duy nhất
	err := r.db.QueryRow(ctx, query, userID, assignmentID, roleCode).Scan(
		&activated,
		&userExists,
		&currentStatus,
	)
	if err != nil {
		return err
	}

	// [COMMENT]: 1. Nếu user không tồn tại trong DB, trả về lỗi ErrUserNotFound
	if !userExists {
		return iamTaxonomy.ErrUserNotFound
	}

	// [COMMENT]: 2. Nếu không active được (do trạng thái hiện tại khác 'pending-active')
	if !activated {
		// [COMMENT]: Idempotent: Nếu đã active từ trước thì coi như thành công và bỏ qua
		if currentStatus == "active" {
			return nil
		}
		// [COMMENT]: Nếu ở các trạng thái khác (như suspended, disabled), trả về lỗi nghiệp vụ
		return fmt.Errorf("user status is %s, cannot activate", currentStatus)
	}

	return nil
}
