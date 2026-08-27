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
	iamproto "controlplane/internal/iam/transport/proto"

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
		SELECT u.username, r.version,
		       p.module || ':' || p.object || ':' || p.behavior AS perm
		FROM %s.users u
		CROSS JOIN %s.platform_roles r
		JOIN %s.platform_role_permissions rp ON rp.role_id=r.id
		JOIN %s.permissions p ON rp.permission_id = p.id
		WHERE u.id=$2 AND r.id=$1
		ORDER BY p.module, p.object, p.behavior
	`, r.schema, r.schema, r.schema, r.schema)

	rows, err := tx.Query(ctx, queryPerms, roleID, userID)
	if err != nil {
		return fmt.Errorf("platform rbac repo: query permissions: %w", err)
	}
	defer rows.Close()

	var username string
	var roleVersion int64
	var perms []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&username, &roleVersion, &p); err != nil {
			return fmt.Errorf("platform rbac repo: scan permission: %w", err)
		}
		perms = append(perms, p)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close() // đóng sớm để tránh lock connection
	if username == "" || len(perms) == 0 {
		return iamTaxonomy.ErrNotFound
	}
	for index := range perms {
		perms[index] = username + ":00000000-0000-0000-0000-000000000000:" + perms[index]
	}

	// [COMMENT]: B. Serialize danh sách permissions thành Protobuf binary byte array (để repository thuần chất cơ sở dữ liệu)
	roleEntry := &iamproto.RoleEntry{
		Permissions: perms,
	}
	binaryBytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(roleEntry)
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
			SELECT id, name, role_level, version
			FROM %s.platform_roles
			WHERE id = $3
		),
		assigner_check AS (
			SELECT ti.id, ti.username, ri.id AS role_id, ri.name AS role_name, ri.role_level, ri.version
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
				id, user_id, username, workspace_id, role_id, role_name, role_level, role_version, list_perm, created_at, updated_at
			)
			SELECT 
				gen_random_uuid(), id, username, '00000000-0000-0000-0000-000000000000', role_id, role_name, role_level, version, $1, NOW(), NOW()
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
			'platform' AS scope,
			r.created_by,
			COALESCE(up_creator.fullname, '') as created_by_name,
			COALESCE(sub_ur.cnt, 0) as assignments_count,
			COALESCE(sub_rp.cnt, 0) as permissions_count,
			r.created_at, 
			r.updated_at
		FROM %s.platform_roles r
		LEFT JOIN %s.user_profiles up_creator ON r.created_by = up_creator.user_id
		LEFT JOIN (
			SELECT role_id, COUNT(id) as cnt 
			FROM %s.user_role 
			GROUP BY role_id
		) sub_ur ON sub_ur.role_id = r.id
		LEFT JOIN (
			SELECT role_id, COUNT(permission_id) as cnt 
			FROM %s.platform_role_permissions
			GROUP BY role_id
		) sub_rp ON sub_rp.role_id = r.id
		WHERE r.role_level > $1
		ORDER BY r.role_level ASC
	`, r.schema, r.schema, r.schema, r.schema)

	rows, err := r.db.Query(ctx, query, callerLevel)
	if err != nil {
		return nil, fmt.Errorf("rbac platform repo: query platform roles: %w", err)
	}
	defer rows.Close()

	var roles []iamEntity.Role
	for rows.Next() {
		var role iamModel.Role
		var createdByName string // [COMMENT]: kết quả JOIN từ user_profiles — không phải cột trong bảng roles
		var assignmentsCount, permissionsCount int
		err := rows.Scan(
			&role.ID,
			&role.Code,
			&role.Name,
			&role.Description,
			&role.RoleLevel,
			&role.Scope,
			&role.CreatedBy,
			&createdByName,
			&assignmentsCount,
			&permissionsCount,
			&role.CreatedAt,
			&role.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("rbac platform repo: scan platform role row: %w", err)
		}
		entityRole := iamModel.RoleModelToEntity(role)
		entityRole.CreatedByName = createdByName
		entityRole.AssignmentsCount = assignmentsCount
		entityRole.PermissionsCount = permissionsCount
		roles = append(roles, entityRole)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return roles, nil
}

