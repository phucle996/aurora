package iamRepoImpl

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

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

func (r *AuthRepository) insertPlatformRoleTx(
	ctx context.Context,
	tx pgx.Tx,
	userID uuid.UUID,
	username string,
) (uuid.UUID, int32, error) {
	query := fmt.Sprintf(`
		SELECT r.id, r.name, r.role_level,
		       COALESCE(p.module, ''), COALESCE(p.object, ''), COALESCE(p.behavior, '')
		FROM %s.roles r
		LEFT JOIN %s.role_permissions rp ON rp.role_id = r.id
		LEFT JOIN %s.permissions p ON rp.permission_id = p.id
		WHERE r.code = 'platform_user'
	`, r.schema, r.schema, r.schema)
	rows, err := tx.Query(ctx, query)
	if err != nil {
		return uuid.Nil, 0, fmt.Errorf("iam repo: query platform user role: %w", err)
	}
	defer rows.Close()

	var roleID uuid.UUID
	var roleName string
	var roleLevel int32
	var perms []string
	found := false
	for rows.Next() {
		found = true
		var module, object, behavior string
		if err := rows.Scan(&roleID, &roleName, &roleLevel, &module, &object, &behavior); err != nil {
			return uuid.Nil, 0, fmt.Errorf("iam repo: scan platform user role: %w", err)
		}
		if module != "" && object != "" && behavior != "" {
			perms = append(perms, fmt.Sprintf(
				"%s:00000000-0000-0000-0000-000000000000:%s:%s:%s",
				username, module, object, behavior,
			))
		}
	}
	if err := rows.Err(); err != nil {
		return uuid.Nil, 0, fmt.Errorf("iam repo: iterate platform user role: %w", err)
	}
	if !found {
		return uuid.Nil, 0, iamTaxonomy.ErrRoleNotFound
	}

	rolePayload, err := proto.Marshal(&iamproto.RoleEntry{Permissions: perms})
	if err != nil {
		return uuid.Nil, 0, fmt.Errorf("iam repo: marshal platform user role: %w", err)
	}
	_, err = tx.Exec(ctx, fmt.Sprintf(`
		INSERT INTO %s.user_role (
			id, user_id, username, workspace_id, role_id, role_name, role_level, list_perm, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW(), NOW())
		ON CONFLICT (user_id, workspace_id, role_id) DO NOTHING
	`, r.schema),
		uuid.Must(uuid.NewV7()), userID, username, uuid.Nil, roleID, roleName, roleLevel, rolePayload,
	)
	if err != nil {
		return uuid.Nil, 0, fmt.Errorf("iam repo: insert external user role: %w", err)
	}
	return roleID, roleLevel, nil
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

	passwordHash := userModel.PasswordHash
	loginUser := &iamEntity.LoginUser{
		ID:           userModel.ID,
		Username:     userModel.Username,
		Email:        userModel.Email,
		PasswordHash: &passwordHash,
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

	passwordHash := userModel.PasswordHash
	loginUser := &iamEntity.LoginUser{
		ID:           userModel.ID,
		Username:     userModel.Username,
		Email:        userModel.Email,
		PasswordHash: &passwordHash,
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

func (r *AuthRepository) IsUserActive(ctx context.Context, userID uuid.UUID) (bool, error) {
	var active bool
	query := fmt.Sprintf(`SELECT status = 'active' FROM %s.users WHERE id = $1`, r.schema)
	if err := r.db.QueryRow(ctx, query, userID).Scan(&active); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, iamTaxonomy.ErrUserNotFound
		}
		return false, fmt.Errorf("iam repo: read user activation state: %w", err)
	}
	return active, nil
}

// [COMMENT]: ActivateUser thực hiện kích hoạt tài khoản (chuyển trạng thái sang active)
// và gán vai trò tương ứng cho tài khoản trong một transaction nguyên tử để bảo toàn dữ liệu.
func (r *AuthRepository) ActivateUser(ctx context.Context, userID uuid.UUID, roleCode string, eventID uuid.UUID, eventPayload []byte) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("iam repo: begin activate tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	// [COMMENT]: Khóa user row trước để concurrent verify nối đuôi nhau; cả pending và active retry
	// đều đi qua cùng invariant role + Billing event thay vì nhánh active chỉ repair một nửa.
	var username string
	var status string
	lockUserQuery := fmt.Sprintf(`SELECT username, status FROM %s.users WHERE id = $1 FOR UPDATE`, r.schema)
	if err = tx.QueryRow(ctx, lockUserQuery, userID).Scan(&username, &status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return iamTaxonomy.ErrUserNotFound
		}
		return fmt.Errorf("iam repo: lock user for activation: %w", err)
	}
	if status != "pending-active" && status != "active" {
		return fmt.Errorf("user status is %s, cannot activate", status)
	}
	if status == "pending-active" {
		if _, err = tx.Exec(ctx, fmt.Sprintf(`
			UPDATE %s.users SET status = 'active', updated_at = NOW()
			WHERE id = $1 AND status = 'pending-active'
		`, r.schema), userID); err != nil {
			return fmt.Errorf("iam repo: activate user: %w", err)
		}
	}

	// [COMMENT]: Retry của active user cũng tải role chuẩn và INSERT ON CONFLICT để self-heal dữ liệu legacy thiếu role.
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

	// [COMMENT]: Activation, role và event cùng commit; không tồn tại cửa sổ DB commit nhưng event bị mất.
	_, err = tx.Exec(ctx, fmt.Sprintf(`
		INSERT INTO %s.billing_outbox_records
			(event_id, event_type, schema_version, aggregate_type, aggregate_id, aggregate_version,
			 owner_id, owner_type, actor_user_id, payload, occurred_at)
		VALUES ($1, 'billing.wallet.personal.provision.requested.v1', 1, 'IAM_USER', $2, 1,
		        $2, 'PERSONAL', $2, $3, NOW())
		ON CONFLICT (event_id) DO NOTHING
	`, r.schema), eventID, userID, eventPayload)
	if err != nil {
		return fmt.Errorf("iam repo: insert account verified outbox: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("iam repo: commit activate tx: %w", err)
	}

	return nil
}

// VerifyExternalIdentity logs in an already-linked identity. Missing and
// revoked identities deliberately share the invalid-credentials result: login
// never creates or links an account and provider email is metadata only.
func (r *AuthRepository) VerifyExternalIdentity(
	ctx context.Context,
	req iamEntity.ExternalLoginRequest,
) (*iamEntity.ExternalIdentity, *iamEntity.LoginUser, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("iam repo: begin external login tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	now := time.Now().UTC()
	var identity iamEntity.ExternalIdentity
	var identityFound bool
	identityQuery := fmt.Sprintf(`
		SELECT id, user_id, provider, provider_subject, provider_email,
		       email_verified_at, display_name, avatar_url, last_login_at,
		       revoked_at, created_at, updated_at
		FROM %s.external_identities
		WHERE provider = $1 AND provider_subject = $2
		FOR UPDATE
	`, r.schema)
	var provider string
	var avatarURL *string
	var displayName string
	if err := tx.QueryRow(ctx, identityQuery, string(req.Identity.Provider), req.Identity.Subject).Scan(
		&identity.ID,
		&identity.UserID,
		&provider,
		&identity.ProviderSubject,
		&identity.ProviderEmail,
		&identity.EmailVerifiedAt,
		&displayName,
		&avatarURL,
		&identity.LastLoginAt,
		&identity.RevokedAt,
		&identity.CreatedAt,
		&identity.UpdatedAt,
	); err == nil {
		identityFound = true
		identity.Provider = iamEntity.ExternalProvider(provider)
		identity.DisplayName = displayName
		identity.AvatarURL = avatarURL
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, fmt.Errorf("iam repo: load external identity: %w", err)
	}

	var userID uuid.UUID
	var username, email, status string
	var userPasswordHash *string
	if identityFound {
		// Revocation can only be reversed by a future authenticated link flow; a
		// provider callback must never silently reactivate a detached identity.
		if identity.RevokedAt != nil {
			return nil, nil, iamTaxonomy.ErrInvalidCredentials
		}
		userID = identity.UserID
		if err := tx.QueryRow(ctx, fmt.Sprintf(`
			SELECT id, username, email, password_hash, status
			FROM %s.users
			WHERE id = $1
			FOR UPDATE
		`, r.schema), userID).Scan(&userID, &username, &email, &userPasswordHash, &status); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, nil, iamTaxonomy.ErrUserNotFound
			}
			return nil, nil, fmt.Errorf("iam repo: lock external identity user: %w", err)
		}
		if status != string(iamEntity.UserStatusActive) {
			return nil, nil, iamTaxonomy.ErrInvalidCredentials
		}
		_, err = tx.Exec(ctx, fmt.Sprintf(`
			UPDATE %s.external_identities
			SET provider_email = $1, email_verified_at = $2, display_name = $3,
			    avatar_url = NULLIF($4, ''), last_login_at = $5,
			    updated_at = $5
			WHERE id = $6
		`, r.schema),
			req.Identity.Email, req.Identity.EmailVerifiedAt, req.Identity.DisplayName,
			valueOrEmpty(req.Identity.AvatarURL), now, identity.ID,
		)
		if err != nil {
			return nil, nil, fmt.Errorf("iam repo: update external identity snapshot: %w", err)
		}
	} else {
		// Never auto-link by provider email: that mutable claim is not proof that
		// the browser controls an existing Aurora account.
		return nil, nil, iamTaxonomy.ErrInvalidCredentials
	}

	var roleID string
	var roleLevel int32
	if err := tx.QueryRow(ctx, fmt.Sprintf(`
		SELECT COALESCE(ur.role_id::text, ''), COALESCE(ur.role_level, 99)
		FROM %s.user_role ur
		WHERE ur.user_id = $1
		  AND ur.workspace_id = '00000000-0000-0000-0000-000000000000'
		ORDER BY ur.role_level ASC
		LIMIT 1
	`, r.schema), userID).Scan(&roleID, &roleLevel); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, iamTaxonomy.ErrRoleRequired
		}
		return nil, nil, fmt.Errorf("iam repo: load external login role: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, fmt.Errorf("iam repo: commit external login tx: %w", err)
	}

	loginUser := &iamEntity.LoginUser{
		ID:           userID,
		Username:     username,
		Email:        email,
		PasswordHash: userPasswordHash,
		Status:       iamEntity.UserStatus(status),
		RoleID:       roleID,
		Level:        roleLevel,
	}
	return &identity, loginUser, nil
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
