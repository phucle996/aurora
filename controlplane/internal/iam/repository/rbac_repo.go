package iamRepoImpl

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"controlplane/internal/config"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
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

// [COMMENT]: GetUserRolePermissions lấy danh sách permissions binary của user trong workspace
func (r *RbacRepository) GetUserRolePermissions(ctx context.Context, userID uuid.UUID, workspaceID uuid.UUID) ([]byte, error) {
	// [COMMENT]: Query toàn bộ list_perm của user_role tương ứng với userID.
	// Hỗ trợ lấy cả quyền cụ thể cho workspaceID và quyền wildcard (nil UUID '00000000-0000-0000-0000-000000000000').
	query := fmt.Sprintf(`
		SELECT list_perm FROM %s.user_role
		WHERE user_id = $1 AND (workspace_id = $2 OR workspace_id = '00000000-0000-0000-0000-000000000000')
	`, r.schema)

	rows, err := r.db.Query(ctx, query, userID, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("rbac repo: query user role permissions: %w", err)
	}
	defer rows.Close()

	var mergedPerms []string
	nilUUIDStr := "00000000-0000-0000-0000-000000000000"
	targetWSStr := workspaceID.String()

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

		// [COMMENT]: Map động Nil UUID thành workspaceID thực tế trước khi gộp vào cache
		for _, p := range roleEntry.Permissions {
			mappedPerm := p
			if workspaceID != uuid.Nil {
				mappedPerm = strings.ReplaceAll(p, ":"+nilUUIDStr+":", ":"+targetWSStr+":")
			}
			mergedPerms = append(mergedPerms, mappedPerm)
		}
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	// [COMMENT]: Đóng gói danh sách quyền đã gộp và map động trở lại thành binary Protobuf
	mergedEntry := &iamproto.RoleEntry{
		Permissions: mergedPerms,
	}
	mergedBytes, err := proto.Marshal(mergedEntry)
	if err != nil {
		return nil, fmt.Errorf("rbac repo: marshal merged user role entry: %w", err)
	}

	return mergedBytes, nil
}

// [COMMENT]: GetTenantRolePermissions lấy danh sách permissions binary của tenant trong workspace
func (r *RbacRepository) GetTenantRolePermissions(ctx context.Context, tenantID uuid.UUID, workspaceID uuid.UUID, roleID uuid.UUID) ([]byte, error) {
	// [COMMENT]: Query toàn bộ list_perm của tenant_role tương ứng với tenantID và roleID.
	// Hỗ trợ lấy cả quyền cụ thể cho workspaceID và quyền wildcard (nil UUID '00000000-0000-0000-0000-000000000000').
	query := fmt.Sprintf(`
		SELECT list_perm FROM %s.tenant_role
		WHERE tenant_id = $1 AND role_id = $2 AND (workspace_id = $3 OR workspace_id = '00000000-0000-0000-0000-000000000000')
	`, r.schema)

	rows, err := r.db.Query(ctx, query, tenantID, roleID, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("rbac repo: query tenant role permissions: %w", err)
	}
	defer rows.Close()

	var mergedPerms []string
	nilUUIDStr := "00000000-0000-0000-0000-000000000000"
	targetWSStr := workspaceID.String()

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

		// [COMMENT]: Map động Nil UUID thành workspaceID thực tế trước khi gộp vào cache
		for _, p := range roleEntry.Permissions {
			mappedPerm := p
			if workspaceID != uuid.Nil {
				mappedPerm = strings.ReplaceAll(p, ":"+nilUUIDStr+":", ":"+targetWSStr+":")
			}
			mergedPerms = append(mergedPerms, mappedPerm)
		}
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	// [COMMENT]: Đóng gói danh sách quyền đã gộp và map động trở lại thành binary Protobuf
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
