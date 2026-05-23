package iamRepoImpl

import (
	"context"
	"fmt"
	"strings"

	"controlplane/internal/config"
	iamEntity "controlplane/internal/iam/domain/entity"
	"controlplane/internal/iam/domain/repo"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var _ iamRepoInterface.RbacRepository = (*RbacRepository)(nil)

type RbacRepository struct {
	cfg *config.Config
	db  *pgxpool.Pool
}

func NewRbacRepository(cfg *config.Config, db *pgxpool.Pool) *RbacRepository {
	return &RbacRepository{cfg: cfg, db: db}
}

func (r *RbacRepository) GetRoleByCode(ctx context.Context, code string) (*iamEntity.RoleWithPermissions, error) {
	roleCode := strings.TrimSpace(strings.ToLower(code))
	var role iamEntity.Role
	err := r.db.QueryRow(ctx, `SELECT id, code, name, description, scope_type, role_level, is_system, is_protected, is_assignable, owner_tenant_id, created_at, updated_at FROM roles WHERE code = $1`, roleCode).
		Scan(&role.ID, &role.Code, &role.Name, &role.Description, &role.ScopeType, &role.RoleLevel, &role.IsSystem, &role.IsProtected, &role.IsAssignable, &role.OwnerTenantID, &role.CreatedAt, &role.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("iam rbac repo: get role by code: %w", err)
	}
	perms, err := r.permissionCodesForRole(ctx, role.ID.String())
	if err != nil {
		return nil, err
	}
	return &iamEntity.RoleWithPermissions{Role: &role, Permissions: perms}, nil
}

func (r *RbacRepository) ListRoleEntries(ctx context.Context) ([]*iamEntity.RoleWithPermissions, error) {
	roles, err := r.ListRoles(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*iamEntity.RoleWithPermissions, 0, len(roles))
	for _, role := range roles {
		if role == nil {
			continue
		}
		perms, perr := r.permissionCodesForRole(ctx, role.ID.String())
		if perr != nil {
			return nil, perr
		}
		out = append(out, &iamEntity.RoleWithPermissions{Role: role, Permissions: perms})
	}
	return out, nil
}

func (r *RbacRepository) ListRoles(ctx context.Context) ([]*iamEntity.Role, error) {
	rows, err := r.db.Query(ctx, `SELECT id, code, name, description, scope_type, role_level, is_system, is_protected, is_assignable, owner_tenant_id, created_at, updated_at FROM roles ORDER BY code ASC`)
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

func (r *RbacRepository) GetRoleByID(ctx context.Context, id string) (*iamEntity.Role, error) {
	uid, err := uuid.Parse(strings.TrimSpace(id))
	if err != nil {
		return nil, err
	}
	var role iamEntity.Role
	err = r.db.QueryRow(ctx, `SELECT id, code, name, description, scope_type, role_level, is_system, is_protected, is_assignable, owner_tenant_id, created_at, updated_at FROM roles WHERE id = $1`, uid).
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
	_, err := r.db.Exec(ctx, `INSERT INTO roles (id, code, name, description, scope_type, role_level, is_system, is_protected, is_assignable, owner_tenant_id, created_at, updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,NOW(),NOW())`,
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
	tag, err := r.db.Exec(ctx, `UPDATE roles SET code=$2, name=$3, description=$4, scope_type=$5, role_level=$6, is_system=$7, is_protected=$8, is_assignable=$9, owner_tenant_id=$10, updated_at=NOW() WHERE id=$1`,
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

func (r *RbacRepository) DeleteRole(ctx context.Context, id string) error {
	uid, err := uuid.Parse(strings.TrimSpace(id))
	if err != nil {
		return err
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("iam rbac repo: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM role_permissions WHERE role_id=$1`, uid); err != nil {
		return fmt.Errorf("iam rbac repo: delete role permissions: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM user_role_assignments WHERE role_id=$1`, uid); err != nil {
		return fmt.Errorf("iam rbac repo: delete user roles: %w", err)
	}
	tag, err := tx.Exec(ctx, `DELETE FROM roles WHERE id=$1`, uid)
	if err != nil {
		return fmt.Errorf("iam rbac repo: delete role: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("iam rbac repo: delete role: %w", pgx.ErrNoRows)
	}
	return tx.Commit(ctx)
}

func (r *RbacRepository) ListPermissions(ctx context.Context) ([]*iamEntity.Permission, error) {
	rows, err := r.db.Query(ctx, `SELECT id, code, name, description, resource, action, created_at, updated_at FROM permissions ORDER BY code ASC`)
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

func (r *RbacRepository) GetPermissionByID(ctx context.Context, id string) (*iamEntity.Permission, error) {
	uid, err := uuid.Parse(strings.TrimSpace(id))
	if err != nil {
		return nil, err
	}
	var p iamEntity.Permission
	err = r.db.QueryRow(ctx, `SELECT id, code, name, description, resource, action, created_at, updated_at FROM permissions WHERE id=$1`, uid).
		Scan(&p.ID, &p.Code, &p.Name, &p.Description, &p.Resource, &p.Action, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("iam rbac repo: get permission by id: %w", err)
	}
	return &p, nil
}

func (r *RbacRepository) GetPermissionByCode(ctx context.Context, code string) (*iamEntity.Permission, error) {
	var p iamEntity.Permission
	err := r.db.QueryRow(ctx, `SELECT id, code, name, description, resource, action, created_at, updated_at FROM permissions WHERE code=$1`, strings.ToLower(strings.TrimSpace(code))).
		Scan(&p.ID, &p.Code, &p.Name, &p.Description, &p.Resource, &p.Action, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("iam rbac repo: get permission by code: %w", err)
	}
	return &p, nil
}

func (r *RbacRepository) CreatePermission(ctx context.Context, perm *iamEntity.Permission) error {
	if perm == nil {
		return fmt.Errorf("iam rbac repo: nil permission")
	}
	if perm.ID == uuid.Nil {
		perm.ID = uuid.New()
	}
	_, err := r.db.Exec(ctx, `INSERT INTO permissions (id, code, name, description, resource, action, created_at, updated_at) VALUES ($1,$2,$3,$4,$5,$6,NOW(),NOW())`, perm.ID, strings.ToLower(strings.TrimSpace(perm.Code)), strings.TrimSpace(perm.Name), perm.Description, strings.TrimSpace(perm.Resource), strings.TrimSpace(perm.Action))
	if err != nil {
		return fmt.Errorf("iam rbac repo: create permission: %w", err)
	}
	return nil
}

func (r *RbacRepository) AssignPermission(ctx context.Context, roleID, permissionID string) error {
	rid, err := uuid.Parse(strings.TrimSpace(roleID))
	if err != nil {
		return err
	}
	pid, err := uuid.Parse(strings.TrimSpace(permissionID))
	if err != nil {
		return err
	}
	_, err = r.db.Exec(ctx, `INSERT INTO role_permissions (role_id, permission_id, created_at) VALUES ($1,$2,NOW()) ON CONFLICT DO NOTHING`, rid, pid)
	if err != nil {
		return fmt.Errorf("iam rbac repo: assign permission: %w", err)
	}
	return nil
}

func (r *RbacRepository) RevokePermission(ctx context.Context, roleID, permissionID string) error {
	rid, err := uuid.Parse(strings.TrimSpace(roleID))
	if err != nil {
		return err
	}
	pid, err := uuid.Parse(strings.TrimSpace(permissionID))
	if err != nil {
		return err
	}
	tag, err := r.db.Exec(ctx, `DELETE FROM role_permissions WHERE role_id=$1 AND permission_id=$2`, rid, pid)
	if err != nil {
		return fmt.Errorf("iam rbac repo: revoke permission: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("iam rbac repo: revoke permission: %w", pgx.ErrNoRows)
	}
	return nil
}

func (r *RbacRepository) AssignUserRole(ctx context.Context, userID, roleID string) error {
	uid, err := uuid.Parse(strings.TrimSpace(userID))
	if err != nil {
		return err
	}
	rid, err := uuid.Parse(strings.TrimSpace(roleID))
	if err != nil {
		return err
	}
	// V1 rollout: chỉ ghi assignment platform scope.
	// Tenant/workspace scoped assignment sẽ bổ sung ở phase sau cùng validation invariant theo spec.
	_, err = r.db.Exec(ctx, `INSERT INTO user_role_assignments (id, user_id, role_id, scope_type, assigned_at) VALUES ($1,$2,$3,'platform',NOW()) ON CONFLICT DO NOTHING`, uuid.New(), uid, rid)
	if err != nil {
		return fmt.Errorf("iam rbac repo: assign user role: %w", err)
	}
	return nil
}

func (r *RbacRepository) RevokeUserRole(ctx context.Context, userID, roleID string) error {
	uid, err := uuid.Parse(strings.TrimSpace(userID))
	if err != nil {
		return err
	}
	rid, err := uuid.Parse(strings.TrimSpace(roleID))
	if err != nil {
		return err
	}
	_, err = r.db.Exec(ctx, `UPDATE user_roles SET revoked_at = NOW() WHERE user_id=$1 AND role_id=$2 AND revoked_at IS NULL`, uid, rid)
	if err != nil {
		return fmt.Errorf("iam rbac repo: revoke user role: %w", err)
	}
	return nil
}

func (r *RbacRepository) permissionCodesForRole(ctx context.Context, roleID string) ([]string, error) {
	rid, err := uuid.Parse(strings.TrimSpace(roleID))
	if err != nil {
		return nil, err
	}
	rows, err := r.db.Query(ctx, `SELECT p.code FROM permissions p JOIN role_permissions rp ON rp.permission_id = p.id WHERE rp.role_id=$1 ORDER BY p.code ASC`, rid)
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