// [COMMENT]: CreateRole tạo một vai trò hệ thống mới và map permissions kèm kiểm tra sở hữu tập con quyền của caller
func (r *RbacPlatformRepository) CreateRole(ctx context.Context, callerUserID uuid.UUID, role *iamEntity.Role, permissionIDs []uuid.UUID) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("rbac platform repo: begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// 0. Kiểm tra xem tất cả permissions gán vào có là tập con của caller permissions hay không (ngoại trừ super-admin)
	checkQuery := fmt.Sprintf(`
		SELECT 
			COALESCE((
				SELECT MIN(role_level) 
				FROM %s.user_role 
				WHERE user_id = $1 AND workspace_id = '00000000-0000-0000-0000-000000000000'
			), 999) as caller_level,
			(
				SELECT COUNT(*)
				FROM unnest($2::uuid[]) AS input_perm_id
				WHERE input_perm_id NOT IN (
					SELECT rp.permission_id
					FROM %s.user_role ur
					JOIN %s.platform_role_permissions rp ON ur.role_id = rp.role_id
					WHERE ur.user_id = $1 AND ur.workspace_id = '00000000-0000-0000-0000-000000000000'
				)
			) as unowned_perms_count
	`, r.schema, r.schema, r.schema)

	var callerLevel int
	var unownedPermsCount int
	err = tx.QueryRow(ctx, checkQuery, callerUserID, permissionIDs).Scan(&callerLevel, &unownedPermsCount)
	if err != nil {
		return fmt.Errorf("rbac platform repo: check caller permission subset: %w", err)
	}

	if callerLevel > 0 && unownedPermsCount > 0 {
		return iamTaxonomy.ErrActionNotAllowed
	}

	// 1. Insert role
	roleQuery := fmt.Sprintf(`
		INSERT INTO %s.platform_roles (id, code, name, description, role_level, created_by, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, now(), now())
	`, r.schema)

	_, err = tx.Exec(ctx, roleQuery, role.ID, role.Code, role.Name, role.Description, role.RoleLevel, role.CreatedBy)
	if err != nil {
		return fmt.Errorf("rbac platform repo: insert role: %w", err)
	}

	// 2. Map permissions
	if len(permissionIDs) > 0 {
		permQuery := fmt.Sprintf(`
			INSERT INTO %s.platform_role_permissions (role_id, permission_id, created_at)
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

// [COMMENT]: ListPermissions lấy danh sách permissions catalog được lọc dựa theo quyền của caller
func (r *RbacPlatformRepository) ListPermissions(ctx context.Context, callerUserID uuid.UUID) ([]iamEntity.Permission, error) {
	// A. Lấy level của caller để check xem có phải super-admin không
	checkQuery := fmt.Sprintf(`
		SELECT COALESCE((
			SELECT MIN(role_level) 
			FROM %s.user_role 
			WHERE user_id = $1 AND workspace_id = '00000000-0000-0000-0000-000000000000'
		), 999) as caller_level
	`, r.schema)

	var callerLevel int
	err := r.db.QueryRow(ctx, checkQuery, callerUserID).Scan(&callerLevel)
	if err != nil {
		return nil, fmt.Errorf("rbac platform repo: check caller level: %w", err)
	}

	var query string
	var args []interface{}

	if callerLevel == 0 {
		// B1. Super-admin lấy toàn bộ catalog
		query = fmt.Sprintf(`
			SELECT id, module, object, behavior, COALESCE(description, ''), created_at, updated_at
			FROM %s.permissions
			ORDER BY module ASC, object ASC, behavior ASC
		`, r.schema)
	} else {
		// B2. Các admin khác chỉ lấy danh sách các permissions mà mình đang sở hữu
		query = fmt.Sprintf(`
			SELECT DISTINCT p.id, p.module, p.object, p.behavior, COALESCE(p.description, ''), p.created_at, p.updated_at
			FROM %s.permissions p
			JOIN %s.platform_role_permissions rp ON p.id = rp.permission_id
			JOIN %s.user_role ur ON rp.role_id = ur.role_id
			WHERE ur.user_id = $1 AND ur.workspace_id = '00000000-0000-0000-0000-000000000000'
			ORDER BY p.module ASC, p.object ASC, p.behavior ASC
		`, r.schema, r.schema, r.schema)
		args = append(args, callerUserID)
	}

	rows, err := r.db.Query(ctx, query, args...)
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
		SELECT rl.id, rl.code, rl.name, COALESCE(rl.description, ''), rl.role_level, 'platform', rl.created_at, rl.updated_at
		FROM %s.user_role ur
		JOIN %s.platform_roles rl ON ur.role_id = rl.id
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
		SELECT ur.list_perm
		FROM %s.user_role ur
		JOIN %s.users u ON u.id = ur.user_id AND u.status = 'active'
		WHERE ur.user_id = $1
	`, r.schema, r.schema)

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

func (r *RbacPlatformRepository) ResolvePersonalRoleLevel(ctx context.Context, userID uuid.UUID) (int32, error) {
	var level int32
	query := fmt.Sprintf(`
		SELECT ur.role_level
		FROM %s.users u
		JOIN %s.user_role ur ON ur.user_id = u.id
		JOIN %s.platform_roles role ON role.id = ur.role_id
		WHERE u.id = $1 AND u.status = 'active'
		  AND ur.workspace_id = '00000000-0000-0000-0000-000000000000'::uuid
		ORDER BY ur.role_level ASC
		LIMIT 1
	`, r.schema, r.schema, r.schema)
	if err := r.db.QueryRow(ctx, query, userID).Scan(&level); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, iamTaxonomy.ErrUserNotFound
		}
		return 0, fmt.Errorf("rbac platform repo: resolve personal role level: %w", err)
	}
	return level, nil
}

