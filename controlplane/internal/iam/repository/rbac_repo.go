package iamRepoImpl

import (
	"context"
	"errors"
	"fmt"

	"controlplane/internal/config"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamModel "controlplane/internal/iam/model"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	iamproto "controlplane/internal/iam/transport/rpc/proto"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/proto"
)

// [COMMENT]: RbacRepository thực hiện interface RbacRepository tối giản dạng skeleton cho phase tiếp theo
type RbacRepository struct {
	cfg    *config.Config
	db     *pgxpool.Pool
	schema string
}

// [COMMENT]: NewRbacRepository khởi tạo một thể hiện mới của RbacRepository
func NewRbacRepository(cfg *config.Config, db *pgxpool.Pool) iamRepoInterface.RbacRepository {
	return &RbacRepository{
		cfg:    cfg,
		db:     db,
		schema: cfg.SchemaSQL.IAM,
	}
}

// [COMMENT]: GetUserRolePermissions lấy danh sách permissions binary của user trên tất cả workspaces theo user id
func (r *RbacRepository) GetUserRolePermissions(ctx context.Context, userID uuid.UUID) ([]byte, error) {
	query := fmt.Sprintf(`
		SELECT list_perm FROM %s.user_role
		WHERE user_id = $1
	`, r.schema)

	rows, err := r.db.Query(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("rbac repo: query user role permissions: %w", err)
	}
	defer rows.Close()

	var mergedPerms []string

	for rows.Next() {
		var binaryData []byte
		if err := rows.Scan(&binaryData); err != nil {
			return nil, fmt.Errorf("rbac repo: scan user role permission row: %w", err)
		}
		if len(binaryData) == 0 {
			continue
		}

		var roleEntry iamproto.RoleEntry
		if err := proto.Unmarshal(binaryData, &roleEntry); err != nil {
			return nil, fmt.Errorf("rbac repo: unmarshal user role entry: %w", err)
		}

		mergedPerms = append(mergedPerms, roleEntry.Permissions...)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	// [COMMENT]: Đóng gói danh sách quyền đã gộp trở lại thành binary Protobuf
	mergedEntry := &iamproto.RoleEntry{
		Permissions: mergedPerms,
	}
	mergedBytes, err := proto.Marshal(mergedEntry)
	if err != nil {
		return nil, fmt.Errorf("rbac repo: marshal merged user role entry: %w", err)
	}

	return mergedBytes, nil
}

// [COMMENT]: GetTenantRolePermissions lấy danh sách permissions binary của tenant trên tất cả workspaces theo role
func (r *RbacRepository) GetTenantRolePermissions(ctx context.Context, tenantID uuid.UUID, roleID uuid.UUID) ([]byte, error) {
	query := fmt.Sprintf(`
		SELECT list_perm FROM %s.tenant_role
		WHERE tenant_id = $1 AND role_id = $2
	`, r.schema)

	rows, err := r.db.Query(ctx, query, tenantID, roleID)
	if err != nil {
		return nil, fmt.Errorf("rbac repo: query tenant role permissions: %w", err)
	}
	defer rows.Close()

	var mergedPerms []string

	for rows.Next() {
		var binaryData []byte
		if err := rows.Scan(&binaryData); err != nil {
			return nil, fmt.Errorf("rbac repo: scan tenant role permission row: %w", err)
		}
		if len(binaryData) == 0 {
			continue
		}

		var roleEntry iamproto.RoleEntry
		if err := proto.Unmarshal(binaryData, &roleEntry); err != nil {
			return nil, fmt.Errorf("rbac repo: unmarshal tenant role entry: %w", err)
		}

		mergedPerms = append(mergedPerms, roleEntry.Permissions...)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	// [COMMENT]: Đóng gói danh sách quyền đã gộp trở lại thành binary Protobuf
	mergedEntry := &iamproto.RoleEntry{
		Permissions: mergedPerms,
	}
	mergedBytes, err := proto.Marshal(mergedEntry)
	if err != nil {
		return nil, fmt.Errorf("rbac repo: marshal merged tenant role entry: %w", err)
	}

	return mergedBytes, nil
}

// [COMMENT]: AssignUserRole gán role cho user (skeleton)
func (r *RbacRepository) AssignUserRole(ctx context.Context, userRole *iamEntity.UserRole) error {
	// [COMMENT]: Logic insert/update database sẽ được viết ở phase tiếp theo
	return nil
}

// [COMMENT]: AssignTenantRole gán role cho tenant (skeleton)
func (r *RbacRepository) AssignTenantRole(ctx context.Context, tenantRole *iamEntity.TenantRole) error {
	// [COMMENT]: Logic insert/update database sẽ được viết ở phase tiếp theo
	return nil
}

// [COMMENT]: GetRoleIDByUserID lấy role_id và level của user tại platform scope (nil UUID)
func (r *RbacRepository) GetRoleIDByUserID(ctx context.Context, userID uuid.UUID) (string, int32, error) {
	var roleIDStr string
	var roleLevel int32

	query := fmt.Sprintf(`
		SELECT role_id::text, role_level FROM %s.user_role
		WHERE user_id = $1 AND workspace_id = '00000000-0000-0000-0000-000000000000'
		LIMIT 1
	`, r.schema)

	err := r.db.QueryRow(ctx, query, userID).Scan(&roleIDStr, &roleLevel)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", 0, iamTaxonomy.ErrRoleNotFound
		}
		return "", 0, err
	}

	return roleIDStr, roleLevel, nil
}

