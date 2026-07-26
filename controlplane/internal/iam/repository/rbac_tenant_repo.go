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
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/proto"
)

// [COMMENT]: RbacTenantRepository thực thi interface quản lý RBAC trong phạm vi Tenant
type RbacTenantRepository struct {
	cfg    *config.Config
	db     *pgxpool.Pool
	schema string
}

// [COMMENT]: NewRbacTenantRepository khởi tạo một thể hiện mới của RbacTenantRepository
func NewRbacTenantRepository(cfg *config.Config, db *pgxpool.Pool) iamRepoInterface.RbacTenantRepository {
	return &RbacTenantRepository{
		cfg:    cfg,
		db:     db,
		schema: cfg.SchemaSQL.IAM,
	}
}

// [COMMENT]: ListTenantRoles lấy danh sách roles gán cho tenant cụ thể
func (r *RbacTenantRepository) ListTenantRoles(ctx context.Context, tenantID uuid.UUID) ([]iamEntity.Role, error) {
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
		return nil, fmt.Errorf("rbac tenant repo: query tenant roles: %w", err)
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
			return nil, fmt.Errorf("rbac tenant repo: scan tenant role row: %w", err)
		}
		roles = append(roles, iamModel.RoleModelToEntity(role))
	}

	return roles, nil
}

// [COMMENT]: AssignTenantRole gán role cho tenant thuộc tenant workspace (skeleton)
func (r *RbacTenantRepository) AssignTenantRole(ctx context.Context, tenantRole *iamEntity.TenantRole) error {
	// [COMMENT]: Logic insert/update database sẽ được viết ở phase tiếp theo
	return nil
}

// [COMMENT]: GetTenantRolePermissions lấy danh sách permissions binary của tenant theo role
func (r *RbacTenantRepository) GetTenantRolePermissions(ctx context.Context, tenantID uuid.UUID, roleID uuid.UUID) ([]byte, error) {
	query := fmt.Sprintf(`
		SELECT list_perm FROM %s.tenant_role
		WHERE tenant_id = $1 AND role_id = $2
	`, r.schema)

	rows, err := r.db.Query(ctx, query, tenantID, roleID)
	if err != nil {
		return nil, fmt.Errorf("rbac tenant repo: query tenant role permissions: %w", err)
	}
	defer rows.Close()

	var mergedPerms []string

	for rows.Next() {
		var binaryData []byte
		if err := rows.Scan(&binaryData); err != nil {
			return nil, fmt.Errorf("rbac tenant repo: scan tenant role permission row: %w", err)
		}
		if len(binaryData) == 0 {
			continue
		}

		var roleEntry iamproto.RoleEntry
		if err := proto.Unmarshal(binaryData, &roleEntry); err != nil {
			return nil, fmt.Errorf("rbac tenant repo: unmarshal tenant role entry: %w", err)
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
		return nil, fmt.Errorf("rbac tenant repo: marshal merged tenant role entry: %w", err)
	}

	return mergedBytes, nil
}

// [COMMENT]: GetRoleIDByTenantID lấy role_id và level của tenant tại platform scope (nil UUID) phục vụ check session
func (r *RbacTenantRepository) GetRoleIDByTenantID(ctx context.Context, tenantID uuid.UUID) (string, int32, error) {
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

func (r *RbacTenantRepository) GetUserTenantBillingPermissions(
	ctx context.Context,
	userID uuid.UUID,
	tenantID uuid.UUID,
) ([]byte, error) {
	query := fmt.Sprintf(`
		SELECT tr.list_perm
		FROM %s.tenant_memberships tm
		JOIN %s.tenants t
		  ON t.id=tm.tenant_id AND t.status='active'
		JOIN %s.users u
		  ON u.id=tm.user_id AND u.status='active'
		JOIN %s.roles r
		  ON r.code=tm.role AND r.scope='tenant'
		JOIN %s.tenant_role tr
		  ON tr.tenant_id=tm.tenant_id
		 AND tr.workspace_id='00000000-0000-0000-0000-000000000000'
		 AND tr.role_id=r.id
		WHERE tm.tenant_id=$1 AND tm.user_id=$2 AND tm.status='active'
		LIMIT 1
	`, r.cfg.SchemaSQL.Hierarchy, r.cfg.SchemaSQL.Hierarchy, r.schema, r.schema, r.schema)

	var binaryData []byte
	if err := r.db.QueryRow(ctx, query, tenantID, userID).Scan(&binaryData); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, iamTaxonomy.ErrActionNotAllowed
		}
		return nil, fmt.Errorf("rbac tenant repo: query user tenant Billing permissions: %w", err)
	}
	var source iamproto.RoleEntry
	if err := proto.Unmarshal(binaryData, &source); err != nil {
		return nil, fmt.Errorf("rbac tenant repo: decode user tenant Billing permissions: %w", err)
	}

	prefix := tenantID.String() + ":00000000-0000-0000-0000-000000000000:billing:"
	filtered := make([]string, 0, len(source.Permissions))
	for _, permission := range source.Permissions {
		if len(permission) > len(prefix) && permission[:len(prefix)] == prefix {
			filtered = append(filtered, permission)
		}
	}
	if len(filtered) == 0 {
		return nil, iamTaxonomy.ErrActionNotAllowed
	}
	result, err := proto.Marshal(&iamproto.RoleEntry{Permissions: filtered})
	if err != nil {
		return nil, fmt.Errorf("rbac tenant repo: encode user tenant Billing permissions: %w", err)
	}
	return result, nil
}

func (r *RbacTenantRepository) GetUserTenantRolePermissions(
	ctx context.Context,
	userID uuid.UUID,
	tenantID uuid.UUID,
) ([]byte, error) {
	query := fmt.Sprintf(`
		SELECT tr.list_perm
		FROM %s.tenant_memberships tm
		JOIN %s.tenants t
		  ON t.id=tm.tenant_id AND t.status='active'
		JOIN %s.users u
		  ON u.id=tm.user_id AND u.status='active'
		JOIN %s.roles r
		  ON r.code=tm.role AND r.scope='tenant'
		JOIN %s.tenant_role tr
		  ON tr.tenant_id=tm.tenant_id
		 AND tr.workspace_id='00000000-0000-0000-0000-000000000000'
		 AND tr.role_id=r.id
		WHERE tm.tenant_id=$1 AND tm.user_id=$2 AND tm.status='active'
		LIMIT 1
	`, r.cfg.SchemaSQL.Hierarchy, r.cfg.SchemaSQL.Hierarchy, r.schema, r.schema, r.schema)

	var binaryData []byte
	if err := r.db.QueryRow(ctx, query, tenantID, userID).Scan(&binaryData); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, iamTaxonomy.ErrActionNotAllowed
		}
		return nil, fmt.Errorf("rbac tenant repo: query user tenant role permissions: %w", err)
	}
	var entry iamproto.RoleEntry
	if err := proto.Unmarshal(binaryData, &entry); err != nil {
		return nil, fmt.Errorf("rbac tenant repo: decode user tenant role permissions: %w", err)
	}
	expectedPrefix := tenantID.String() + ":"
	for _, permission := range entry.Permissions {
		parts := strings.Split(permission, ":")
		if len(parts) != 5 || parts[0] != tenantID.String() || !strings.HasPrefix(permission, expectedPrefix) {
			return nil, fmt.Errorf("rbac tenant repo: invalid tenant role permission %q", permission)
		}
	}
	return binaryData, nil
}