// [COMMENT]: DeleteRolePlatform xóa vai trò platform nếu callerLevel < roleLevel và không còn user/tenant nào được gán
func (r *RbacPlatformRepository) DeleteRolePlatform(ctx context.Context, callerLevel uint8, roleID uuid.UUID) error {
	query := fmt.Sprintf(`
		WITH check_role AS (
			SELECT role_level FROM %s.platform_roles WHERE id = $2
		),
		check_user_assignments AS (
			SELECT COUNT(id) as cnt FROM %s.user_role WHERE role_id = $2
		),
		delete_role AS (
			DELETE FROM %s.platform_roles
			WHERE id = $2
			  AND (SELECT role_level FROM check_role) > $1
			  AND (SELECT cnt FROM check_user_assignments) = 0
			RETURNING id
		)
		SELECT 
			EXISTS(SELECT 1 FROM check_role) AS role_exists,
			COALESCE((SELECT role_level FROM check_role), 0) > $1 AS hierarchy_ok,
			((SELECT cnt FROM check_user_assignments) = 0) AS not_in_use,
			EXISTS(SELECT 1 FROM delete_role) AS deleted;
	`, r.schema, r.schema, r.schema)

	var roleExists, hierarchyOk, notInUse, deleted bool
	err := r.db.QueryRow(ctx, query, callerLevel, roleID).Scan(&roleExists, &hierarchyOk, &notInUse, &deleted)
	if err != nil {
		return fmt.Errorf("rbac platform repo: execute delete role cte: %w", err)
	}

	if !roleExists {
		return iamTaxonomy.ErrRoleNotFound
	}
	if !hierarchyOk || !notInUse {
		return iamTaxonomy.ErrActionNotAllowed
	}
	if !deleted {
		return iamTaxonomy.ErrZeroRowsAffected
	}

	return nil
}

// [COMMENT]: GetRoleDetails lấy chi tiết một vai trò platform cùng danh sách đối tượng permission bậc 3 dưới dạng truy vấn kết hợp (JOIN) và kiểm tra caller level
func (r *RbacPlatformRepository) GetRoleDetails(ctx context.Context, callerLevel uint8, roleID uuid.UUID) (*iamEntity.Role, []iamEntity.Permission, error) {
	query := fmt.Sprintf(`
		SELECT 
			r.id, 
			r.code, 
			r.name, 
			COALESCE(r.description, ''), 
			r.role_level,
			'platform' AS scope,
			r.created_by,
			COALESCE(up_creator.fullname, '') as created_by_name,
			COALESCE(sub_ur.cnt, 0) as assignments_count,
			r.created_at, 
			r.updated_at,
			COALESCE(p.id, '00000000-0000-0000-0000-000000000000'::uuid) as perm_id,
			COALESCE(p.module, '') as perm_module,
			COALESCE(p.object, '') as perm_object,
			COALESCE(p.behavior, '') as perm_behavior,
			COALESCE(p.description, '') as perm_description,
			COALESCE(p.created_at, '0001-01-01 00:00:00Z'::timestamptz) as perm_created_at,
			COALESCE(p.updated_at, '0001-01-01 00:00:00Z'::timestamptz) as perm_updated_at
		FROM %s.platform_roles r
		LEFT JOIN %s.user_profiles up_creator ON r.created_by = up_creator.user_id
		LEFT JOIN (
			SELECT role_id, COUNT(id) as cnt 
			FROM %s.user_role 
			GROUP BY role_id
		) sub_ur ON sub_ur.role_id = r.id
		LEFT JOIN %s.platform_role_permissions rp ON rp.role_id = r.id
		LEFT JOIN %s.permissions p ON rp.permission_id = p.id
		WHERE r.id = $1
	`, r.schema, r.schema, r.schema, r.schema, r.schema)

	rows, err := r.db.Query(ctx, query, roleID)
	if err != nil {
		return nil, nil, fmt.Errorf("rbac platform repo: query role details join: %w", err)
	}
	defer rows.Close()

	var role *iamEntity.Role
	var permissions []iamEntity.Permission

	for rows.Next() {
		var roleModel iamModel.Role
		var permModel iamModel.Permission
		var createdByName string // [COMMENT]: kết quả JOIN từ user_profiles — không phải cột trong bảng roles
		var assignmentsCount int

		err := rows.Scan(
			&roleModel.ID,
			&roleModel.Code,
			&roleModel.Name,
			&roleModel.Description,
			&roleModel.RoleLevel,
			&roleModel.Scope,
			&roleModel.CreatedBy,
			&createdByName,
			&assignmentsCount,
			&roleModel.CreatedAt,
			&roleModel.UpdatedAt,
			&permModel.ID,
			&permModel.Module,
			&permModel.Object,
			&permModel.Behavior,
			&permModel.Description,
			&permModel.CreatedAt,
			&permModel.UpdatedAt,
		)
		if err != nil {
			return nil, nil, fmt.Errorf("rbac platform repo: scan role detail join row: %w", err)
		}

		if role == nil {
			// Ràng buộc kiểm tra cấp bậc: callerLevel phải có quyền cao hơn (chỉ số nhỏ hơn) so với role Level lấy ra
			if roleModel.RoleLevel <= int(callerLevel) {
				return nil, nil, iamTaxonomy.ErrActionNotAllowed
			}

			entityRole := iamModel.RoleModelToEntity(roleModel)
			entityRole.CreatedByName = createdByName
			entityRole.AssignmentsCount = assignmentsCount
			role = &entityRole
		}

		if permModel.ID != uuid.Nil {
			permissions = append(permissions, iamModel.PermissionModelToEntity(permModel))
		}
	}

	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	if role == nil {
		return nil, nil, iamTaxonomy.ErrRoleNotFound
	}

	role.PermissionsCount = len(permissions)
	return role, permissions, nil
}

