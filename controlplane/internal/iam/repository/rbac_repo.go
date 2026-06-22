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

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type RbacRepository struct {
	cfg                               *config.Config
	db                                *pgxpool.Pool
	schema                            string
	getRoleByCodeQuery                string
	listRolesQuery                    string
	getRoleByIDQuery                  string
	createRoleQuery                   string
	updateRoleQuery                   string
	deleteRolePermissionsQuery        string
	deleteUserRoleAssignsQuery        string
	deleteRoleQuery                   string
	listPermissionsQuery              string
	getPermissionByIDQuery            string
	getPermissionByCodeQuery          string
	assignPermissionQuery             string
	revokePermissionQuery             string
	assignUserRoleQuery               string
	revokeUserRoleQuery               string
	permissionCodesForRoleQuery       string
	getUserMaxRoleLevelQuery          string
	getPermissionCodesByRoleCodeQuery string
	listSystemRoleEntriesQuery        string
	getUserRoleAndLevelByScopeQuery   string
	getUserFallbackRoleAndLevelQuery  string
}

func NewRbacRepository(cfg *config.Config, db *pgxpool.Pool) iamRepoInterface.RbacRepository {
	schema := cfg.SchemaSQL.IAM
	return &RbacRepository{
		cfg:    cfg,
		db:     db,
		schema: schema,
		getRoleByCodeQuery: fmt.Sprintf(
			`SELECT id, code, name, description, scope_type, role_level, is_system, is_protected, is_assignable, owner_tenant_id, created_at, updated_at FROM %s.roles WHERE code = $1`,
			schema,
		),
		listRolesQuery: fmt.Sprintf(
			`SELECT id, code, name, description, scope_type, role_level, is_system, is_protected, is_assignable, owner_tenant_id, created_at, updated_at FROM %s.roles ORDER BY code ASC`,
			schema,
		),
		getRoleByIDQuery: fmt.Sprintf(
			`SELECT id, code, name, description, scope_type, role_level, is_system, is_protected, is_assignable, owner_tenant_id, created_at, updated_at FROM %s.roles WHERE id = $1`,
			schema,
		),
		createRoleQuery: fmt.Sprintf(
			`INSERT INTO %s.roles (id, code, name, description, scope_type, role_level, is_system, is_protected, is_assignable, owner_tenant_id, created_at, updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,NOW(),NOW())`,
			schema,
		),
		updateRoleQuery: fmt.Sprintf(
			`UPDATE %s.roles SET code=$2, name=$3, description=$4, scope_type=$5, role_level=$6, is_system=$7, is_protected=$8, is_assignable=$9, owner_tenant_id=$10, updated_at=NOW() WHERE id=$1`,
			schema,
		),
		deleteRolePermissionsQuery: fmt.Sprintf(
			`DELETE FROM %s.role_permissions WHERE role_id=$1`,
			schema,
		),
		deleteUserRoleAssignsQuery: fmt.Sprintf(
			`DELETE FROM %s.user_role_assignments WHERE role_id=$1`,
			schema,
		),
		deleteRoleQuery: fmt.Sprintf(
			`DELETE FROM %s.roles WHERE id=$1`,
			schema,
		),
		listPermissionsQuery: fmt.Sprintf(
			`SELECT id, code, name, description, resource, action, created_at, updated_at FROM %s.permissions ORDER BY code ASC`,
			schema,
		),
		getPermissionByIDQuery: fmt.Sprintf(
			`SELECT id, code, name, description, resource, action, created_at, updated_at FROM %s.permissions WHERE id=$1`,
			schema,
		),
		getPermissionByCodeQuery: fmt.Sprintf(
			`SELECT id, code, name, description, resource, action, created_at, updated_at FROM %s.permissions WHERE code=$1`,
			schema,
		),
		assignPermissionQuery: fmt.Sprintf(
			`INSERT INTO %s.role_permissions (role_id, permission_id, created_at) VALUES ($1,$2,NOW()) ON CONFLICT DO NOTHING`,
			schema,
		),
		revokePermissionQuery: fmt.Sprintf(
			`DELETE FROM %s.role_permissions WHERE role_id=$1 AND permission_id=$2`,
			schema,
		),
		assignUserRoleQuery: fmt.Sprintf(
			`INSERT INTO %s.user_role_assignments (id, user_id, role_id, scope_type, tenant_id, workspace_id, expires_at, assigned_at) VALUES ($1,$2,$3,$4,$5,$6,$7,NOW()) ON CONFLICT DO NOTHING`,
			schema,
		),
		revokeUserRoleQuery: fmt.Sprintf(
			`UPDATE %s.user_role_assignments SET revoked_at = NOW() WHERE user_id=$1 AND role_id=$2 AND revoked_at IS NULL`,
			schema,
		),
		permissionCodesForRoleQuery: fmt.Sprintf(
			`SELECT p.code FROM %s.permissions p JOIN %s.role_permissions rp ON rp.permission_id = p.id WHERE rp.role_id=$1 ORDER BY p.code ASC`,
			schema,
			schema,
		),
		getUserMaxRoleLevelQuery: fmt.Sprintf(
			`SELECT COALESCE(MIN(r.role_level), 999999) FROM %s.user_role_assignments ura JOIN %s.roles r ON ura.role_id = r.id WHERE ura.user_id = $1 AND (ura.expires_at IS NULL OR ura.expires_at > NOW()) AND ura.revoked_at IS NULL`,
			schema,
			schema,
		),
		// Query only permission codes for a role code to minimize overhead (for lazy load fallback)
		getPermissionCodesByRoleCodeQuery: fmt.Sprintf(
			`SELECT p.code FROM %s.permissions p JOIN %s.role_permissions rp ON rp.permission_id = p.id JOIN %s.roles r ON rp.role_id = r.id WHERE r.code = $1 ORDER BY p.code ASC`,
			schema,
			schema,
			schema,
		),
		// Preload/Warm up all system and protected roles & their permissions in a single joint query
		listSystemRoleEntriesQuery: fmt.Sprintf(
			`SELECT r.id, r.code, r.name, r.description, r.scope_type, r.role_level, r.is_system, r.is_protected, r.is_assignable, r.owner_tenant_id, r.created_at, r.updated_at, p.code FROM %s.roles r LEFT JOIN %s.role_permissions rp ON rp.role_id = r.id LEFT JOIN %s.permissions p ON rp.permission_id = p.id WHERE r.is_system = true OR r.is_protected = true ORDER BY r.code ASC`,
			schema,
			schema,
			schema,
		),
		getUserRoleAndLevelByScopeQuery: fmt.Sprintf(
			`SELECT r.code, r.role_level FROM %s.user_role_assignments ura JOIN %s.roles r ON ura.role_id = r.id WHERE ura.user_id = $1 AND (ura.expires_at IS NULL OR ura.expires_at > NOW()) AND ura.revoked_at IS NULL AND ((($2 = '' OR $2 = 'global' OR $2 = 'platform') AND ura.scope_type = 'platform') OR (ura.tenant_id::text = $2) OR (ura.workspace_id::text = $2)) ORDER BY r.role_level ASC LIMIT 1`,
			schema,
			schema,
		),
		getUserFallbackRoleAndLevelQuery: fmt.Sprintf(
			`SELECT r.code, r.role_level FROM %s.user_role_assignments ura JOIN %s.roles r ON ura.role_id = r.id WHERE ura.user_id = $1 AND (ura.expires_at IS NULL OR ura.expires_at > NOW()) AND ura.revoked_at IS NULL ORDER BY r.role_level ASC LIMIT 1`,
			schema,
			schema,
		),
	}
}

