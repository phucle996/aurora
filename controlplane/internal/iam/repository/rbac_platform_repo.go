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

// [COMMENT]: AssignUserRole thực hiện gán vai trò platform cho người dùng, biên dịch danh sách quyền của role sang nhị phân Protobuf bytea và lưu trữ sử dụng CTE nguyên tử trong một Transaction để đảm bảo tính cô lập và bền vững
func (r *RbacPlatformRepository) AssignUserRole(ctx context.Context, callerLevel uint8, userID uuid.UUID, roleID uuid.UUID) error {
	// [COMMENT]: 1. Khởi tạo một Transaction để đảm bảo tính cô lập (Read Committed) và ngăn chặn race conditions
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("platform rbac repo: begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	// [COMMENT]: A. Lấy danh sách string permissions thuộc về roleID từ bảng role_permissions và permissions trong phạm vi transaction
	queryPerms := fmt.Sprintf(`
		SELECT p.module || ':' || p.object || ':' || p.behavior AS perm
		FROM %s.role_permissions rp
		JOIN %s.permissions p ON rp.permission_id = p.id
		WHERE rp.role_id = $1
	`, r.schema, r.schema)

	rows, err := tx.Query(ctx, queryPerms, roleID)
	if err != nil {
		return fmt.Errorf("platform rbac repo: query permissions: %w", err)
	}
	defer rows.Close()

	var perms []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return fmt.Errorf("platform rbac repo: scan permission: %w", err)
		}
		perms = append(perms, p)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close() // đóng sớm để tránh lock connection

	// [COMMENT]: B. Serialize danh sách permissions thành Protobuf binary byte array (để repository thuần chất cơ sở dữ liệu)
	roleEntry := &iamproto.RoleEntry{
		Permissions: perms,
	}
	binaryBytes, err := proto.Marshal(roleEntry)
	if err != nil {
		return fmt.Errorf("platform rbac repo: marshal role entry: %w", err)
	}

	// [COMMENT]: C. Thực hiện CTE nguyên tử để:
	// 1. Kiểm tra target user tồn tại và lấy level hiện tại của target user.
	// 2. Kiểm tra role gán tồn tại và lấy level của role gán.
	// 3. So sánh caller level với level của target user & role gán.
	// 4. Nếu hợp lệ, xóa vai trò cũ tại platform scope (nil UUID) và chèn vai trò mới.
	queryAssign := fmt.Sprintf(`
		WITH target_info AS (
			SELECT u.id, u.username, ur.role_level AS target_user_level
			FROM %s.users u
			LEFT JOIN %s.user_role ur ON u.id = ur.user_id AND ur.workspace_id = '00000000-0000-0000-0000-000000000000'
			WHERE u.id = $2
		),
		to_assign_role_info AS (
			SELECT id, name, role_level
			FROM %s.roles
			WHERE id = $3 AND scope = 'platform'
		),
		assigner_check AS (
			SELECT ti.id, ti.username, ri.id AS role_id, ri.name AS role_name, ri.role_level
			FROM target_info ti
			CROSS JOIN to_assign_role_info ri
			WHERE $4 < COALESCE(ti.target_user_level, 999) AND $4 < ri.role_level
		),
		deleter AS (
			DELETE FROM %s.user_role
			WHERE user_id = $2 AND workspace_id = '00000000-0000-0000-0000-000000000000'
			  AND EXISTS (SELECT 1 FROM assigner_check)
		),
		inserter AS (
			INSERT INTO %s.user_role (
				id, user_id, username, workspace_id, role_id, role_name, role_level, list_perm, created_at, updated_at
			)
			SELECT 
				gen_random_uuid(), id, username, '00000000-0000-0000-0000-000000000000', role_id, role_name, role_level, $1, NOW(), NOW()
			FROM assigner_check
			RETURNING id
		)
		SELECT
			(SELECT COUNT(*) FROM target_info) AS user_exists,
			(SELECT COUNT(*) FROM to_assign_role_info) AS role_exists,
			(SELECT COUNT(*) FROM assigner_check) AS check_success,
			(SELECT COUNT(*) FROM inserter) AS insert_success
	`, r.schema, r.schema, r.schema, r.schema, r.schema)

	var userExists, roleExists, checkSuccess, insertSuccess int
	err = tx.QueryRow(ctx, queryAssign, binaryBytes, userID, roleID, callerLevel).Scan(&userExists, &roleExists, &checkSuccess, &insertSuccess)
	if err != nil {
		return fmt.Errorf("platform rbac repo: assign user role query: %w", err)
	}

	// [COMMENT]: D. Xử lý kết quả trả về phân cấp lỗi
	if userExists == 0 {
		return iamTaxonomy.ErrUserNotFound
	}
	if roleExists == 0 {
		return iamTaxonomy.ErrRoleNotFound
	}
	if checkSuccess == 0 || insertSuccess == 0 {
		return iamTaxonomy.ErrActionNotAllowed
	}

	// [COMMENT]: E. Commit transaction sau khi mọi kiểm tra và chèn bản ghi thành công
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("platform rbac repo: commit transaction: %w", err)
	}

	return nil
}

// [COMMENT]: ListPlatformRoles lấy danh sách roles có scope là platform có level thấp hơn (role_level > callerLevel)
func (r *RbacPlatformRepository) ListPlatformRoles(ctx context.Context, callerLevel uint8) ([]iamEntity.Role, error) {
	query := fmt.Sprintf(`
		SELECT 
			r.id, 
			r.code, 
			r.name, 
			COALESCE(r.description, ''), 
			r.role_level, 
			r.scope, 
			COALESCE(sub_ur.cnt, 0) as assignments_count,
			COALESCE(sub_rp.cnt, 0) as permissions_count,
			r.created_at, 
			r.updated_at
		FROM %s.roles r
		LEFT JOIN (
			SELECT role_id, COUNT(id) as cnt 
			FROM %s.user_role 
			GROUP BY role_id
		) sub_ur ON sub_ur.role_id = r.id
		LEFT JOIN (
			SELECT role_id, COUNT(permission_id) as cnt 
			FROM %s.role_permissions 
			GROUP BY role_id
		) sub_rp ON sub_rp.role_id = r.id
		WHERE r.scope = 'platform' AND r.role_level > $1
		ORDER BY r.role_level ASC
	`, r.schema, r.schema, r.schema)

	rows, err := r.db.Query(ctx, query, callerLevel)
	if err != nil {
		return nil, fmt.Errorf("rbac platform repo: query platform roles: %w", err)
	}
	defer rows.Close()

	var roles []iamEntity.Role
	for rows.Next() {
		var role iamModel.Role
		var assignmentsCount, permissionsCount int
		err := rows.Scan(
			&role.ID,
			&role.Code,
			&role.Name,
			&role.Description,
			&role.RoleLevel,
			&role.Scope,
			&assignmentsCount,
			&permissionsCount,
			&role.CreatedAt,
			&role.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("rbac platform repo: scan platform role row: %w", err)
		}
		entityRole := iamModel.RoleModelToEntity(role)
		entityRole.AssignmentsCount = assignmentsCount
		entityRole.PermissionsCount = permissionsCount
		roles = append(roles, entityRole)
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