// [COMMENT]: GetRoleIDByTenantID lấy role_id và level của tenant tại platform scope (nil UUID)
func (r *RbacRepository) GetRoleIDByTenantID(ctx context.Context, tenantID uuid.UUID) (string, int32, error) {
	var roleIDStr string
	var roleLevel int32

	query := fmt.Sprintf(`
		SELECT role_id::text, role_level FROM %s.tenant_role
		WHERE tenant_id = $1 AND workspace_id = '00000000-0000-0000-0000-000000000000'
		LIMIT 1
	`, r.schema)

	err := r.db.QueryRow(ctx, query, tenantID).Scan(&roleIDStr, &roleLevel)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", 0, iamTaxonomy.ErrRoleNotFound
		}
		return "", 0, err
	}

	return roleIDStr, roleLevel, nil
}

// [COMMENT]: ListPlatformRoles lấy toàn bộ danh sách roles có scope là platform
func (r *RbacRepository) ListPlatformRoles(ctx context.Context) ([]iamEntity.Role, error) {
	query := fmt.Sprintf(`
		SELECT id, code, name, COALESCE(description, ''), role_level, scope, created_at, updated_at
		FROM %s.roles
		WHERE scope = 'platform'
		ORDER BY role_level ASC
	`, r.schema)

	rows, err := r.db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("rbac repo: query platform roles: %w", err)
	}
	defer rows.Close()

	var roles []iamEntity.Role
	for rows.Next() {
		var role iamModel.Role
		err := rows.Scan(
			&role.ID,
			&role.Code,
			&role.Name,
			&role.Description,
			&role.RoleLevel,
			&role.Scope,
			&role.CreatedAt,
			&role.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("rbac repo: scan platform role row: %w", err)
		}
		roles = append(roles, iamModel.RoleModelToEntity(role))
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return roles, nil
}

func (r *RbacRepository) ListTenantRoles(ctx context.Context, tenantID uuid.UUID) ([]iamEntity.Role, error) {
	query := fmt.Sprintf(`
		SELECT DISTINCT
			r.id,
			r.code,
			r.name,
			COALESCE(r.description, ''),
			r.role_level,
			r.scope,
			r.created_at,
			r.updated_at
		FROM %s.roles r
		JOIN %s.tenant_role tr ON tr.role_id = r.id
		WHERE tr.tenant_id = $1
		ORDER BY r.role_level ASC
	`, r.schema, r.schema)

	rows, err := r.db.Query(ctx, query, tenantID)
	if err != nil {
		return nil, fmt.Errorf("rbac repo: query tenant roles: %w", err)
	}
	defer rows.Close()

	var roles []iamEntity.Role
	for rows.Next() {
		var role iamModel.Role
		err := rows.Scan(
			&role.ID,
			&role.Code,
			&role.Name,
			&role.Description,
			&role.RoleLevel,
			&role.Scope,
			&role.CreatedAt,
			&role.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("rbac repo: scan tenant role row: %w", err)
		}
		roles = append(roles, iamModel.RoleModelToEntity(role))
	}

	return roles, nil
}