func (r *RbacRepository) GetRoleByCode(ctx context.Context, code string) (*iamEntity.RoleWithPermissions, error) {
	roleCode := strings.TrimSpace(strings.ToLower(code))
	var role iamEntity.Role
	err := r.db.QueryRow(ctx, r.getRoleByCodeQuery, roleCode).
		Scan(&role.ID, &role.Code, &role.Name, &role.Description, &role.ScopeType, &role.RoleLevel, &role.IsSystem, &role.IsProtected, &role.IsAssignable, &role.OwnerTenantID, &role.CreatedAt, &role.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("iam rbac repo: get role by code: %w", err)
	}
	perms, err := r.permissionCodesForRole(ctx, role.ID)
	if err != nil {
		return nil, err
	}
	return &iamEntity.RoleWithPermissions{Role: &role, Permissions: perms}, nil
}

func (r *RbacRepository) GetPermissionCodesByRoleCode(ctx context.Context, roleCode string) ([]string, error) {
	// Query only permission codes for a role code to minimize data parsed from DB.
	code := strings.TrimSpace(strings.ToLower(roleCode))
	rows, err := r.db.Query(ctx, r.getPermissionCodesByRoleCodeQuery, code)
	if err != nil {
		return nil, fmt.Errorf("iam rbac repo: get permission codes by role code: %w", err)
	}
	defer rows.Close()

	var perms []string
	for rows.Next() {
		var perm string
		if err := rows.Scan(&perm); err != nil {
			return nil, fmt.Errorf("iam rbac repo: scan permission code: %w", err)
		}
		perms = append(perms, perm)
	}
	return perms, rows.Err()
}

