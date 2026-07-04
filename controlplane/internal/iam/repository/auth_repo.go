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
	iamproto "controlplane/internal/iam/transport/rpc/proto"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/proto"
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

func (r *AuthRepository) LoginUserGlobal(ctx context.Context, username string) (*iamEntity.LoginUser, error) {
	// [COMMENT]: Thực hiện truy vấn JOIN bảng users với user_role để lấy thông tin user kèm max role ở platform scope (workspace nil UUID)
	query := fmt.Sprintf(`
		SELECT 
			u.id,
			u.username,
			u.email,
			u.password_hash, 
			u.status,
			COALESCE(ur.role_id::text, '') AS role_id,
			COALESCE(ur.role_level, 99)    AS role_level
		FROM %s.users u
		LEFT JOIN %s.user_role ur ON ur.user_id = u.id 
		                         AND ur.workspace_id = '00000000-0000-0000-0000-000000000000'
		WHERE u.username = $1
		ORDER BY ur.role_level ASC NULLS LAST
		LIMIT 1
	`, r.schema, r.schema)

	var (
		userModel iamModel.User
		roleID    string
		roleLevel int32
	)
	if err := r.db.QueryRow(ctx, query, username).Scan(
		&userModel.ID,
		&userModel.Username,
		&userModel.Email,
		&userModel.PasswordHash,
		&userModel.Status,
		&roleID,
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
		RoleID:       roleID,
		Level:        roleLevel,
	}

	return loginUser, nil
}

func (r *AuthRepository) LoginUserTenant(
	ctx context.Context,
	username string,
	tenantDomain string,
) (*iamEntity.LoginUser, error) {
	// [COMMENT]: Query JOIN hierarchy.tenant_memberships, tenants, tenant_domains
	// và LEFT JOIN sang iam.user_role để lấy max role thuộc tenant đó.
	query := fmt.Sprintf(`
		SELECT
			u.id,
			u.username,
			u.email,
			u.password_hash,
			u.status,
			t.id::text   AS tenant_id,
			COALESCE(ur.role_id::text, '') AS role_id,
			COALESCE(ur.role_level, 99)    AS role_level
		FROM %s.users u
		JOIN hierarchy.tenant_memberships tm ON tm.user_id = u.id AND tm.status = 'active'
		JOIN hierarchy.tenants t             ON t.id = tm.tenant_id
		JOIN hierarchy.tenant_domains td     ON td.tenant_id = t.id AND td.domain = $2
		LEFT JOIN %s.user_role ur            ON ur.user_id = u.id 
		                                    AND ur.workspace_id = '00000000-0000-0000-0000-000000000000'
		WHERE u.username = $1
		ORDER BY ur.role_level ASC NULLS LAST
		LIMIT 1
	`, r.schema, r.schema)

	var (
		userModel iamModel.User
		tenantID  string
		roleID    string
		roleLevel int32
	)
	if err := r.db.QueryRow(ctx, query, username, tenantDomain).Scan(
		&userModel.ID,
		&userModel.Username,
		&userModel.Email,
		&userModel.PasswordHash,
		&userModel.Status,
		&tenantID,
		&roleID,
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
		RoleID:       roleID,
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

// [COMMENT]: ActivateUser thực hiện kích hoạt tài khoản (chuyển trạng thái sang active)
// và gán vai trò tương ứng cho tài khoản trong một transaction nguyên tử để bảo toàn dữ liệu.
func (r *AuthRepository) ActivateUser(ctx context.Context, userID uuid.UUID, roleCode string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("iam repo: begin activate tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	// [COMMENT]: 1. Cập nhật status của user thành active, đồng thời SELECT ra username để build key 5 cấp
	var username string
	var status string
	queryUpdate := fmt.Sprintf(`
		UPDATE %s.users 
		SET status = 'active', updated_at = NOW() 
		WHERE id = $1 AND status = 'pending-active'
		RETURNING username, status
	`, r.schema)

	err = tx.QueryRow(ctx, queryUpdate, userID).Scan(&username, &status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// [COMMENT]: Idempotent check: Kiểm tra nếu user đã active từ trước
			var currentStatus string
			errCheck := r.db.QueryRow(ctx, fmt.Sprintf("SELECT status FROM %s.users WHERE id = $1", r.schema), userID).Scan(&currentStatus)
			if errCheck != nil {
				if errors.Is(errCheck, pgx.ErrNoRows) {
					return iamTaxonomy.ErrUserNotFound
				}
				return errCheck
			}
			if currentStatus == "active" {
				return nil
			}
			return fmt.Errorf("user status is %s, cannot activate", currentStatus)
		}
		return err
	}

	// [COMMENT]: 2. Truy vấn danh sách permissions tĩnh (3 cấp) của role tương ứng từ DB dựa trên roleCode
	queryRolePerms := fmt.Sprintf(`
		SELECT r.id, r.name, r.role_level, COALESCE(p.module, ''), COALESCE(p.object, ''), COALESCE(p.behavior, '')
		FROM %s.roles r
		LEFT JOIN %s.role_permissions rp ON rp.role_id = r.id
		LEFT JOIN %s.permissions p ON rp.permission_id = p.id
		WHERE r.code = $1
	`, r.schema, r.schema, r.schema)

	rows, err := tx.Query(ctx, queryRolePerms, roleCode)
	if err != nil {
		return err
	}
	defer rows.Close()

	var roleID uuid.UUID
	var roleName string
	var roleLevel int
	var perms []string
	roleFound := false

	for rows.Next() {
		roleFound = true
		var mod, obj, beh string
		if err := rows.Scan(&roleID, &roleName, &roleLevel, &mod, &obj, &beh); err != nil {
			return fmt.Errorf("iam repo: scan role permission row: %w", err)
		}
		if mod != "" && obj != "" && beh != "" {
			// [COMMENT]: Ghép thành key 5 cấp định dạng: <username>:<workspace_id>:<module>:<object>:<behavior>
			// WorkspaceID sử dụng nil UUID ("00000000-0000-0000-0000-000000000000") đại diện cho platform scope
			permKey := fmt.Sprintf("%s:00000000-0000-0000-0000-000000000000:%s:%s:%s", username, mod, obj, beh)
			perms = append(perms, permKey)
		}
	}

	if err := rows.Err(); err != nil {
		return err
	}

	// [COMMENT]: Nếu không tìm thấy thông tin role nào từ câu query JOIN ở trên
	if !roleFound {
		return iamTaxonomy.ErrRoleNotFound
	}

	// [COMMENT]: 3. Serialize danh sách permissions thành Protobuf binary byte array
	roleEntry := &iamproto.RoleEntry{
		Permissions: perms,
	}
	binaryBytes, err := proto.Marshal(roleEntry)
	if err != nil {
		return fmt.Errorf("iam repo: marshal role entry: %w", err)
	}

	// [COMMENT]: 4. Chèn mapping user_role mới vào database
	queryInsertRole := fmt.Sprintf(`
		INSERT INTO %s.user_role (
			id, user_id, username, workspace_id, role_id, role_name, role_level, list_perm, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW(), NOW())
		ON CONFLICT (user_id, workspace_id, role_id) DO NOTHING
	`, r.schema)

	_, err = tx.Exec(ctx, queryInsertRole,
		uuid.Must(uuid.NewV7()),
		userID,
		username,
		uuid.Nil, // workspace_id (nil UUID)
		roleID,
		roleName,
		roleLevel,
		binaryBytes,
	)
	if err != nil {
		return fmt.Errorf("iam repo: insert user role assignment: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("iam repo: commit activate tx: %w", err)
	}

	return nil
}