// [COMMENT]: UpdateRole thực hiện cập nhật tên, mô tả, đồng bộ quyền gán cho vai trò platform, biên dịch lại nhị phân list_perm cho tất cả users đang gán vai trò này và trả về danh sách user ID bị ảnh hưởng dưới dạng Transaction nguyên tử
func (r *RbacPlatformRepository) UpdateRole(ctx context.Context, callerUserID uuid.UUID, callerLevel uint8, input *iamEntity.UpdateRoleInput) ([]uuid.UUID, error) {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return nil, fmt.Errorf("platform rbac repo: begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	checkQuery := fmt.Sprintf(`
		SELECT
			COALESCE((
				SELECT MIN(role_level)
				FROM %s.user_role
				WHERE user_id = $1 AND workspace_id = '00000000-0000-0000-0000-000000000000'
			), 999),
			(
				SELECT COUNT(*)
				FROM unnest($2::uuid[]) AS input_perm_id
				WHERE input_perm_id NOT IN (
					SELECT rp.permission_id
					FROM %s.user_role ur
					JOIN %s.platform_role_permissions rp ON ur.role_id = rp.role_id
					WHERE ur.user_id = $1 AND ur.workspace_id = '00000000-0000-0000-0000-000000000000'
				)
			)
	`, r.schema, r.schema, r.schema)
	var callerUserLevel int
	var unownedPermsCount int
	if err := tx.QueryRow(ctx, checkQuery, callerUserID, input.PermissionIDs).Scan(&callerUserLevel, &unownedPermsCount); err != nil {
		return nil, fmt.Errorf("rbac platform repo: check caller permission subset: %w", err)
	}
	if callerUserLevel > 0 && unownedPermsCount > 0 {
		return nil, iamTaxonomy.ErrActionNotAllowed
	}

	var currentRoleLevel int
	if err := tx.QueryRow(ctx, fmt.Sprintf(`
		SELECT role_level FROM %s.platform_roles WHERE id=$1 FOR UPDATE
	`, r.schema), input.ID).Scan(&currentRoleLevel); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, iamTaxonomy.ErrRoleNotFound
		}
		return nil, fmt.Errorf("rbac platform repo: lock role: %w", err)
	}
	if currentRoleLevel <= int(callerLevel) {
		return nil, iamTaxonomy.ErrActionNotAllowed
	}

	queryPerms := fmt.Sprintf(`
		SELECT module, object, behavior
		FROM %s.permissions
		WHERE id=ANY($1::uuid[])
		ORDER BY module, object, behavior
	`, r.schema)
	rows, err := tx.Query(ctx, queryPerms, input.PermissionIDs)
	if err != nil {
		return nil, fmt.Errorf("rbac platform repo: query permission strings: %w", err)
	}
	permissionParts := make([]string, 0, len(input.PermissionIDs))
	for rows.Next() {
		var module, object, behavior string
		if err := rows.Scan(&module, &object, &behavior); err != nil {
			rows.Close()
			return nil, fmt.Errorf("rbac platform repo: scan permission string: %w", err)
		}
		permissionParts = append(permissionParts, module+":"+object+":"+behavior)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("rbac platform repo: iterate permission strings: %w", err)
	}
	rows.Close()
	uniquePermissionIDs := make(map[uuid.UUID]struct{}, len(input.PermissionIDs))
	for _, permissionID := range input.PermissionIDs {
		uniquePermissionIDs[permissionID] = struct{}{}
	}
	if len(permissionParts) == 0 || len(permissionParts) != len(uniquePermissionIDs) {
		return nil, iamTaxonomy.ErrPreconditionFailed
	}

	var roleVersion int64
	queryUpdate := fmt.Sprintf(`
		WITH update_role AS (
			UPDATE %s.platform_roles r
			SET name=$2, description=$3, version=version+1, updated_at=now()
			WHERE id=$1
			RETURNING id, name, version
		), delete_old_perms AS (
			DELETE FROM %s.platform_role_permissions rp
			USING update_role updated
			WHERE rp.role_id=updated.id
			RETURNING rp.permission_id
		), delete_fence AS (
			SELECT count(*) FROM delete_old_perms
		), insert_new_perms AS (
			INSERT INTO %s.platform_role_permissions (role_id, permission_id)
			SELECT updated.id, permission_id
			FROM update_role updated
			CROSS JOIN delete_fence
			CROSS JOIN unnest($4::uuid[]) permission_id
			RETURNING role_id
		)
		SELECT updated.version
		FROM update_role updated
		WHERE (SELECT count(*) FROM insert_new_perms)=$5
	`, r.schema, r.schema, r.schema)
	if err := tx.QueryRow(ctx, queryUpdate, input.ID, input.Name, input.Description, input.PermissionIDs, len(uniquePermissionIDs)).Scan(&roleVersion); err != nil {
		return nil, fmt.Errorf("rbac platform repo: update role definition: %w", err)
	}

	assignmentRows, err := tx.Query(ctx, fmt.Sprintf(`
		SELECT id, user_id, username, workspace_id
		FROM %s.user_role
		WHERE role_id=$1
		ORDER BY user_id, workspace_id
		FOR UPDATE
	`, r.schema), input.ID)
	if err != nil {
		return nil, fmt.Errorf("rbac platform repo: lock role assignments: %w", err)
	}
	type assignment struct {
		id          uuid.UUID
		userID      uuid.UUID
		username    string
		workspaceID uuid.UUID
	}
	assignments := make([]assignment, 0)
	for assignmentRows.Next() {
		var current assignment
		if err := assignmentRows.Scan(&current.id, &current.userID, &current.username, &current.workspaceID); err != nil {
			assignmentRows.Close()
			return nil, fmt.Errorf("rbac platform repo: scan role assignment: %w", err)
		}
		assignments = append(assignments, current)
	}
	if err := assignmentRows.Err(); err != nil {
		assignmentRows.Close()
		return nil, fmt.Errorf("rbac platform repo: iterate role assignments: %w", err)
	}
	assignmentRows.Close()

	affectedUserIDs := make([]uuid.UUID, 0, len(assignments))
	for _, current := range assignments {
		permissions := make([]string, 0, len(permissionParts))
		for _, permission := range permissionParts {
			permissions = append(permissions, current.username+":"+current.workspaceID.String()+":"+permission)
		}
		compiled, err := proto.MarshalOptions{Deterministic: true}.Marshal(&iamproto.RoleEntry{Permissions: permissions})
		if err != nil {
			return nil, fmt.Errorf("rbac platform repo: compile role assignment: %w", err)
		}
		command, err := tx.Exec(ctx, fmt.Sprintf(`
			UPDATE %s.user_role
			SET role_name=$2, role_version=$3, list_perm=$4, updated_at=now()
			WHERE id=$1
		`, r.schema), current.id, input.Name, roleVersion, compiled)
		if err != nil {
			return nil, fmt.Errorf("rbac platform repo: update compiled assignment: %w", err)
		}
		if command.RowsAffected() != 1 {
			return nil, iamTaxonomy.ErrConflict
		}
		affectedUserIDs = append(affectedUserIDs, current.userID)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("platform rbac repo: commit transaction: %w", err)
	}

	return affectedUserIDs, nil
}