func (r *RbacRepository) ListSystemRoleEntries(ctx context.Context) ([]*iamEntity.RoleWithPermissions, error) {
	// Query all system and protected roles & their permissions in a single joint query.
	rows, err := r.db.Query(ctx, r.listSystemRoleEntriesQuery)
	if err != nil {
		return nil, fmt.Errorf("iam rbac repo: list system role entries: %w", err)
	}
	defer rows.Close()

	roleMap := make(map[uuid.UUID]*iamEntity.RoleWithPermissions)
	var roleOrder []uuid.UUID

	for rows.Next() {
		var role iamEntity.Role
		var permCode *string // Use pointer in case the role has no permissions (NULL from LEFT JOIN)
		if err := rows.Scan(
			&role.ID, &role.Code, &role.Name, &role.Description, &role.ScopeType,
			&role.RoleLevel, &role.IsSystem, &role.IsProtected, &role.IsAssignable,
			&role.OwnerTenantID, &role.CreatedAt, &role.UpdatedAt, &permCode,
		); err != nil {
			return nil, fmt.Errorf("iam rbac repo: scan system role entry: %w", err)
		}

		entry, exists := roleMap[role.ID]
		if !exists {
			entry = &iamEntity.RoleWithPermissions{
				Role:        &role,
				Permissions: []string{},
			}
			roleMap[role.ID] = entry
			roleOrder = append(roleOrder, role.ID)
		}
		if permCode != nil && *permCode != "" {
			entry.Permissions = append(entry.Permissions, *permCode)
		}
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]*iamEntity.RoleWithPermissions, 0, len(roleOrder))
	for _, id := range roleOrder {
		out = append(out, roleMap[id])
	}
	return out, nil
}

func (r *RbacRepository) ListRoles(ctx context.Context) ([]*iamEntity.Role, error) {
	rows, err := r.db.Query(ctx, r.listRolesQuery)
	if err != nil {
		return nil, fmt.Errorf("iam rbac repo: list roles: %w", err)
	}
	defer rows.Close()
	var out []*iamEntity.Role
	for rows.Next() {
		var item iamEntity.Role
		if err := rows.Scan(&item.ID, &item.Code, &item.Name, &item.Description, &item.ScopeType, &item.RoleLevel, &item.IsSystem, &item.IsProtected, &item.IsAssignable, &item.OwnerTenantID, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, fmt.Errorf("iam rbac repo: scan role: %w", err)
		}
		out = append(out, &item)
	}
	return out, rows.Err()
}

