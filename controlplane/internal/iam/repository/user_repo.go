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
	db              *pgxpool.Pool
	schema          string
	hierarchySchema string
}

func NewUserRepository(
	cfg *config.Config,
	db *pgxpool.Pool,
) iamRepoInterface.UserRepository {
	return &UserRepository{
		db:              db,
		schema:          cfg.SchemaSQL.IAM,
		hierarchySchema: cfg.SchemaSQL.Hierarchy,
	}
}

// ListUsers keeps the selected workspace as a context-integrity fence for this
// platform-global read. The route has already authorized the trusted caller
// level; this repository compares target levels against that exact request gate
// and does not resolve a different caller role from durable assignments.
func (r *UserRepository) ListUsers(ctx context.Context, queryEntity iamEntity.ListUsers) ([]iamEntity.ListUsers, error) {
	sqlQuery := fmt.Sprintf(`
		WITH selected_workspace AS MATERIALIZED (
			SELECT workspace.id
			FROM %s.personal_workspaces workspace
			WHERE workspace.id = $2
			  AND workspace.owner_id = $1
			  AND workspace.zone_id = $3
		),
		targets AS MATERIALIZED (
			SELECT
				u.id,
				u.username,
				u.email,
				u.status,
				ur.role_level,
				ur.role_name,
				EXISTS (
					SELECT 1 FROM %s.mfa_settings ms
					WHERE ms.user_id = u.id
				) AS mfa_enabled,
				(
					SELECT COUNT(*)::integer FROM %s.devices d
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
			CROSS JOIN selected_workspace
			LEFT JOIN %s.user_profiles up ON u.id = up.user_id
			LEFT JOIN LATERAL (
				SELECT last_seen_ip, last_seen_at
				FROM %s.devices
				WHERE user_id = u.id AND revoked_at IS NULL
				ORDER BY last_seen_at DESC NULLS LAST
				LIMIT 1
			) ld ON true
			WHERE ur.role_level > $4
			ORDER BY u.created_at DESC
			LIMIT $5 OFFSET $6
		)
		SELECT
			EXISTS (SELECT 1 FROM selected_workspace) AS workspace_valid,
			targets.id, targets.username, targets.email, targets.status,
			targets.role_level, targets.role_name, targets.mfa_enabled,
			targets.devices_count, targets.bio, targets.fullname,
			targets.last_seen_ip, targets.last_seen_at,
			targets.created_at, targets.updated_at
		FROM (SELECT 1) anchor
		LEFT JOIN targets ON TRUE
		ORDER BY targets.created_at DESC NULLS LAST
	`, r.hierarchySchema, r.schema, r.schema, r.schema, r.schema, r.schema, r.schema)

	rows, err := r.db.Query(
		ctx,
		sqlQuery,
		queryEntity.ActorUserID,
		queryEntity.WorkspaceID,
		queryEntity.ZoneID,
		queryEntity.CallerLevel,
		queryEntity.Limit,
		queryEntity.Offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []iamEntity.ListUsers
	for rows.Next() {
		var workspaceValid bool
		var id *uuid.UUID
		var username, email, status, roleName, bio, fullname, lastSeenIP *string
		var level, devicesCount *int32
		var mfaEnabled *bool
		var lastSeenAt, createdAt, updatedAt *time.Time
		if err := rows.Scan(
			&workspaceValid,
			&id,
			&username,
			&email,
			&status,
			&level,
			&roleName,
			&mfaEnabled,
			&devicesCount,
			&bio,
			&fullname,
			&lastSeenIP,
			&lastSeenAt,
			&createdAt,
			&updatedAt,
		); err != nil {
			return nil, err
		}
		if !workspaceValid {
			return nil, iamTaxonomy.ErrActionNotAllowed
		}
		if id == nil {
			continue
		}
		users = append(users, iamEntity.ListUsers{
			ID:           *id,
			Username:     *username,
			Email:        *email,
			Status:       *status,
			RoleLevel:    *level,
			RoleName:     *roleName,
			MFAEnabled:   *mfaEnabled,
			DevicesCount: *devicesCount,
			Bio:          *bio,
			Fullname:     *fullname,
			LastSeenIP:   *lastSeenIP,
			LastSeenAt:   lastSeenAt,
			CreatedAt:    *createdAt,
			UpdatedAt:    *updatedAt,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return users, nil
}

// [COMMENT]: UpdateUserStatus thực hiện cập nhật trạng thái hoạt động (status) của user dưới DB nếu đủ phân cấp dùng 1 query CTE để tối ưu và tránh race condition
func (r *UserRepository) UpdateUserStatus(ctx context.Context, workflow iamEntity.UpdateUserStatus) error {
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
	err := r.db.QueryRow(
		ctx,
		query,
		workflow.Status,
		workflow.TargetUserID,
		workflow.CallerLevel,
	).Scan(&userExists, &updateSuccess)
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

// [COMMENT]: GetMyProfile lấy profile qua entity riêng của self-profile workflow.
func (r *UserRepository) GetMyProfile(ctx context.Context, workflow *iamEntity.GetMyProfile) error {
	query := fmt.Sprintf(`
		SELECT u.id, u.username, u.email, u.phone,
		       p.fullname, p.address, p.avatar_url, p.bio, p.locale, p.timezone,
		       p.created_at, p.updated_at
		FROM %s.users u
		JOIN %s.user_profiles p ON p.user_id = u.id
		WHERE u.id = $1
	`, r.schema, r.schema)

	err := r.db.QueryRow(ctx, query, workflow.UserID).Scan(
		&workflow.UserID,
		&workflow.Username,
		&workflow.AccountEmail,
		&workflow.Phone,
		&workflow.Fullname,
		&workflow.Address,
		&workflow.AvatarURL,
		&workflow.Bio,
		&workflow.Locale,
		&workflow.Timezone,
		&workflow.CreatedAt,
		&workflow.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return iamTaxonomy.ErrUserNotFound
		}
		return err
	}
	return nil
}

func (r *UserRepository) UpdateMyProfile(ctx context.Context, workflow *iamEntity.UpdateMyProfile) error {
	query := fmt.Sprintf(`
		WITH target AS MATERIALIZED (
			SELECT u.id
			FROM %s.users u
			JOIN %s.user_profiles p ON p.user_id = u.id
			WHERE u.id = $1
			FOR UPDATE OF u, p
		),
		updated_user AS (
			UPDATE %s.users
			SET phone = NULLIF($2, ''), updated_at = NOW()
			WHERE id = (SELECT id FROM target)
			RETURNING id, username, email, phone
		),
		updated_profile AS (
			UPDATE %s.user_profiles p
			SET fullname = $3,
			    address = NULLIF($4, ''),
			    avatar_url = NULLIF($5, ''),
			    bio = NULLIF($6, ''),
			    locale = $7,
			    timezone = $8,
			    updated_at = NOW()
			FROM updated_user u
			WHERE p.user_id = u.id
			RETURNING p.user_id, p.fullname, p.address, p.avatar_url, p.bio,
			          p.locale, p.timezone, p.created_at, p.updated_at
		)
		SELECT p.user_id, u.username, u.email, u.phone,
		       p.fullname, p.address, p.avatar_url, p.bio, p.locale, p.timezone,
		       p.created_at, p.updated_at
		FROM updated_profile p
		JOIN updated_user u ON u.id = p.user_id
	`, r.schema, r.schema, r.schema, r.schema)

	err := r.db.QueryRow(
		ctx,
		query,
		workflow.UserID,
		valueOrEmpty(workflow.Phone),
		workflow.Fullname,
		valueOrEmpty(workflow.Address),
		valueOrEmpty(workflow.AvatarURL),
		valueOrEmpty(workflow.Bio),
		workflow.Locale,
		workflow.Timezone,
	).Scan(
		&workflow.UserID,
		&workflow.Username,
		&workflow.AccountEmail,
		&workflow.Phone,
		&workflow.Fullname,
		&workflow.Address,
		&workflow.AvatarURL,
		&workflow.Bio,
		&workflow.Locale,
		&workflow.Timezone,
		&workflow.CreatedAt,
		&workflow.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return iamTaxonomy.ErrUserNotFound
		}
		return fmt.Errorf("iam repo: update self profile: %w", err)
	}
	return nil
}

func (r *UserRepository) GetMySocialLinks(
	ctx context.Context,
	workflow *iamEntity.GetMySocialLinks,
) ([]iamEntity.GetMySocialLinks, error) {
	var (
		userExists        bool
		googleProvider    *string
		googleEmail       *string
		googleVerifiedAt  *time.Time
		googleLastLoginAt *time.Time
		googleLinkedAt    *time.Time
		githubProvider    *string
		githubEmail       *string
		githubVerifiedAt  *time.Time
		githubLastLoginAt *time.Time
		githubLinkedAt    *time.Time
	)
	query := fmt.Sprintf(`
		SELECT
			(u.id IS NOT NULL),
			g.provider, g.provider_email, g.email_verified_at, g.last_login_at, g.linked_at,
			h.provider, h.provider_email, h.email_verified_at, h.last_login_at, h.linked_at
		FROM (SELECT id FROM %s.users WHERE id = $1) u
		LEFT JOIN LATERAL (
			SELECT provider, provider_email, email_verified_at, last_login_at, linked_at
			FROM %s.external_identities
			WHERE user_id = u.id AND provider = 'google'
			LIMIT 1
		) g ON true
		LEFT JOIN LATERAL (
			SELECT provider, provider_email, email_verified_at, last_login_at, linked_at
			FROM %s.external_identities
			WHERE user_id = u.id AND provider = 'github'
			LIMIT 1
		) h ON true
	`, r.schema, r.schema, r.schema)
	err := r.db.QueryRow(ctx, query, workflow.UserID).Scan(
		&userExists,
		&googleProvider,
		&googleEmail,
		&googleVerifiedAt,
		&googleLastLoginAt,
		&googleLinkedAt,
		&githubProvider,
		&githubEmail,
		&githubVerifiedAt,
		&githubLastLoginAt,
		&githubLinkedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, iamTaxonomy.ErrUserNotFound
		}
		return nil, fmt.Errorf("iam repo: get self social links: %w", err)
	}
	if !userExists {
		return nil, iamTaxonomy.ErrUserNotFound
	}

	summary := func(
		provider *string,
		email *string,
		verifiedAt, lastLoginAt, linkedAt *time.Time,
		expected string,
	) iamEntity.GetMySocialLinks {
		result := iamEntity.GetMySocialLinks{
			UserID:   workflow.UserID,
			Provider: expected,
			State:    "not_linked",
		}
		if provider == nil {
			return result
		}
		result.ProviderEmail = valueOrEmpty(email)
		result.EmailVerifiedAt = verifiedAt
		result.LastLoginAt = lastLoginAt
		result.LinkedAt = linkedAt
		result.State = "linked"
		return result
	}
	links := []iamEntity.GetMySocialLinks{
		summary(
			googleProvider,
			googleEmail,
			googleVerifiedAt,
			googleLastLoginAt,
			googleLinkedAt,
			"google",
		),
		summary(
			githubProvider,
			githubEmail,
			githubVerifiedAt,
			githubLastLoginAt,
			githubLinkedAt,
			"github",
		),
	}
	return links, nil
}

func (r *UserRepository) LinkExternalIdentity(
	ctx context.Context,
	workflow iamEntity.LinkExternalIdentity,
) error {
	query := fmt.Sprintf(`
		WITH target_user AS MATERIALIZED (
			SELECT id
			FROM %s.users
			WHERE id = $1
			  AND status = 'active'
			  AND password_hash IS NOT NULL
			FOR UPDATE
		),
		subject_identity AS MATERIALIZED (
			SELECT id, user_id
			FROM %s.external_identities
			WHERE provider = $2 AND provider_subject = $3
			FOR UPDATE
		),
		active_provider AS MATERIALIZED (
			SELECT id
			FROM %s.external_identities
			WHERE user_id = $1 AND provider = $2
			FOR UPDATE
		),
		updated_identity AS (
			UPDATE %s.external_identities e
			SET provider_email = $4,
			    email_verified_at = $5,
			    display_name = $6,
			    avatar_url = NULLIF($7, ''),
			    linked_at = NOW(),
			    updated_at = NOW()
			WHERE e.id = (SELECT id FROM subject_identity)
			  AND e.user_id = $1
			  AND EXISTS (SELECT 1 FROM target_user)
			  AND NOT EXISTS (
				  SELECT 1 FROM active_provider active WHERE active.id <> e.id
			  )
			RETURNING e.id
		),
		inserted_identity AS (
			INSERT INTO %s.external_identities (
				id, user_id, provider, provider_subject, provider_email,
				email_verified_at, display_name, avatar_url, linked_at,
				created_at, updated_at
			)
			SELECT gen_random_uuid(), id, $2, $3, $4, $5, $6,
			       NULLIF($7, ''), NOW(), NOW(), NOW()
			FROM target_user
			WHERE NOT EXISTS (SELECT 1 FROM subject_identity)
			  AND NOT EXISTS (SELECT 1 FROM active_provider)
			ON CONFLICT DO NOTHING
			RETURNING id
		)
		SELECT
			EXISTS (SELECT 1 FROM target_user),
			COALESCE((SELECT user_id::text FROM subject_identity), ''),
			(SELECT COUNT(*) FROM active_provider),
			(SELECT COUNT(*) FROM updated_identity) + (SELECT COUNT(*) FROM inserted_identity)
	`, r.schema, r.schema, r.schema, r.schema, r.schema)

	var (
		userActive       bool
		subjectOwner     string
		activeProvider   int
		identitiesLinked int
	)
	err := r.db.QueryRow(
		ctx,
		query,
		workflow.UserID,
		workflow.Provider,
		workflow.ProviderSubject,
		workflow.ProviderEmail,
		workflow.EmailVerifiedAt,
		workflow.DisplayName,
		valueOrEmpty(workflow.AvatarURL),
	).Scan(&userActive, &subjectOwner, &activeProvider, &identitiesLinked)
	if err != nil {
		return fmt.Errorf("iam repo: link external identity: %w", err)
	}
	if !userActive {
		return iamTaxonomy.ErrInvalidCredentials
	}
	if subjectOwner != "" && subjectOwner != workflow.UserID.String() {
		return iamTaxonomy.ErrExternalIdentityConflict
	}
	if identitiesLinked == 0 && activeProvider > 0 {
		return iamTaxonomy.ErrSocialProviderAlreadyLinked
	}
	if identitiesLinked == 0 {
		// A concurrent insert can win after this statement snapshot was taken;
		// the desired provider slot is then no longer available.
		return iamTaxonomy.ErrSocialProviderAlreadyLinked
	}
	return nil
}

func (r *UserRepository) UnlinkMySocialLink(
	ctx context.Context,
	workflow iamEntity.UnlinkMySocialLink,
) error {
	query := fmt.Sprintf(`
		WITH target_user AS (
			SELECT id FROM %s.users WHERE id = $1
		),
		unlinked AS (
			DELETE FROM %s.external_identities
			WHERE user_id = $1 AND provider = $2
			  AND EXISTS (SELECT 1 FROM target_user)
			RETURNING id
		)
		SELECT EXISTS (SELECT 1 FROM target_user), (SELECT COUNT(*) FROM unlinked)
	`, r.schema, r.schema)
	var (
		userExists bool
		affected   int
	)
	if err := r.db.QueryRow(ctx, query, workflow.UserID, workflow.Provider).Scan(&userExists, &affected); err != nil {
		return fmt.Errorf("iam repo: unlink self social identity: %w", err)
	}
	if !userExists {
		return iamTaxonomy.ErrUserNotFound
	}
	// Desired-state DELETE is idempotent. A lost success response can be
	// retried with a fresh critical proof without changing another identity.
	return nil
}

func (r *UserRepository) GetUserAuthMethods(
	ctx context.Context,
	queryEntity iamEntity.GetUserAuthMethods,
) ([]iamEntity.GetUserAuthMethods, error) {
	var (
		userExists        bool
		allowed           bool
		accountEmail      string
		passwordSet       bool
		googleProvider    *string
		googleEmail       *string
		googleVerifiedAt  *time.Time
		googleLastLoginAt *time.Time
		googleLinkedAt    *time.Time
		githubProvider    *string
		githubEmail       *string
		githubVerifiedAt  *time.Time
		githubLastLoginAt *time.Time
		githubLinkedAt    *time.Time
	)
	query := fmt.Sprintf(`
		WITH effective_role AS (
			SELECT user_id, MIN(role_level) AS role_level
			FROM %s.user_role
			WHERE workspace_id = '00000000-0000-0000-0000-000000000000'
			GROUP BY user_id
		)
		SELECT
			(u.id IS NOT NULL),
			(er.user_id IS NOT NULL AND er.role_level > $1),
			u.email,
			(u.password_hash IS NOT NULL),
			g.provider, g.provider_email, g.email_verified_at, g.last_login_at, g.linked_at,
			h.provider, h.provider_email, h.email_verified_at, h.last_login_at, h.linked_at
		FROM %s.users u
		LEFT JOIN effective_role er ON er.user_id = u.id
		LEFT JOIN LATERAL (
			SELECT provider, provider_email, email_verified_at, last_login_at, linked_at
			FROM %s.external_identities
			WHERE user_id = u.id AND provider = 'google'
			LIMIT 1
		) g ON true
		LEFT JOIN LATERAL (
			SELECT provider, provider_email, email_verified_at, last_login_at, linked_at
			FROM %s.external_identities
			WHERE user_id = u.id AND provider = 'github'
			LIMIT 1
		) h ON true
		WHERE u.id = $2
	`, r.schema, r.schema, r.schema, r.schema)
	err := r.db.QueryRow(ctx, query, queryEntity.CallerLevel, queryEntity.UserID).Scan(
		&userExists,
		&allowed,
		&accountEmail,
		&passwordSet,
		&googleProvider,
		&googleEmail,
		&googleVerifiedAt,
		&googleLastLoginAt,
		&googleLinkedAt,
		&githubProvider,
		&githubEmail,
		&githubVerifiedAt,
		&githubLastLoginAt,
		&githubLinkedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, iamTaxonomy.ErrUserNotFound
		}
		return nil, fmt.Errorf("iam repo: get user auth methods: %w", err)
	}
	if !userExists {
		return nil, iamTaxonomy.ErrUserNotFound
	}
	if !allowed {
		return nil, iamTaxonomy.ErrActionNotAllowed
	}

	summary := func(provider *string, email *string, verifiedAt, lastLoginAt, linkedAt *time.Time, expected string) iamEntity.GetUserAuthMethods {
		result := iamEntity.GetUserAuthMethods{
			CallerLevel:  queryEntity.CallerLevel,
			UserID:       queryEntity.UserID,
			AccountEmail: accountEmail,
			PasswordSet:  passwordSet,
			Provider:     expected,
			State:        "not_linked",
		}
		if provider == nil {
			return result
		}
		result.ProviderEmail = valueOrEmpty(email)
		result.EmailVerifiedAt = verifiedAt
		result.LastLoginAt = lastLoginAt
		result.LinkedAt = linkedAt
		result.State = "linked"
		return result
	}
	return []iamEntity.GetUserAuthMethods{
		summary(googleProvider, googleEmail, googleVerifiedAt, googleLastLoginAt, googleLinkedAt, "google"),
		summary(githubProvider, githubEmail, githubVerifiedAt, githubLastLoginAt, githubLinkedAt, "github"),
	}, nil
}

// [COMMENT]: ResetUserPassword cập nhật mật khẩu mới của user dưới DB và ghi nhận mật khẩu cũ vào password_history qua CTE duy nhất bảo vệ phân cấp quyền lực
func (r *UserRepository) ResetUserPassword(ctx context.Context, workflow iamEntity.ResetUserPassword) error {
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
	err := r.db.QueryRow(
		ctx,
		query,
		workflow.PasswordHash,
		workflow.TargetUserID,
		workflow.CallerLevel,
	).Scan(&userExists, &updateSuccess)
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
