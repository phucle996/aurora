package iamRepoImpl

import (
	"context"
	"errors"
	"fmt"

	"controlplane/internal/config"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	iamproto "controlplane/internal/iam/transport/proto"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/proto"
)

// [COMMENT]: PersonalRbacRepository thực thi interface quản lý RBAC toàn cục
type PersonalRbacRepository struct {
	cfg    *config.Config
	db     *pgxpool.Pool
	schema string
}

// [COMMENT]: NewPersonalRbacRepository khởi tạo một thể hiện mới của PersonalRbacRepository
func NewPersonalRbacRepository(cfg *config.Config, db *pgxpool.Pool) iamRepoInterface.PersonalRbacRepository {
	return &PersonalRbacRepository{
		cfg:    cfg,
		db:     db,
		schema: cfg.SchemaSQL.IAM,
	}
}

// [COMMENT]: AssignUserRole thực hiện gán vai trò platform cho người dùng sử dụng CTE nguyên tử kiểm tra phân cấp
func (r *PersonalRbacRepository) AssignUserRole(ctx context.Context, callerLevel uint8, userID uuid.UUID, roleID uuid.UUID) error {
	// [COMMENT]: 1. Khởi tạo một Transaction để đảm bảo tính cô lập (Read Committed) và ngăn chặn race conditions
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("platform rbac repo: begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	// [COMMENT]: 2. Thực hiện CTE nguyên tử để:
	// - Kiểm tra target user tồn tại và lấy level hiện tại của target user.
	// - Kiểm tra role gán tồn tại và lấy level của role gán.
	// - So sánh caller level với level của target user & role gán.
	// - Nếu hợp lệ, xóa vai trò cũ tại platform scope (nil UUID) và chèn vai trò mới.
	queryAssign := fmt.Sprintf(`
		WITH target_info AS (
			SELECT u.id, u.username, ur.role_level AS target_user_level
			FROM %s.users u
			LEFT JOIN %s.user_role ur ON u.id = ur.user_id AND ur.workspace_id = '00000000-0000-0000-0000-000000000000'
			WHERE u.id = $1
		),
		to_assign_role_info AS (
			SELECT id, name, role_level, version
			FROM %s.platform_roles
			WHERE id = $2
		),
		assigner_check AS (
			SELECT ti.id, ti.username, ri.id AS role_id, ri.name AS role_name, ri.role_level, ri.version
			FROM target_info ti
			CROSS JOIN to_assign_role_info ri
			WHERE $3 < COALESCE(ti.target_user_level, 999) AND $3 < ri.role_level
		),
		deleter AS (
			DELETE FROM %s.user_role
			WHERE user_id = $1 AND workspace_id = '00000000-0000-0000-0000-000000000000'
			  AND EXISTS (SELECT 1 FROM assigner_check)
		),
		inserter AS (
			INSERT INTO %s.user_role (
				id, user_id, username, workspace_id, role_id, role_name, role_level, role_version, created_at, updated_at
			)
			SELECT 
				gen_random_uuid(), id, username, '00000000-0000-0000-0000-000000000000', role_id, role_name, role_level, version, NOW(), NOW()
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
	err = tx.QueryRow(ctx, queryAssign, userID, roleID, callerLevel).Scan(&userExists, &roleExists, &checkSuccess, &insertSuccess)
	if err != nil {
		return fmt.Errorf("platform rbac repo: assign user role query: %w", err)
	}

	// [COMMENT]: 3. Xử lý kết quả trả về phân cấp lỗi
	if userExists == 0 {
		return iamTaxonomy.ErrUserNotFound
	}
	if roleExists == 0 {
		return iamTaxonomy.ErrRoleNotFound
	}
	if checkSuccess == 0 || insertSuccess == 0 {
		return iamTaxonomy.ErrActionNotAllowed
	}

	// [COMMENT]: 4. Commit transaction sau khi mọi kiểm tra và chèn bản ghi thành công
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("platform rbac repo: commit transaction: %w", err)
	}

	return nil
}

// [COMMENT]: ListPlatformRoles lấy danh sách roles có scope là platform có level thấp hơn (role_level > callerLevel)
func (r *PersonalRbacRepository) ListPlatformRoles(ctx context.Context, callerLevel uint8) ([]iamEntity.Role, error) {
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
		var role iamEntity.Role
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
		role.CreatedByName = createdByName
		role.AssignmentsCount = assignmentsCount
		role.PermissionsCount = permissionsCount
		roles = append(roles, role)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return roles, nil
}

// [COMMENT]: CreateRole tạo một vai trò hệ thống mới và map permissions kèm kiểm tra sở hữu tập con quyền của caller
func (r *PersonalRbacRepository) CreateRole(ctx context.Context, callerUserID uuid.UUID, role *iamEntity.Role, permissionIDs []uuid.UUID) error {
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
func (r *PersonalRbacRepository) ListPermissions(ctx context.Context, callerUserID uuid.UUID) ([]iamEntity.Permission, error) {
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
		var p iamEntity.Permission
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
		perms = append(perms, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return perms, nil
}

// [COMMENT]: GetUserRoleDetails lấy thông tin chi tiết vai trò của user kèm kiểm tra cấp bậc
func (r *PersonalRbacRepository) GetUserRoleDetails(ctx context.Context, userID uuid.UUID, callerLevel int32) (*iamEntity.Role, error) {
	var role iamEntity.Role

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

	return &role, nil
}

// [COMMENT]: GetUserRolePermissions truy vấn định nghĩa Role của user và JIT compile danh sách quyền 5 cấp (username:workspace_id:module:object:behavior) sang binary Protobuf bytea
func (r *PersonalRbacRepository) GetUserRolePermissions(ctx context.Context, userID uuid.UUID) ([]byte, error) {
	query := fmt.Sprintf(`
		SELECT ur.username, ur.workspace_id,
		       p.module || ':' || p.object || ':' || p.behavior AS perm
		FROM %s.user_role ur
		JOIN %s.users u ON u.id = ur.user_id AND u.status = 'active'
		JOIN %s.platform_roles pr ON pr.id = ur.role_id
		JOIN %s.platform_role_permissions prp ON prp.role_id = pr.id
		JOIN %s.permissions p ON p.id = prp.permission_id
		WHERE ur.user_id = $1
		ORDER BY p.module, p.object, p.behavior
	`, r.schema, r.schema, r.schema, r.schema, r.schema)

	rows, err := r.db.Query(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("rbac platform repo: query user role permissions: %w", err)
	}
	defer rows.Close()

	var perms []string
	for rows.Next() {
		var username string
		var workspaceID uuid.UUID
		var perm string
		if err := rows.Scan(&username, &workspaceID, &perm); err != nil {
			return nil, fmt.Errorf("rbac platform repo: scan user role permission row: %w", err)
		}
		perms = append(perms, username+":"+workspaceID.String()+":"+perm)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	roleEntry := &iamproto.RoleEntry{
		Permissions: perms,
	}
	compiledBytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(roleEntry)
	if err != nil {
		return nil, fmt.Errorf("rbac platform repo: marshal user role entry: %w", err)
	}

	return compiledBytes, nil
}

// [COMMENT]: ResolvePersonalRoleLevel tra cứu cấp bậc (role_level nhỏ nhất) của user trong phạm vi personal scope (workspace nil UUID)
func (r *PersonalRbacRepository) ResolvePersonalRoleLevel(ctx context.Context, userID uuid.UUID) (int32, error) {
	var level int32
	// [COMMENT]: 1. Truy vấn role_level thấp nhất (quyền cao nhất) của user đang active tại personal scope
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
func (r *PersonalRbacRepository) DeleteRolePlatform(ctx context.Context, callerLevel uint8, roleID uuid.UUID) error {
	// [COMMENT]: Sử dụng CTE nguyên tử để kiểm tra đồng thời:
	// 1. Role có tồn tại hay không.
	// 2. Caller có cấp bậc cao hơn role (callerLevel < role_level).
	// 3. Role không còn bất kỳ user nào được gán (not in use).
	// 4. Thực thi DELETE và trả về trạng thái tổng thể.
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

	// [COMMENT]: Phân loại lỗi trả về theo đúng domain taxonomy
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
func (r *PersonalRbacRepository) GetRoleDetails(ctx context.Context, callerLevel uint8, roleID uuid.UUID) (*iamEntity.Role, []iamEntity.Permission, error) {
	// [COMMENT]: 1. Truy vấn thông tin vai trò, thông tin người tạo, số lượng phân bổ (user_role), và toàn bộ permissions liên kết
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

	// [COMMENT]: 2. Duyệt qua các dòng kết quả để xây dựng đối tượng Role và danh sách Permissions con
	for rows.Next() {
		var roleRow iamEntity.Role
		var perm iamEntity.Permission
		var createdByName string // [COMMENT]: kết quả JOIN từ user_profiles — không phải cột trong bảng roles
		var assignmentsCount int

		err := rows.Scan(
			&roleRow.ID,
			&roleRow.Code,
			&roleRow.Name,
			&roleRow.Description,
			&roleRow.RoleLevel,
			&roleRow.Scope,
			&roleRow.CreatedBy,
			&createdByName,
			&assignmentsCount,
			&roleRow.CreatedAt,
			&roleRow.UpdatedAt,
			&perm.ID,
			&perm.Module,
			&perm.Object,
			&perm.Behavior,
			&perm.Description,
			&perm.CreatedAt,
			&perm.UpdatedAt,
		)
		if err != nil {
			return nil, nil, fmt.Errorf("rbac platform repo: scan role detail join row: %w", err)
		}

		if role == nil {
			// [COMMENT]: Ràng buộc kiểm tra cấp bậc: callerLevel phải có quyền cao hơn (chỉ số nhỏ hơn) so với role Level lấy ra
			if roleRow.RoleLevel <= int(callerLevel) {
				return nil, nil, iamTaxonomy.ErrActionNotAllowed
			}

			roleRow.CreatedByName = createdByName
			roleRow.AssignmentsCount = assignmentsCount
			role = &roleRow
		}

		if perm.ID != uuid.Nil {
			permissions = append(permissions, perm)
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

// [COMMENT]: UpdateRole thực hiện cập nhật tên, mô tả, đồng bộ quyền gán cho vai trò platform, cập nhật role_version trong platform_roles & user_role và trả về danh sách user ID bị ảnh hưởng dưới dạng Transaction nguyên tử O(1)
func (r *PersonalRbacRepository) UpdateRole(ctx context.Context, callerUserID uuid.UUID, callerLevel uint8, input *iamEntity.UpdateRoleInput) ([]uuid.UUID, error) {
	// [COMMENT]: 1. Khởi tạo Transaction với mức cô lập RepeatableRead để ngăn chặn race condition khi cập nhật quyền và role version
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return nil, fmt.Errorf("platform rbac repo: begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// [COMMENT]: 2. Kiểm tra xem toàn bộ permissions mới có phải là tập con của caller permissions hay không (Super-admin callerLevel=0 được bỏ qua)
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

	// [COMMENT]: 3. Khóa bản ghi role bằng FOR UPDATE và kiểm tra phân cấp cấp bậc (callerLevel phải nhỏ hơn role_level mục tiêu)
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

	uniquePermissionIDs := make(map[uuid.UUID]struct{}, len(input.PermissionIDs))
	for _, permissionID := range input.PermissionIDs {
		uniquePermissionIDs[permissionID] = struct{}{}
	}

	// [COMMENT]: 4. Thực thi CTE cập nhật definition: tăng version của role, xóa permissions cũ, chèn permissions mới và xác thực tính toàn vẹn
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

	// [COMMENT]: 5. Đồng bộ role_name và role_version cho các bản ghi user_role liên kết trong 1 câu SQL duy nhất (O(1))
	if _, err := tx.Exec(ctx, fmt.Sprintf(`
		UPDATE %s.user_role
		SET role_name = $2, role_version = $3, updated_at = NOW()
		WHERE role_id = $1
	`, r.schema), input.ID, input.Name, roleVersion); err != nil {
		return nil, fmt.Errorf("rbac platform repo: update user_role version: %w", err)
	}

	// [COMMENT]: 6. Lấy danh sách user_id bị ảnh hưởng để trả về cho tầng Service kích hoạt Invalidation Cache trên L1/L2
	userRows, err := tx.Query(ctx, fmt.Sprintf(`
		SELECT DISTINCT user_id
		FROM %s.user_role
		WHERE role_id = $1
	`, r.schema), input.ID)
	if err != nil {
		return nil, fmt.Errorf("rbac platform repo: query affected users: %w", err)
	}
	defer userRows.Close()

	var affectedUserIDs []uuid.UUID
	for userRows.Next() {
		var uid uuid.UUID
		if err := userRows.Scan(&uid); err != nil {
			return nil, fmt.Errorf("rbac platform repo: scan affected user: %w", err)
		}
		affectedUserIDs = append(affectedUserIDs, uid)
	}
	if err := userRows.Err(); err != nil {
		return nil, fmt.Errorf("rbac platform repo: iterate affected users: %w", err)
	}

	// [COMMENT]: 7. Commit Transaction sau khi hoàn tất cập nhật
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("platform rbac repo: commit transaction: %w", err)
	}

	return affectedUserIDs, nil
}