func (r *RbacRepository) GetRoleByID(ctx context.Context, id uuid.UUID) (*iamEntity.Role, error) {
	var role iamEntity.Role
	err := r.db.QueryRow(ctx, r.getRoleByIDQuery, id).
		Scan(&role.ID, &role.Code, &role.Name, &role.Description, &role.ScopeType, &role.RoleLevel, &role.IsSystem, &role.IsProtected, &role.IsAssignable, &role.OwnerTenantID, &role.CreatedAt, &role.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("iam rbac repo: get role: %w", err)
	}
	return &role, nil
}

func (r *RbacRepository) CreateRole(ctx context.Context, role *iamEntity.Role) error {
	if role == nil {
		return fmt.Errorf("iam rbac repo: nil role")
	}
	if role.ID == uuid.Nil {
		role.ID = uuid.New()
	}
	_, err := r.db.Exec(ctx, r.createRoleQuery,
		role.ID,
		strings.ToLower(strings.TrimSpace(role.Code)),
		strings.TrimSpace(role.Name),
		role.Description,
		role.ScopeType,
		role.RoleLevel,
		role.IsSystem,
		role.IsProtected,
		role.IsAssignable,
		role.OwnerTenantID,
	)
	if err != nil {
		return fmt.Errorf("iam rbac repo: create role: %w", err)
	}
	return nil
}