// [COMMENT]: CreateRole tạo một vai trò mới và gán các permissions tương ứng trong một transaction
func (r *RbacRepository) CreateRole(ctx context.Context, role *iamEntity.Role, permissionIDs []uuid.UUID) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("rbac repo: begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// 1. Insert role
	roleQuery := fmt.Sprintf(`
		INSERT INTO %s.roles (id, code, name, description, role_level, scope, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, now(), now())
	`, r.schema)

	_, err = tx.Exec(ctx, roleQuery, role.ID, role.Code, role.Name, role.Description, role.RoleLevel, role.Scope)
	if err != nil {
		return fmt.Errorf("rbac repo: insert role: %w", err)
	}

	// 2. Map permissions
	if len(permissionIDs) > 0 {
		permQuery := fmt.Sprintf(`
			INSERT INTO %s.role_permissions (role_id, permission_id, created_at)
			VALUES ($1, $2, now())
		`, r.schema)

		for _, permID := range permissionIDs {
			_, err = tx.Exec(ctx, permQuery, role.ID, permID)
			if err != nil {
				return fmt.Errorf("rbac repo: insert role permission link for %s: %w", permID, err)
			}
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("rbac repo: commit tx: %w", err)
	}

	return nil
}

// [COMMENT]: ListPermissions lấy danh sách toàn bộ các permissions catalog trong DB
func (r *RbacRepository) ListPermissions(ctx context.Context) ([]iamEntity.Permission, error) {
	query := fmt.Sprintf(`
		SELECT id, module, object, behavior, COALESCE(description, ''), created_at, updated_at
		FROM %s.permissions
		ORDER BY module ASC, object ASC, behavior ASC
	`, r.schema)

	rows, err := r.db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("rbac repo: query permissions: %w", err)
	}
	defer rows.Close()

	var perms []iamEntity.Permission
	for rows.Next() {
		var p iamModel.Permission
		err := rows.Scan(
			&p.ID,
			&p.Module,
			&p.Object,
			&p.Behavior,
			&p.Description,
			&p.CreatedAt,
			&p.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("rbac repo: scan permission row: %w", err)
		}
		perms = append(perms, iamModel.PermissionModelToEntity(p))
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return perms, nil
}

// [COMMENT]: GetUserRoleDetails lấy thông tin chi tiết vai trò của user kèm kiểm tra cấp bậc
func (r *RbacRepository) GetUserRoleDetails(ctx context.Context, userID uuid.UUID, callerLevel int32) (*iamEntity.Role, error) {
	var role iamModel.Role

	query := fmt.Sprintf(`
		SELECT rl.id, rl.code, rl.name, COALESCE(rl.description, ''), rl.role_level, rl.scope, rl.created_at, rl.updated_at
		FROM %s.user_role ur
		JOIN %s.roles rl ON ur.role_id = rl.id
		WHERE ur.user_id = $1 
		  AND ur.workspace_id = '00000000-0000-0000-0000-000000000000'
		  AND rl.role_level > $2
		LIMIT 1
	`, r.schema, r.schema)

	err := r.db.QueryRow(ctx, query, userID, callerLevel).Scan(
		&role.ID,
		&role.Code,
		&role.Name,
		&role.Description,
		&role.RoleLevel,
		&role.Scope,
		&role.CreatedAt,
		&role.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, iamTaxonomy.ErrRoleNotFound
		}
		return nil, fmt.Errorf("rbac repo: query user role details: %w", err)
	}

	entityRole := iamModel.RoleModelToEntity(role)
	return &entityRole, nil
}
