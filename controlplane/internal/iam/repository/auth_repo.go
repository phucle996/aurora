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
	iamTaxonomy "controlplane/internal/iam/taxonomy"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type AuthRepository struct {
	db              *pgxpool.Pool
	schema          string
	hierarchySchema string
}

func NewAuthRepository(
	cfg *config.Config,
	db *pgxpool.Pool,
) iamRepoInterface.AuthRepository {
	return &AuthRepository{
		db:              db,
		schema:          cfg.SchemaSQL.IAM,
		hierarchySchema: cfg.SchemaSQL.Hierarchy,
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
			COALESCE(ur.role_level, 99)    AS role_level
		FROM %s.users u
		LEFT JOIN %s.user_role ur ON ur.user_id = u.id 
		                         AND ur.workspace_id = '00000000-0000-0000-0000-000000000000'
		WHERE u.username = $1
		ORDER BY ur.role_level ASC NULLS LAST
		LIMIT 1
	`, r.schema, r.schema)

	var (
		id           uuid.UUID
		uname        string
		email        string
		passwordHash string
		status       string
		roleLevel    int32
	)
	if err := r.db.QueryRow(ctx, query, username).Scan(
		&id,
		&uname,
		&email,
		&passwordHash,
		&status,
		&roleLevel,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, iamTaxonomy.ErrUserNotFound
		}
		return nil, fmt.Errorf("iam repo: get login user by username: %w", err)
	}

	loginUser := &iamEntity.LoginUser{
		ID:           id,
		Username:     uname,
		Email:        email,
		PasswordHash: passwordHash,
		Status:       iamEntity.UserStatus(status),
		Level:        roleLevel,
	}

	return loginUser, nil
}

func (r *AuthRepository) LoginUserTenant(
	ctx context.Context,
	username string,
	tenantDomain string,
) (*iamEntity.LoginUser, error) {
	// [COMMENT]: Tenant login resolves the actor's durable membership binding;
	// a platform user_role must never be reused as tenant authority.
	query := fmt.Sprintf(`
		SELECT
			u.id,
			u.username,
			u.email,
			u.password_hash,
			u.status,
			t.id::text   AS tenant_id,
			t.code       AS tenant_code,
			revision.role_level
		FROM %s.users u
		JOIN %s.tenant_memberships tm ON tm.user_id = u.id AND tm.status = 'active'
		JOIN %s.tenants t             ON t.id = tm.tenant_id AND t.status='active'
		JOIN %s.tenant_domains td     ON td.tenant_id = t.id AND lower(td.domain) = lower($2)
		JOIN %s.membership_role mr            ON mr.membership_id=tm.id
		                                    AND mr.workspace_id='00000000-0000-0000-0000-000000000000'
		JOIN %s.tenant_role_revisions revision ON revision.id=mr.tenant_role_revision_id
		WHERE u.username = $1
		ORDER BY revision.role_level ASC
		LIMIT 1
	`, r.schema, r.hierarchySchema, r.hierarchySchema, r.hierarchySchema, r.schema, r.schema)

	var (
		id           uuid.UUID
		uname        string
		email        string
		passwordHash string
		status       string
		tenantID     string
		tenantCode   string
		roleLevel    int32
	)
	if err := r.db.QueryRow(ctx, query, username, tenantDomain).Scan(
		&id,
		&uname,
		&email,
		&passwordHash,
		&status,
		&tenantID,
		&tenantCode,
		&roleLevel,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, iamTaxonomy.ErrUserNotFound
		}
		return nil, fmt.Errorf("iam repo: login user by username and tenant domain: %w", err)
	}

	loginUser := &iamEntity.LoginUser{
		ID:           id,
		Username:     uname,
		Email:        email,
		PasswordHash: passwordHash,
		Status:       iamEntity.UserStatus(status),
		TenantID:     &tenantID,
		TenantCode:   &tenantCode,
		Level:        roleLevel,
	}

	return loginUser, nil
}

func (r *AuthRepository) CreateRegisteredUser(ctx context.Context, record *iamEntity.RegisterAccount) error {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("iam repo: begin register tx: %w", err)
	}
	defer tx.Rollback(ctx)

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
		record.ID,
		record.Username,
		record.Email,
		record.Phone,
		record.PasswordHash,
		string(record.Status),
		record.CreatedAt,
		record.UpdatedAt,
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
		record.ID,
		record.Fullname,
		nil,
		nil,
		record.Locale,
		record.Timezone,
		record.CreatedAt,
		record.UpdatedAt,
	); err != nil {
		return fmt.Errorf("iam repo: insert user profile: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("iam repo: commit register tx: %w", err)
	}
	return nil
}

// ActivateUser commits account activation and per-active-Zone personal workspace
// bootstrap in one transaction. It accepts separate workflow commands so IAM
// activation does not absorb hierarchy placement fields into its own entity.
func (r *AuthRepository) ActivateUser(
	ctx context.Context,
	activation iamEntity.AccountActivation,
	workspaces iamEntity.BootstrapPersonalWorkspaces,
) error {
	if activation.UserID == uuid.Nil || workspaces.OwnerID != activation.UserID {
		return iamTaxonomy.ErrInvalidArgument
	}

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
	if err = tx.QueryRow(ctx, lockUserQuery, activation.UserID).Scan(&username, &status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return iamTaxonomy.ErrUserNotFound
		}
		return fmt.Errorf("iam repo: lock user for activation: %w", err)
	}
	if status != "pending-active" && status != "active" {
		return fmt.Errorf("user status is %s, cannot activate", status)
	}
	// [COMMENT]: Retry của active user cũng tải role chuẩn và INSERT ON CONFLICT để self-heal dữ liệu legacy thiếu role.
	queryRole := fmt.Sprintf(`
		SELECT id, name, role_level, version
		FROM %s.platform_roles
		WHERE code = $1
	`, r.schema)

	var roleID uuid.UUID
	var roleName string
	var roleLevel int
	var roleVersion int64
	if err := tx.QueryRow(ctx, queryRole, activation.RoleCode).Scan(&roleID, &roleName, &roleLevel, &roleVersion); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return iamTaxonomy.ErrRoleNotFound
		}
		return fmt.Errorf("iam repo: query platform role: %w", err)
	}

	// [COMMENT]: 3. Chèn mapping user_role mới vào database
	queryInsertRole := fmt.Sprintf(`
		INSERT INTO %s.user_role (
			id, user_id, username, workspace_id, role_id, role_name, role_level, role_version, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW(), NOW())
		ON CONFLICT (user_id, workspace_id, role_id) DO NOTHING
	`, r.schema)

	_, err = tx.Exec(ctx, queryInsertRole,
		uuid.Must(uuid.NewV7()),
		activation.UserID,
		username,
		uuid.Nil, // workspace_id (nil UUID)
		roleID,
		roleName,
		roleLevel,
		roleVersion,
	)
	if err != nil {
		return fmt.Errorf("iam repo: insert user role assignment: %w", err)
	}

	// One CTE activates the pending row, snapshots every active Zone under a
	// shared lock, fans out a deterministic owner-and-Zone code, and writes the
	// wallet-provision command. Retried activation only inserts rows for newly active Zones
	// because the owner/code conflict is idempotent.
	var activeZoneCount int
	err = tx.QueryRow(ctx, fmt.Sprintf(`
		WITH activated_user AS MATERIALIZED (
			UPDATE %s.users
			SET status = 'active', updated_at = NOW()
			WHERE id = $4 AND status = 'pending-active'
			RETURNING id
		), active_zones AS MATERIALIZED (
			SELECT id
			FROM %s.zones
			WHERE status = 'active'
			FOR SHARE
		), seeded_workspaces AS (
			INSERT INTO %s.personal_workspaces
				(name, code, description, zone_id, owner_id, created_at, updated_at)
			SELECT $1, $2 || '-' || zone.id::text, $3, zone.id, $4, $5, $6
			FROM active_zones AS zone
			ON CONFLICT (owner_id, code) DO NOTHING
			RETURNING id
		), lifecycle_fact_outbox AS (
			INSERT INTO %s.lifecycle_fact_outbox_records
				(event_id, event_type, schema_version, aggregate_type, aggregate_id, aggregate_version,
				 owner_id, owner_type, actor_user_id, payload, occurred_at)
			VALUES ($7, 'billing.personal_wallet.provision.requested.v1', 1, 'IAM_USER', $4, 1,
			        $4, 'PERSONAL', $4, $8, NOW())
			ON CONFLICT (event_id) DO NOTHING
			RETURNING id
		)
		SELECT COUNT(*)::int FROM active_zones
	`, r.schema, r.hierarchySchema, r.hierarchySchema, r.schema),
		workspaces.Name,
		workspaces.CodePrefix,
		workspaces.Description,
		workspaces.OwnerID,
		workspaces.CreatedAt,
		workspaces.UpdatedAt,
		activation.LifecycleEventID,
		activation.LifecycleEventPayload,
	).Scan(&activeZoneCount)
	if err != nil {
		return fmt.Errorf("iam repo: seed activation workspaces and outbox: %w", err)
	}
	if activeZoneCount == 0 {
		return iamTaxonomy.ErrBootstrapZoneUnavailable
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
		       created_at, updated_at
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
	var username, email, userPasswordHash, status string
	if identityFound {
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

	var roleLevel int32
	if err := tx.QueryRow(ctx, fmt.Sprintf(`
		SELECT COALESCE(ur.role_level, 99)
		FROM %s.user_role ur
		WHERE ur.user_id = $1
		  AND ur.workspace_id = '00000000-0000-0000-0000-000000000000'
		ORDER BY ur.role_level ASC
		LIMIT 1
	`, r.schema), userID).Scan(&roleLevel); err != nil {
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