func (r *RbacRepository) UpdateRole(ctx context.Context, role *iamEntity.Role) error {
	if role == nil || role.ID == uuid.Nil {
		return fmt.Errorf("iam rbac repo: invalid role payload")
	}
	tag, err := r.db.Exec(ctx, r.updateRoleQuery,
		role.ID,
		strings.ToLower(strings.TrimSpace(role.Code)),
		strings.TrimSpace(role.Name),
		role.Description,
		role.ScopeType,
		role.RoleLevel,
		role.IsSystem,
		role.IsProtected,
		role.IsAssignable,
		role.OwnerTenantID,
	)
	if err != nil {
		return fmt.Errorf("iam rbac repo: update role: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("iam rbac repo: update role: %w", pgx.ErrNoRows)
	}
	return nil
}

func (r *RbacRepository) DeleteRole(ctx context.Context, id uuid.UUID) error {
	if id == uuid.Nil {
		return fmt.Errorf("iam rbac repo: invalid role id")
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("iam rbac repo: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, r.deleteRolePermissionsQuery, id); err != nil {
		return fmt.Errorf("iam rbac repo: delete role permissions: %w", err)
	}
	if _, err := tx.Exec(ctx, r.deleteUserRoleAssignsQuery, id); err != nil {
		return fmt.Errorf("iam rbac repo: delete user roles: %w", err)
	}
	tag, err := tx.Exec(ctx, r.deleteRoleQuery, id)
	if err != nil {
		return fmt.Errorf("iam rbac repo: delete role: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("iam rbac repo: delete role: %w", pgx.ErrNoRows)
	}
	return tx.Commit(ctx)
}

func (r *RbacRepository) ListPermissions(ctx context.Context) ([]*iamEntity.Permission, error) {
	rows, err := r.db.Query(ctx, r.listPermissionsQuery)
	if err != nil {
		return nil, fmt.Errorf("iam rbac repo: list permissions: %w", err)
	}
	defer rows.Close()
	var out []*iamEntity.Permission
	for rows.Next() {
		var item iamEntity.Permission
		if err := rows.Scan(&item.ID, &item.Code, &item.Name, &item.Description, &item.Resource, &item.Action, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, fmt.Errorf("iam rbac repo: scan permission: %w", err)
		}
		out = append(out, &item)
	}
	return out, rows.Err()
}

func (r *RbacRepository) GetPermissionByID(ctx context.Context, id uuid.UUID) (*iamEntity.Permission, error) {
	var p iamEntity.Permission
	err := r.db.QueryRow(ctx, r.getPermissionByIDQuery, id).
		Scan(&p.ID, &p.Code, &p.Name, &p.Description, &p.Resource, &p.Action, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("iam rbac repo: get permission by id: %w", err)
	}
	return &p, nil
}

func (r *RbacRepository) GetPermissionByCode(ctx context.Context, code string) (*iamEntity.Permission, error) {
	var p iamEntity.Permission
	err := r.db.QueryRow(ctx, r.getPermissionByCodeQuery, strings.ToLower(strings.TrimSpace(code))).
		Scan(&p.ID, &p.Code, &p.Name, &p.Description, &p.Resource, &p.Action, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("iam rbac repo: get permission by code: %w", err)
	}
	return &p, nil
}

func (r *RbacRepository) AssignPermission(ctx context.Context, roleID, permissionID uuid.UUID) error {
	_, err := r.db.Exec(ctx, r.assignPermissionQuery, roleID, permissionID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" { // foreign key violation
			return pgx.ErrNoRows
		}
		return fmt.Errorf("iam rbac repo: assign permission: %w", err)
	}
	return nil
}

func (r *RbacRepository) RevokePermission(ctx context.Context, roleID, permissionID uuid.UUID) error {
	tag, err := r.db.Exec(ctx, r.revokePermissionQuery, roleID, permissionID)
	if err != nil {
		return fmt.Errorf("iam rbac repo: revoke permission: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (r *RbacRepository) AssignUserRole(ctx context.Context, userID, roleID uuid.UUID, scopeType iamEntity.RoleScopeType, tenantID, workspaceID *uuid.UUID, expiresAt *time.Time) error {
	_, err := r.db.Exec(ctx, r.assignUserRoleQuery, uuid.New(), userID, roleID, scopeType, tenantID, workspaceID, expiresAt)
	if err != nil {
		return fmt.Errorf("iam rbac repo: assign user role: %w", err)
	}
	return nil
}

func (r *RbacRepository) RevokeUserRole(ctx context.Context, userID, roleID uuid.UUID) error {
	_, err := r.db.Exec(ctx, r.revokeUserRoleQuery, userID, roleID)
	if err != nil {
		return fmt.Errorf("iam rbac repo: revoke user role: %w", err)
	}
	return nil
}

func (r *RbacRepository) permissionCodesForRole(ctx context.Context, roleID uuid.UUID) ([]string, error) {
	rows, err := r.db.Query(ctx, r.permissionCodesForRoleQuery, roleID)
	if err != nil {
		return nil, fmt.Errorf("iam rbac repo: query perms for role: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("iam rbac repo: scan perm code: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (r *RbacRepository) GetUserMaxRoleLevel(ctx context.Context, userID uuid.UUID) (int, error) {
	var level int
	err := r.db.QueryRow(ctx, r.getUserMaxRoleLevelQuery, userID).Scan(&level)
	if err != nil {
		return 999999, fmt.Errorf("iam rbac repo: get user max role level: %w", err)
	}
	return level, nil
}

func (r *RbacRepository) GetUserRoleAndLevelByScope(ctx context.Context, userID uuid.UUID, scope string) (string, int, error) {
	// [COMMENT]: Thực hiện truy vấn lấy role và level theo scope truyền từ Gateway
	var code string
	var level int
	err := r.db.QueryRow(ctx, r.getUserRoleAndLevelByScopeQuery, userID, strings.TrimSpace(scope)).Scan(&code, &level)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// [COMMENT]: Nếu không tìm thấy role cụ thể theo scope, thực hiện fallback lấy role cao nhất của user nói chung
			fallbackErr := r.db.QueryRow(ctx, r.getUserFallbackRoleAndLevelQuery, userID).Scan(&code, &level)
			if fallbackErr != nil {
				if errors.Is(fallbackErr, pgx.ErrNoRows) {
					// Fallback mặc định nếu user tồn tại nhưng vì lý do nào đó không được gán bất kỳ role nào
					return "platform_user", 8, nil
				}
				return "", 999999, fmt.Errorf("iam rbac repo: fallback get user role: %w", fallbackErr)
			}
			return code, level, nil
		}
		return "", 999999, fmt.Errorf("iam rbac repo: get user role by scope: %w", err)
	}
	return code, level, nil
}
