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

// [COMMENT]: RbacPlatformRepository thực thi interface quản lý RBAC toàn cục
type RbacPlatformRepository struct {
	cfg    *config.Config
	db     *pgxpool.Pool
	schema string
}

// [COMMENT]: NewRbacPlatformRepository khởi tạo một thể hiện mới của RbacPlatformRepository
func NewRbacPlatformRepository(cfg *config.Config, db *pgxpool.Pool) iamRepoInterface.RbacPlatformRepository {
	return &RbacPlatformRepository{
		cfg:    cfg,
		db:     db,
		schema: cfg.SchemaSQL.IAM,
	}
}

// [COMMENT]: AssignUserRole gán role cho user cấp platform (skeleton)
func (r *RbacPlatformRepository) AssignUserRole(ctx context.Context, userRole *iamEntity.UserRole) error {
	// [COMMENT]: Logic insert/update database sẽ được viết ở phase tiếp theo
	return nil
}

// [COMMENT]: AssignTenantRole gán role cho tenant cấp platform (skeleton)
func (r *RbacPlatformRepository) AssignTenantRole(ctx context.Context, tenantRole *iamEntity.TenantRole) error {
	// [COMMENT]: Logic insert/update database sẽ được viết ở phase tiếp theo
	return nil
}

// [COMMENT]: ListPlatformRoles lấy toàn bộ danh sách roles có scope là platform
func (r *RbacPlatformRepository) ListPlatformRoles(ctx context.Context) ([]iamEntity.Role, error) {
	query := fmt.Sprintf(`
		SELECT id, code, name, COALESCE(description, ''), role_level, scope, created_at, updated_at
		FROM %s.roles
		WHERE scope = 'platform'
		ORDER BY role_level ASC
	`, r.schema)

	rows, err := r.db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("rbac platform repo: query platform roles: %w", err)
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
			return nil, fmt.Errorf("rbac platform repo: scan platform role row: %w", err)
		}
		roles = append(roles, iamModel.RoleModelToEntity(role))
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return roles, nil
}

// [COMMENT]: CreateRole tạo một vai trò hệ thống mới và map permissions
func (r *RbacPlatformRepository) CreateRole(ctx context.Context, role *iamEntity.Role, permissionIDs []uuid.UUID) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("rbac platform repo: begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// 1. Insert role
	roleQuery := fmt.Sprintf(`
		INSERT INTO %s.roles (id, code, name, description, role_level, scope, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, now(), now())
	`, r.schema)

	_, err = tx.Exec(ctx, roleQuery, role.ID, role.Code, role.Name, role.Description, role.RoleLevel, role.Scope)
	if err != nil {
		return fmt.Errorf("rbac platform repo: insert role: %w", err)
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
				return fmt.Errorf("rbac platform repo: insert role permission link for %s: %w", permID, err)
			}
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("rbac platform repo: commit tx: %w", err)
	}

	return nil
}

// [COMMENT]: ListPermissions lấy danh sách toàn bộ permissions catalog trong DB
func (r *RbacPlatformRepository) ListPermissions(ctx context.Context) ([]iamEntity.Permission, error) {
	query := fmt.Sprintf(`
		SELECT id, module, object, behavior, COALESCE(description, ''), created_at, updated_at
		FROM %s.permissions
		ORDER BY module ASC, object ASC, behavior ASC
	`, r.schema)

	rows, err := r.db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("rbac platform repo: query permissions: %w", err)
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
			return nil, fmt.Errorf("rbac platform repo: scan permission row: %w", err)
		}
		perms = append(perms, iamModel.PermissionModelToEntity(p))
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return perms, nil
}

// [COMMENT]: GetUserRoleDetails lấy thông tin chi tiết vai trò của user kèm kiểm tra cấp bậc
func (r *RbacPlatformRepository) GetUserRoleDetails(ctx context.Context, userID uuid.UUID, callerLevel int32) (*iamEntity.Role, error) {
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
		return nil, fmt.Errorf("rbac platform repo: query user role details: %w", err)
	}

	entityRole := iamModel.RoleModelToEntity(role)
	return &entityRole, nil
}

// [COMMENT]: GetUserRolePermissions lấy danh sách permissions binary của user theo user id
func (r *RbacPlatformRepository) GetUserRolePermissions(ctx context.Context, userID uuid.UUID) ([]byte, error) {
	query := fmt.Sprintf(`
		SELECT list_perm FROM %s.user_role
		WHERE user_id = $1
	`, r.schema)

	rows, err := r.db.Query(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("rbac platform repo: query user role permissions: %w", err)
	}
	defer rows.Close()

	var mergedPerms []string

	for rows.Next() {
		var binaryData []byte
		if err := rows.Scan(&binaryData); err != nil {
			return nil, fmt.Errorf("rbac platform repo: scan user role permission row: %w", err)
		}
		if len(binaryData) == 0 {
			continue
		}

		var roleEntry iamproto.RoleEntry
		if err := proto.Unmarshal(binaryData, &roleEntry); err != nil {
			return nil, fmt.Errorf("rbac platform repo: unmarshal user role entry: %w", err)
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
		return nil, fmt.Errorf("rbac platform repo: marshal merged user role entry: %w", err)
	}

	return mergedBytes, nil
}

// [COMMENT]: GetRoleIDByUserID lấy role_id và level của user tại platform scope (nil UUID) phục vụ check session
func (r *RbacPlatformRepository) GetRoleIDByUserID(ctx context.Context, userID uuid.UUID) (string, int32, error) {
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
